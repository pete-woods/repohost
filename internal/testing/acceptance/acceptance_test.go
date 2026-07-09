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

// keepVersions is the retention limit for the acceptance run. It is chosen so
// repohost-tool (published in three versions) has its oldest, 0.9.0, evicted
// while its two newest survive, and repohost-hello (two versions) keeps both.
// One run therefore proves both newest-version selection among several
// candidates and eviction of the surplus. evictedVersion is the version that
// retention should have removed from the served metadata.
const (
	keepVersions   = 2
	evictedVersion = "0.9.0"
)

// TestAcceptance publishes both a Debian (apt) and an RPM (yum) repository into
// the SAME bucket, then installs from each with a real apt and dnf client
// pointed at the one base URL. It proves two things end to end:
//
//   - Coexistence: both formats live under a single backend. Their key layouts
//     do not collide (apt: pool/ + dists/; yum: Packages/ + repodata/) and
//     neither publisher does a bucket-wide List, so one bucket can serve both
//     apt-get and dnf from the same URL.
//   - Retention: KeepVersions evicts surplus versions from the metadata a real
//     client sees, not just from the object store.
//
// Each phase is a subtest so its timing and failure attribution are visible.
func TestAcceptance(t *testing.T) {
	ctx := context.Background()

	h := setup(ctx, t)
	signer, publicKey := generateSigner(t)

	published := t.Run("publish deb + rpm to one bucket (signed)", func(t *testing.T) {
		apt := repohost.NewAPT(h.backend, repohost.APTConfig{
			Distribution: "stable",
			Origin:       "repohost",
			Label:        "repohost",
			KeepVersions: keepVersions,
			Signer:       signer,
		})
		yum := repohost.NewYUM(h.backend, repohost.YUMConfig{
			KeepVersions: keepVersions,
			Signer:       signer,
		})
		for _, p := range testPackages() {
			for _, version := range p.versions {
				err := apt.Add(ctx, "main", bytes.NewReader(buildDeb(t, p.name, version)))
				assert.NilError(t, err, "publish deb %s %s", p.name, version)

				err = yum.Add(ctx, bytes.NewReader(buildRPM(t, p.name, version)))
				assert.NilError(t, err, "publish rpm %s %s", p.name, version)
			}
		}
	})
	assert.Assert(t, published)

	t.Run("debian: apt install + retention", func(t *testing.T) {
		client := h.startClient(ctx, t, "debian:bookworm-slim")

		configured := t.Run("configure a signed apt source", func(t *testing.T) {
			// signed-by (not trusted=yes) means apt actually verifies the signature.
			execOK(ctx, t, client, "sh", "-c",
				"rm -f /etc/apt/sources.list /etc/apt/sources.list.d/* && mkdir -p /etc/apt/keyrings")
			err := client.CopyToContainer(ctx, publicKey, "/etc/apt/keyrings/repohost.asc", 0o644)
			assert.NilError(t, err)
			sources := fmt.Sprintf("deb [signed-by=/etc/apt/keyrings/repohost.asc] %s stable main\n", h.baseURL)
			err = client.CopyToContainer(ctx, []byte(sources), "/etc/apt/sources.list.d/repohost.list", 0o644)
			assert.NilError(t, err)
		})
		assert.Assert(t, configured)

		installed := t.Run("apt-get update and install", func(t *testing.T) {
			execOK(ctx, t, client, "apt-get", "update")
			execOK(ctx, t, client, append([]string{"apt-get", "install", "-y"}, packageNames()...)...)
		})
		assert.Assert(t, installed)

		t.Run("each package installs at its newest version", func(t *testing.T) {
			for _, p := range testPackages() {
				out := execOK(ctx, t, client, p.name)
				assert.Check(t, cmp.Contains(out, p.name+" "+pkgMarker), "%s binary", p.name)

				version := execOK(ctx, t, client, "dpkg-query", "-W", "-f=${Version}", p.name)
				assert.Check(t, cmp.Equal(strings.TrimSpace(version), p.latest), "%s version", p.name)
			}
		})

		t.Run("index offers the retained versions, not the evicted one", func(t *testing.T) {
			madison := execOK(ctx, t, client, "apt-cache", "madison", "repohost-tool")
			assert.Check(t, cmp.Contains(madison, "2.0.1"))
			assert.Check(t, cmp.Contains(madison, "2.0.0"))
			assert.Check(t, !strings.Contains(madison, evictedVersion))
		})
	})

	t.Run("fedora: dnf install + retention", func(t *testing.T) {
		client := h.startClient(ctx, t, "fedora:41")

		configured := t.Run("configure a signed dnf repo", func(t *testing.T) {
			execOK(ctx, t, client, "mkdir", "-p", "/etc/pki/rpm-gpg")
			err := client.CopyToContainer(ctx, publicKey, "/etc/pki/rpm-gpg/repohost.asc", 0o644)
			assert.NilError(t, err)
			// repo_gpgcheck=1 verifies repomd.xml.asc against gpgkey; gpgcheck=0 skips
			// per-package RPM signatures (not the repo host's concern).
			repoFile := fmt.Sprintf("[repohost]\nname=repohost\nbaseurl=%s/\nenabled=1\ngpgcheck=0\nrepo_gpgcheck=1\ngpgkey=file:///etc/pki/rpm-gpg/repohost.asc\n", h.baseURL)
			err = client.CopyToContainer(ctx, []byte(repoFile), "/etc/yum.repos.d/repohost.repo", 0o644)
			assert.NilError(t, err)
		})
		assert.Assert(t, configured)

		installed := t.Run("dnf install", func(t *testing.T) {
			// Enable only our repository so the install is hermetic (no external mirrors).
			execOK(ctx, t, client, append([]string{"dnf", "install", "-y", "--disablerepo=*", "--enablerepo=repohost"}, packageNames()...)...)
		})
		assert.Assert(t, installed)

		t.Run("each package installs at its newest version", func(t *testing.T) {
			for _, p := range testPackages() {
				out := execOK(ctx, t, client, p.name)
				assert.Check(t, cmp.Contains(out, p.name+" "+pkgMarker), "%s binary", p.name)

				version := execOK(ctx, t, client, "rpm", "-q", "--qf", "%{VERSION}", p.name)
				assert.Check(t, cmp.Equal(strings.TrimSpace(version), p.latest), "%s version", p.name)
			}
		})

		t.Run("index offers the retained versions, not the evicted one", func(t *testing.T) {
			list := execOK(ctx, t, client, "dnf", "--disablerepo=*", "--enablerepo=repohost", "list", "--showduplicates", "repohost-tool")
			assert.Check(t, cmp.Contains(list, "2.0.1"))
			assert.Check(t, cmp.Contains(list, "2.0.0"))
			assert.Check(t, !strings.Contains(list, evictedVersion))
		})
	})
}
