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

package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// maxControlSize caps how much of a control file we will read, guarding against
// a decompression bomb. Real control files are a few kilobytes.
const maxControlSize = 10 << 20

// Field is a single control field, preserving declaration order. Value holds the
// text after the first line's "Name:" (one leading space removed) with any folded
// continuation lines appended verbatim, separated by newlines.
type Field struct {
	Name  string
	Value string
}

// Package is the metadata parsed from a Debian control paragraph — either a
// .deb's control file or a stanza of a Packages index. Fields preserves the
// original field order so the paragraph can be re-emitted losslessly.
type Package struct {
	Name         string
	Version      string
	Architecture string
	Fields       []Field
}

// Get returns the value of the named field (matched case-insensitively) and
// whether it was present.
func (p *Package) Get(name string) (string, bool) {
	for _, f := range p.Fields {
		if strings.EqualFold(f.Name, name) {
			return f.Value, true
		}
	}
	return "", false
}

// ParseDeb extracts the control metadata from the bytes of a .deb file. The
// control tarball may be uncompressed or gzip-, xz-, or zstd-compressed, which
// together cover every compression dpkg-deb emits.
func ParseDeb(data []byte) (*Package, error) {
	members, err := readAr(data)
	if err != nil {
		return nil, err
	}

	for _, m := range members {
		if !strings.HasPrefix(m.name, "control.tar") {
			continue
		}
		control, err := readControl(m.name, m.data)
		if err != nil {
			return nil, err
		}
		return ParseControl(control)
	}

	return nil, errors.New("deb: no control.tar member found")
}

// readControl decompresses a control tarball according to its member name and
// returns the contents of its ./control file.
func readControl(name string, data []byte) ([]byte, error) {
	r, closeFn, err := decompress(name, data)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return findControlFile(r)
}

// decompress returns a reader over the tar stream inside a control member, along
// with a cleanup function to release the decompressor.
func decompress(name string, data []byte) (io.Reader, func(), error) {
	br := bytes.NewReader(data)
	noop := func() {}

	switch name {
	case "control.tar":
		return br, noop, nil
	case "control.tar.gz":
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, nil, fmt.Errorf("deb: control.tar.gz: %w", err)
		}
		return gz, func() { _ = gz.Close() }, nil
	case "control.tar.xz":
		xr, err := xz.NewReader(br)
		if err != nil {
			return nil, nil, fmt.Errorf("deb: control.tar.xz: %w", err)
		}
		return xr, noop, nil
	case "control.tar.zst":
		zr, err := zstd.NewReader(br)
		if err != nil {
			return nil, nil, fmt.Errorf("deb: control.tar.zst: %w", err)
		}
		return zr, zr.Close, nil
	default:
		return nil, nil, fmt.Errorf("deb: unsupported control archive %q", name)
	}
}

// findControlFile reads the ./control entry from a tar stream.
func findControlFile(r io.Reader) ([]byte, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("deb: reading control tar: %w", err)
		}
		if strings.TrimPrefix(hdr.Name, "./") == "control" {
			control, err := io.ReadAll(io.LimitReader(tr, maxControlSize))
			if err != nil {
				return nil, fmt.Errorf("deb: reading control file: %w", err)
			}
			return control, nil
		}
	}

	return nil, errors.New("deb: control file not found in control tarball")
}

// ParseControl parses a single control paragraph into a Package, requiring the
// Package, Version, and Architecture fields to be present.
func ParseControl(data []byte) (*Package, error) {
	fields, _, err := parseParagraph(splitLines(data), 0)
	if err != nil {
		return nil, err
	}
	return newPackage(fields)
}

// ParseControlFile parses a Packages-style file of blank-line-separated
// paragraphs into one Package per stanza.
func ParseControlFile(data []byte) ([]*Package, error) {
	lines := splitLines(data)
	var pkgs []*Package
	for pos := 0; pos < len(lines); {
		// Skip blank lines between paragraphs.
		if strings.TrimSpace(lines[pos]) == "" {
			pos++
			continue
		}
		fields, next, err := parseParagraph(lines, pos)
		if err != nil {
			return nil, err
		}
		pkg, err := newPackage(fields)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
		pos = next
	}
	return pkgs, nil
}

// FormatStanza renders fields as an RFC822 paragraph terminated by a newline,
// the inverse of parseParagraph.
func FormatStanza(fields []Field) []byte {
	var b strings.Builder
	for _, f := range fields {
		lines := strings.Split(f.Value, "\n")
		b.WriteString(f.Name)
		b.WriteByte(':')
		if lines[0] != "" {
			b.WriteByte(' ')
			b.WriteString(lines[0])
		}
		b.WriteByte('\n')
		for _, cont := range lines[1:] {
			b.WriteString(cont)
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}

func newPackage(fields []Field) (*Package, error) {
	p := &Package{Fields: fields}
	p.Name, _ = p.Get("Package")
	p.Version, _ = p.Get("Version")
	p.Architecture, _ = p.Get("Architecture")
	if p.Name == "" || p.Version == "" || p.Architecture == "" {
		return nil, fmt.Errorf("deb: control missing required field (Package=%q Version=%q Architecture=%q)",
			p.Name, p.Version, p.Architecture)
	}
	return p, nil
}

// parseParagraph consumes one paragraph starting at lines[start] and returns its
// fields plus the index of the line after the paragraph (the blank line or EOF).
func parseParagraph(lines []string, start int) (fields []Field, next int, err error) {
	pos := start
	for pos < len(lines) {
		line := lines[pos]
		if strings.TrimSpace(line) == "" {
			break
		}

		if line[0] == ' ' || line[0] == '\t' {
			if len(fields) == 0 {
				return nil, pos, fmt.Errorf("deb: control continuation line before any field: %q", line)
			}
			fields[len(fields)-1].Value += "\n" + line
			pos++
			continue
		}

		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			return nil, pos, fmt.Errorf("deb: malformed control line (no colon): %q", line)
		}
		name := line[:colon]
		value := strings.TrimPrefix(line[colon+1:], " ")
		fields = append(fields, Field{Name: name, Value: value})
		pos++
	}
	return fields, pos, nil
}

// splitLines splits on newlines and strips a trailing carriage return from each
// line so CRLF input is handled.
func splitLines(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}
