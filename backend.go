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
	gcs "cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/pete-woods/repohost/internal/storage"
	"github.com/pete-woods/repohost/internal/storage/gcsstore"
	"github.com/pete-woods/repohost/internal/storage/s3store"
)

// Backend is the object-storage backend a repository is published to. Construct
// one with S3 or GCS and pass it to NewAPT or NewYUM. The caller owns the
// underlying client and its configuration (credentials, region, endpoint), which
// is what lets the same repository code target any supported provider.
type Backend struct {
	store storage.Store
}

// S3 returns a Backend that publishes to bucket using the given S3 client. It
// works against AWS S3 and any S3-compatible service.
func S3(client *s3.Client, bucket string) Backend {
	return Backend{store: s3store.New(client, bucket)}
}

// GCS returns a Backend that publishes to bucket using the given Google Cloud
// Storage client.
func GCS(client *gcs.Client, bucket string) Backend {
	return Backend{store: gcsstore.New(client, bucket)}
}
