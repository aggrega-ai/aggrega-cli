// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "aggrega",
	Short: "Aggrega compiler CLI",
	Long:  "Aggrega compiles declarative aggregates into intermediate representation and deployment artifacts.",
}

// Execute runs the root command. On cobra dispatch error — unknown
// subcommand, flag-parse failure, RunE error from any subcommand —
// the error is printed to stderr and the process exits with status 1.
// Successful dispatch returns normally; the caller (main) then
// terminates with exit status 0. The function intentionally handles
// the error+exit boundary itself rather than returning the error to
// the caller, so main remains a thin shim.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
