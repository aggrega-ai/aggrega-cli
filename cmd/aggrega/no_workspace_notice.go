// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// noWorkspaceNotice writes one line on errOut saying which folder a command used
// as its workspace root where no workspace was found from the folder it ran in
// and nothing named one, and how to name the workspace instead; it writes
// nothing in every other case.
//
// Whether to say it is the shared resolution's one predicate
// (workspaceconfig.ResolvedWorkspace.FoundNoWorkspace), asked of the resolution
// the command uses, so this line and the assistant server's sentence for the same
// fact cannot come to disagree about when it holds. The folder is the
// resolution's root, named absolutely as this surface names every other
// location.
//
// It is a notice and never a refusal: the command runs, and an empty listing or
// an entity not found is then read against the folder the line names rather
// than as a configuration problem. It goes to the error stream, which is never
// the stream a caller parses, and it is written whether or not that stream is a
// terminal: a log of a run that used an unintended folder is where the line is
// most needed.
func noWorkspaceNotice(errOut io.Writer, rw *workspaceconfig.ResolvedWorkspace) {
	if !rw.FoundNoWorkspace() {
		return
	}
	fmt.Fprintf(errOut, "note: no Aggrega workspace was found from %s, so it is used as the workspace root; "+
		"pass --%s naming your workspace, or run the command inside it\n",
		rw.Root(), workspaceconfig.WorkspaceRootFlagName)
}
