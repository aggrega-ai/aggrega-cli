// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"

	"github.com/spf13/cobra"
)

var serveWorkspaceRoot string

func newServeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run as a long-running compiler-as-service over JSON-RPC 2.0 on stdin/stdout",
		Long: `Run the Aggrega compiler as a long-running service. The daemon
reads JSON-RPC 2.0 messages from stdin and writes responses to stdout
under the aggrega/ method namespace. State (parsed YAML, diagnostics,
resolved catalog) holds across requests so IDE clients can issue many
cheap operations without re-paying per-invocation startup cost.

The daemon anchors its workspace root from either --workspace-root or
the current working directory. When --workspace-root is set, that path
is used as-is (and validated to exist as a directory). Otherwise the
daemon walks upward from the current working directory looking for
any of:

  .aggrega-config.yaml   .aggrega/        aggregate*.yaml
  release*.yaml          manifest*.yaml

The first directory containing a marker anchors the workspace.

Configuration is the merged composition of three layers, in
precedence order: workspace .aggrega-config.yaml (when discovered)
overrides ~/.config/aggrega/config.yaml (or $AGGREGA_CONFIG, or
$XDG_CONFIG_HOME/aggrega/config.yaml), which overrides built-in
defaults.

The daemon honors the aggrega/shutdown JSON-RPC notification for a
cooperative cross-platform exit: the daemon completes the in-flight
handler if any, refuses new requests with code 1005, and exits
cleanly within a 2-second drain budget.`,
		RunE: runServe,
	}
	c.Flags().StringVar(&serveWorkspaceRoot, "workspace-root", "",
		"absolute path to use as the workspace root (skips discovery)")
	return c
}

func runServe(cmd *cobra.Command, args []string) error {
	root, err := validateWorkspaceRootFlag(serveWorkspaceRoot)
	if err != nil {
		return err
	}
	return serve.Run(serve.RunConfig{
		Stdin:         cmd.InOrStdin(),
		Stdout:        cmd.OutOrStdout(),
		Stderr:        cmd.ErrOrStderr(),
		WorkspaceRoot: root,
	})
}

// validateWorkspaceRootFlag enforces the contract on --workspace-root.
// Empty string is treated as unset (the daemon falls through to
// discovery). A non-empty value resolves to an absolute path, must
// exist on disk, and must be a directory.
func validateWorkspaceRootFlag(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("--workspace-root: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("--workspace-root: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("--workspace-root: %s is not a directory", abs)
	}
	return abs, nil
}
