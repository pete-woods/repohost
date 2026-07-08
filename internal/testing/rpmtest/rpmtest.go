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

// Package rpmtest builds minimal .rpm files for tests.
//
// There is no general-purpose RPM writer in Go, and the only consumer of these
// fixtures is github.com/cavaliergopher/rpm, which reads metadata from the
// header tags and never touches the cpio payload. So these files are
// header-only: a valid 96-byte lead, an empty signature header, and a main
// header carrying the tags we read. The payload is empty.
package rpmtest

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// RPM header tag identifiers (see rpm's rpmtag.h).
const (
	tagName           = 1000
	tagVersion        = 1001
	tagRelease        = 1002
	tagEpoch          = 1003
	tagSummary        = 1004
	tagDescription    = 1005
	tagBuildTime      = 1006
	tagBuildHost      = 1007
	tagSize           = 1009
	tagVendor         = 1011
	tagLicense        = 1014
	tagGroup          = 1016
	tagURL            = 1020
	tagArch           = 1022
	tagFileSizes      = 1028
	tagFileModes      = 1030
	tagFileDigests    = 1035
	tagFileLinks      = 1036
	tagFileFlags      = 1037
	tagFileMTimes     = 1034
	tagFileUser       = 1039
	tagFileGroup      = 1040
	tagSourceRPM      = 1044
	tagProvideFlags   = 1112
	tagProvideName    = 1047
	tagProvideVersion = 1113
	tagRequireFlags   = 1048
	tagRequireName    = 1049
	tagRequireVersion = 1050
	tagDirIndexes     = 1116
	tagBaseNames      = 1117
	tagDirNames       = 1118
)

// RPM tag data types.
const (
	typeInt32       = 4
	typeString      = 6
	typeStringArray = 8
	typeI18NString  = 9
)

// Dep is a provides/requires entry for a built package.
type Dep struct {
	Name    string
	Flags   int
	Version string // full EVR string, or empty for an unversioned dependency
}

// FileEntry is a file installed by a built package.
type FileEntry struct {
	Path  string
	Dir   bool
	Ghost bool
}

// Options describes the package to build. Only Name, Version, Release, and Arch
// are required.
type Options struct {
	Name          string
	Version       string
	Release       string
	Arch          string
	Epoch         int
	Summary       string
	Description   string
	License       string
	Vendor        string
	Group         string
	URL           string
	BuildHost     string
	SourceRPM     string
	InstalledSize int64
	Provides      []Dep
	Requires      []Dep
	Files         []FileEntry
}

// Package builds a minimal .rpm for the given identity.
func Package(t testing.TB, name, version, release, arch string) []byte {
	t.Helper()
	return Build(t, Options{Name: name, Version: version, Release: release, Arch: arch})
}

// Build assembles a .rpm from opts.
func Build(t testing.TB, opts Options) []byte {
	t.Helper()

	entries := mainHeaderTags(opts)

	var buf bytes.Buffer
	buf.Write(lead(opts.Name))
	buf.Write(encodeHeader(nil, true))      // empty signature header (padded)
	buf.Write(encodeHeader(entries, false)) // main header
	// Payload intentionally omitted; the reader stops at the payload start.
	return buf.Bytes()
}

// lead builds the 96-byte legacy lead.
func lead(name string) []byte {
	b := make([]byte, 96)
	b[0], b[1], b[2], b[3] = 0xED, 0xAB, 0xEE, 0xDB
	b[4] = 3 // version major (the reader requires 3 or 4)
	copy(b[10:76], name)
	binary.BigEndian.PutUint16(b[78:80], 5) // signature type: header-style
	return b
}

type tagEntry struct {
	tag   int
	typ   int
	count int
	data  []byte
}

func mainHeaderTags(opts Options) []tagEntry {
	var e []tagEntry
	add := func(entry tagEntry) { e = append(e, entry) }

	add(strTag(tagName, typeString, opts.Name))
	add(strTag(tagVersion, typeString, opts.Version))
	add(strTag(tagRelease, typeString, opts.Release))
	add(strTag(tagArch, typeString, opts.Arch))
	if opts.Epoch > 0 {
		add(int32Tag(tagEpoch, []int{opts.Epoch}))
	}
	if opts.InstalledSize > 0 {
		add(int32Tag(tagSize, []int{int(opts.InstalledSize)}))
	}
	add(int32Tag(tagBuildTime, []int{0}))

	for _, o := range []struct {
		tag int
		typ int
		val string
	}{
		{tagSummary, typeI18NString, opts.Summary},
		{tagDescription, typeI18NString, opts.Description},
		{tagLicense, typeString, opts.License},
		{tagVendor, typeString, opts.Vendor},
		{tagGroup, typeI18NString, opts.Group},
		{tagURL, typeString, opts.URL},
		{tagBuildHost, typeString, opts.BuildHost},
		{tagSourceRPM, typeString, opts.SourceRPM},
	} {
		if o.val != "" {
			add(strTag(o.tag, o.typ, o.val))
		}
	}

	if len(opts.Provides) > 0 {
		e = append(e, depTags(opts.Provides, tagProvideName, tagProvideFlags, tagProvideVersion)...)
	}
	if len(opts.Requires) > 0 {
		e = append(e, depTags(opts.Requires, tagRequireName, tagRequireFlags, tagRequireVersion)...)
	}
	if len(opts.Files) > 0 {
		e = append(e, fileTags(opts.Files)...)
	}
	return e
}

