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
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCompareVersions(t *testing.T) {
	// want is the expected sign of Compare(a, b); each case is also checked in
	// the reverse direction, which must yield the negation.
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"0:1.0", "1.0", 0},             // absent epoch is zero
		{"1.0-1", "1.0-1", 0},           // absent revision equals "0"
		{"2.0", "1.0", 1},               // upstream numeric
		{"1:0", "2.0", 1},               // epoch dominates
		{"1.10", "1.9", 1},              // digit runs compare numerically, not lexically
		{"1.0-2", "1.0-1", 1},           // revision compared
		{"1.0-1.1", "1.0-1", 1},         // '.' outranks end of string in the revision
		{"1.0a", "1.0", 1},              // trailing letter outranks end of string
		{"1.0~rc1", "1.0", -1},          // tilde sorts before end of string
		{"1.0~rc1", "1.0~rc2", -1},      // tilde-prefixed parts still compare normally
		{"1.0~~", "1.0~", -1},           // more tildes sort earlier
		{"1.0~", "1.0", -1},             // a single tilde still sorts before end of string
		{"1.0+deb1", "1.0", 1},          // '+' outranks end of string
		{"1:1.0~rc1", "1:1.0", -1},      // equal epochs fall through to upstream
		{"2.2.1-5", "2.2.1-5+deb1", -1}, // revision '+deb1' outranks end of string
		{"1.0", "1.0-0", 0},             // empty revision equals "0"
		{"1.0-alpha", "1.0-1", 1},       // 'alpha' letter outranks the digit boundary
	}

	for _, tc := range cases {
		got := deb.CompareVersions(tc.a, tc.b)
		assert.Check(t, cmp.Equal(got, tc.want), "Compare(%q, %q)", tc.a, tc.b)

		rev := deb.CompareVersions(tc.b, tc.a)
		assert.Check(t, cmp.Equal(rev, -tc.want), "Compare(%q, %q) (reversed)", tc.b, tc.a)
	}
}
