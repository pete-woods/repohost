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

// Command repohost publishes apt and yum/dnf package repositories to cloud
// object storage. See "repohost push --help".
package main

import (
	"os"

	"github.com/pete-woods/repohost/internal/cli"
)

// version is stamped at build time via -ldflags "-X main.version=..."; see
// .goreleaser.yml. It defaults to "dev" for `go build`/`go install`.
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		os.Exit(1)
	}
}
