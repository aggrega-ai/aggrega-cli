// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// configInitInvocation is how a reader types the command that seeds a
// configuration. The first-run notice names it and the root command withholds
// the notice on it, both from this one spelling, so the sentence a reader is
// shown and the invocation it withholds on cannot come to name different things.
const configInitInvocation = "aggrega config init"

// firstRunConfigNotice writes one line on errOut routing a reader whose
// resolution found no configuration file to the command that creates one, and
// writes nothing in every other case.
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
