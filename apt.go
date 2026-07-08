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

package repohost

import (
	"context"
	"io"

	"github.com/pete-woods/repohost/internal/apt"
	"github.com/pete-woods/repohost/internal/sign"
)

// Signer signs repository metadata. Implement it to wrap your GPG key; repohost
// itself takes no crypto dependency. Leave the Signer config field nil to
// publish an unsigned repository. The same Signer works for both apt and yum.
type Signer = sign.Signer

// APTConfig configures an apt repository. Distribution is required; Components
// defaults to {"main"}; KeepVersions of zero keeps every version.
type APTConfig = apt.Config

// APT publishes an apt repository to a cloud object-storage bucket.
type APT struct {
	publisher *apt.Publisher
}

// NewAPT returns an APT that publishes to the given backend (see S3 and GCS).
func NewAPT(backend Backend, cfg APTConfig) *APT {
	return &APT{publisher: apt.New(backend.store, cfg)}
}

// Add uploads a .deb read from deb into the named component ("" means "main"),
// updates the repository's indexes, applies the retention policy, and
// republishes the Release. Publishing assumes a single writer per repository.
func (a *APT) Add(ctx context.Context, component string, deb io.Reader) error {
	return a.publisher.Add(ctx, component, deb)
}
