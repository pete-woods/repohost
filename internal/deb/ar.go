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
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	arMagic       = "!<arch>\n"
	arHeaderLen   = 60
	arSizeOffset  = 48
	arSizeLen     = 10
	arMagicOffset = 58
)

// arMember is a single entry in an ar archive.
type arMember struct {
	name string
	data []byte
}

// readAr parses a System V / GNU ar archive (the container format used by .deb
// files) and returns its members in order. It is intentionally minimal: it
// handles the fixed 60-byte headers and 2-byte alignment padding, and does not
// implement the GNU long-name extension, since .deb member names are short.
func readAr(data []byte) ([]arMember, error) {
	if !bytes.HasPrefix(data, []byte(arMagic)) {
		return nil, errors.New("deb: not an ar archive (bad magic)")
	}

	var members []arMember
	pos := len(arMagic)
	for pos < len(data) {
		if pos+arHeaderLen > len(data) {
			// A well-formed archive ends exactly on a member boundary; anything
			// left over that is only padding is tolerated.
			if strings.TrimSpace(string(data[pos:])) == "" {
				break
			}
			return nil, errors.New("deb: truncated ar header")
		}

		header := data[pos : pos+arHeaderLen]
		pos += arHeaderLen

		if string(header[arMagicOffset:arHeaderLen]) != "`\n" {
			return nil, errors.New("deb: bad ar header magic")
		}

		// Names are space-padded to 16 bytes; GNU ar also appends a trailing '/'.
		name := strings.TrimRight(string(header[0:16]), " ")
		name = strings.TrimSuffix(name, "/")

		sizeStr := strings.TrimSpace(string(header[arSizeOffset : arSizeOffset+arSizeLen]))
		size, err := strconv.Atoi(sizeStr)
		if err != nil {
			return nil, fmt.Errorf("deb: bad ar member size %q: %w", sizeStr, err)
		}
		if size < 0 || pos+size > len(data) {
			return nil, errors.New("deb: ar member size exceeds archive")
		}

		members = append(members, arMember{name: name, data: data[pos : pos+size]})
		pos += size

		// Members are padded to an even offset with a single newline.
		if size%2 == 1 {
			pos++
		}
	}

	return members, nil
}
