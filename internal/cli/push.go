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

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	gcs "cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/pete-woods/repohost"
	"github.com/pete-woods/repohost/pgp"
)

// signPassphraseEnv is the name of the environment variable the signing key's
// passphrase is read from, so the passphrase never appears on the command line
// or in shell history. (The value is a variable name, not a secret.)
const signPassphraseEnv = "REPOHOST_SIGN_PASSPHRASE" //nolint:gosec // G101: env var name, not a credential

// backendOptions holds the flags common to every repository-mutating command:
// which backend and bucket to target, signing, and dry-run. push and rm both
// embed or use it.
type backendOptions struct {
	backend   string
	bucket    string
	endpoint  string
	region    string
	pathStyle bool
	signKey   string
	dryRun    bool
}

func (o *backendOptions) addFlags(pf *pflag.FlagSet) {
	pf.StringVar(&o.backend, "backend", "s3", "storage backend (s3, gcs)")
	pf.StringVar(&o.bucket, "bucket", "", "destination bucket (required)")
	pf.StringVar(&o.endpoint, "endpoint", "", "S3-compatible endpoint URL, e.g. for MinIO/SeaweedFS (optional)")
	pf.StringVar(&o.region, "region", "", "S3 region (defaults to the AWS environment)")
	pf.BoolVar(&o.pathStyle, "s3-path-style", false, "use path-style S3 addressing (needed by most S3-compatible services)")
	pf.StringVar(&o.signKey, "sign-key", "", "path to an ASCII-armored PGP private key to sign the repository; passphrase via "+signPassphraseEnv)
	pf.BoolVar(&o.dryRun, "dry-run", false, "log the intended actions without writing anything")
}

// pushOptions is backendOptions plus the retention limit, which only applies
// when adding packages.
type pushOptions struct {
	backendOptions
	keepVersions int
}

func newPushCmd() *cobra.Command {
	opts := &pushOptions{}

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Publish packages to a repository",
	}

	pf := cmd.PersistentFlags()
	opts.addFlags(pf)
	pf.IntVar(&opts.keepVersions, "keep-versions", 0, "versions to retain per package (0 keeps all)")

	cmd.AddCommand(newPushDebCmd(opts), newPushRPMCmd(opts))
	return cmd
}

