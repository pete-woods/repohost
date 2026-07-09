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

// Package acceptance holds end-to-end tests that publish real packages through
// repohost and install them with real apt and dnf clients.
//
// The tests reuse the SeaweedFS and fake-gcs-server started by docker compose
// (see docker-compose.yml), rather than starting their own. The host publishes
// through each service's mapped port on localhost; client containers attach to
// the compose network and reach the services by name (seaweedfs:9123,
// fake-gcs-server:9124). Going container-to-container (rather than back out
// through the host) avoids relying on host.docker.internal, so it works the same
// on Docker Desktop and Linux CI. Bring the dependencies up first with:
//
//	docker compose up -d
package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/docker/go-sdk/container"
	"github.com/docker/go-sdk/container/exec"
	"github.com/goreleaser/nfpm/v2"
	"github.com/goreleaser/nfpm/v2/files"
	"gotest.tools/v3/assert"

	"github.com/pete-woods/repohost"
	"github.com/pete-woods/repohost/internal/testing/s3test"
	"github.com/pete-woods/repohost/pgp"

	_ "github.com/goreleaser/nfpm/v2/deb" // register the deb packager
	_ "github.com/goreleaser/nfpm/v2/rpm" // register the rpm packager
)

const (
	// pkgMarker is printed (prefixed with the package name) by each installed
	// test binary, proving that specific package's payload was delivered.
	pkgMarker = "REPOHOST_OK"

	// composeNetwork is the fixed name of the docker compose default network (set
	// in docker-compose.yml); client containers attach to it to reach services.
	composeNetwork = "repohost"
	// seaweedHost is the SeaweedFS service name + in-container S3 port.
	seaweedHost = "seaweedfs:9123"
)

// pkgSpec is a test package published in several versions. latest is the version
// a package manager should select and install.
type pkgSpec struct {
	name     string
	versions []string
	latest   string
}

// testPackages is the set the acceptance tests publish: multiple distinct names,
// each in several versions (one with three, to also exercise version ordering),
// so we verify the repository indexes and serves more than one package and picks
// the newest version of each independently.
func testPackages() []pkgSpec {
	return []pkgSpec{
		{name: "repohost-hello", versions: []string{"1.0.0", "1.1.0"}, latest: "1.1.0"},
		{name: "repohost-tool", versions: []string{"0.9.0", "2.0.0", "2.0.1"}, latest: "2.0.1"},
	}
}

// packageNames returns just the names, for a single install command.
func packageNames() []string {
	specs := testPackages()
	names := make([]string, 0, len(specs))
	for _, p := range specs {
		names = append(names, p.name)
	}
	return names
}

// harness is a repository backend with a fresh bucket, ready for publishing.
// baseURL is how client containers reach the repository root over the compose
// network.
//
// Acceptance runs against S3 (SeaweedFS) only. A GCS acceptance run is not
// included because fake-gcs-server gates its plain /<bucket>/<object> download
// route to a single -public-host: it cannot serve both the host-side store
// tests (localhost) and the in-container apt/dnf clients (fake-gcs-server) at
// once. This is a fixture limitation, not a repohost one — a real public GCS
// bucket serves apt/dnf fine, and the GCS backend itself is covered by
// gcsstore's and the root package's GCS integration tests.
type harness struct {
	backend repohost.Backend
	baseURL string
}

// setup creates a public-read bucket on the compose SeaweedFS and returns a
// harness. It is a parent-scoped precondition (not a subtest) so the bucket
// cleanup lives for the whole test. ForceLocal makes a missing SeaweedFS a
// failure rather than a skip.
func setup(ctx context.Context, t *testing.T) *harness {
	t.Helper()
	fix := &s3test.Fixture{ForceLocal: true}
	s3test.Setup(ctx, t, fix)
	return &harness{
		backend: repohost.S3(fix.Client, fix.Bucket),
		baseURL: fmt.Sprintf("http://%s/%s", seaweedHost, fix.Bucket),
	}
}

// startClient starts a long-lived client container ready to receive Exec calls.
// Like setup it is parent-scoped so the container outlives the individual stage
// subtests. A short terminate timeout keeps teardown fast — the "sleep" command
// ignores SIGTERM, so without it Docker waits its full 10s stop grace.
func (h *harness) startClient(ctx context.Context, t *testing.T, image string) *container.Container {
	t.Helper()
	ctr, err := container.Run(ctx,
		container.WithImage(image),
		container.WithCmd("sleep", "infinity"),
		// Attach to the compose network so the client can reach seaweedfs by name.
		container.WithNetworkName(nil, composeNetwork),
	)
	assert.NilError(t, err, "start client %s", image)
	container.Cleanup(t, ctr, container.TerminateTimeout(time.Second))
	return ctr
}

