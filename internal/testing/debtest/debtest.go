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

// Package debtest builds minimal but valid .deb files for use in tests.
package debtest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	"gotest.tools/v3/assert"
)

// Compression selects how a .deb's control.tar member is compressed. It mirrors
// the compressors dpkg-deb can emit.
type Compression int

const (
	// Gzip is the legacy default and the default for this package's Build helper.
	Gzip Compression = iota
	// Xz is the dpkg-deb default (uniform compression) on Debian.
	Xz
	// Zstd is the default on Ubuntu.
	Zstd
	// None leaves control.tar uncompressed.
	None
)

func (c Compression) memberName(base string) string {
	switch c {
	case Xz:
		return base + ".xz"
	case Zstd:
		return base + ".zst"
	case None:
		return base
	case Gzip:
		return base + ".gz"
	default:
		return base + ".gz"
	}
}

// Control returns a minimal control paragraph for the given identity, including
// the Maintainer and Description fields a realistic package carries.
func Control(name, version, arch string) string {
	return fmt.Sprintf("Package: %s\n"+
		"Version: %s\n"+
		"Architecture: %s\n"+
		"Maintainer: Test <test@example.com>\n"+
		"Description: test package %s\n",
		name, version, arch, name)
}

// Package builds a .deb with a gzip control tarball for the given identity. It
// is shorthand for Build(t, Control(name, version, arch)).
func Package(t testing.TB, name, version, arch string) []byte {
	t.Helper()
	return Build(t, Control(name, version, arch))
}

// Build assembles a valid .deb with a gzip-compressed control tarball.
func Build(t testing.TB, control string) []byte {
	t.Helper()
	return BuildWith(t, control, Gzip)
}

// BuildWith assembles a valid .deb (an ar archive of debian-binary, the control
// tarball compressed with comp, and an empty gzipped data.tar) from the given
// control paragraph.
func BuildWith(t testing.TB, control string, comp Compression) []byte {
	t.Helper()
	if !strings.HasSuffix(control, "\n") {
		control += "\n"
	}

	controlTar := makeTar(t, "./control", []byte(control))
	dataTar := makeTar(t, "", nil)

	var buf bytes.Buffer
	buf.WriteString("!<arch>\n")
	writeArMember(t, &buf, "debian-binary", []byte("2.0\n"))
	writeArMember(t, &buf, comp.memberName("control.tar"), compress(t, controlTar, comp))
	writeArMember(t, &buf, "data.tar.gz", compress(t, dataTar, Gzip))
	return buf.Bytes()
}

// writeArMember appends a single ar member with a 60-byte header and 2-byte
// alignment padding.
func writeArMember(t testing.TB, buf *bytes.Buffer, name string, data []byte) {
	t.Helper()
	// name(16) mtime(12) uid(6) gid(6) mode(8) size(10) magic(2) = 60 bytes.
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d`\n", name, 0, 0, 0, "100644", len(data))
	assert.Assert(t, len(header) == 60, "ar header must be 60 bytes, got %d", len(header))

	buf.WriteString(header)
	buf.Write(data)
	if len(data)%2 == 1 {
		buf.WriteByte('\n')
	}
}

// makeTar returns an uncompressed tar containing a single regular file. When
// name is empty it returns an empty (but valid) tar, used for data.tar.
func makeTar(t testing.TB, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	if name != "" {
		err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
		})
		assert.NilError(t, err)
		_, err = tw.Write(data)
		assert.NilError(t, err)
	}

	err := tw.Close()
	assert.NilError(t, err)
	return buf.Bytes()
}

// compress returns data compressed with comp.
func compress(t testing.TB, data []byte, comp Compression) []byte {
	t.Helper()
	if comp == None {
		return data
	}

	var buf bytes.Buffer
	var w io.WriteCloser
	var err error
	switch comp {
	case Gzip:
		w = gzip.NewWriter(&buf)
	case Xz:
		w, err = xz.NewWriter(&buf)
		assert.NilError(t, err)
	case Zstd:
		w, err = zstd.NewWriter(&buf)
		assert.NilError(t, err)
	case None:
		return data
	default:
		t.Fatalf("debtest: unknown compression %d", comp)
	}

	_, err = w.Write(data)
	assert.NilError(t, err)
	err = w.Close()
	assert.NilError(t, err)
	return buf.Bytes()
}
