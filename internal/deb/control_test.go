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

package deb_test

import (
	"testing"

	"github.com/pete-woods/repohost/internal/deb"
	"github.com/pete-woods/repohost/internal/testing/debtest"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestParseDeb(t *testing.T) {
	data := debtest.Package(t, "hello", "2.10-1", "amd64")

	pkg, err := deb.ParseDeb(data)
	assert.NilError(t, err)

	assert.Check(t, cmp.Equal(pkg.Name, "hello"))
	assert.Check(t, cmp.Equal(pkg.Version, "2.10-1"))
	assert.Check(t, cmp.Equal(pkg.Architecture, "amd64"))

	maintainer, ok := pkg.Get("Maintainer")
	assert.Check(t, ok)
	assert.Check(t, cmp.Equal(maintainer, "Test <test@example.com>"))
}

func TestParseDebCompressions(t *testing.T) {
	cases := []struct {
		name string
		comp debtest.Compression
	}{
		{"gzip", debtest.Gzip},
		{"xz", debtest.Xz},
		{"zstd", debtest.Zstd},
		{"uncompressed", debtest.None},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := debtest.BuildWith(t, debtest.Control("hello", "2.10-1", "amd64"), tc.comp)

			pkg, err := deb.ParseDeb(data)
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(pkg.Name, "hello"))
			assert.Check(t, cmp.Equal(pkg.Version, "2.10-1"))
			assert.Check(t, cmp.Equal(pkg.Architecture, "amd64"))
		})
	}
}

func TestParseDebRejectsUnknownControlCompression(t *testing.T) {
	// control.tar.lz (lzip) is unsupported. Its member name is the same length as
	// control.tar.gz, so it can be forged in place without disturbing offsets.
	data := debtest.Build(t, debtest.Control("hello", "1.0", "amd64"))
	forged := replaceArMemberName(t, data, "control.tar.gz", "control.tar.lz")

	_, err := deb.ParseDeb(forged)
	assert.Check(t, cmp.ErrorContains(err, "unsupported control archive"))
}

func TestParseControlFoldedFields(t *testing.T) {
	// Multi-line Description with a folded blank line (" .") must round-trip
	// through parse and FormatStanza unchanged.
	control := "Package: multi\n" +
		"Version: 1.0\n" +
		"Architecture: all\n" +
		"Description: short synopsis\n" +
		" first paragraph line\n" +
		" .\n" +
		" second paragraph line\n"

	pkg, err := deb.ParseControl([]byte(control))
	assert.NilError(t, err)

	desc, ok := pkg.Get("Description")
	assert.Check(t, ok)
	wantDesc := "short synopsis\n first paragraph line\n .\n second paragraph line"
	assert.Check(t, cmp.Equal(desc, wantDesc))

	formatted := deb.FormatStanza(pkg.Fields)
	assert.Check(t, cmp.Equal(string(formatted), control))
}

func TestParseControlFile(t *testing.T) {
	file := "Package: a\nVersion: 1.0\nArchitecture: amd64\n" +
		"\n" +
		"Package: b\nVersion: 2.0\nArchitecture: arm64\n"

	pkgs, err := deb.ParseControlFile([]byte(file))
	assert.NilError(t, err)
	assert.Assert(t, cmp.Len(pkgs, 2))
	assert.Check(t, cmp.Equal(pkgs[0].Name, "a"))
	assert.Check(t, cmp.Equal(pkgs[1].Name, "b"))
	assert.Check(t, cmp.Equal(pkgs[1].Architecture, "arm64"))
}

func TestParseControlMissingField(t *testing.T) {
	_, err := deb.ParseControl([]byte("Package: x\nVersion: 1.0\n"))
	assert.Check(t, cmp.ErrorContains(err, "missing required field"))
}

// replaceArMemberName rewrites a 16-byte ar member name in place, used to forge
// a .deb with an unsupported control compression. old and new must be the same
// length so surrounding offsets are unaffected.
func replaceArMemberName(t testing.TB, data []byte, oldName, newName string) []byte {
	t.Helper()
	assert.Assert(t, len(oldName) == len(newName), "names must match length")

	out := make([]byte, len(data))
	copy(out, data)
	idx := indexOf(out, []byte(oldName))
	assert.Assert(t, idx >= 0, "member %q not found", oldName)
	copy(out[idx:idx+len(newName)], newName)
	return out
}

func indexOf(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}
