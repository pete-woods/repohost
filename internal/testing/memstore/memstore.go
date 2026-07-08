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

// Package memstore provides an in-memory storage.Store for fast, docker-free
// tests of the repository publishers.
package memstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/pete-woods/repohost/internal/storage"
)

// Store is an in-memory implementation of storage.Store.
type Store struct {
	mu   sync.Mutex
	objs map[string][]byte
}

// New returns an empty Store.
func New() *Store {
	return &Store{objs: make(map[string][]byte)}
}

// Put stores body under key.
func (s *Store) Put(_ context.Context, key string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objs[key] = data
	return nil
}

// Get returns a reader for key, or an ErrNotExist-wrapped error if absent.
func (s *Store) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objs[key]
	if !ok {
		return nil, fmt.Errorf("memstore get %q: %w", key, storage.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Delete removes key. Deleting a missing key is not an error.
func (s *Store) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objs, key)
	return nil
}

// List returns objects whose key begins with prefix, ordered lexicographically.
func (s *Store) List(_ context.Context, prefix string) ([]storage.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]string, 0, len(s.objs))
	for k := range s.objs {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	objs := make([]storage.Object, 0, len(keys))
	for _, k := range keys {
		objs = append(objs, storage.Object{Key: k, Size: int64(len(s.objs[k]))})
	}
	return objs, nil
}

// Has reports whether an object exists at key.
func (s *Store) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objs[key]
	return ok
}

// Data returns the stored bytes for key and whether it exists.
func (s *Store) Data(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objs[key]
	return data, ok
}
