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

package s3store_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/pete-woods/repohost/internal/storage"
	"github.com/pete-woods/repohost/internal/storage/s3store"
	"github.com/pete-woods/repohost/internal/testing/s3test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestStore(t *testing.T) {
	ctx := context.Background()
	fix := s3test.Default(ctx, t)
	store := s3store.New(fix.Client, fix.Bucket)

	// Put a handful of objects across two prefixes.
	contents := map[string]string{
		"pool/a/one.txt": "hello",
		"pool/a/two.txt": "world",
		"pool/b/three":   "!",
	}
	for key, body := range contents {
		err := store.Put(ctx, key, strings.NewReader(body))
		assert.NilError(t, err, "put %q", key)
	}

	t.Run("Get returns the stored body", func(t *testing.T) {
		rc, err := store.Get(ctx, "pool/a/one.txt")
		assert.NilError(t, err)
		defer func() { _ = rc.Close() }()

		data, err := io.ReadAll(rc)
		assert.NilError(t, err)
		got := string(data)
		assert.Check(t, cmp.Equal(got, "hello"))
	})

	t.Run("Get of a missing key reports ErrNotExist", func(t *testing.T) {
		_, err := store.Get(ctx, "pool/a/missing.txt")
		assert.Check(t, cmp.ErrorIs(err, storage.ErrNotExist))
	})

	t.Run("List is scoped to the prefix", func(t *testing.T) {
		objs, err := store.List(ctx, "pool/a/")
		assert.NilError(t, err)

		keys := objectKeys(objs)
		assert.Check(t, cmp.DeepEqual(keys, []string{"pool/a/one.txt", "pool/a/two.txt"}))
	})

	t.Run("List of the whole store returns everything", func(t *testing.T) {
		objs, err := store.List(ctx, "")
		assert.NilError(t, err)
		assert.Check(t, cmp.Len(objs, len(contents)))
	})

	t.Run("Delete is idempotent", func(t *testing.T) {
		err := store.Delete(ctx, "pool/a/one.txt")
		assert.NilError(t, err)

		// Deleting an already-absent key must not error.
		err = store.Delete(ctx, "pool/a/one.txt")
		assert.NilError(t, err)

		_, err = store.Get(ctx, "pool/a/one.txt")
		assert.Check(t, cmp.ErrorIs(err, storage.ErrNotExist))
	})
}

// objectKeys extracts the keys from a list result, which S3 returns in
// lexicographic order.
func objectKeys(objs []storage.Object) []string {
	keys := make([]string, 0, len(objs))
	for _, o := range objs {
		keys = append(keys, o.Key)
	}
	return keys
}
