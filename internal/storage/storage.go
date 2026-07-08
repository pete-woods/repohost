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

// Package storage defines the object-storage abstraction that repository
// publishing is built on. Backend implementations live in sub-packages such as
// s3store; GCS support will follow.
package storage

import (
	"context"
	"errors"
	"io"
)

// ErrNotExist is returned (wrapped) by Store.Get when the requested key does
// not exist. Callers should test for it with errors.Is.
var ErrNotExist = errors.New("storage: object does not exist")

// Object describes a single stored object as returned by Store.List.
type Object struct {
	// Key is the full object key, including any prefix.
	Key string
	// Size is the object size in bytes.
	Size int64
}

// Store is the object-storage abstraction that repositories are published to.
//
// Implementations wrap a specific backend (for example s3store for S3; GCS
// support will follow). Keys are '/'-separated paths without a leading slash,
// mirroring the layout a client would fetch over HTTP.
type Store interface {
	// Put stores body under key, overwriting any existing object at that key.
	Put(ctx context.Context, key string, body io.Reader) error

	// Get returns a reader for the object stored under key. If the key does not
	// exist it returns an error satisfying errors.Is(err, ErrNotExist). The
	// caller is responsible for closing the returned reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object stored under key. Deleting a key that does not
	// exist is not an error.
	Delete(ctx context.Context, key string) error

	// List returns every object whose key begins with prefix. An empty prefix
	// lists the whole store.
	List(ctx context.Context, prefix string) ([]Object, error)
}