// execOK runs cmd in ctr, streaming its combined output to stdout as it arrives,
// fails the test if it exits non-zero, and returns the captured output.
func execOK(ctx context.Context, t *testing.T, ctr *container.Container, cmd ...string) string {
	t.Helper()
	fmt.Fprintf(os.Stdout, "\n$ %s\n", strings.Join(cmd, " "))

	start := time.Now()
	code, reader, err := ctr.Exec(ctx, cmd, exec.Multiplexed())
	assert.NilError(t, err, "exec %v", cmd)

	var captured bytes.Buffer
	stream := &lineStreamer{sink: os.Stdout, prefix: "  | "}
	_, err = io.Copy(io.MultiWriter(&captured, stream), reader)
	assert.NilError(t, err)
	stream.flush()

	fmt.Fprintf(os.Stdout, "  └ exit %d (%s)\n", code, time.Since(start).Round(time.Millisecond))
	assert.Assert(t, code == 0, "command %v exited %d", cmd, code)
	return captured.String()
}

// lineStreamer writes whole lines to sink with a prefix, so streamed container
// output is readable and clearly attributed.
type lineStreamer struct {
	sink   io.Writer
	prefix string
	buf    []byte
}

func (s *lineStreamer) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		fmt.Fprintf(s.sink, "%s%s\n", s.prefix, s.buf[:i])
		s.buf = s.buf[i+1:]
	}
	return len(p), nil
}

func (s *lineStreamer) flush() {
	if len(s.buf) > 0 {
		fmt.Fprintf(s.sink, "%s%s\n", s.prefix, s.buf)
		s.buf = nil
	}
}

// buildDeb builds a real .deb for the named package at the given version and
// host arch.
func buildDeb(t *testing.T, name, version string) []byte {
	t.Helper()
	return buildPackage(t, "deb", name, version)
}

// buildRPM builds a real .rpm for the named package at the given version and
// host arch.
func buildRPM(t *testing.T, name, version string) []byte {
	t.Helper()
	return buildPackage(t, "rpm", name, version)
}

func buildPackage(t *testing.T, format, name, version string) []byte {
	t.Helper()

	// The binary echoes "<name> REPOHOST_OK" so verification confirms this exact
	// package's payload was delivered, not just that some package installed.
	script := filepath.Join(t.TempDir(), name)
	err := os.WriteFile(script, []byte("#!/bin/sh\necho "+name+" "+pkgMarker+"\n"), 0o755)
	assert.NilError(t, err)

	info := nfpm.WithDefaults(&nfpm.Info{
		Name:        name,
		Arch:        hostArch(),
		Version:     version,
		Maintainer:  "repohost tests <test@example.com>",
		Description: "repohost acceptance test package",
		Overridables: nfpm.Overridables{
			Contents: files.Contents{
				{Source: script, Destination: "/usr/bin/" + name},
			},
		},
	})

	packager, err := nfpm.Get(format)
	assert.NilError(t, err)

	var buf bytes.Buffer
	err = packager.Package(info, &buf)
	assert.NilError(t, err, "build %s package", format)
	return buf.Bytes()
}

// generateSigner creates a throwaway OpenPGP key and returns a Signer for it
// plus its ASCII-armored public key (which the client imports to verify the
// repository).
func generateSigner(t *testing.T) (*pgp.Signer, []byte) {
	t.Helper()

	entity, err := openpgp.NewEntity("repohost acceptance", "", "acceptance@example.com", nil)
	assert.NilError(t, err)

	var priv bytes.Buffer
	w, err := armor.Encode(&priv, openpgp.PrivateKeyType, nil)
	assert.NilError(t, err)
	err = entity.SerializePrivate(w, nil)
	assert.NilError(t, err)
	err = w.Close()
	assert.NilError(t, err)

	signer, err := pgp.NewSigner(priv.Bytes(), "")
	assert.NilError(t, err)

	pub, err := signer.ArmoredPublicKey()
	assert.NilError(t, err)
	return signer, pub
}

// hostArch maps the host GOARCH to the nfpm architecture, so packages match the
// natively-run client containers (no cross-arch emulation).
func hostArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}
