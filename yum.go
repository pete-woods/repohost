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

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pete-woods/repohost/internal/storage/s3store"
	"github.com/pete-woods/repohost/internal/yum"
)

// YUMConfig configures a yum/dnf repository. KeepVersions of zero keeps every
// version; Signer is optional (it signs repomd.xml).
type YUMConfig = yum.Config

// YUM publishes an RPM (yum/dnf) repository to a cloud object-storage bucket.
type YUM struct {
	publisher *yum.Publisher
}

// NewYUM returns a YUM that publishes to bucket using the given S3 client. As
// with NewAPT, the caller owns the client and its configuration, so the same
// code targets AWS S3 or any S3-compatible service. A GCS constructor will
// follow.
func NewYUM(client *s3.Client, bucket string, cfg YUMConfig) *YUM {
	return &YUM{publisher: yum.New(s3store.New(client, bucket), cfg)}
}

// Add reads an .rpm from rpm, uploads it into the repository's Packages tree,
// applies the retention policy, and regenerates (and signs) the repodata.
// Publishing assumes a single writer per repository.
func (y *YUM) Add(ctx context.Context, rpm io.Reader) error {
	return y.publisher.Add(ctx, rpm)
}
