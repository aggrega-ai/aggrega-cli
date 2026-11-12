// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
)

func newLogoutCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "logout <registry>",
		Short: "Remove a stored registry credential",
		Long: "Delete the stored credential for a registry from the shared OCI credential store. " +
			"Removing an absent credential is not an error.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return logoutRun(cmd.Context(), logoutConfig{Registry: args[0]})
		},
	}
	return c
}

// logoutConfig is the input contract for the logout workflow. Test bodies set
// Store/Out to inject an isolated credential store and output sink; nil means
// production.
type logoutConfig struct {
	Registry string

	Store credentials.Store // nil → NewStoreFromDocker
	Out   io.Writer         // nil → os.Stdout
}

// logoutRun removes the stored credential and reports it at the terminal.
//
// The removal itself lives in the shared registry-operations layer, which the
// daemon reaches too, so this holds only the sentence a terminal reader gets.
func logoutRun(ctx context.Context, cfg logoutConfig) error {
	res, err := registryops.Logout(ctx, registryops.LogoutOptions{
		Registry: cfg.Registry,
		Store:    cfg.Store,
	})
	if err != nil {
		return err
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "Logged out of %s\n", res.Registry)
	return nil
}
