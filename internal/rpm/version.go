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

// Package rpm provides helpers for working with RPM artifacts. For now it
// implements RPM version comparison (rpmvercmp), used to decide which package
// versions to retain.
package rpm

import (
	"strconv"
	"strings"
)

// CompareVersions compares two RPM version strings using the rpmvercmp
// algorithm. Each string may be a full EVR of the form [epoch:]version[-release].
// It returns -1 if a sorts before b, +1 if a sorts after b, and 0 if they are
// equal.
func CompareVersions(a, b string) int {
	epochA, versionA, releaseA := splitEVR(a)
	epochB, versionB, releaseB := splitEVR(b)

	if epochA != epochB {
		return sign(epochA - epochB)
	}
	if c := vercmp(versionA, versionB); c != 0 {
		return c
	}
	return vercmp(releaseA, releaseB)
}

// splitEVR separates a string into epoch, version, and release. A missing epoch
// is 0; a missing release is empty. Non-conforming input (for example a
// non-numeric epoch) is treated leniently so comparison never fails.
func splitEVR(s string) (epoch int, version, release string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		if e, err := strconv.Atoi(s[:i]); err == nil {
			epoch = e
			s = s[i+1:]
		}
	}

	version = s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		version = s[:i]
		release = s[i+1:]
	}
	return epoch, version, release
}

// vercmp is a port of RPM's rpmvercmp. It walks both strings segment by segment,
// where a segment is a maximal run of digits or of letters, and separators
// between segments are ignored. A '~' sorts before everything (pre-releases) and
// a '^' sorts after the base version (post-releases).
func vercmp(a, b string) int {
	if a == b {
		return 0
	}

	i, j := 0, 0
	for i < len(a) || j < len(b) {
		i = skipSeparators(a, i)
		j = skipSeparators(b, j)

		if r, matched := compareTilde(a, i, b, j); matched {
			if r != 0 {
				return r
			}
			i++
			j++
			continue
		}
		if r, matched := compareCaret(a, i, b, j); matched {
			if r != 0 {
				return r
			}
			i++
			j++
			continue
		}

		// If either side ran out, the loop is finished.
		if i >= len(a) || j >= len(b) {
			break
		}

		ni, nj, r := compareSegment(a, i, b, j)
		if r != 0 {
			return r
		}
		i, j = ni, nj
	}

	return endResult(a, i, b, j)
}

// skipSeparators advances past any characters that are neither alphanumeric nor
// one of the '~'/'^' separators that carry ordering meaning.
func skipSeparators(s string, i int) int {
	for i < len(s) && !isAlnum(s[i]) && s[i] != '~' && s[i] != '^' {
		i++
	}
	return i
}

// compareTilde handles the '~' separator, which sorts before everything else
// including the end of a string. matched reports whether a tilde was involved;
// when it is and the returned int is zero, both sides had a tilde and the caller
// should advance past it.
func compareTilde(a string, i int, b string, j int) (result int, matched bool) {
	aTilde := i < len(a) && a[i] == '~'
	bTilde := j < len(b) && b[j] == '~'
	switch {
	case !aTilde && !bTilde:
		return 0, false
	case !aTilde:
		return 1, true
	case !bTilde:
		return -1, true
	default:
		return 0, true
	}
}

// compareCaret handles the '^' separator. It behaves like tilde except that a
// string ending is lower than a '^', so a base version sorts before its
// post-release.
func compareCaret(a string, i int, b string, j int) (result int, matched bool) {
	aCaret := i < len(a) && a[i] == '^'
	bCaret := j < len(b) && b[j] == '^'
	switch {
	case !aCaret && !bCaret:
		return 0, false
	case i >= len(a):
		return -1, true
	case j >= len(b):
		return 1, true
	case !aCaret:
		return 1, true
	case !bCaret:
		return -1, true
	default:
		return 0, true
	}
}

// compareSegment consumes one maximal digit or letter run from each side and
// returns the advanced indices and the comparison result. A result of zero means
// the segments were equal and the caller should continue with the returned
// indices. The type of run is chosen from the first side, mirroring rpmvercmp.
func compareSegment(a string, i int, b string, j int) (ni, nj, result int) {
	startA, startB := i, j
	isNum := isDigit(a[i])
	if isNum {
		for i < len(a) && isDigit(a[i]) {
			i++
		}
		for j < len(b) && isDigit(b[j]) {
			j++
		}
	} else {
		for i < len(a) && isAlpha(a[i]) {
			i++
		}
		for j < len(b) && isAlpha(b[j]) {
			j++
		}
	}

	segA := a[startA:i]
	segB := b[startB:j]

	// A run type that only matched on one side means the sides differ in type;
	// numeric always outranks alphabetic.
	if len(segB) == 0 {
		if isNum {
			return i, j, 1
		}
		return i, j, -1
	}

	if isNum {
		segA = strings.TrimLeft(segA, "0")
		segB = strings.TrimLeft(segB, "0")
		if len(segA) != len(segB) {
			if len(segA) > len(segB) {
				return i, j, 1
			}
			return i, j, -1
		}
	}
	return i, j, sign(strings.Compare(segA, segB))
}

// endResult resolves the comparison once at least one side is exhausted: the
// side with characters remaining is the larger version.
func endResult(a string, i int, b string, j int) int {
	switch {
	case i >= len(a) && j >= len(b):
		return 0
	case i >= len(a):
		return -1
	default:
		return 1
	}
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAlnum(c byte) bool {
	return isDigit(c) || isAlpha(c)
}

func sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	default:
		return 0
	}
}