func newPushDebCmd(opts *pushOptions) *cobra.Command {
	var distribution, component, origin, label, description string

	cmd := &cobra.Command{
		Use:   "deb [flags] <file.deb>...",
		Short: "Publish .deb packages to an apt repository",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if distribution == "" {
				return errors.New("--distribution is required")
			}
			if err := checkFiles(args); err != nil {
				return err
			}

			log := slog.Default().With("backend", opts.backend, "bucket", opts.bucket, "format", "deb")
			if opts.dryRun {
				logDryRun(log, args, "distribution", distribution, "component", component)
				return nil
			}

			ctx := cmd.Context()
			backend, cleanup, err := opts.newBackend(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			signer, err := opts.signer()
			if err != nil {
				return err
			}

			apt := repohost.NewAPT(backend, repohost.APTConfig{
				Distribution: distribution,
				Components:   []string{component},
				Origin:       origin,
				Label:        label,
				Description:  description,
				KeepVersions: opts.keepVersions,
				Signer:       signer,
			})
			for _, file := range args {
				if err := pushFile(file, func(r io.Reader) error { return apt.Add(ctx, component, r) }); err != nil {
					return err
				}
				log.Info("published", "file", file, "distribution", distribution, "component", component)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&distribution, "distribution", "", "target distribution/suite, e.g. stable or bookworm (required)")
	f.StringVar(&component, "component", "main", "target component")
	f.StringVar(&origin, "origin", "", "Release Origin field")
	f.StringVar(&label, "label", "", "Release Label field")
	f.StringVar(&description, "description", "", "Release Description field")
	return cmd
}

func newPushRPMCmd(opts *pushOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "rpm [flags] <file.rpm>...",
		Short: "Publish .rpm packages to a yum/dnf repository",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if err := checkFiles(args); err != nil {
				return err
			}

			log := slog.Default().With("backend", opts.backend, "bucket", opts.bucket, "format", "rpm")
			if opts.dryRun {
				logDryRun(log, args)
				return nil
			}

			ctx := cmd.Context()
			backend, cleanup, err := opts.newBackend(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			signer, err := opts.signer()
			if err != nil {
				return err
			}

			yum := repohost.NewYUM(backend, repohost.YUMConfig{
				KeepVersions: opts.keepVersions,
				Signer:       signer,
			})
			for _, file := range args {
				if err := pushFile(file, func(r io.Reader) error { return yum.Add(ctx, r) }); err != nil {
					return err
				}
				log.Info("published", "file", file)
			}
			return nil
		},
	}
}

// validate checks the flags common to every backend command.
func (o *backendOptions) validate() error {
	if o.bucket == "" {
		return errors.New("--bucket is required")
	}
	switch o.backend {
	case "s3", "gcs":
		return nil
	default:
		return fmt.Errorf("invalid --backend %q (want s3 or gcs)", o.backend)
	}
}

// signer builds a Signer from --sign-key, or returns nil (unsigned) when unset.
func (o *backendOptions) signer() (repohost.Signer, error) {
	if o.signKey == "" {
		return nil, nil
	}
	key, err := os.ReadFile(o.signKey) //nolint:gosec // operator-supplied signing key path
	if err != nil {
		return nil, fmt.Errorf("reading --sign-key: %w", err)
	}
	signer, err := pgp.NewSigner(key, os.Getenv(signPassphraseEnv))
	if err != nil {
		return nil, err
	}
	return signer, nil
}

// newBackend constructs the storage backend and a cleanup to release any client
// resources (the GCS client holds connections; the S3 client needs none).
func (o *backendOptions) newBackend(ctx context.Context) (repohost.Backend, func(), error) {
	switch o.backend {
	case "s3":
		backend, err := o.newS3Backend(ctx)
		return backend, func() {}, err
	case "gcs":
		return o.newGCSBackend(ctx)
	default:
		return repohost.Backend{}, func() {}, fmt.Errorf("invalid --backend %q", o.backend)
	}
}

func (o *backendOptions) newS3Backend(ctx context.Context) (repohost.Backend, error) {
	var loadOpts []func(*awsconfig.LoadOptions) error
	if o.region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(o.region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return repohost.Backend{}, fmt.Errorf("loading AWS config: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(p *s3.Options) {
		if o.endpoint != "" {
			p.BaseEndpoint = aws.String(o.endpoint)
		}
		p.UsePathStyle = o.pathStyle
	})
	return repohost.S3(client, o.bucket), nil
}

func (o *backendOptions) newGCSBackend(ctx context.Context) (repohost.Backend, func(), error) {
	client, err := gcs.NewClient(ctx)
	if err != nil {
		return repohost.Backend{}, func() {}, fmt.Errorf("creating GCS client: %w", err)
	}
	return repohost.GCS(client, o.bucket), func() { _ = client.Close() }, nil
}

// checkFiles fails fast if any argument is missing or a directory, so a typo
// does not leave a repository half-published.
func checkFiles(paths []string) error {
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory, not a package", p)
		}
	}
	return nil
}

// pushFile opens path and hands the reader to add, closing the file afterwards.
func pushFile(path string, add func(io.Reader) error) error {
	f, err := os.Open(path) //nolint:gosec // operator-supplied package path
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := add(f); err != nil {
		return fmt.Errorf("publishing %s: %w", path, err)
	}
	return nil
}

// logDryRun reports the resolved target and the files that would be published,
// writing nothing. extra carries format-specific context (e.g. distribution).
func logDryRun(log *slog.Logger, files []string, extra ...any) {
	log.Info("dry run: no changes will be written", extra...)
	for _, file := range files {
		log.Info("would publish", "file", file)
	}
}
