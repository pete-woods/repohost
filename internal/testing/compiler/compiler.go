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

// Package compiler builds Go binaries on demand, for acceptance tests that need
// to exec the program under test as a real subprocess.
package compiler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// New returns a Compiler that writes binaries into baseDir, building them with
// the given -ldflags value (e.g. "-w -s").
func New(baseDir, ldFlags string) *Compiler {
	return &Compiler{
		baseDir: baseDir,
		ldFlags: ldFlags,
	}
}

// Compiler builds Go binaries into a base directory.
type Compiler struct {
	baseDir string
	ldFlags string
}

// Dir returns the directory the compiler writes binaries into.
func (c *Compiler) Dir() string {
	return c.baseDir
}

// Work can be added to the compiler.
type Work struct {
	// Name determines the name of the binary the compiler will prodice.
	Name string
	// Target is the target directory to compile, where the source will be found relative to the test, e.g. ../..
	Target string
	// Source is the source name of the Go package to compile, e.g. ./cmd/mybin
	Source string
	// ExtraArgs can be used to append args to the command - for instance compiling with coverage
	ExtraArgs []string
	// Tags allows specifying extra build tags to the Go compiler.
	Tags string
	// Environment allows specifying extra environment variables to the Go compiler (e.g. GOOS=linux GOARCH=arm64)
	Environment []string

	// Result is a pointer to a string where the final compiled binary was produced.
	Result *string
}

// Compile a binary for testing. target is the path to the main package.
func (c *Compiler) Compile(ctx context.Context, work Work) (string, error) {
	cwd, err := filepath.Abs(work.Target)
	if err != nil {
		return "", err
	}

	goos := runtime.GOOS
	for _, e := range work.Environment {
		if strings.HasPrefix(e, "GOOS=") {
			goos = strings.SplitN(e, "=", 2)[1]
		}
	}

	path := binaryPath(work.Name, c.baseDir, goos)
	goBin := goPath()
	var cmd *exec.Cmd

	args := []string{
		"build",
		"-ldflags=" + c.ldFlags,
	}
	args = append(args, work.ExtraArgs...)
	args = append(args, "-o", path)
	if work.Tags != "" {
		args = append(args, "-tags", work.Tags)
	}
	args = append(args, work.Source)

	// #nosec - this is fine
	cmd = exec.CommandContext(ctx, goBin, args...)

	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Env = append(cmd.Env, work.Environment...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		return "", err
	}

	if work.Result != nil {
		*work.Result = path
	}
	return path, err
}

func goPath() string {
	goroot := os.Getenv("GOROOT")
	if goroot == "" {
		return "go"
	}
	return filepath.Join(goroot, "bin", "go")
}

func binaryPath(name, tempDir, goos string) string {
	path := filepath.Join(tempDir, name)
	if goos == "windows" {
		return path + ".exe"
	}
	return path
}
