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

func newServeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run as a long-running compiler-as-service over JSON-RPC 2.0 on stdin/stdout",
		Long: `Run the Aggrega compiler as a long-running service. The daemon
reads JSON-RPC 2.0 messages from stdin and writes responses to stdout
under the aggrega/ method namespace. State (parsed YAML, diagnostics,
resolved catalog) holds across requests so IDE clients can issue many
cheap operations without re-paying per-invocation startup cost.

The daemon resolves its workspace root through the same path every
aggrega command uses: --workspace-root when set (used as-is, validated
to exist as a directory), otherwise discovery walking upward from the
current working directory. A directory anchors the workspace when it
holds a .aggrega-config.yaml carrying a workspace: block, or — as a
fallback — any of .aggrega/, aggregate*.yaml, release*.yaml,
manifest*.yaml.

Configuration is the cascade composition of the .aggrega-config.yaml
files from the workspace root down to the working directory (nearer
overrides farther), beneath which $AGGREGA_CONFIG (when set) and the
built-in defaults apply.

The daemon honors the aggrega/shutdown JSON-RPC notification for a
cooperative cross-platform exit: the daemon completes the in-flight
handler if any, refuses new requests with code 1005, and exits
cleanly within a 2-second drain budget.`,
		RunE: runServe,
	}
	c.Flags().Bool("render-diagnostics", false,
		"Run the render pass on IR-graph inspection so render-time warnings are produced. "+
			"Off by default (faster; the returned graph is identical either way, only render-stage "+
			"advisory warnings are dropped). When set, overrides the serve.render_diagnostics config key.")
	return c
}

func runServe(cmd *cobra.Command, args []string) error {
	flagRoot, _ := cmd.Flags().GetString("workspace-root")
	root, err := validateWorkspaceRootFlag(workspaceRootFromFlagOrEnv(cmd, flagRoot, nil))
	if err != nil {
		return err
	}
	// Pass the flag only when the operator set it, so an unset flag falls
	// through to the config-file key rather than forcing false over it.
	var renderDiagnostics *bool
	if cmd.Flags().Changed("render-diagnostics") {
		v, _ := cmd.Flags().GetBool("render-diagnostics")
		renderDiagnostics = &v
	}
	return serve.Run(serve.RunConfig{
		Stdin:             cmd.InOrStdin(),
		Stdout:            cmd.OutOrStdout(),
		Stderr:            cmd.ErrOrStderr(),
		WorkspaceRoot:     root,
		CompilerVersion:   provenanceCompilerVersion(),
		RenderDiagnostics: renderDiagnostics,
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
