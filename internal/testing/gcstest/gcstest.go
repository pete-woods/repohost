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

package gcstest

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"gotest.tools/v3/assert"
)

// Fixture holds the configuration and live client used to create and tear down
// a temporary Cloud Storage bucket backed by fake-gcs-server.
type Fixture struct {
	Client             *storage.Client
	URL                string // URL is the fake-gcs-server JSON API endpoint, e.g. http://localhost:9124/storage/v1/
	ProjectID          string // ProjectID is the project the bucket is created under; the emulator accepts any value
	Bucket             string
	Location           string
	Versioned          bool // Versioned is set true if we have managed to enable object versioning
	ForceLocal         bool // ForceLocal will fail on a local run if fake-gcs-server is not running
	ForceVersioned     bool // ForceVersioned will fail if the bucket can not be set versioned
	DisallowPublicRead bool // DisallowPublicRead will not set a public-read default object ACL on the bucket
}

// Default sets up and returns the default fake-gcs-server fixture
func Default(ctx context.Context, t testing.TB) *Fixture {
	fix := &Fixture{}
	Setup(ctx, t, fix)
	return fix
}

// Setup will take the given fixture adding default values as needed and update the fields in the fixture
// with whatever values were used.
func Setup(ctx context.Context, t testing.TB, fix *Fixture) {
	t.Helper()
	setConfigDefaults(t, fix)
	skipIfNotRunning(t, fix)

	assert.Assert(t, fix.Client == nil, "fixture client is expected to be nil")

	err := runSetup(ctx, fix)
	assert.NilError(t, err)

	t.Cleanup(func() {
		fix.clean(t)
	})
}

func runSetup(ctx context.Context, fix *Fixture) error {
	client, err := newClient(ctx, fix)
	if err != nil {
		return fmt.Errorf("create storage client failed: %w", err)
	}
	fix.Client = client

	bucketAttrs := &storage.BucketAttrs{
		Location:          fix.Location,
		VersioningEnabled: true,
	}
	if !fix.DisallowPublicRead {
		// fake-gcs-server does not implement bucket IAM and does not enforce read
		// ACLs, so this is a no-op locally; it is the correct set-once, bucket-wide
		// public-read analog when the fixture is pointed at real GCS.
		bucketAttrs.PredefinedDefaultObjectACL = "publicRead"
	}

	bucket := fix.Client.Bucket(fix.Bucket)
	err = bucket.Create(ctx, fix.ProjectID, bucketAttrs)
	if err != nil {
		return fmt.Errorf("create bucket failed: %w", err)
	}

	// Versioning is applied atomically at create time, so read the bucket back to
	// discover whether the backend actually honoured it (the fake-gcs-server
	// filesystem backend, unlike the memory backend, silently ignores it).
	got, err := bucket.Attrs(ctx)
	fix.Versioned = err == nil && got.VersioningEnabled

	if !fix.Versioned && fix.ForceVersioned {
		if err != nil {
			return fmt.Errorf("forced bucket versioning failed: reading bucket attrs: %w", err)
		}
		return errors.New("forced bucket versioning failed: versioning not enabled (fake-gcs-server needs the memory backend)")
	}

	return nil
}

func newClient(ctx context.Context, fix *Fixture) (*storage.Client, error) {
	return storage.NewClient(ctx,
		option.WithEndpoint(fix.URL),
		option.WithoutAuthentication(),
	)
}

func setConfigDefaults(t testing.TB, fix *Fixture) {
	if fix.URL == "" {
		fix.URL = "http://localhost:9124/storage/v1/"
	}
	if fix.ProjectID == "" {
		fix.ProjectID = "repohost-test"
	}
	if fix.Bucket == "" {
		fix.Bucket = BucketName(t)
	}
	if fix.Location == "" {
		fix.Location = "US"
	}
}

func skipIfNotRunning(t testing.TB, fix *Fixture) {
	t.Helper()
	if fix.ForceLocal {
		return
	}
	if strings.EqualFold("true", strings.ToLower(os.Getenv("CI"))) {
		return
	}

	u, err := url.Parse(fix.URL)
	assert.Assert(t, err)

	//nolint:noctx // Cancellation not needed for this test helper
	conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
	if err != nil {
		t.Skip("fake-gcs-server is not running")
	}
	_ = conn.Close()
}

func BucketName(t testing.TB) string {
	t.Helper()

	r := rand.Uint32() >> 8 //#nosec:G404 // just to avoid matching bucket names in case of failed cleanup
	prefix := strings.ToLower(t.Name())
	prefix = strings.ReplaceAll(prefix, "_", "-")
	prefix = strings.ReplaceAll(prefix, "/", "-")

	// Bucket names are limited to 63 characters. This will truncate the test name to 54 characters and allow for a
	// random suffix of 8 digits (max value of a 24 bit number is 16777216)
	if len(prefix) > 54 {
		prefix = prefix[:54]
	}

	return prefix + "-" + strconv.Itoa(int(r))
}

func (f *Fixture) clean(t testing.TB) {
	t.Helper()
	ctx := context.Background()

	bucket := f.Client.Bucket(f.Bucket)

	var err error
	for range 5 {
		f.emptyBucket(ctx, t, bucket)

		err = bucket.Delete(ctx)
		if err == nil || errors.Is(err, storage.ErrBucketNotExist) {
			err = nil
			break
		}
		time.Sleep(time.Second)
	}
	assert.NilError(t, err)
}

func (f *Fixture) emptyBucket(ctx context.Context, t testing.TB, bucket *storage.BucketHandle) {
	t.Helper()

	// Collect first, then delete: mutating the bucket while its object iterator is
	// mid-pagination is best avoided.
	var objects []*storage.ObjectAttrs
	it := bucket.Objects(ctx, &storage.Query{Versions: f.Versioned})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if errors.Is(err, storage.ErrBucketNotExist) {
			return
		}
		if err != nil {
			t.Fatalf("Failed to list objects: %v", err)
			return
		}
		objects = append(objects, attrs)
	}

	for _, attrs := range objects {
		f.deleteObject(ctx, t, bucket, attrs)
	}
}

func (f *Fixture) deleteObject(ctx context.Context, t testing.TB, bucket *storage.BucketHandle, attrs *storage.ObjectAttrs) {
	obj := bucket.Object(attrs.Name)
	// A versioned bucket lists every generation; each must be deleted by
	// generation, otherwise deleting the live object just archives it.
	if f.Versioned && attrs.Generation != 0 {
		obj = obj.Generation(attrs.Generation)
	}

	err := obj.Delete(ctx)
	if err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
		t.Fatalf("Failed to delete object: %v", err)
	}
}
