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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/fs"
	"gotest.tools/v3/icmd"

	"github.com/pete-woods/repohost/internal/testing/compiler"
	"github.com/pete-woods/repohost/internal/testing/s3test"
)

// TestAcceptanceCLI compiles the real repohost CLI and drives it against
// SeaweedFS, exactly as an operator would: --dry-run writes nothing, a signed
// `push deb` produces a verifiable apt repo, and a signed `push rpm` into the
// same bucket produces a yum repo without disturbing the apt one.
func TestAcceptanceCLI(t *testing.T) {
	ctx := context.Background()

	// Bucket and output dirs are parent-scoped: their cleanups must outlive the
	// subtests that use the binary and packages built into them.
	fix := &s3test.Fixture{ForceLocal: true}
	s3test.Setup(ctx, t, fix)
	binDir := fs.NewDir(t, "repohost-cli")
	pkgDir := fs.NewDir(t, "repohost-pkgs")

	var (
		bin     string
		keyPath string
		debs    []string
		rpms    []string
	)

	built := t.Run("compile the repohost CLI", func(t *testing.T) {
		var err error
		bin, err = compiler.New(binDir.Path(), "-w -s").
			Compile(ctx, compiler.Work{Name: "repohost", Target: "../../..", Source: "./cmd/repohost"})
		assert.NilError(t, err)
	})
	assert.Assert(t, built)

	prepared := t.Run("build a signing key and packages", func(t *testing.T) {
		keyPath = writeSignKey(t, pkgDir.Path())
		debs = []string{
			writePackage(t, pkgDir.Path(), "repohost-hello_1.0.0.deb", buildDeb(t, "repohost-hello", "1.0.0")),
			writePackage(t, pkgDir.Path(), "repohost-hello_1.1.0.deb", buildDeb(t, "repohost-hello", "1.1.0")),
		}
		rpms = []string{
			writePackage(t, pkgDir.Path(), "repohost-hello-1.0.0.rpm", buildRPM(t, "repohost-hello", "1.0.0")),
			writePackage(t, pkgDir.Path(), "repohost-hello-1.1.0.rpm", buildRPM(t, "repohost-hello", "1.1.0")),
		}
	})
	assert.Assert(t, prepared)

	// Credentials via the AWS environment; endpoint/path-style point the CLI at
	// the fixture's SeaweedFS, matching how s3test builds its own client.
	env := []string{
		"AWS_ACCESS_KEY_ID=" + fix.Key,
		"AWS_SECRET_ACCESS_KEY=" + fix.Secret,
		"AWS_REGION=" + fix.Region,
	}
	s3Flags := []string{
		"--backend", "s3",
		"--bucket", fix.Bucket,
		"--endpoint", fix.URL,
		"--region", fix.Region,
		"--s3-path-style",
	}

	t.Run("deb dry-run writes nothing", func(t *testing.T) {
		ran := t.Run("push --dry-run succeeds", func(t *testing.T) {
			args := append([]string{"push", "deb", "--distribution", "stable", "--dry-run"}, s3Flags...)
			args = append(args, debs...)
			res := icmd.RunCmd(icmd.Command(bin, args...), icmd.WithEnv(env...))
			assert.Check(t, res.Equal(icmd.Success))
		})
		assert.Assert(t, ran)

		t.Run("bucket stays empty", func(t *testing.T) {
			count := s3ObjectCount(ctx, t, fix)
			assert.Check(t, cmp.Equal(count, 0))
		})
	})

	t.Run("deb push (signed)", func(t *testing.T) {
		pushed := t.Run("push succeeds", func(t *testing.T) {
			args := append([]string{"push", "deb", "--distribution", "stable", "--origin", "repohost", "--sign-key", keyPath}, s3Flags...)
			args = append(args, debs...)
			res := icmd.RunCmd(icmd.Command(bin, args...), icmd.WithEnv(env...))
			assert.Check(t, res.Equal(icmd.Success))
		})
		assert.Assert(t, pushed)

		t.Run("Packages index lists the newest version", func(t *testing.T) {
			arch := hostArch()
			packages := string(s3Get(ctx, t, fix, "dists/stable/main/binary-"+arch+"/Packages"))
			assert.Check(t, cmp.Contains(packages, "Package: repohost-hello"))
			assert.Check(t, cmp.Contains(packages, "Version: 1.1.0"))
		})

		t.Run("Release is signed (InRelease)", func(t *testing.T) {
			inRelease := s3Exists(ctx, t, fix, "dists/stable/InRelease")
			assert.Check(t, inRelease)
		})
	})

	t.Run("deb rm removes a version and re-signs", func(t *testing.T) {
		removed := t.Run("rm succeeds", func(t *testing.T) {
			args := append([]string{"rm", "deb", "--distribution", "stable", "--sign-key", keyPath}, s3Flags...)
			args = append(args, "repohost-hello", "1.0.0")
			res := icmd.RunCmd(icmd.Command(bin, args...), icmd.WithEnv(env...))
			assert.Check(t, res.Equal(icmd.Success))
		})
		assert.Assert(t, removed)

		t.Run("removed version is gone, newest remains", func(t *testing.T) {
			arch := hostArch()
			packages := string(s3Get(ctx, t, fix, "dists/stable/main/binary-"+arch+"/Packages"))
			assert.Check(t, !strings.Contains(packages, "Version: 1.0.0"))
			assert.Check(t, cmp.Contains(packages, "Version: 1.1.0"))
		})

		t.Run("Release re-signed (InRelease present)", func(t *testing.T) {
			inRelease := s3Exists(ctx, t, fix, "dists/stable/InRelease")
			assert.Check(t, inRelease)
		})
	})

	t.Run("rpm push into the same bucket (signed)", func(t *testing.T) {
		pushed := t.Run("push succeeds", func(t *testing.T) {
			args := append([]string{"push", "rpm", "--sign-key", keyPath}, s3Flags...)
			args = append(args, rpms...)
			res := icmd.RunCmd(icmd.Command(bin, args...), icmd.WithEnv(env...))
			assert.Check(t, res.Equal(icmd.Success))
		})
		assert.Assert(t, pushed)

		t.Run("repodata is written and signed", func(t *testing.T) {
			repomd := s3Exists(ctx, t, fix, "repodata/repomd.xml")
			assert.Check(t, repomd, "repomd.xml")
			asc := s3Exists(ctx, t, fix, "repodata/repomd.xml.asc")
			assert.Check(t, asc, "repomd.xml.asc")
		})

		t.Run("apt repo still coexists", func(t *testing.T) {
			inRelease := s3Exists(ctx, t, fix, "dists/stable/InRelease")
			assert.Check(t, inRelease)
		})
	})
}

