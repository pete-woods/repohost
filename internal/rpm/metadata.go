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

package rpm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	librpm "github.com/cavaliergopher/rpm"
)

// fileFlagGhost is the RPMFILE_GHOST bit: a file the package owns but does not
// install (e.g. a log file created at runtime).
const fileFlagGhost = 1 << 6

// Dependency sense flags (RPMSENSE_*), as returned by Dependency.Flags. They
// encode the version comparison and, for requires, whether the dependency is a
// prerequisite or scriptlet dependency.
const (
	DepFlagLess    = 1 << 1
	DepFlagGreater = 1 << 2
	DepFlagEqual   = 1 << 3

	DepFlagPrereq       = 1 << 6
	DepFlagScriptPre    = 1 << 9
	DepFlagScriptPost   = 1 << 10
	DepFlagScriptPreUn  = 1 << 11
	DepFlagScriptPostUn = 1 << 12
)

// DepFlagPre is the mask of sense bits that mark a require as a prerequisite,
// rendered as pre="1" in primary.xml.
const DepFlagPre = DepFlagPrereq | DepFlagScriptPre | DepFlagScriptPost | DepFlagScriptPreUn | DepFlagScriptPostUn

// Dependency is a provides/requires/conflicts/obsoletes relationship declared
// by a package.
type Dependency struct {
	Name    string
	Flags   int
	Epoch   int
	Version string
	Release string
}

// File is a path installed (or owned) by a package.
type File struct {
	Path  string
	IsDir bool
	Ghost bool
}

// Package is the metadata parsed from a .rpm file. It isolates the underlying
// RPM library and carries everything the yum repository metadata needs.
type Package struct {
	Name         string
	Epoch        int
	Version      string
	Release      string
	Architecture string

	Summary     string
	Description string
	Packager    string
	URL         string
	License     string
	Vendor      string
	Group       string
	BuildHost   string
	BuildTime   int64
	SourceRPM   string

	PackageSize   int64 // size of the .rpm file itself
	InstalledSize int64 // disk space consumed once installed
	ArchiveSize   int64 // size of the uncompressed payload

	HeaderStart int // byte offset of the header within the .rpm
	HeaderEnd   int

	Provides  []Dependency
	Requires  []Dependency
	Conflicts []Dependency
	Obsoletes []Dependency

	Files []File

	// SHA256 is the checksum of the whole .rpm file; yum uses it as the pkgid.
	SHA256 string
}

// ParseRPM reads the header metadata from the bytes of a .rpm file.
func ParseRPM(data []byte) (*Package, error) {
	pkg, err := librpm.Read(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("rpm: %w", err)
	}

	start, end := pkg.HeaderRange()
	sum := sha256.Sum256(data)

	p := &Package{
		Name:          pkg.Name(),
		Epoch:         pkg.Epoch(),
		Version:       pkg.Version(),
		Release:       pkg.Release(),
		Architecture:  pkg.Architecture(),
		Summary:       pkg.Summary(),
		Description:   pkg.Description(),
		Packager:      pkg.Packager(),
		URL:           pkg.URL(),
		License:       pkg.License(),
		Vendor:        pkg.Vendor(),
		Group:         firstOf(pkg.Groups()),
		BuildHost:     pkg.BuildHost(),
		BuildTime:     pkg.BuildTime().Unix(),
		SourceRPM:     pkg.SourceRPM(),
		PackageSize:   int64(len(data)),
		InstalledSize: int64(pkg.Size()),        //nolint:gosec // rpm sizes fit int64
		ArchiveSize:   int64(pkg.ArchiveSize()), //nolint:gosec // rpm sizes fit int64
		HeaderStart:   start,
		HeaderEnd:     end,
		Provides:      convertDeps(pkg.Provides()),
		Requires:      convertDeps(pkg.Requires()),
		Conflicts:     convertDeps(pkg.Conflicts()),
		Obsoletes:     convertDeps(pkg.Obsoletes()),
		Files:         convertFiles(pkg.Files()),
		SHA256:        hex.EncodeToString(sum[:]),
	}

	if p.Name == "" || p.Version == "" || p.Release == "" || p.Architecture == "" {
		return nil, fmt.Errorf("rpm: missing required tag (name=%q version=%q release=%q arch=%q)",
			p.Name, p.Version, p.Release, p.Architecture)
	}
	return p, nil
}

// EVR returns the package's epoch:version-release string, suitable for
// CompareVersions.
func (p *Package) EVR() string {
	return fmt.Sprintf("%d:%s-%s", p.Epoch, p.Version, p.Release)
}

// FileName returns the conventional .rpm filename, name-version-release.arch.rpm.
func (p *Package) FileName() string {
	return fmt.Sprintf("%s-%s-%s.%s.rpm", p.Name, p.Version, p.Release, p.Architecture)
}

func convertDeps(in []librpm.Dependency) []Dependency {
	if len(in) == 0 {
		return nil
	}
	out := make([]Dependency, 0, len(in))
	for _, d := range in {
		out = append(out, Dependency{
			Name:    d.Name(),
			Flags:   d.Flags(),
			Epoch:   d.Epoch(),
			Version: d.Version(),
			Release: d.Release(),
		})
	}
	return out
}

func convertFiles(in []librpm.FileInfo) []File {
	if len(in) == 0 {
		return nil
	}
	out := make([]File, 0, len(in))
	for i := range in {
		f := &in[i]
		out = append(out, File{
			Path:  f.Name(),
			IsDir: f.IsDir(),
			Ghost: f.Flags()&fileFlagGhost != 0,
		})
	}
	return out
}

func firstOf(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
