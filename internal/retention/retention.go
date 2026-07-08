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

// Package retention provides the primitive for capping how many versions of a
// package are kept in a repository.
package retention

import "slices"

// Prune splits items into those to retain and those to remove so that at most
// keep items survive per group.
//
// Items are grouped by the key returned from group. Within each group, compare
// orders two items from oldest to newest (like strings.Compare: negative when a
// is older than b), and the newest keep items are retained while the remainder
// are returned for removal. Groups with keep or fewer members are retained in
// full. A keep of zero or less schedules every item for removal.
//
// The input order of items is preserved in both returned slices, and items that
// compare as equal are retained in input order.
func Prune[T any](items []T, keep int, group func(T) string, compare func(a, b T) int) (retain, remove []T) {
	grouped := make(map[string][]int)
	for i := range items {
		key := group(items[i])
		grouped[key] = append(grouped[key], i)
	}

	removeIdx := make(map[int]struct{})
	for _, idxs := range grouped {
		if len(idxs) <= keep {
			continue
		}
		// Order newest first by negating compare, then drop everything past the
		// keep-th item. SortStableFunc keeps equal versions in input order.
		ordered := slices.Clone(idxs)
		slices.SortStableFunc(ordered, func(a, b int) int {
			return compare(items[b], items[a])
		})
		drop := ordered
		if keep > 0 {
			drop = ordered[keep:]
		}
		for _, idx := range drop {
			removeIdx[idx] = struct{}{}
		}
	}

	retain = make([]T, 0, len(items)-len(removeIdx))
	remove = make([]T, 0, len(removeIdx))
	for i := range items {
		if _, dropped := removeIdx[i]; dropped {
			remove = append(remove, items[i])
		} else {
			retain = append(retain, items[i])
		}
	}
	return retain, remove
}
