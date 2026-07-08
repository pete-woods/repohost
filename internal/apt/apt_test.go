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

package apt_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pete-woods/repohost/internal/apt"
	"github.com/pete-woods/repohost/internal/deb"
	"github.com/pete-woods/repohost/internal/storage"
	"github.com/pete-woods/repohost/internal/testing/debtest"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestAddPublishesPoolPackagesAndRelease(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	pub := apt.New(store, apt.Config{
		Distribution: "stable",
		Origin:       "Acme",
		Label:        "Acme",
		Description:  "Acme packages",
	})

	deb1 := debtest.Package(t, "hello", "2.10", "amd64")
	err := pub.Add(ctx, "main", bytes.NewReader(deb1))
	assert.NilError(t, err)

	poolKey := "pool/main/h/hello/hello_2.10_amd64.deb"
	assert.Check(t, store.has(poolKey), "expected pool object %s", poolKey)

	pkgs := readPackages(t, store, "dists/stable/main/binary-amd64/Packages")
	assert.Assert(t, cmp.Len(pkgs, 1))
	entry := pkgs[0]
	assert.Check(t, cmp.Equal(entry.Name, "hello"))

	filename, _ := entry.Get("Filename")
	assert.Check(t, cmp.Equal(filename, poolKey))
	size, _ := entry.Get("Size")
	assert.Check(t, cmp.Equal(size, strconv.Itoa(len(deb1))))
	_, hasSHA256 := entry.Get("SHA256")
	assert.Check(t, hasSHA256, "Packages stanza must carry a SHA256")

	assert.Check(t, store.has("dists/stable/main/binary-amd64/Packages.gz"))

	release := getString(t, store, "dists/stable/Release")
	for _, want := range []string{
		"Origin: Acme",
		"Suite: stable",
		"Codename: stable",
		"Architectures: amd64",
		"Components: main",
		"main/binary-amd64/Packages",
		"SHA256:",
	} {
		assert.Check(t, cmp.Contains(release, want))
	}
}

func TestReleaseChecksumsMatchStoredFiles(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	pub := apt.New(store, apt.Config{Distribution: "stable"})

	err := pub.Add(ctx, "main", bytes.NewReader(debtest.Package(t, "hello", "1.0", "amd64")))
	assert.NilError(t, err)

	release := getString(t, store, "dists/stable/Release")
	sums := parseSHA256Section(release)
	assert.Assert(t, len(sums) != 0, "Release must list SHA256 checksums")

	for relPath, want := range sums {
		data, ok := store.get("dists/stable/" + relPath)
		assert.Check(t, ok, "Release references missing file %q", relPath)
		if !ok {
			continue
		}
		gotHash := sha256.Sum256(data)
		assert.Check(t, cmp.Equal(hex.EncodeToString(gotHash[:]), want.hash), "hash mismatch for %q", relPath)
		assert.Check(t, cmp.Equal(int64(len(data)), want.size), "size mismatch for %q", relPath)
	}
}

func TestAddArchitectureAllUsesBinaryAllBucket(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	pub := apt.New(store, apt.Config{Distribution: "stable"})

	err := pub.Add(ctx, "main", bytes.NewReader(debtest.Package(t, "docs", "1.0", "all")))
	assert.NilError(t, err)

	assert.Check(t, store.has("pool/main/d/docs/docs_1.0_all.deb"))
	assert.Check(t, store.has("dists/stable/main/binary-all/Packages"))

	release := getString(t, store, "dists/stable/Release")
	archLine := fieldLine(release, "Architectures")
	assert.Check(t, cmp.Contains(strings.Fields(archLine), "all"))
}

