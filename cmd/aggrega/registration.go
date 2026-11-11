// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
)

// mustAttach registers a command as the one carrying an operation, and is how
// every capability in this tree reaches it.
//
// The registrar refuses a command the operation table does not account for — an
// identifier that is not an operation, an operation this surface declares
// unavailable, or a command whose invocation path is not the one the table gives
// — and this turns that refusal into a startup failure. The tree is a
// compile-time literal, so a refusal is a programming error a reader fixes by
// editing either the command or the row, and a binary that started anyway would
// serve a command nobody declared.
//
// It is also where a command is given the shared progress surface, where the
// operation's row says its command shows the steps it waits on
// (clisurface.OperationWaitDisplay). Applying it here, from the row, is what keeps
// the set of commands showing progress the table's to decide, so no command is
// given it, or left without it, by an edit elsewhere.
func mustAttach(parent, cmd *cobra.Command, id string) {
	if display, _ := clisurface.OperationWaitDisplay(id); display == clisurface.ShowsWaitSteps {
		showsOperationProgress(cmd)
	}
	if err := clisurface.Attach(parent, cmd, id); err != nil {
		panic(err.Error())
	}
}

// mustAttachGroup registers a command that only holds subcommands. A grouping
// command runs nothing, so it is no capability and names no operation.
func mustAttachGroup(parent, cmd *cobra.Command) {
	if err := clisurface.AttachGroup(parent, cmd); err != nil {
		panic(err.Error())
	}
}

// mustAttachContributed registers a command a configured plugin contributes,
// which is the plugin's capability rather than the compiler's and is therefore
// absent from the operation set. What it shows while it waits is the one
// declaration for every contributed command
// (clisurface.ContributedCommandWaitDisplay).
func mustAttachContributed(parent, cmd *cobra.Command) {
	if clisurface.ContributedCommandWaitDisplay == clisurface.ShowsWaitSteps {
		showsOperationProgress(cmd)
	}
	if err := clisurface.AttachContributed(parent, cmd); err != nil {
		panic(err.Error())
	}
}
