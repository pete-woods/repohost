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

package repohost_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/pete-woods/repohost"
	"github.com/pete-woods/repohost/internal/testing/debtest"
	"github.com/pete-woods/repohost/internal/testing/s3test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// TestAPTPublishAndRetention drives the public API end to end against a real
// S3-compatible backend (SeaweedFS via the s3test fixture): two versions of a
// package are added with KeepVersions=1, and the published layout plus the
// pruning of the old version are verified.
func TestAPTPublishAndRetention(t *testing.T) {
	ctx := context.Background()
	fix := s3test.Default(ctx, t)

	repo := repohost.NewAPT(repohost.S3(fix.Client, fix.Bucket), repohost.APTConfig{
		Distribution: "stable",
		Origin:       "Acme",
		Label:        "Acme",
		Description:  "Acme packages",
		KeepVersions: 1,
	})

	for _, version := range []string{"1.0", "1.1"} {
		deb := debtest.Package(t, "hello", version, "amd64")
		err := repo.Add(ctx, "main", bytes.NewReader(deb))
		assert.NilError(t, err, "add hello %s", version)
	}

	// Newest version retained in the pool, oldest pruned.
	newest := objectExists(ctx, t, fix, "pool/main/h/hello/hello_1.1_amd64.deb")
	assert.Check(t, newest, "newest version should remain in the pool")
	oldest := objectExists(ctx, t, fix, "pool/main/h/hello/hello_1.0_amd64.deb")
	assert.Check(t, !oldest, "oldest version should be pruned from the pool")

	// The Packages index reflects only the retained version.
	packages := string(getObject(ctx, t, fix, "dists/stable/main/binary-amd64/Packages"))
	assert.Check(t, cmp.Contains(packages, "Version: 1.1"))
	assert.Check(t, !strings.Contains(packages, "Version: 1.0"), "pruned version must not linger in the index")

	// Release was published and describes the repository.
	release := string(getObject(ctx, t, fix, "dists/stable/Release"))
	assert.Check(t, cmp.Contains(release, "Suite: stable"))
	assert.Check(t, cmp.Contains(release, "Architectures: amd64"))
	assert.Check(t, cmp.Contains(release, "main/binary-amd64/Packages"))
}

// TestAPTRemove adds two versions of a package (keeping both), then removes the
// older one explicitly and checks the pool file and index entry are gone while
// the other version survives and the Release is republished.
func TestAPTRemove(t *testing.T) {
	ctx := context.Background()
	fix := s3test.Default(ctx, t)

	repo := repohost.NewAPT(repohost.S3(fix.Client, fix.Bucket), repohost.APTConfig{Distribution: "stable"})
	for _, version := range []string{"1.0", "1.1"} {
		deb := debtest.Package(t, "hello", version, "amd64")
		err := repo.Add(ctx, "main", bytes.NewReader(deb))
		assert.NilError(t, err, "add hello %s", version)
	}

	removed, err := repo.Remove(ctx, "main", "hello", "1.0")
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(removed, 1))

	gone := objectExists(ctx, t, fix, "pool/main/h/hello/hello_1.0_amd64.deb")
	assert.Check(t, !gone, "removed version's pool file should be deleted")
	survivor := objectExists(ctx, t, fix, "pool/main/h/hello/hello_1.1_amd64.deb")
	assert.Check(t, survivor, "other version should remain")

	packages := string(getObject(ctx, t, fix, "dists/stable/main/binary-amd64/Packages"))
	assert.Check(t, !strings.Contains(packages, "Version: 1.0"), "removed version must leave the index")
	assert.Check(t, cmp.Contains(packages, "Version: 1.1"))

	// Removing an absent version is a no-op, not an error.
	none, err := repo.Remove(ctx, "main", "hello", "9.9")
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(none, 0))
}

func getObject(ctx context.Context, t *testing.T, fix *s3test.Fixture, key string) []byte {
	t.Helper()
	out, err := fix.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(fix.Bucket),
		Key:    aws.String(key),
	})
	assert.NilError(t, err, "get %s", key)
	defer func() { _ = out.Body.Close() }()

	data, err := io.ReadAll(out.Body)
	assert.NilError(t, err)
	return data
}

func objectExists(ctx context.Context, t *testing.T, fix *s3test.Fixture, key string) bool {
	t.Helper()
	_, err := fix.Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(fix.Bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return false
		}
	}
	assert.NilError(t, err, "head %s", key)
	return false
}
