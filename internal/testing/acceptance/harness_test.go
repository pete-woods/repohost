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
// The tests reuse the SeaweedFS started by docker compose (see
// docker-compose.yml), rather than starting their own: the host publishes to it
// at localhost:9123, and client containers reach it at host.docker.internal:9123
// (Docker Desktop maps that to the host; a Linux CI would add
// host.docker.internal:host-gateway). Bring it up first with:
//
//	docker compose up -d seaweedfs
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

	"github.com/docker/go-sdk/container"
	"github.com/docker/go-sdk/container/exec"
	"github.com/goreleaser/nfpm/v2"
	"github.com/goreleaser/nfpm/v2/files"
	"gotest.tools/v3/assert"

	"github.com/pete-woods/repohost/internal/testing/s3test"

	_ "github.com/goreleaser/nfpm/v2/deb" // register the deb packager
	_ "github.com/goreleaser/nfpm/v2/rpm" // register the rpm packager
)

const (
	// pkgName is the test package that installs /usr/bin/repohost-hello.
	pkgName = "repohost-hello"
	// pkgMarker is what the installed binary prints, proving delivery + install.
	pkgMarker = "REPOHOST_OK"

	// clientBaseHost is how a client container reaches the compose SeaweedFS
	// (its host-mapped S3 port), served from the host.
	clientBaseHost = "host.docker.internal:9123"
)

// harness is a bucket on the compose SeaweedFS, ready for publishing. baseURL is
// how client containers reach the repository root.
type harness struct {
	fixture *s3test.Fixture
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
		fixture: fix,
		baseURL: fmt.Sprintf("http://%s/%s", clientBaseHost, fix.Bucket),
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

// buildDeb builds a real .deb for the package at the given version and host arch.
func buildDeb(t *testing.T, version string) []byte {
	t.Helper()
	return buildPackage(t, "deb", version)
}

// buildRPM builds a real .rpm for the package at the given version and host arch.
func buildRPM(t *testing.T, version string) []byte {
	t.Helper()
	return buildPackage(t, "rpm", version)
}

func buildPackage(t *testing.T, format, version string) []byte {
	t.Helper()

	script := filepath.Join(t.TempDir(), pkgName)
	err := os.WriteFile(script, []byte("#!/bin/sh\necho "+pkgMarker+"\n"), 0o755)
	assert.NilError(t, err)

	info := nfpm.WithDefaults(&nfpm.Info{
		Name:        pkgName,
		Arch:        hostArch(),
		Version:     version,
		Maintainer:  "repohost tests <test@example.com>",
		Description: "repohost acceptance test package",
		Overridables: nfpm.Overridables{
			Contents: files.Contents{
				{Source: script, Destination: "/usr/bin/" + pkgName},
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

// hostArch maps the host GOARCH to the nfpm architecture, so packages match the
// natively-run client containers (no cross-arch emulation).
func hostArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}
