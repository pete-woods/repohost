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

package s3test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestSetup(t *testing.T) {
	ctx := context.Background()

	t.Run("some-set", func(t *testing.T) {
		fix := &Fixture{
			Key:    "seaweed",
			Secret: "seaweed123",
			Region: "us-east-2",
			URL:    "http://localhost:9123",
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
			Bucket: "a-specific-bucket-seaweed",
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
	key := aws.String("the-key.txt")

	t.Run("setup-add-delete", func(t *testing.T) {
		Setup(ctx, t, fix)

		assert.Assert(t, t.Run("Check bucket is created", func(t *testing.T) {
			assert.Check(t, len(fix.Bucket) > 0)

			t.Run("Upload object", func(t *testing.T) {
				_, err := fix.Client.PutObject(ctx, &s3.PutObjectInput{
					Bucket: aws.String(fix.Bucket),
					Key:    key,
					Body:   strings.NewReader("the-body"),
				})
				assert.Assert(t, err)
			})

			t.Run("Delete object", func(t *testing.T) {
				_, err := fix.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(fix.Bucket),
					Key:    key,
				})
				assert.Assert(t, err)
			})
		}))

	})

	t.Run("Check bucket is deleted", func(t *testing.T) {
		_, err := fix.Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(fix.Bucket),
			Key:    key,
		})
		assert.Check(t, cmp.ErrorContains(err, "NoSuchBucket"))
	})
}
