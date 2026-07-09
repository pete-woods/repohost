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

package compiler

import (
	"context"
	"os"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/fs"
	"gotest.tools/v3/icmd"
)

// TestCompiler_Compile builds the real repohost CLI and runs it, which both
// exercises the compiler and doubles as a build smoke test for cmd/repohost.
func TestCompiler_Compile(t *testing.T) {
	tempDir := fs.NewDir(t, "")

	c := New(tempDir.Path(), "-w -s")

	binary := ""
	built := t.Run("compile the repohost CLI", func(t *testing.T) {
		var err error
		binary, err = c.Compile(context.Background(), Work{
			Name:   "repohost",
			Target: "../../..", // repo root, relative to this package
			Source: "./cmd/repohost",
		})
		assert.NilError(t, err)

		_, err = os.Stat(binary)
		assert.Check(t, err)
	})
	assert.Assert(t, built) // nothing else works if the build failed

	t.Run("binary runs and lists the push command", func(t *testing.T) {
		res := icmd.RunCommand(binary, "--help")
		assert.Check(t, res.Equal(icmd.Expected{Out: "push"}))
	})
}
