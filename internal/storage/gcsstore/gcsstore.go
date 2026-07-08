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

// Package gcsstore implements the storage.Store interface on top of Google Cloud
// Storage (and GCS-compatible backends such as fake-gcs-server).
package gcsstore

import (
	"context"
	"errors"
	"fmt"
	"io"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/pete-woods/repohost/internal/storage"
)

// Store is a storage.Store backed by a single GCS bucket.
type Store struct {
	client *gcs.Client
	bucket string
}

// Ensure Store satisfies the interface it is written against.
var _ storage.Store = (*Store)(nil)

// New returns a Store that reads and writes objects in bucket using client.
func New(client *gcs.Client, bucket string) *Store {
	return &Store{client: client, bucket: bucket}
}

func (s *Store) object(key string) *gcs.ObjectHandle {
	return s.client.Bucket(s.bucket).Object(key)
}

// Put stores body under key, overwriting any existing object.
func (s *Store) Put(ctx context.Context, key string, body io.Reader) error {
	w := s.object(key).NewWriter(ctx)
	if _, err := io.Copy(w, body); err != nil {
		_ = w.Close()
		return fmt.Errorf("gcsstore: put %q: %w", key, err)
	}
	// Close finalizes the upload; upload errors surface here.
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcsstore: put %q: %w", key, err)
	}
	return nil
}

// Get returns a reader for the object stored under key. A missing key is
// reported as an error satisfying errors.Is(err, storage.ErrNotExist).
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := s.object(key).NewReader(ctx)
	if err != nil {
		if errors.Is(err, gcs.ErrObjectNotExist) {
			return nil, fmt.Errorf("gcsstore: get %q: %w", key, storage.ErrNotExist)
		}
		return nil, fmt.Errorf("gcsstore: get %q: %w", key, err)
	}
	return r, nil
}

// Delete removes the object stored under key. Deleting a missing key is not an
// error, matching the semantics of the other stores.
func (s *Store) Delete(ctx context.Context, key string) error {
	err := s.object(key).Delete(ctx)
	if err != nil && !errors.Is(err, gcs.ErrObjectNotExist) {
		return fmt.Errorf("gcsstore: delete %q: %w", key, err)
	}
	return nil
}

// List returns every object whose key begins with prefix.
func (s *Store) List(ctx context.Context, prefix string) ([]storage.Object, error) {
	var objects []storage.Object

	it := s.client.Bucket(s.bucket).Objects(ctx, &gcs.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcsstore: list %q: %w", prefix, err)
		}
		objects = append(objects, storage.Object{Key: attrs.Name, Size: attrs.Size})
	}

	return objects, nil
}