func TestRetentionPrunesOldVersions(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	pub := apt.New(store, apt.Config{Distribution: "stable", KeepVersions: 2})

	for _, v := range []string{"1.0", "1.1", "1.2"} {
		err := pub.Add(ctx, "main", bytes.NewReader(debtest.Package(t, "hello", v, "amd64")))
		assert.NilError(t, err)
	}

	pkgs := readPackages(t, store, "dists/stable/main/binary-amd64/Packages")
	got := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		got = append(got, p.Version)
	}
	sort.Strings(got)
	assert.Check(t, cmp.DeepEqual(got, []string{"1.1", "1.2"}))

	// The pruned version's pool object must be deleted, the kept ones retained.
	assert.Check(t, !store.has("pool/main/h/hello/hello_1.0_amd64.deb"), "old version should be pruned from the pool")
	assert.Check(t, store.has("pool/main/h/hello/hello_1.1_amd64.deb"))
	assert.Check(t, store.has("pool/main/h/hello/hello_1.2_amd64.deb"))
}

func TestSignerWritesInReleaseAndDetachedSignature(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	pub := apt.New(store, apt.Config{Distribution: "stable", Signer: fakeSigner{}})

	err := pub.Add(ctx, "main", bytes.NewReader(debtest.Package(t, "hello", "1.0", "amd64")))
	assert.NilError(t, err)

	inRelease := getString(t, store, "dists/stable/InRelease")
	assert.Check(t, strings.HasPrefix(inRelease, "-----CLEARSIGNED-----\n"))
	assert.Check(t, cmp.Contains(inRelease, "Suite: stable"))

	sig := getString(t, store, "dists/stable/Release.gpg")
	assert.Check(t, cmp.Equal(sig, "-----DETACHED SIGNATURE-----"))
}

// --- helpers ---

type fakeSigner struct{}

func (fakeSigner) ClearSign(_ context.Context, data []byte) ([]byte, error) {
	return append([]byte("-----CLEARSIGNED-----\n"), data...), nil
}

func (fakeSigner) DetachSign(_ context.Context, _ []byte) ([]byte, error) {
	return []byte("-----DETACHED SIGNATURE-----"), nil
}

func readPackages(t testing.TB, s *memStore, key string) []*deb.Package {
	t.Helper()
	data, ok := s.get(key)
	assert.Assert(t, ok, "missing index %s", key)
	pkgs, err := deb.ParseControlFile(data)
	assert.NilError(t, err)
	return pkgs
}

func getString(t testing.TB, s *memStore, key string) string {
	t.Helper()
	data, ok := s.get(key)
	assert.Assert(t, ok, "missing object %s", key)
	return string(data)
}

// fieldLine returns the value of a single-line Release field.
func fieldLine(release, name string) string {
	for _, line := range strings.Split(release, "\n") {
		if v, ok := strings.CutPrefix(line, name+": "); ok {
			return v
		}
	}
	return ""
}

type checksum struct {
	size int64
	hash string
}

// parseSHA256Section returns the path -> {size, hash} entries under the SHA256
// section of a Release file.
func parseSHA256Section(release string) map[string]checksum {
	out := make(map[string]checksum)
	inSection := false
	for _, line := range strings.Split(release, "\n") {
		if line == "SHA256:" {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, " ") {
			f := strings.Fields(line)
			if len(f) == 3 {
				size, _ := strconv.ParseInt(f[1], 10, 64)
				out[f[2]] = checksum{size: size, hash: f[0]}
			}
			continue
		}
		if inSection {
			break
		}
	}
	return out
}

// memStore is an in-memory storage.Store for fast, docker-free tests.
type memStore struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{objs: make(map[string][]byte)}
}

func (m *memStore) Put(_ context.Context, key string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = data
	return nil
}

func (m *memStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objs[key]
	if !ok {
		return nil, fmt.Errorf("memstore get %q: %w", key, storage.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, key)
	return nil
}

func (m *memStore) List(_ context.Context, prefix string) ([]storage.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	keys := make([]string, 0, len(m.objs))
	for k := range m.objs {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	objs := make([]storage.Object, 0, len(keys))
	for _, k := range keys {
		objs = append(objs, storage.Object{Key: k, Size: int64(len(m.objs[k]))})
	}
	return objs, nil
}

func (m *memStore) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objs[key]
	return ok
}

func (m *memStore) get(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objs[key]
	return data, ok
}