// writeSignKey generates a throwaway armored PGP private key in dir and returns
// its path for --sign-key.
func writeSignKey(t *testing.T, dir string) string {
	t.Helper()
	entity, err := openpgp.NewEntity("repohost cli acceptance", "", "cli@example.com", nil)
	assert.NilError(t, err)

	var priv bytes.Buffer
	w, err := armor.Encode(&priv, openpgp.PrivateKeyType, nil)
	assert.NilError(t, err)
	err = entity.SerializePrivate(w, nil)
	assert.NilError(t, err)
	err = w.Close()
	assert.NilError(t, err)

	return writePackage(t, dir, "signing-key.asc", priv.Bytes())
}

func writePackage(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	err := os.WriteFile(path, data, 0o600)
	assert.NilError(t, err)
	return path
}

func s3ObjectCount(ctx context.Context, t *testing.T, fix *s3test.Fixture) int {
	t.Helper()
	out, err := fix.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &fix.Bucket})
	assert.NilError(t, err)
	return len(out.Contents)
}

func s3Exists(ctx context.Context, t *testing.T, fix *s3test.Fixture, key string) bool {
	t.Helper()
	_, err := fix.Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &fix.Bucket, Key: &key})
	return err == nil
}

func s3Get(ctx context.Context, t *testing.T, fix *s3test.Fixture, key string) []byte {
	t.Helper()
	out, err := fix.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: &fix.Bucket, Key: &key})
	assert.NilError(t, err, "get %s", key)
	defer func() { _ = out.Body.Close() }()

	data, err := io.ReadAll(out.Body)
	assert.NilError(t, err)
	return data
}
