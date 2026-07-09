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

// Package apt publishes Debian (apt) repositories to a storage.Store: it lays
// out the pool, maintains the per-architecture Packages indexes, and rebuilds a
// (optionally signed) Release on every change.
//
// The publisher assumes a single writer: object storage offers no atomic
// read-modify-write, and each Add reads the current indexes, mutates them, and
// writes them back.
package apt

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/pete-woods/repohost/internal/deb"
	"github.com/pete-woods/repohost/internal/retention"
	"github.com/pete-woods/repohost/internal/sign"
	"github.com/pete-woods/repohost/internal/storage"
)

const defaultComponent = "main"

// Config configures an apt repository.
type Config struct {
	// Distribution is the suite/codename, e.g. "stable" or "bookworm". Required.
	Distribution string
	// Components are the repository components. Defaults to {"main"}.
	Components []string
	// Origin, Label, and Description populate the matching Release fields. All
	// optional.
	Origin      string
	Label       string
	Description string
	// KeepVersions caps how many versions of a package are retained per
	// component and architecture. Zero keeps all versions.
	KeepVersions int
	// Signer, if set, signs the Release file (InRelease and Release.gpg).
	// Optional.
	Signer sign.Signer
}

// Publisher publishes an apt repository to a Store.
type Publisher struct {
	store storage.Store
	cfg   Config
}

// New returns a Publisher writing to store. Components defaults to {"main"}.
func New(store storage.Store, cfg Config) *Publisher {
	if len(cfg.Components) == 0 {
		cfg.Components = []string{defaultComponent}
	}
	return &Publisher{store: store, cfg: cfg}
}

// Add reads a .deb from r, uploads it into the pool, updates the Packages index
// for its architecture in the given component (empty means "main"), applies the
// retention policy, and republishes the Release. The distribution's single
// writer is the caller's responsibility.
func (p *Publisher) Add(ctx context.Context, component string, r io.Reader) error {
	if component == "" {
		component = defaultComponent
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("apt: reading .deb: %w", err)
	}
	pkg, err := deb.ParseDeb(data)
	if err != nil {
		return err
	}

	filename := poolPath(component, pkg)
	md5sum, sha1sum, sha256sum := checksums(data)

	if err := p.store.Put(ctx, filename, bytes.NewReader(data)); err != nil {
		return err
	}

	entry := &deb.Package{
		Name:         pkg.Name,
		Version:      pkg.Version,
		Architecture: pkg.Architecture,
		Fields: append(append([]deb.Field(nil), pkg.Fields...),
			deb.Field{Name: "Filename", Value: filename},
			deb.Field{Name: "Size", Value: strconv.Itoa(len(data))},
			deb.Field{Name: "MD5sum", Value: md5sum},
			deb.Field{Name: "SHA1", Value: sha1sum},
			deb.Field{Name: "SHA256", Value: sha256sum},
		),
	}

	if err := p.updateIndex(ctx, component, entry); err != nil {
		return err
	}
	return p.publishRelease(ctx)
}

// Remove deletes every package matching name and version from the component
// (empty means "main") — across all architectures — dropping both the pool
// files and the Packages index entries, then republishes (and re-signs) the
// Release. It returns the number of packages removed. Removing a version that is
// not present is not an error (it returns 0). The single writer is the caller's
// responsibility.
func (p *Publisher) Remove(ctx context.Context, component, name, version string) (int, error) {
	if component == "" {
		component = defaultComponent
	}

	// The architectures a package spans aren't known up front, so scan the
	// component's per-architecture Packages indexes.
	prefix := path.Join("dists", p.cfg.Distribution, component) + "/"
	objs, err := p.store.List(ctx, prefix)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, o := range objs {
		if path.Base(o.Key) != "Packages" {
			continue // skip Packages.gz; it is regenerated from Packages
		}

		existing, err := p.readPackages(ctx, o.Key)
		if err != nil {
			return removed, err
		}

		kept := make([]*deb.Package, 0, len(existing))
		var drop []*deb.Package
		for _, pk := range existing {
			if pk.Name == name && pk.Version == version {
				drop = append(drop, pk)
				continue
			}
			kept = append(kept, pk)
		}
		if len(drop) == 0 {
			continue
		}

		for _, pk := range drop {
			if fn, ok := pk.Get("Filename"); ok {
				if err := p.store.Delete(ctx, fn); err != nil {
					return removed, err
				}
			}
		}
		removed += len(drop)

		content := buildPackages(kept)
		if err := p.store.Put(ctx, o.Key, bytes.NewReader(content)); err != nil {
			return removed, err
		}
		gz, err := gzipBytes(content)
		if err != nil {
			return removed, err
		}
		if err := p.store.Put(ctx, o.Key+".gz", bytes.NewReader(gz)); err != nil {
			return removed, err
		}
	}

	if removed == 0 {
		return 0, nil
	}
	if err := p.publishRelease(ctx); err != nil {
		return removed, err
	}
	return removed, nil
}

