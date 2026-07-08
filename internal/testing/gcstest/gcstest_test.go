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

package gcstest

import (
	"context"
	"io"
	"testing"

	"gotest.tools/v3/assert"
)

func TestSetup(t *testing.T) {
	ctx := context.Background()

	t.Run("some-set", func(t *testing.T) {
		fix := &Fixture{
			ProjectID: "repohost-test",
			Location:  "US",
			URL:       "http://localhost:9124/storage/v1/",
		}
		testSetup(ctx, t, fix)
	})

	t.Run("default", func(t *testing.T) {
		fix := Default(ctx, t)

		t.Run("is-versioned", func(t *testing.T) {
			assert.Check(t, fix.Versioned)
		})
	})

	t.Run("specific-bucket", func(t *testing.T) {
		fix := &Fixture{
			Bucket: "a-specific-bucket-gcs",
		}
		testSetup(ctx, t, fix)
	})

	t.Run("force-versioned", func(t *testing.T) {
		fix := &Fixture{
			ForceVersioned: true,
		}
		Setup(ctx, t, fix)
	})
}

func testSetup(ctx context.Context, t *testing.T, fix *Fixture) {
	const key = "the-key.txt"

	t.Run("setup-add-delete", func(t *testing.T) {
		Setup(ctx, t, fix)

		assert.Assert(t, t.Run("Check bucket is created", func(t *testing.T) {
			assert.Check(t, len(fix.Bucket) > 0)

			obj := fix.Client.Bucket(fix.Bucket).Object(key)

			t.Run("Upload object", func(t *testing.T) {
				w := obj.NewWriter(ctx)
				_, writeErr := io.WriteString(w, "the-body")
				assert.Check(t, writeErr)
				closeErr := w.Close()
				assert.Check(t, closeErr)
			})

			t.Run("Delete object", func(t *testing.T) {
				err := obj.Delete(ctx)
				assert.Check(t, err)
			})
		}))
	})

	t.Run("Check bucket is deleted", func(t *testing.T) {
		// The sibling subtest above registered the cleanup, which fires when it
		// returns — so by now the bucket is gone and the object is unreadable.
		_, err := fix.Client.Bucket(fix.Bucket).Object(key).Attrs(ctx)
		assert.Check(t, err != nil, "object should be unreadable after the bucket is deleted")
	})
}
