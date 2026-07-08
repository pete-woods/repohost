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

package rpm_test

import (
	"testing"

	"github.com/pete-woods/repohost/internal/rpm"
	"github.com/pete-woods/repohost/internal/testing/rpmtest"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestParseRPM(t *testing.T) {
	data := rpmtest.Build(t, rpmtest.Options{
		Name:          "hello",
		Version:       "2.10",
		Release:       "3.el9",
		Arch:          "x86_64",
		Epoch:         1,
		Summary:       "A greeting",
		License:       "GPLv3+",
		Vendor:        "Acme",
		InstalledSize: 4096,
		Provides: []rpmtest.Dep{
			{Name: "hello", Flags: rpm.DepFlagEqual, Version: "1:2.10-3.el9"},
			{Name: "hello(x86-64)", Flags: rpm.DepFlagEqual, Version: "1:2.10-3.el9"},
		},
		Requires: []rpmtest.Dep{
			{Name: "libc.so.6"},
			{Name: "rtld(GNU_HASH)"},
		},
		Files: []rpmtest.FileEntry{
			{Path: "/usr/bin/hello"},
			{Path: "/usr/share/doc/hello", Dir: true},
			{Path: "/var/log/hello.log", Ghost: true},
		},
	})

	pkg, err := rpm.ParseRPM(data)
	assert.NilError(t, err)

	assert.Check(t, cmp.Equal(pkg.Name, "hello"))
	assert.Check(t, cmp.Equal(pkg.Epoch, 1))
	assert.Check(t, cmp.Equal(pkg.Version, "2.10"))
	assert.Check(t, cmp.Equal(pkg.Release, "3.el9"))
	assert.Check(t, cmp.Equal(pkg.Architecture, "x86_64"))
	assert.Check(t, cmp.Equal(pkg.Summary, "A greeting"))
	assert.Check(t, cmp.Equal(pkg.License, "GPLv3+"))
	assert.Check(t, cmp.Equal(pkg.InstalledSize, int64(4096)))
	assert.Check(t, cmp.Equal(pkg.PackageSize, int64(len(data))))
	assert.Check(t, cmp.Equal(pkg.EVR(), "1:2.10-3.el9"))
	assert.Check(t, cmp.Equal(pkg.FileName(), "hello-2.10-3.el9.x86_64.rpm"))
	assert.Check(t, len(pkg.SHA256) == 64)

	// Header range should point past the lead (96) + signature header.
	assert.Check(t, pkg.HeaderStart > 96)
	assert.Check(t, pkg.HeaderEnd > pkg.HeaderStart)

	assert.Assert(t, cmp.Len(pkg.Provides, 2))
	assert.Check(t, cmp.Equal(pkg.Provides[0].Name, "hello"))
	assert.Check(t, cmp.Equal(pkg.Provides[0].Flags, rpm.DepFlagEqual))
	assert.Check(t, cmp.Equal(pkg.Provides[0].Version, "2.10"))
	assert.Check(t, cmp.Equal(pkg.Provides[0].Epoch, 1))

	assert.Assert(t, cmp.Len(pkg.Requires, 2))
	assert.Check(t, cmp.Equal(pkg.Requires[0].Name, "libc.so.6"))

	assert.Assert(t, cmp.Len(pkg.Files, 3))
	byPath := make(map[string]rpm.File)
	for _, f := range pkg.Files {
		byPath[f.Path] = f
	}
	assert.Check(t, cmp.Equal(byPath["/usr/bin/hello"].IsDir, false))
	assert.Check(t, cmp.Equal(byPath["/usr/share/doc/hello"].IsDir, true))
	assert.Check(t, cmp.Equal(byPath["/var/log/hello.log"].Ghost, true))
}

func TestParseRPMRejectsNonRPM(t *testing.T) {
	_, err := rpm.ParseRPM([]byte("not an rpm"))
	assert.Check(t, err != nil)
}