// updateIndex loads the Packages index for the entry's component and
// architecture, inserts the entry (replacing any identical name+version),
// applies retention, deletes pruned pool objects, and writes Packages{,.gz}.
func (p *Publisher) updateIndex(ctx context.Context, component string, entry *deb.Package) error {
	key := packagesKey(p.cfg.Distribution, component, entry.Architecture)

	existing, err := p.readPackages(ctx, key)
	if err != nil {
		return err
	}

	kept := make([]*deb.Package, 0, len(existing)+1)
	for _, pk := range existing {
		if pk.Name == entry.Name && pk.Version == entry.Version {
			continue // replaced by the new entry
		}
		kept = append(kept, pk)
	}
	kept = append(kept, entry)

	if p.cfg.KeepVersions > 0 {
		var removed []*deb.Package
		kept, removed = retention.Prune(kept, p.cfg.KeepVersions,
			func(pk *deb.Package) string { return pk.Name },
			func(a, b *deb.Package) int { return deb.CompareVersions(a.Version, b.Version) },
		)
		for _, pk := range removed {
			if fn, ok := pk.Get("Filename"); ok {
				if err := p.store.Delete(ctx, fn); err != nil {
					return err
				}
			}
		}
	}

	content := buildPackages(kept)
	if err := p.store.Put(ctx, key, bytes.NewReader(content)); err != nil {
		return err
	}

	gz, err := gzipBytes(content)
	if err != nil {
		return err
	}
	return p.store.Put(ctx, key+".gz", bytes.NewReader(gz))
}

// readPackages fetches and parses a Packages index, treating a missing index as
// empty.
func (p *Publisher) readPackages(ctx context.Context, key string) ([]*deb.Package, error) {
	rc, err := p.store.Get(ctx, key)
	if errors.Is(err, storage.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("apt: reading %s: %w", key, err)
	}
	return deb.ParseControlFile(data)
}

// buildPackages renders package stanzas into a Packages file, one blank line
// between stanzas.
func buildPackages(pkgs []*deb.Package) []byte {
	var b bytes.Buffer
	for i, pkg := range pkgs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.Write(deb.FormatStanza(pkg.Fields))
	}
	return b.Bytes()
}

// poolPath returns the pool object key for a package, following the Debian
// convention of a first-letter prefix directory (first four letters for lib*).
func poolPath(component string, pkg *deb.Package) string {
	prefix := pkg.Name[:1]
	if strings.HasPrefix(pkg.Name, "lib") && len(pkg.Name) >= 4 {
		prefix = pkg.Name[:4]
	}
	file := fmt.Sprintf("%s_%s_%s.deb", pkg.Name, pkg.Version, pkg.Architecture)
	return path.Join("pool", component, prefix, pkg.Name, file)
}

// packagesKey returns the store key of a binary Packages index.
func packagesKey(distribution, component, arch string) string {
	return path.Join("dists", distribution, component, "binary-"+arch, "Packages")
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return nil, fmt.Errorf("apt: gzip: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("apt: gzip close: %w", err)
	}
	return buf.Bytes(), nil
}
