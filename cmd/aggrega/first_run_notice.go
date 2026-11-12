// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// configInitInvocation is how a reader types the command that seeds a
// configuration, as the first-run notice names it. That command is
// machine-scoped, so the notice is never written on it and a reader running it is
// not told to run it; a test holds both facts against the live tree.
const configInitInvocation = "aggrega config init"

// firstRunConfigNotice writes one line on errOut routing a reader whose
// resolution found no configuration file to the command that creates one, and
// writes nothing in every other case.
//
// Which commands may write it at all is decided by the root command, by the one
// declaration that decides the warning a command writes outside a workspace
// (workspaceScopedCommand): a command whose result does not depend on the
// workspace adds neither line to its answer.
//
// It is silent unless errOut is an interactive terminal, so a redirected or
// piped error stream — a continuous-integration log, a capture, a pipeline —
// receives nothing at all and the notice cannot accumulate where nobody reads
// it. It writes to no other stream, asks nothing and waits for nothing, and
// returns no error: a reader who has not configured anything yet has not failed
// at anything, so neither what a caller parses nor the status the process exits
// with may move because of it.
//
// An environment override alone is not a configuration file and does not
// suppress it. A reader who exported a variable still has nothing on disk for
// the next invocation to read, and writing that file is exactly what the
// command named here does.
func firstRunConfigNotice(errOut io.Writer, rw *workspaceconfig.ResolvedWorkspace) {
	if rw == nil || rw.ConfigFileContributed() {
		return
	}
	if !fileTerminal(errOut) {
		return
	}
	fmt.Fprintf(errOut,
		"No configuration file found; run '%s' to seed one with a registry and its trust policy.\n",
		configInitInvocation)
}
