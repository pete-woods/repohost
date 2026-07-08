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

	gcs "cloud.google.com/go/storage"
	"github.com/pete-woods/repohost"
	"github.com/pete-woods/repohost/internal/testing/debtest"
	"github.com/pete-woods/repohost/internal/testing/gcstest"
	"github.com/pete-woods/repohost/internal/testing/rpmtest"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// TestAPTPublishGCS drives the public API end to end against Google Cloud Storage
// (fake-gcs-server via the gcstest fixture), proving the repohost.GCS backend
// seam publishes an apt repository and applies retention.
func TestAPTPublishGCS(t *testing.T) {
	ctx := context.Background()
	fix := gcstest.Default(ctx, t)

	repo := repohost.NewAPT(repohost.GCS(fix.Client, fix.Bucket), repohost.APTConfig{
		Distribution: "stable",
		Origin:       "Acme",
		KeepVersions: 1,
	})
	for _, version := range []string{"1.0", "1.1"} {
		err := repo.Add(ctx, "main", bytes.NewReader(debtest.Package(t, "hello", version, "amd64")))
		assert.NilError(t, err, "add hello %s", version)
	}

	newest := gcsObjectExists(ctx, t, fix, "pool/main/h/hello/hello_1.1_amd64.deb")
	assert.Check(t, newest, "newest version should remain in the pool")
	oldest := gcsObjectExists(ctx, t, fix, "pool/main/h/hello/hello_1.0_amd64.deb")
	assert.Check(t, !oldest, "oldest version should be pruned from the pool")

	packages := string(gcsGetObject(ctx, t, fix, "dists/stable/main/binary-amd64/Packages"))
	assert.Check(t, cmp.Contains(packages, "Version: 1.1"))
	assert.Check(t, !strings.Contains(packages, "Version: 1.0"), "pruned version must not linger in the index")

	release := string(gcsGetObject(ctx, t, fix, "dists/stable/Release"))
	assert.Check(t, cmp.Contains(release, "Suite: stable"))
}

// TestYUMPublishGCS proves the repohost.GCS backend seam publishes a yum
// repository and applies retention.
func TestYUMPublishGCS(t *testing.T) {
	ctx := context.Background()
	fix := gcstest.Default(ctx, t)

	repo := repohost.NewYUM(repohost.GCS(fix.Client, fix.Bucket), repohost.YUMConfig{KeepVersions: 1})
	for _, version := range []string{"1.0", "2.0"} {
		err := repo.Add(ctx, bytes.NewReader(rpmtest.Package(t, "hello", version, "1", "x86_64")))
		assert.NilError(t, err, "add hello %s", version)
	}

	newest := gcsObjectExists(ctx, t, fix, "Packages/h/hello-2.0-1.x86_64.rpm")
	assert.Check(t, newest, "newest version should remain")
	oldest := gcsObjectExists(ctx, t, fix, "Packages/h/hello-1.0-1.x86_64.rpm")
	assert.Check(t, !oldest, "oldest version should be pruned")

	repomd := string(gcsGetObject(ctx, t, fix, "repodata/repomd.xml"))
	assert.Check(t, cmp.Contains(repomd, `type="primary"`))
}

func gcsGetObject(ctx context.Context, t *testing.T, fix *gcstest.Fixture, key string) []byte {
	t.Helper()
	r, err := fix.Client.Bucket(fix.Bucket).Object(key).NewReader(ctx)
	assert.NilError(t, err, "get %s", key)
	defer func() { _ = r.Close() }()

	data, err := io.ReadAll(r)
	assert.NilError(t, err)
	return data
}

func gcsObjectExists(ctx context.Context, t *testing.T, fix *gcstest.Fixture, key string) bool {
	t.Helper()
	_, err := fix.Client.Bucket(fix.Bucket).Object(key).Attrs(ctx)
	if errors.Is(err, gcs.ErrObjectNotExist) {
		return false
	}
	assert.NilError(t, err, "attrs %s", key)
	return true
}
