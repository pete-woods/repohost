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

// Package deb provides helpers for working with Debian .deb artifacts. For now
// it implements dpkg version comparison, used to decide which package versions
// to retain.
package deb

import (
	"strconv"
	"strings"
)

// CompareVersions compares two Debian version strings using the dpkg algorithm
// (Debian Policy 5.6.12). Each version has the form [epoch:]upstream[-revision].
// It returns -1 if a sorts before b, +1 if a sorts after b, and 0 if they are
// equal.
func CompareVersions(a, b string) int {
	epochA, upstreamA, revisionA := split(a)
	epochB, upstreamB, revisionB := split(b)

	if epochA != epochB {
		return sign(epochA - epochB)
	}
	if c := verrevcmp(upstreamA, upstreamB); c != 0 {
		return c
	}
	return verrevcmp(revisionA, revisionB)
}

// split separates a version into its epoch, upstream version, and Debian
// revision. A missing epoch is 0 and a missing revision is empty (which
// compares equal to "0"). Input that does not conform (for example a
// non-numeric epoch) is treated leniently so comparison never fails.
func split(v string) (epoch int, upstream, revision string) {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		if e, err := strconv.Atoi(v[:i]); err == nil {
			epoch = e
			v = v[i+1:]
		}
	}

	upstream = v
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		upstream = v[:i]
		revision = v[i+1:]
	}
	return epoch, upstream, revision
}

// verrevcmp is a direct port of dpkg's verrevcmp: it compares two version
// components (an upstream version or a revision) segment by segment, alternating
// between non-digit and digit runs.
func verrevcmp(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		firstDiff := 0

		// Compare the leading non-digit run character by character.
		for (i < len(a) && !isDigit(a[i])) || (j < len(b) && !isDigit(b[j])) {
			ac := 0
			if i < len(a) {
				ac = order(a[i])
			}
			bc := 0
			if j < len(b) {
				bc = order(b[j])
			}
			if ac != bc {
				return sign(ac - bc)
			}
			i++
			j++
		}

		// Numeric runs compare by value, so strip leading zeros first.
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}

		// The longer digit run is the larger number; firstDiff breaks ties of
		// equal length.
		for i < len(a) && isDigit(a[i]) && j < len(b) && isDigit(b[j]) {
			if firstDiff == 0 {
				firstDiff = int(a[i]) - int(b[j])
			}
			i++
			j++
		}
		if i < len(a) && isDigit(a[i]) {
			return 1
		}
		if j < len(b) && isDigit(b[j]) {
			return -1
		}
		if firstDiff != 0 {
			return sign(firstDiff)
		}
	}
	return 0
}

// order maps a byte to its dpkg sort weight. A digit weighs 0, the same as the
// end of a string, so that reaching a digit is treated as the non-digit run
// having ended. Letters sort by their ASCII value, '~' sorts before everything
// (including the end of string), and any other character sorts after letters.
func order(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return 0
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return int(c)
	case c == '~':
		return -1
	default:
		return int(c) + 256
	}
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
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
