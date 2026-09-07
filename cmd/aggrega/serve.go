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
		Args: cobra.NoArgs,
		RunE: runServe,
	}
	c.Flags().Bool("build-on-inspect", false,
		"Run the artifact-rendering stage while inspecting an IR graph, so the advisories aggrega "+
			"build raises while rendering also appear as editor problems. It adds none of the "+
			"checks a build halts on, so the editor still draws a graph where a build would "+
			"refuse. Nothing is written to disk: no rendered artifacts, no output directory, no "+
			"lockfile. Off by default, matching aggrega inspect, which raises no such advisory "+
			"either. When set, overrides the serve.build_on_inspect config key.")
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
	var buildOnInspect *bool
	if cmd.Flags().Changed("build-on-inspect") {
		v, _ := cmd.Flags().GetBool("build-on-inspect")
		buildOnInspect = &v
	}
	return serve.Run(serve.RunConfig{
		Stdin:           cmd.InOrStdin(),
		Stdout:          cmd.OutOrStdout(),
		Stderr:          cmd.ErrOrStderr(),
		WorkspaceRoot:   root,
		CompilerVersion: provenanceCompilerVersion(),
		Toolchain:       toolchainStamp(),
		BuildOnInspect:  buildOnInspect,
		// Supplied for the same reason the compiler version and the toolchain stamp
		// are: the value belongs to this package and the daemon lives below it. The
		// install's collision guard reads the freshly built command tree, which is
		// what authoritatively decides the built-in command names, so handing the
		// daemon this one implementation is what keeps both surfaces installing
		// under the same guard instead of one of them under a copy of it.
		InstallCLIPlugin: installCLIPlugin,
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
