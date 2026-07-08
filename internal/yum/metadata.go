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

package yum

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/pete-woods/repohost/internal/rpm"
)

// Metadata XML namespaces (createrepo_c).
const (
	nsCommon    = "http://linux.duke.edu/metadata/common"
	nsFilelists = "http://linux.duke.edu/metadata/filelists"
	nsOther     = "http://linux.duke.edu/metadata/other"
	nsRepo      = "http://linux.duke.edu/metadata/repo"
	nsRPM       = "http://linux.duke.edu/metadata/rpm"
)

// The rpm: prefixed elements are emitted with a struct-tag prefix hack plus an
// explicit xmlns:rpm attribute on the root. This produces createrepo_c-style
// output; the files are only ever marshaled (state is kept separately), so the
// encoding/xml unmarshal asymmetry around prefixes does not apply.

// --- repomd.xml ---

type repomd struct {
	XMLName  xml.Name     `xml:"repomd"`
	Xmlns    string       `xml:"xmlns,attr"`
	XmlnsRPM string       `xml:"xmlns:rpm,attr"`
	Revision int64        `xml:"revision"`
	Data     []repomdData `xml:"data"`
}

type repomdData struct {
	Type         string    `xml:"type,attr"`
	Checksum     hashValue `xml:"checksum"`
	OpenChecksum hashValue `xml:"open-checksum"`
	Location     location  `xml:"location"`
	Timestamp    int64     `xml:"timestamp"`
	Size         int64     `xml:"size"`
	OpenSize     int64     `xml:"open-size"`
}

type hashValue struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type location struct {
	Href string `xml:"href,attr"`
}

// --- primary.xml ---

type primaryMetadata struct {
	XMLName  xml.Name         `xml:"metadata"`
	Xmlns    string           `xml:"xmlns,attr"`
	XmlnsRPM string           `xml:"xmlns:rpm,attr"`
	Packages int              `xml:"packages,attr"`
	Package  []primaryPackage `xml:"package"`
}

type primaryPackage struct {
	Type        string          `xml:"type,attr"`
	Name        string          `xml:"name"`
	Arch        string          `xml:"arch"`
	Version     evr             `xml:"version"`
	Checksum    primaryChecksum `xml:"checksum"`
	Summary     string          `xml:"summary"`
	Description string          `xml:"description"`
	Packager    string          `xml:"packager,omitempty"`
	URL         string          `xml:"url,omitempty"`
	Time        primaryTime     `xml:"time"`
	Size        primarySize     `xml:"size"`
	Location    location        `xml:"location"`
	Format      primaryFormat   `xml:"format"`
}

type evr struct {
	Epoch int    `xml:"epoch,attr"`
	Ver   string `xml:"ver,attr"`
	Rel   string `xml:"rel,attr"`
}

type primaryChecksum struct {
	Type  string `xml:"type,attr"`
	PkgID string `xml:"pkgid,attr"`
	Value string `xml:",chardata"`
}

type primaryTime struct {
	File  int64 `xml:"file,attr"`
	Build int64 `xml:"build,attr"`
}

type primarySize struct {
	Package   int64 `xml:"package,attr"`
	Installed int64 `xml:"installed,attr"`
	Archive   int64 `xml:"archive,attr"`
}

type primaryFormat struct {
	License     string        `xml:"rpm:license,omitempty"`
	Vendor      string        `xml:"rpm:vendor,omitempty"`
	Group       string        `xml:"rpm:group,omitempty"`
	BuildHost   string        `xml:"rpm:buildhost,omitempty"`
	SourceRPM   string        `xml:"rpm:sourcerpm,omitempty"`
	HeaderRange headerRange   `xml:"rpm:header-range"`
	Provides    *entryList    `xml:"rpm:provides"`
	Requires    *entryList    `xml:"rpm:requires"`
	Conflicts   *entryList    `xml:"rpm:conflicts"`
	Obsoletes   *entryList    `xml:"rpm:obsoletes"`
	Files       []primaryFile `xml:"file"`
}

type headerRange struct {
	Start int `xml:"start,attr"`
	End   int `xml:"end,attr"`
}

type entryList struct {
	Entries []rpmEntry `xml:"rpm:entry"`
}

type rpmEntry struct {
	Name  string `xml:"name,attr"`
	Flags string `xml:"flags,attr,omitempty"`
	Epoch string `xml:"epoch,attr,omitempty"`
	Ver   string `xml:"ver,attr,omitempty"`
	Rel   string `xml:"rel,attr,omitempty"`
	Pre   string `xml:"pre,attr,omitempty"`
}

type primaryFile struct {
	Type string `xml:"type,attr,omitempty"`
	Path string `xml:",chardata"`
}

// --- filelists.xml ---

type filelists struct {
	XMLName  xml.Name           `xml:"filelists"`
	Xmlns    string             `xml:"xmlns,attr"`
	Packages int                `xml:"packages,attr"`
	Package  []filelistsPackage `xml:"package"`
}

type filelistsPackage struct {
	PkgID   string      `xml:"pkgid,attr"`
	Name    string      `xml:"name,attr"`
	Arch    string      `xml:"arch,attr"`
	Version evr         `xml:"version"`
	Files   []fileEntry `xml:"file"`
}

type fileEntry struct {
	Type string `xml:"type,attr,omitempty"`
	Path string `xml:",chardata"`
}

// --- other.xml ---

type otherdata struct {
	XMLName  xml.Name       `xml:"otherdata"`
	Xmlns    string         `xml:"xmlns,attr"`
	Packages int            `xml:"packages,attr"`
	Package  []otherPackage `xml:"package"`
}

