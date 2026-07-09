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

// Package cli implements the repohost command-line interface. The main package
// (cmd/repohost) is a thin wrapper around Execute so the command wiring stays
// testable and out of package main.
package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

// Execute builds the root command, runs it, and returns any error. Errors are
// logged (structured) here rather than printed by cobra, so all output shares
// one format.
func Execute() error {
	err := newRootCmd().Execute()
	if err != nil {
		slog.Error("command failed", "error", err)
	}
	return err
}

func newRootCmd() *cobra.Command {
	var logLevel, logFormat string

	root := &cobra.Command{
		Use:   "repohost",
		Short: "Publish apt and yum/dnf package repositories to cloud object storage",
		Long: "repohost publishes Debian (apt) and RPM (yum/dnf) package repositories\n" +
			"to an S3 or GCS bucket. Point apt-get or dnf at the bucket's public URL.",
		// Errors are logged by Execute; usage on a runtime error is just noise.
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			logger, err := newLogger(logLevel, logFormat)
			if err != nil {
				return err
			}
			slog.SetDefault(logger)
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	pf.StringVar(&logFormat, "log-format", "text", "log format (text, json)")

	root.AddCommand(newPushCmd())
	return root
}

// newLogger builds a slog logger writing to stderr at the given level and format.
func newLogger(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("invalid --log-level %q (want debug, info, warn or error)", level)
	}

	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, opts)), nil
	default:
		return nil, fmt.Errorf("invalid --log-format %q (want text or json)", format)
	}
}
