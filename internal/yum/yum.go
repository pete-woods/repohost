// Copyright 2026 Pete Steyert-Woods
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package yum publishes RPM (yum/dnf) repositories to a storage.Store: it lays
// out the Packages tree and regenerates the createrepo-style repodata
// (repomd.xml plus primary/filelists/other) on every change, optionally signing
// repomd.xml.
//
// The published metadata matches createrepo_c. Repository state is kept in a
// small JSON manifest under repodata/ (which dnf ignores) rather than by parsing
// the XML back, so the published documents stay canonical. The publisher assumes
// a single writer.
package yum

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/pete-woods/repohost/internal/retention"
	"github.com/pete-woods/repohost/internal/rpm"
	"github.com/pete-woods/repohost/internal/sign"
	"github.com/pete-woods/repohost/internal/storage"
)

// stateKey is where the private package manifest lives. dnf never reads it.
const stateKey = "repodata/repohost-state.json"

// Config configures a yum repository.
type Config struct {
	// KeepVersions caps how many versions of a package are retained per name and
	// architecture. Zero keeps all versions.
	KeepVersions int
	// Signer, if set, signs repomd.xml (written as repomd.xml.asc). Optional.
	Signer sign.Signer
}

// Publisher publishes a yum repository to a Store.
type Publisher struct {
	store storage.Store
	cfg   Config
}

// New returns a Publisher writing to store.
func New(store storage.Store, cfg Config) *Publisher {
	return &Publisher{store: store, cfg: cfg}
}

// packageEntry pairs parsed RPM metadata with its location in the repository. It
// is the unit persisted in the state manifest and rendered into the metadata.
type packageEntry struct {
	Meta     *rpm.Package `json:"meta"`
	Location string       `json:"location"`
}

// Add reads an .rpm from r, uploads it into the Packages tree, updates the
// repository state, applies the retention policy, and regenerates (and signs)
// the repodata. The single writer is the caller's responsibility.
func (p *Publisher) Add(ctx context.Context, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("yum: reading .rpm: %w", err)
	}
	pkg, err := rpm.ParseRPM(data)
	if err != nil {
		return err
	}

	location := poolPath(pkg)
	if err := p.store.Put(ctx, location, bytes.NewReader(data)); err != nil {
		return err
	}

	entries, err := p.loadState(ctx)
	if err != nil {
		return err
	}
	entries = replaceOrAppend(entries, packageEntry{Meta: pkg, Location: location})

	if p.cfg.KeepVersions > 0 {
		var removed []packageEntry
		entries, removed = retention.Prune(entries, p.cfg.KeepVersions,
			func(e packageEntry) string { return e.Meta.Name + "\x00" + e.Meta.Architecture },
			func(a, b packageEntry) int { return rpm.CompareVersions(a.Meta.EVR(), b.Meta.EVR()) },
		)
		for _, e := range removed {
			if err := p.store.Delete(ctx, e.Location); err != nil {
				return err
			}
		}
	}

	return p.publish(ctx, entries)
}

// Remove deletes every package matching name and version (across all
// architectures and releases of that version), drops the RPM files, and
// regenerates (and re-signs) the repodata. It returns the number of packages
// removed. Removing a version that is not present is not an error (it returns
// 0). The single writer is the caller's responsibility.
func (p *Publisher) Remove(ctx context.Context, name, version string) (int, error) {
	entries, err := p.loadState(ctx)
	if err != nil {
		return 0, err
	}

	kept := make([]packageEntry, 0, len(entries))
	var drop []packageEntry
	for _, e := range entries {
		if e.Meta.Name == name && e.Meta.Version == version {
			drop = append(drop, e)
			continue
		}
		kept = append(kept, e)
	}
	if len(drop) == 0 {
		return 0, nil
	}

	for _, e := range drop {
		if err := p.store.Delete(ctx, e.Location); err != nil {
			return 0, err
		}
	}
	if err := p.publish(ctx, kept); err != nil {
		return len(drop), err
	}
	return len(drop), nil
}

// publish renders and writes the repodata, signs repomd.xml, persists the state
// manifest, and removes any stale metadata files.
func (p *Publisher) publish(ctx context.Context, entries []packageEntry) error {
	slices.SortFunc(entries, func(a, b packageEntry) int {
		if c := strings.Compare(a.Meta.Name, b.Meta.Name); c != 0 {
			return c
		}
		if c := strings.Compare(a.Meta.Architecture, b.Meta.Architecture); c != 0 {
			return c
		}
		return rpm.CompareVersions(a.Meta.EVR(), b.Meta.EVR())
	})

	ts := time.Now().Unix()

	primary, err := p.writeMeta(ctx, "primary", buildPrimary(entries), ts)
	if err != nil {
		return err
	}
	filelists, err := p.writeMeta(ctx, "filelists", buildFilelists(entries), ts)
	if err != nil {
		return err
	}
	other, err := p.writeMeta(ctx, "other", buildOther(entries), ts)
	if err != nil {
		return err
	}

	doc := &repomd{
		Xmlns:    nsRepo,
		XmlnsRPM: nsRPM,
		Revision: ts,
		Data:     []repomdData{primary.dataEntry("primary"), filelists.dataEntry("filelists"), other.dataEntry("other")},
	}
	repomdXML, err := marshalXML(doc)
	if err != nil {
		return err
	}
	if err := p.store.Put(ctx, "repodata/repomd.xml", bytes.NewReader(repomdXML)); err != nil {
		return err
	}
	if err := p.signRepomd(ctx, repomdXML); err != nil {
		return err
	}

	if err := p.saveState(ctx, entries); err != nil {
		return err
	}
	return p.cleanupMetadata(ctx, map[string]bool{
		primary.href:   true,
		filelists.href: true,
		other.href:     true,
	})
}