type otherPackage struct {
	PkgID   string `xml:"pkgid,attr"`
	Name    string `xml:"name,attr"`
	Arch    string `xml:"arch,attr"`
	Version evr    `xml:"version"`
	// Changelogs are omitted: the RPM library exposes them only as unstructured
	// strings, and dnf install does not need them.
}

// buildPrimary renders the primary metadata document for the given packages.
func buildPrimary(entries []packageEntry) *primaryMetadata {
	pkgs := make([]primaryPackage, 0, len(entries))
	for _, e := range entries {
		m := e.Meta
		pkgs = append(pkgs, primaryPackage{
			Type:        "rpm",
			Name:        m.Name,
			Arch:        m.Architecture,
			Version:     evr{Epoch: m.Epoch, Ver: m.Version, Rel: m.Release},
			Checksum:    primaryChecksum{Type: "sha256", PkgID: "YES", Value: m.SHA256},
			Summary:     m.Summary,
			Description: m.Description,
			Packager:    m.Packager,
			URL:         m.URL,
			Time:        primaryTime{File: m.BuildTime, Build: m.BuildTime},
			Size:        primarySize{Package: m.PackageSize, Installed: m.InstalledSize, Archive: m.ArchiveSize},
			Location:    location{Href: e.Location},
			Format: primaryFormat{
				License:     m.License,
				Vendor:      m.Vendor,
				Group:       m.Group,
				BuildHost:   m.BuildHost,
				SourceRPM:   m.SourceRPM,
				HeaderRange: headerRange{Start: m.HeaderStart, End: m.HeaderEnd},
				Provides:    entriesOf(m.Provides, false),
				Requires:    entriesOf(m.Requires, true),
				Conflicts:   entriesOf(m.Conflicts, false),
				Obsoletes:   entriesOf(m.Obsoletes, false),
				Files:       primaryFiles(m.Files),
			},
		})
	}
	return &primaryMetadata{
		Xmlns:    nsCommon,
		XmlnsRPM: nsRPM,
		Packages: len(pkgs),
		Package:  pkgs,
	}
}

// buildFilelists renders the filelists document (all files per package).
func buildFilelists(entries []packageEntry) *filelists {
	pkgs := make([]filelistsPackage, 0, len(entries))
	for _, e := range entries {
		m := e.Meta
		files := make([]fileEntry, 0, len(m.Files))
		for _, f := range m.Files {
			files = append(files, fileEntry{Type: fileType(f), Path: f.Path})
		}
		pkgs = append(pkgs, filelistsPackage{
			PkgID:   m.SHA256,
			Name:    m.Name,
			Arch:    m.Architecture,
			Version: evr{Epoch: m.Epoch, Ver: m.Version, Rel: m.Release},
			Files:   files,
		})
	}
	return &filelists{Xmlns: nsFilelists, Packages: len(pkgs), Package: pkgs}
}

// buildOther renders the other document (package identities; no changelogs).
func buildOther(entries []packageEntry) *otherdata {
	pkgs := make([]otherPackage, 0, len(entries))
	for _, e := range entries {
		m := e.Meta
		pkgs = append(pkgs, otherPackage{
			PkgID:   m.SHA256,
			Name:    m.Name,
			Arch:    m.Architecture,
			Version: evr{Epoch: m.Epoch, Ver: m.Version, Rel: m.Release},
		})
	}
	return &otherdata{Xmlns: nsOther, Packages: len(pkgs), Package: pkgs}
}

// entriesOf converts a dependency slice to a primary entry list, or nil when
// empty (so the element is omitted).
func entriesOf(deps []rpm.Dependency, isRequire bool) *entryList {
	if len(deps) == 0 {
		return nil
	}
	entries := make([]rpmEntry, 0, len(deps))
	for _, d := range deps {
		e := rpmEntry{Name: d.Name}
		if flags := senseFlags(d.Flags); flags != "" {
			e.Flags = flags
			e.Epoch = strconv.Itoa(d.Epoch)
			e.Ver = d.Version
			e.Rel = d.Release
		}
		if isRequire && d.Flags&rpm.DepFlagPre != 0 {
			e.Pre = "1"
		}
		entries = append(entries, e)
	}
	return &entryList{Entries: entries}
}

// senseFlags maps RPM sense bits to the createrepo flags string, or "" when the
// dependency carries no version comparison.
func senseFlags(flags int) string {
	eq := flags&rpm.DepFlagEqual != 0
	lt := flags&rpm.DepFlagLess != 0
	gt := flags&rpm.DepFlagGreater != 0
	switch {
	case lt && eq:
		return "LE"
	case gt && eq:
		return "GE"
	case lt:
		return "LT"
	case gt:
		return "GT"
	case eq:
		return "EQ"
	default:
		return ""
	}
}

// primaryFiles returns the subset of files createrepo records in primary.xml:
// files under a bin directory, under /etc, or /usr/lib/sendmail.
func primaryFiles(files []rpm.File) []primaryFile {
	var out []primaryFile
	for _, f := range files {
		if !isPrimaryFile(f.Path) {
			continue
		}
		out = append(out, primaryFile{Type: fileType(f), Path: f.Path})
	}
	return out
}

func isPrimaryFile(path string) bool {
	return strings.Contains(path, "bin/") ||
		strings.HasPrefix(path, "/etc/") ||
		path == "/usr/lib/sendmail"
}

func fileType(f rpm.File) string {
	switch {
	case f.Ghost:
		return "ghost"
	case f.IsDir:
		return "dir"
	default:
		return ""
	}
}
