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

package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pete-woods/repohost"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// TestAcceptanceAPT publishes two versions of a real .deb through repohost.APT
// to a containerized SeaweedFS, then installs it from a real debian container
// via apt-get. Each phase is a subtest so its timing shows in the test output.
func TestAcceptanceAPT(t *testing.T) {
	ctx := context.Background()

	h := setup(ctx, t)
	client := h.startClient(ctx, t, "debian:bookworm-slim")

	t.Run("publish two versions", func(t *testing.T) {
		apt := repohost.NewAPT(h.fixture.Client, h.fixture.Bucket, repohost.APTConfig{
			Distribution: "stable",
			Origin:       "repohost",
			Label:        "repohost",
		})
		for _, version := range []string{"1.0.0", "1.1.0"} {
			err := apt.Add(ctx, "main", bytes.NewReader(buildDeb(t, version)))
			assert.NilError(t, err, "publish deb %s", version)
		}
	})

	t.Run("configure apt for our repo only", func(t *testing.T) {
		execOK(ctx, t, client, "sh", "-c", "rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*")
		sources := fmt.Sprintf("deb [trusted=yes] %s stable main\n", h.baseURL)
		err := client.CopyToContainer(ctx, []byte(sources), "/etc/apt/sources.list.d/repohost.list", 0o644)
		assert.NilError(t, err)
	})

	t.Run("apt-get update", func(t *testing.T) {
		execOK(ctx, t, client, "apt-get", "update")
	})

	t.Run("apt-get install", func(t *testing.T) {
		execOK(ctx, t, client, "apt-get", "install", "-y", pkgName)
	})

	t.Run("verify installed package", func(t *testing.T) {
		out := execOK(ctx, t, client, pkgName)
		assert.Check(t, cmp.Contains(out, pkgMarker), "installed binary should run")

		version := execOK(ctx, t, client, "dpkg-query", "-W", "-f=${Version}", pkgName)
		assert.Check(t, cmp.Equal(strings.TrimSpace(version), "1.1.0"), "newest published version should be installed")
	})
}
