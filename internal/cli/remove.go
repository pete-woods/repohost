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
	"errors"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/pete-woods/repohost"
)

func newRemoveCmd() *cobra.Command {
	opts := &backendOptions{}

	cmd := &cobra.Command{
		Use:     "rm",
		Aliases: []string{"remove"},
		Short:   "Remove a package version from a repository",
	}
	// --sign-key matters here too: removing a package regenerates the repository
	// metadata, which must be re-signed for a signed repo to stay valid.
	opts.addFlags(cmd.PersistentFlags())

	cmd.AddCommand(newRemoveDebCmd(opts), newRemoveRPMCmd(opts))
	return cmd
}

func newRemoveDebCmd(opts *backendOptions) *cobra.Command {
	var distribution, component string

	cmd := &cobra.Command{
		Use:   "deb [flags] <name> <version>",
		Short: "Remove a .deb package version from an apt repository",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if distribution == "" {
				return errors.New("--distribution is required")
			}
			name, version := args[0], args[1]

			log := slog.Default().With("backend", opts.backend, "bucket", opts.bucket, "format", "deb")
			if opts.dryRun {
				log.Info("dry run: no changes will be written", "distribution", distribution, "component", component, "package", name, "version", version)
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
				Signer:       signer,
			})
			removed, err := apt.Remove(ctx, component, name, version)
			if err != nil {
				return err
			}
			logRemoved(log, name, version, removed)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&distribution, "distribution", "", "target distribution/suite, e.g. stable or bookworm (required)")
	f.StringVar(&component, "component", "main", "target component")
	return cmd
}

func newRemoveRPMCmd(opts *backendOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "rpm [flags] <name> <version>",
		Short: "Remove an .rpm package version from a yum/dnf repository",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			name, version := args[0], args[1]

			log := slog.Default().With("backend", opts.backend, "bucket", opts.bucket, "format", "rpm")
			if opts.dryRun {
				log.Info("dry run: no changes will be written", "package", name, "version", version)
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

			yum := repohost.NewYUM(backend, repohost.YUMConfig{Signer: signer})
			removed, err := yum.Remove(ctx, name, version)
			if err != nil {
				return err
			}
			logRemoved(log, name, version, removed)
			return nil
		},
	}
}

// logRemoved reports the outcome; a zero count is a warning, not an error, since
// removal is idempotent.
func logRemoved(log *slog.Logger, name, version string, removed int) {
	if removed == 0 {
		log.Warn("no matching package found", "package", name, "version", version)
		return
	}
	log.Info("removed", "package", name, "version", version, "count", removed)
}
