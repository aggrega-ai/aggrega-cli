// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// noWorkspaceNotice writes one warning on errOut naming the folder a command used
// as its workspace root where no workspace was found from the folder it ran in
// and nothing named one, and the command that makes that folder a workspace; it
// writes nothing in every other case.
//
// Whether to say it is the shared resolution's one predicate
// (workspaceconfig.ResolvedWorkspace.FoundNoWorkspace), asked of the resolution
// the command uses, so this line and the assistant server's sentence for the same
// fact cannot come to disagree about when it holds; and the words are the shared
// sentence (clisurface.NoWorkspaceWarning) that server says too. The folder is the
// resolution's root, named absolutely as this surface names every other location.
//
// It is a warning and never a refusal: the command runs, and an empty listing or
// an entity not found is then read against the folder the line names rather
// than as a configuration problem. It goes to the error stream, which is never
// the stream a caller parses, and it is written whether or not that stream is a
// terminal: a log of a run that used an unintended folder is where the line is
// most needed. Which commands may write it at all is decided by the root command
// (warnsOutsideAWorkspace).
func noWorkspaceNotice(errOut io.Writer, rw *workspaceconfig.ResolvedWorkspace) {
	if !rw.FoundNoWorkspace() {
		return
	}
	fmt.Fprintf(errOut, "warning: %s\n", clisurface.NoWorkspaceWarning(rw.Root()))
}

// warnsOutsideAWorkspace reports whether the command at path writes the warning
// that the folder it runs in is no workspace (noWorkspaceNotice).
//
// It does where the operation the command carries is declared to depend on the
// workspace root (clisurface.WorkspaceScoped), and nowhere else — the one
// declaration the assistant server reads for the same operation. A command whose
// result does not depend on it has nothing to tell its reader about workspaces,
// even when reading its configuration resolves one — loading a plugin does, to
// size the compiled-module cache. A command that carries no operation is a
// command a configured plugin contributes, or one the command framework adds
// itself, and it writes no warning either: the warning is said where the
// declaration says it is owed, never by default.
func warnsOutsideAWorkspace(path string) bool {
	scope, declared := clisurface.CommandWorkspaceScope(path)
	return declared && scope == clisurface.WorkspaceScoped
}
