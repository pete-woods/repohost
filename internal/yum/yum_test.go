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

package yum_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"testing"

	"github.com/pete-woods/repohost/internal/rpm"
	"github.com/pete-woods/repohost/internal/testing/memstore"
	"github.com/pete-woods/repohost/internal/testing/rpmtest"
	"github.com/pete-woods/repohost/internal/yum"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestAddPublishesLayoutAndMetadata(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	pub := yum.New(store, yum.Config{})

	data := rpmtest.Build(t, rpmtest.Options{
		Name: "hello", Version: "2.10", Release: "1.el9", Arch: "x86_64",
		Summary: "A greeting", License: "GPLv3+",
		Provides: []rpmtest.Dep{{Name: "hello", Flags: rpm.DepFlagEqual, Version: "2.10-1.el9"}},
		Requires: []rpmtest.Dep{{Name: "libc.so.6"}},
		Files:    []rpmtest.FileEntry{{Path: "/usr/bin/hello"}},
	})
	sum := sha256.Sum256(data)
	pkgid := hex.EncodeToString(sum[:])

	err := pub.Add(ctx, bytes.NewReader(data))
	assert.NilError(t, err)

	assert.Check(t, store.Has("Packages/h/hello-2.10-1.el9.x86_64.rpm"))
	assert.Check(t, store.Has("repodata/repomd.xml"))
	assert.Check(t, store.Has("repodata/repohost-state.json"))

	md := parseRepomd(t, store)
	byType := map[string]repomdEntry{}
	for _, d := range md.Data {
		byType[d.Type] = d
	}
	for _, typ := range []string{"primary", "filelists", "other"} {
		_, ok := byType[typ]
		assert.Check(t, ok, "repomd missing %s", typ)
	}

	primaryXML := gunzip(t, mustData(t, store, byType["primary"].Location.Href))
	for _, want := range []string{
		`xmlns:rpm="http://linux.duke.edu/metadata/rpm"`,
		`<name>hello</name>`,
		`<arch>x86_64</arch>`,
		`ver="2.10"`,
		`rel="1.el9"`,
		`pkgid="YES"`,
		pkgid,
		`href="Packages/h/hello-2.10-1.el9.x86_64.rpm"`,
		`<rpm:header-range`,
		`<rpm:entry name="hello"`,
		`<file>/usr/bin/hello</file>`,
	} {
		assert.Check(t, cmp.Contains(string(primaryXML), want))
	}

	filelistsXML := gunzip(t, mustData(t, store, byType["filelists"].Location.Href))
	assert.Check(t, cmp.Contains(string(filelistsXML), `pkgid="`+pkgid+`"`))
	assert.Check(t, cmp.Contains(string(filelistsXML), `<file>/usr/bin/hello</file>`))
}

func TestRepomdChecksumsMatchStoredFiles(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	pub := yum.New(store, yum.Config{})

	err := pub.Add(ctx, bytes.NewReader(rpmtest.Package(t, "hello", "1.0", "1", "x86_64")))
	assert.NilError(t, err)

	md := parseRepomd(t, store)
	assert.Assert(t, len(md.Data) == 3)
	for _, d := range md.Data {
		gz := mustData(t, store, d.Location.Href)
		gzHash := sha256.Sum256(gz)
		assert.Check(t, cmp.Equal(hex.EncodeToString(gzHash[:]), d.Checksum.Value), "gz checksum for %s", d.Type)

		openHash := sha256.Sum256(gunzip(t, gz))
		assert.Check(t, cmp.Equal(hex.EncodeToString(openHash[:]), d.OpenChecksum.Value), "open-checksum for %s", d.Type)
	}
}

func TestRetentionPrunesOldVersions(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	pub := yum.New(store, yum.Config{KeepVersions: 1})

	for _, v := range []string{"1.0", "2.0"} {
		err := pub.Add(ctx, bytes.NewReader(rpmtest.Package(t, "hello", v, "1", "x86_64")))
		assert.NilError(t, err)
	}

	assert.Check(t, !store.Has("Packages/h/hello-1.0-1.x86_64.rpm"), "old version should be pruned")
	assert.Check(t, store.Has("Packages/h/hello-2.0-1.x86_64.rpm"))

	md := parseRepomd(t, store)
	byType := map[string]repomdEntry{}
	for _, d := range md.Data {
		byType[d.Type] = d
	}
	primaryXML := string(gunzip(t, mustData(t, store, byType["primary"].Location.Href)))
	assert.Check(t, cmp.Contains(primaryXML, `ver="2.0"`))
	assert.Check(t, !contains(primaryXML, `ver="1.0"`), "pruned version must not remain in primary.xml")
	assert.Check(t, cmp.Contains(primaryXML, `packages="1"`))
}

func TestSignerWritesRepomdSignature(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	pub := yum.New(store, yum.Config{Signer: fakeSigner{}})

	err := pub.Add(ctx, bytes.NewReader(rpmtest.Package(t, "hello", "1.0", "1", "x86_64")))
	assert.NilError(t, err)

	sig, ok := store.Data("repodata/repomd.xml.asc")
	assert.Assert(t, ok, "repomd.xml.asc must be written")
	assert.Check(t, cmp.Equal(string(sig), "-----DETACHED-----"))
}

// --- helpers ---

type fakeSigner struct{}

func (fakeSigner) ClearSign(_ context.Context, data []byte) ([]byte, error) { return data, nil }
func (fakeSigner) DetachSign(_ context.Context, _ []byte) ([]byte, error) {
	return []byte("-----DETACHED-----"), nil
}

type repomdDoc struct {
	Data []repomdEntry `xml:"data"`
}

type repomdEntry struct {
	Type     string `xml:"type,attr"`
	Checksum struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"checksum"`
	OpenChecksum struct {
		Value string `xml:",chardata"`
	} `xml:"open-checksum"`
	Location struct {
		Href string `xml:"href,attr"`
	} `xml:"location"`
}

func parseRepomd(t testing.TB, store *memstore.Store) repomdDoc {
	t.Helper()
	var doc repomdDoc
	err := xml.Unmarshal(mustData(t, store, "repodata/repomd.xml"), &doc)
	assert.NilError(t, err)
	return doc
}

func mustData(t testing.TB, store *memstore.Store, key string) []byte {
	t.Helper()
	data, ok := store.Data(key)
	assert.Assert(t, ok, "missing object %s", key)
	return data
}

func gunzip(t testing.TB, data []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	assert.NilError(t, err)
	out, err := io.ReadAll(gz)
	assert.NilError(t, err)
	return out
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
