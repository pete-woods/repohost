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

// TestAcceptanceYUM publishes two versions of a real, signed .rpm repository
// through repohost.YUM to a containerized SeaweedFS, then installs it from a real
// fedora container via dnf with repository signature verification enabled
// (repo_gpgcheck=1). Each phase is a subtest so its timing shows in the output.
func TestAcceptanceYUM(t *testing.T) {
	ctx := context.Background()

	h := setup(ctx, t)
	client := h.startClient(ctx, t, "fedora:41")
	signer, publicKey := generateSigner(t)

	t.Run("publish two versions (signed)", func(t *testing.T) {
		repo := repohost.NewYUM(h.fixture.Client, h.fixture.Bucket, repohost.YUMConfig{Signer: signer})
		for _, version := range []string{"1.0.0", "1.1.0"} {
			err := repo.Add(ctx, bytes.NewReader(buildRPM(t, version)))
			assert.NilError(t, err, "publish rpm %s", version)
		}
	})

	t.Run("configure signed dnf repo", func(t *testing.T) {
		execOK(ctx, t, client, "mkdir", "-p", "/etc/pki/rpm-gpg")
		err := client.CopyToContainer(ctx, publicKey, "/etc/pki/rpm-gpg/repohost.asc", 0o644)
		assert.NilError(t, err)

		// repo_gpgcheck=1 verifies repomd.xml.asc against gpgkey; gpgcheck=0 skips
		// per-package RPM signatures (the test packages are not RPM-signed — that
		// is the package builder's concern, not the repo host's).
		repoFile := fmt.Sprintf("[repohost]\nname=repohost\nbaseurl=%s/\nenabled=1\ngpgcheck=0\nrepo_gpgcheck=1\ngpgkey=file:///etc/pki/rpm-gpg/repohost.asc\n", h.baseURL)
		err = client.CopyToContainer(ctx, []byte(repoFile), "/etc/yum.repos.d/repohost.repo", 0o644)
		assert.NilError(t, err)
	})

	t.Run("dnf install", func(t *testing.T) {
		// Enable only our repository so the install is hermetic (no external mirrors).
		execOK(ctx, t, client, "dnf", "install", "-y", "--disablerepo=*", "--enablerepo=repohost", pkgName)
	})

	t.Run("verify installed package", func(t *testing.T) {
		out := execOK(ctx, t, client, pkgName)
		assert.Check(t, cmp.Contains(out, pkgMarker), "installed binary should run")

		version := execOK(ctx, t, client, "rpm", "-q", "--qf", "%{VERSION}", pkgName)
		assert.Check(t, cmp.Equal(strings.TrimSpace(version), "1.1.0"), "newest published version should be installed")
	})
}
