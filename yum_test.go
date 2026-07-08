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
	"compress/gzip"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/pete-woods/repohost"
	"github.com/pete-woods/repohost/internal/testing/rpmtest"
	"github.com/pete-woods/repohost/internal/testing/s3test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// TestYUMPublishAndRetention drives the public YUM API end to end against a real
// S3-compatible backend (SeaweedFS via s3test): two versions of a package are
// added with KeepVersions=1, and the published repodata plus the pruning of the
// old version are verified.
func TestYUMPublishAndRetention(t *testing.T) {
	ctx := context.Background()
	fix := s3test.Default(ctx, t)

	repo := repohost.NewYUM(fix.Client, fix.Bucket, repohost.YUMConfig{KeepVersions: 1})

	for _, version := range []string{"1.0", "2.0"} {
		rpmData := rpmtest.Build(t, rpmtest.Options{
			Name: "hello", Version: version, Release: "1", Arch: "x86_64",
			Summary:  "A greeting",
			Provides: []rpmtest.Dep{{Name: "hello", Flags: 8, Version: version + "-1"}},
			Files:    []rpmtest.FileEntry{{Path: "/usr/bin/hello"}},
		})
		err := repo.Add(ctx, bytes.NewReader(rpmData))
		assert.NilError(t, err, "add hello %s", version)
	}

	// Newest version retained in the pool, oldest pruned.
	newest := objectExists(ctx, t, fix, "Packages/h/hello-2.0-1.x86_64.rpm")
	assert.Check(t, newest, "newest version should remain in the pool")
	oldest := objectExists(ctx, t, fix, "Packages/h/hello-1.0-1.x86_64.rpm")
	assert.Check(t, !oldest, "oldest version should be pruned from the pool")

	// repomd.xml references a primary index; fetch and inspect it.
	repomd := string(getObject(ctx, t, fix, "repodata/repomd.xml"))
	assert.Check(t, cmp.Contains(repomd, `type="primary"`))
	primaryHref := hrefForType(repomd, "primary")
	assert.Assert(t, primaryHref != "", "repomd must reference a primary index")

	primaryXML := string(gunzipBytes(t, getObject(ctx, t, fix, primaryHref)))
	assert.Check(t, cmp.Contains(primaryXML, `ver="2.0"`))
	assert.Check(t, !strings.Contains(primaryXML, `ver="1.0"`), "pruned version must not remain in primary.xml")
	assert.Check(t, cmp.Contains(primaryXML, `href="Packages/h/hello-2.0-1.x86_64.rpm"`))
}

// hrefForType extracts the location href of a repomd <data> element by type. It
// is a deliberately small scan, sufficient for the test's known-good output.
func hrefForType(repomd, typ string) string {
	marker := `type="` + typ + `"`
	i := strings.Index(repomd, marker)
	if i < 0 {
		return ""
	}
	rest := repomd[i:]
	h := strings.Index(rest, `href="`)
	if h < 0 {
		return ""
	}
	rest = rest[h+len(`href="`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func gunzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	assert.NilError(t, err)
	out, err := io.ReadAll(gz)
	assert.NilError(t, err)
	return out
}
