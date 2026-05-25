// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/aggrega-ai/aggrega-cli/internal/serve"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run as a long-running compiler-as-service over JSON-RPC 2.0 on stdin/stdout",
	Long: `Run the Aggrega compiler as a long-running service. The daemon
reads JSON-RPC 2.0 messages from stdin and writes responses to stdout
under the aggrega/ method namespace. State (parsed YAML, diagnostics,
resolved catalog) holds across requests so IDE clients can issue many
cheap operations without re-paying per-invocation startup cost.

The daemon discovers its workspace root by walking upward from the
current working directory looking for .aggrega-config.yaml. The IDE
client is expected to set the subprocess CWD to the workspace root
before spawn.`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	return serve.Run(serve.RunConfig{
		Stdin:  cmd.InOrStdin(),
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	})
}
