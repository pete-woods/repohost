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

// Package s3store implements the storage.Store interface on top of Amazon S3
// (and S3-compatible backends such as SeaweedFS).
package s3store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/pete-woods/repohost/internal/storage"
)

// Store is a storage.Store backed by a single S3 bucket.
type Store struct {
	client *s3.Client
	bucket string
}

// Ensure Store satisfies the interface it is written against.
var _ storage.Store = (*Store)(nil)

// New returns a Store that reads and writes objects in bucket using client.
func New(client *s3.Client, bucket string) *Store {
	return &Store{client: client, bucket: bucket}
}

// Put stores body under key, overwriting any existing object.
func (s *Store) Put(ctx context.Context, key string, body io.Reader) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   body,
	})
	if err != nil {
		return fmt.Errorf("s3store: put %q: %w", key, err)
	}
	return nil
}

// Get returns a reader for the object stored under key. A missing key is
// reported as an error satisfying errors.Is(err, storage.ErrNotExist).
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("s3store: get %q: %w", key, storage.ErrNotExist)
		}
		return nil, fmt.Errorf("s3store: get %q: %w", key, err)
	}
	return out.Body, nil
}

// Delete removes the object stored under key. Deleting a missing key is not an
// error, matching S3's own semantics.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3store: delete %q: %w", key, err)
	}
	return nil
}

// List returns every object whose key begins with prefix, paging through the
// bucket as needed.
func (s *Store) List(ctx context.Context, prefix string) ([]storage.Object, error) {
	var objects []storage.Object

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	}
	for {
		out, err := s.client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("s3store: list %q: %w", prefix, err)
		}

		objects = slices.Grow(objects, len(out.Contents))
		for _, o := range out.Contents {
			objects = append(objects, storage.Object{
				Key:  aws.ToString(o.Key),
				Size: aws.ToInt64(o.Size),
			})
		}

		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		input.ContinuationToken = out.NextContinuationToken
	}

	return objects, nil
}

// isNotFound reports whether err represents a missing S3 object. It covers the
// typed NoSuchKey/NotFound errors as well as the generic API error codes that
// S3-compatible backends return for a 404.
func isNotFound(err error) bool {
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}
