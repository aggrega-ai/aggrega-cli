// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// withNoWorkspaceNotice adds, to the text of a tool's answer, the sentence saying
// which folder the answer used as its workspace root, where the workspace the
// call resolved found none and was handed none, and hands every other answer on
// as it was composed.
//
// It runs at the one point every tool's result crosses on its way to the caller,
// and reads the workspace the call itself recorded on its result
// ([toolWorkspace.answer]) — the subject's, where the shared rule found the
// workspace again from the folder of the file the call is about — so it says the
// sentence for exactly the answers whose workspace meets the predicate and
// resolves nothing a second time. Whether the predicate holds is the shared
// resolution's (workspaceconfig.ResolvedWorkspace.FoundNoWorkspace), the one the
// command line's notice asks, so the two surfaces say it for the same
// resolutions and name the same folder.
//
// It is a sentence in the answer's text, and no field of a tool's own payload
// gains it. The structured payload carries it only through the copy of the text
// written after this step ([withTextAnswer]), so a caller reading that payload
// alone still reads the sentence. The answer is not refused, because the folder is a
// legitimate place to answer from; what a caller needs is to know it was used, so
// an empty listing or an entity not found reads as a wrong folder rather than as a
// configuration problem.
//
// Over a listener it never applies, and is not asked: a listener refuses to start
// in a folder no workspace resolves from unless it is handed a root, and a root a
// launch or a call names is a named root. A result whose call resolved no
// workspace — a refusal composed before resolution, a generated command tool,
// whose own command writes this notice on its error stream and so carries it in
// its answer as the command printed it — is handed on unchanged, as is a listing
// answered from a held view, which carries no resolution to ask.
func (s *server) withNoWorkspaceNotice(res *callToolResult) *callToolResult {
	if res == nil || s.transport == transportHTTP || len(res.Content) == 0 {
		return res
	}
	tw := res.workspace
	if tw == nil || !tw.rw.FoundNoWorkspace() {
		return res
	}
	last := len(res.Content) - 1
	res.Content[last].Text += "\n\n" + noWorkspaceSentence(tw.root)
	return res
}

// noWorkspaceSentence is the sentence an answer carries where its workspace was
// found from no marker and named by nobody: the folder used, and the argument
// that names the workspace instead.
func noWorkspaceSentence(folder string) string {
	return fmt.Sprintf("No Aggrega workspace was found from %s and none was named, so this answer used that folder "+
		"as its workspace root. To work in your workspace, send %s naming its folder — the one holding %s.",
		folder, argumentWorkspaceRoot, workspaceconfig.ConfigFilename)
}
