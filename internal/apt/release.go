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

package apt

import (
	"bytes"
	"context"
	"crypto/md5"  //nolint:gosec // apt Release files require a (legacy) MD5Sum section
	"crypto/sha1" //nolint:gosec // apt Release files require a (legacy) SHA1 section
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"
)

// releaseEntry is one indexed file recorded in the Release checksum sections.
type releaseEntry struct {
	path   string // path relative to the Release file's directory
	size   int64
	md5    string
	sha1   string
	sha256 string
}

// publishRelease regenerates dists/<distribution>/Release from the Packages
// indexes currently in the store, then signs it if a Signer is configured.
func (p *Publisher) publishRelease(ctx context.Context) error {
	distDir := path.Join("dists", p.cfg.Distribution) + "/"
	objs, err := p.store.List(ctx, distDir)
	if err != nil {
		return err
	}

	entries := make([]releaseEntry, 0, len(objs))
	archSet := make(map[string]struct{})
	componentSet := make(map[string]struct{})
	for _, comp := range p.cfg.Components {
		componentSet[comp] = struct{}{}
	}

	for _, o := range objs {
		base := path.Base(o.Key)
		if base != "Packages" && base != "Packages.gz" {
			continue
		}

		data, err := p.getBytes(ctx, o.Key)
		if err != nil {
			return err
		}
		md5sum, sha1sum, sha256sum := checksums(data)

		rel := strings.TrimPrefix(o.Key, distDir)
		entries = append(entries, releaseEntry{
			path:   rel,
			size:   int64(len(data)),
			md5:    md5sum,
			sha1:   sha1sum,
			sha256: sha256sum,
		})

		if component, arch, ok := splitBinaryPath(rel); ok {
			componentSet[component] = struct{}{}
			archSet[arch] = struct{}{}
		}
	}

	release := p.buildRelease(sortedKeys(componentSet), sortedKeys(archSet), entries)

	releaseKey := path.Join("dists", p.cfg.Distribution, "Release")
	if err := p.store.Put(ctx, releaseKey, bytes.NewReader(release)); err != nil {
		return err
	}
	return p.signRelease(ctx, release)
}

// signRelease writes InRelease and Release.gpg when a Signer is configured.
func (p *Publisher) signRelease(ctx context.Context, release []byte) error {
	if p.cfg.Signer == nil {
		return nil
	}

	inRelease, err := p.cfg.Signer.ClearSign(ctx, release)
	if err != nil {
		return fmt.Errorf("apt: clear-signing Release: %w", err)
	}
	inReleaseKey := path.Join("dists", p.cfg.Distribution, "InRelease")
	if err := p.store.Put(ctx, inReleaseKey, bytes.NewReader(inRelease)); err != nil {
		return err
	}

	sig, err := p.cfg.Signer.DetachSign(ctx, release)
	if err != nil {
		return fmt.Errorf("apt: detached-signing Release: %w", err)
	}
	gpgKey := path.Join("dists", p.cfg.Distribution, "Release.gpg")
	return p.store.Put(ctx, gpgKey, bytes.NewReader(sig))
}

// buildRelease renders the Release file. Checksum entries are sorted by path for
// deterministic output.
func (p *Publisher) buildRelease(components, arches []string, entries []releaseEntry) []byte {
	slices.SortFunc(entries, func(a, b releaseEntry) int {
		return strings.Compare(a.path, b.path)
	})

	var b strings.Builder
	writeReleaseField(&b, "Origin", p.cfg.Origin)
	writeReleaseField(&b, "Label", p.cfg.Label)
	writeReleaseField(&b, "Suite", p.cfg.Distribution)
	writeReleaseField(&b, "Codename", p.cfg.Distribution)
	writeReleaseField(&b, "Date", time.Now().UTC().Format(time.RFC1123))
	writeReleaseField(&b, "Architectures", strings.Join(arches, " "))
	writeReleaseField(&b, "Components", strings.Join(components, " "))
	writeReleaseField(&b, "Description", p.cfg.Description)

	writeChecksums(&b, "MD5Sum", entries, func(e releaseEntry) string { return e.md5 })
	writeChecksums(&b, "SHA1", entries, func(e releaseEntry) string { return e.sha1 })
	writeChecksums(&b, "SHA256", entries, func(e releaseEntry) string { return e.sha256 })

	return []byte(b.String())
}

func (p *Publisher) getBytes(ctx context.Context, key string) ([]byte, error) {
	rc, err := p.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("apt: reading %s: %w", key, err)
	}
	return data, nil
}

func writeReleaseField(b *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

func writeChecksums(b *strings.Builder, name string, entries []releaseEntry, pick func(releaseEntry) string) {
	b.WriteString(name)
	b.WriteString(":\n")
	for _, e := range entries {
		fmt.Fprintf(b, " %s %d %s\n", pick(e), e.size, e.path)
	}
}

// splitBinaryPath extracts the component and architecture from a Release-
// relative index path such as "main/binary-amd64/Packages".
func splitBinaryPath(rel string) (component, arch string, ok bool) {
	parts := strings.Split(rel, "/")
	if len(parts) < 3 {
		return "", "", false
	}
	arch, found := strings.CutPrefix(parts[len(parts)-2], "binary-")
	if !found {
		return "", "", false
	}
	return strings.Join(parts[:len(parts)-2], "/"), arch, true
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func checksums(data []byte) (md5sum, sha1sum, sha256sum string) {
	m := md5.Sum(data)   //nolint:gosec // apt requires the legacy MD5Sum checksum
	s1 := sha1.Sum(data) //nolint:gosec // apt requires the legacy SHA1 checksum
	s2 := sha256.Sum256(data)
	return hex.EncodeToString(m[:]), hex.EncodeToString(s1[:]), hex.EncodeToString(s2[:])
}
