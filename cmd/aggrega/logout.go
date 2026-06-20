// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

func newLogoutCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "logout <registry>",
		Short: "Remove a stored registry credential",
		Long: "Delete the stored credential for a registry from the shared OCI credential store. " +
			"Removing an absent credential is not an error.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return logoutRun(logoutConfig{Registry: args[0]})
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

// logoutRun deletes the stored credential for the registry. Deletion is
// idempotent: removing a credential that is not present is not an error.
func logoutRun(cfg logoutConfig) error {
	if cfg.Registry == "" {
		return fmt.Errorf("aggrega logout: registry argument required")
	}
	store := cfg.Store
	if store == nil {
		s, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
		if err != nil {
			return fmt.Errorf("aggrega logout: credential store: %w", err)
		}
		store = s
	}
	if err := store.Delete(context.Background(), cfg.Registry); err != nil {
		return fmt.Errorf("aggrega logout: remove credential for %s: %w", cfg.Registry, err)
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "Logged out of %s\n", cfg.Registry)
	return nil
}
