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

package retention_test

import (
	"testing"

	"github.com/pete-woods/repohost/internal/deb"
	"github.com/pete-woods/repohost/internal/retention"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// pkg is a minimal stand-in for a package artifact, grouped by name and
// architecture and ordered by its Debian version.
type pkg struct {
	Name    string
	Arch    string
	Version string
}

func groupPkg(p pkg) string { return p.Name + "/" + p.Arch }

func comparePkg(a, b pkg) int { return deb.CompareVersions(a.Version, b.Version) }

func TestPrune(t *testing.T) {
	// Deliberately unsorted, and interleaved across groups, to prove both that
	// the newest versions are kept regardless of input order and that input
	// order is otherwise preserved in the results.
	items := []pkg{
		{"foo", "amd64", "1.0"},
		{"bar", "amd64", "2.0"},
		{"foo", "amd64", "1.10"},
		{"foo", "arm64", "1.0"},
		{"foo", "amd64", "1.2"},
		{"foo", "amd64", "1.9"},
	}

	retain, remove := retention.Prune(items, 2, groupPkg, comparePkg)

	// foo/amd64 has four versions; the two newest by dpkg ordering are 1.10 and
	// 1.9 (numeric, so 1.10 > 1.9 > 1.2 > 1.0). Everything else is under the cap.
	wantRemove := []pkg{
		{"foo", "amd64", "1.0"},
		{"foo", "amd64", "1.2"},
	}
	assert.Check(t, cmp.DeepEqual(remove, wantRemove))

	wantRetain := []pkg{
		{"bar", "amd64", "2.0"},
		{"foo", "amd64", "1.10"},
		{"foo", "arm64", "1.0"},
		{"foo", "amd64", "1.9"},
	}
	assert.Check(t, cmp.DeepEqual(retain, wantRetain))
}

func TestPruneKeepsAllWhenUnderLimit(t *testing.T) {
	items := []pkg{
		{"foo", "amd64", "1.0"},
		{"foo", "amd64", "1.1"},
	}

	retain, remove := retention.Prune(items, 2, groupPkg, comparePkg)

	assert.Check(t, cmp.DeepEqual(retain, items))
	assert.Check(t, cmp.Len(remove, 0))
}

func TestPruneKeepOne(t *testing.T) {
	items := []pkg{
		{"foo", "amd64", "1.0"},
		{"foo", "amd64", "1.2"},
		{"foo", "amd64", "1.1"},
	}

	retain, remove := retention.Prune(items, 1, groupPkg, comparePkg)

	wantRetain := []pkg{{"foo", "amd64", "1.2"}}
	assert.Check(t, cmp.DeepEqual(retain, wantRetain))

	wantRemove := []pkg{
		{"foo", "amd64", "1.0"},
		{"foo", "amd64", "1.1"},
	}
	assert.Check(t, cmp.DeepEqual(remove, wantRemove))
}

func TestPruneKeepZeroRemovesEverything(t *testing.T) {
	items := []pkg{
		{"foo", "amd64", "1.0"},
		{"bar", "amd64", "2.0"},
	}

	retain, remove := retention.Prune(items, 0, groupPkg, comparePkg)

	assert.Check(t, cmp.Len(retain, 0))
	assert.Check(t, cmp.DeepEqual(remove, items))
}
