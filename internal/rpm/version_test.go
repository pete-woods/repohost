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
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCompareVersions(t *testing.T) {
	// want is the expected sign of Compare(a, b); each case is also checked in
	// the reverse direction, which must yield the negation. Many of these mirror
	// RPM's own rpmvercmp test vectors.
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "2.0", -1},
		{"2.0", "1.0", 1},
		{"1.0", "1.0.1", -1},      // extra numeric segment is newer
		{"1.0.1", "1.0", 1},       //
		{"1.0", "1", 1},           // "1.0" has an extra segment over "1"
		{"5.5p1", "5.5p2", -1},    // trailing numeric segment differs
		{"5.5p10", "5.5p2", 1},    // numeric, not lexical
		{"1.a", "1.0", -1},        // alphabetic sorts before numeric
		{"1.0001", "1.1", 0},      // leading zeros are ignored
		{"1b.0", "1a.0", 1},       // alpha segments compare lexically
		{"1.0~rc1", "1.0", -1},    // tilde is a pre-release, sorts before base
		{"1.0~rc1", "1.0~rc1", 0}, //
		{"1.0~rc1", "1.0~rc2", -1},
		{"1.0", "1.0^", -1},      // caret is a post-release, sorts after base
		{"1.0^", "1.0^git1", -1}, // caret followed by more content
		{"1:1.0", "2.0", 1},      // epoch dominates the version
		{"0:1.0", "1.0", 0},      // absent epoch is zero
		{"1.0-1", "1.0-2", -1},   // release compared when version is equal
		{"1.0-1", "1.0-1", 0},    //
		{"1.0-1.el8", "1.0-1.el9", -1},
	}

	for _, tc := range cases {
		got := rpm.CompareVersions(tc.a, tc.b)
		assert.Check(t, cmp.Equal(got, tc.want), "Compare(%q, %q)", tc.a, tc.b)

		rev := rpm.CompareVersions(tc.b, tc.a)
		assert.Check(t, cmp.Equal(rev, -tc.want), "Compare(%q, %q) (reversed)", tc.b, tc.a)
	}
}