// metaFile records the checksums and sizes of one written metadata document.
type metaFile struct {
	href    string
	gzSum   string
	xmlSum  string
	gzSize  int64
	xmlSize int64
	ts      int64
}

func (m metaFile) dataEntry(typ string) repomdData {
	return repomdData{
		Type:         typ,
		Checksum:     hashValue{Type: "sha256", Value: m.gzSum},
		OpenChecksum: hashValue{Type: "sha256", Value: m.xmlSum},
		Location:     location{Href: m.href},
		Timestamp:    m.ts,
		Size:         m.gzSize,
		OpenSize:     m.xmlSize,
	}
}

// writeMeta marshals a metadata document, gzips it, stores it under its
// content-addressed name, and returns its checksums and sizes.
func (p *Publisher) writeMeta(ctx context.Context, name string, doc any, ts int64) (metaFile, error) {
	xmlBytes, err := marshalXML(doc)
	if err != nil {
		return metaFile{}, err
	}
	gz, err := gzipBytes(xmlBytes)
	if err != nil {
		return metaFile{}, err
	}

	gzSum := sha256hex(gz)
	href := path.Join("repodata", fmt.Sprintf("%s-%s.xml.gz", gzSum, name))
	if err := p.store.Put(ctx, href, bytes.NewReader(gz)); err != nil {
		return metaFile{}, err
	}

	return metaFile{
		href:    href,
		gzSum:   gzSum,
		xmlSum:  sha256hex(xmlBytes),
		gzSize:  int64(len(gz)),
		xmlSize: int64(len(xmlBytes)),
		ts:      ts,
	}, nil
}

func (p *Publisher) signRepomd(ctx context.Context, repomdXML []byte) error {
	if p.cfg.Signer == nil {
		return nil
	}
	sig, err := p.cfg.Signer.DetachSign(ctx, repomdXML)
	if err != nil {
		return fmt.Errorf("yum: signing repomd.xml: %w", err)
	}
	return p.store.Put(ctx, "repodata/repomd.xml.asc", bytes.NewReader(sig))
}

// loadState reads the package manifest, treating a missing manifest as empty.
func (p *Publisher) loadState(ctx context.Context) ([]packageEntry, error) {
	rc, err := p.store.Get(ctx, stateKey)
	if errors.Is(err, storage.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("yum: reading state: %w", err)
	}
	var entries []packageEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("yum: parsing state: %w", err)
	}
	return entries, nil
}

func (p *Publisher) saveState(ctx context.Context, entries []packageEntry) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("yum: encoding state: %w", err)
	}
	return p.store.Put(ctx, stateKey, bytes.NewReader(data))
}

// cleanupMetadata removes content-addressed metadata files that are no longer
// referenced by the current repomd.
func (p *Publisher) cleanupMetadata(ctx context.Context, keep map[string]bool) error {
	objs, err := p.store.List(ctx, "repodata/")
	if err != nil {
		return err
	}
	for _, o := range objs {
		if isMetadataFile(o.Key) && !keep[o.Key] {
			if err := p.store.Delete(ctx, o.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

func isMetadataFile(key string) bool {
	for _, suffix := range []string{"-primary.xml.gz", "-filelists.xml.gz", "-other.xml.gz"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

// replaceOrAppend inserts entry, replacing any existing entry with the same
// location (i.e. the same name-version-release.arch).
func replaceOrAppend(entries []packageEntry, entry packageEntry) []packageEntry {
	out := entries[:0:0]
	for _, e := range entries {
		if e.Location != entry.Location {
			out = append(out, e)
		}
	}
	return append(out, entry)
}

// poolPath returns the Packages key for a package: Packages/<c>/<nvra>.rpm where
// c is the lowercased first letter of the name.
func poolPath(pkg *rpm.Package) string {
	prefix := strings.ToLower(pkg.Name[:1])
	return path.Join("Packages", prefix, pkg.FileName())
}

func marshalXML(v any) ([]byte, error) {
	body, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("yum: marshaling xml: %w", err)
	}
	out := make([]byte, 0, len(xml.Header)+len(body)+1)
	out = append(out, xml.Header...)
	out = append(out, body...)
	out = append(out, '\n')
	return out, nil
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return nil, fmt.Errorf("yum: gzip: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("yum: gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