func depTags(deps []Dep, nameTag, flagsTag, versionTag int) []tagEntry {
	names := make([]string, len(deps))
	flags := make([]int, len(deps))
	versions := make([]string, len(deps))
	for i, d := range deps {
		names[i] = d.Name
		flags[i] = d.Flags
		versions[i] = d.Version
	}
	return []tagEntry{
		strArrayTag(nameTag, names),
		int32Tag(flagsTag, flags),
		strArrayTag(versionTag, versions),
	}
}

func fileTags(files []FileEntry) []tagEntry {
	var dirNames []string
	dirIndex := make(map[string]int)
	indexOf := func(dir string) int {
		if i, ok := dirIndex[dir]; ok {
			return i
		}
		i := len(dirNames)
		dirNames = append(dirNames, dir)
		dirIndex[dir] = i
		return i
	}

	n := len(files)
	baseNames := make([]string, n)
	dirIndexes := make([]int, n)
	modes := make([]int, n)
	sizes := make([]int, n)
	mtimes := make([]int, n)
	flags := make([]int, n)
	users := make([]string, n)
	groups := make([]string, n)
	digests := make([]string, n)
	links := make([]string, n)

	for i, f := range files {
		dir, base := splitPath(f.Path)
		baseNames[i] = base
		dirIndexes[i] = indexOf(dir)
		if f.Dir {
			modes[i] = 0o040755 // S_IFDIR | 0755
		} else {
			modes[i] = 0o100644 // S_IFREG | 0644
		}
		if f.Ghost {
			flags[i] = 1 << 6 // RPMFILE_GHOST
		}
		users[i] = "root"
		groups[i] = "root"
	}

	return []tagEntry{
		strArrayTag(tagBaseNames, baseNames),
		strArrayTag(tagDirNames, dirNames),
		int32Tag(tagDirIndexes, dirIndexes),
		int32Tag(tagFileModes, modes),
		int32Tag(tagFileSizes, sizes),
		int32Tag(tagFileMTimes, mtimes),
		int32Tag(tagFileFlags, flags),
		strArrayTag(tagFileUser, users),
		strArrayTag(tagFileGroup, groups),
		strArrayTag(tagFileDigests, digests),
		strArrayTag(tagFileLinks, links),
	}
}

// splitPath separates a file path into its directory (with trailing slash) and
// base name, matching how RPM stores DIRNAMES/BASENAMES.
func splitPath(p string) (dir, base string) {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return "", p
	}
	return p[:i+1], p[i+1:]
}

func strTag(tag, typ int, s string) tagEntry {
	return tagEntry{tag: tag, typ: typ, count: 1, data: append([]byte(s), 0)}
}

func strArrayTag(tag int, values []string) tagEntry {
	var data []byte
	for _, s := range values {
		data = append(data, s...)
		data = append(data, 0)
	}
	return tagEntry{tag: tag, typ: typeStringArray, count: len(values), data: data}
}

func int32Tag(tag int, values []int) tagEntry {
	data := make([]byte, 4*len(values))
	for i, v := range values {
		putU32(data[i*4:], v)
	}
	return tagEntry{tag: tag, typ: typeInt32, count: len(values), data: data}
}

// encodeHeader serializes an RPM header structure: a 16-byte header, one 16-byte
// index entry per tag, then the data store. When pad is set (signature header
// only) the store is padded to an 8-byte boundary.
func encodeHeader(entries []tagEntry, pad bool) []byte {
	var store bytes.Buffer
	index := make([]byte, 0, len(entries)*16)
	for _, e := range entries {
		idx := make([]byte, 16)
		putU32(idx[0:4], e.tag)
		putU32(idx[4:8], e.typ)
		putU32(idx[8:12], store.Len())
		putU32(idx[12:16], e.count)
		index = append(index, idx...)
		store.Write(e.data)
	}

	hdr := make([]byte, 16)
	hdr[0], hdr[1], hdr[2] = 0x8E, 0xAD, 0xE8
	hdr[3] = 1
	putU32(hdr[8:12], len(entries))
	putU32(hdr[12:16], store.Len())

	var buf bytes.Buffer
	buf.Write(hdr)
	buf.Write(index)
	buf.Write(store.Bytes())
	if pad {
		if p := (8 - store.Len()%8) % 8; p != 0 {
			buf.Write(make([]byte, p))
		}
	}
	return buf.Bytes()
}

func putU32(b []byte, v int) {
	binary.BigEndian.PutUint32(b, uint32(v)) //nolint:gosec // fixture values are small and non-negative
}
