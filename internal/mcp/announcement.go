// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"fmt"
	"sort"
	"strings"
)

// announcedToolNames reads the tools this server was told to announce, and is
// the whole of the mapping between what a configuration file writes and what
// every listing here is narrowed by.
//
// A nil result announces everything, and that is the posture rather than a
// fallback: the protocol puts the decision about how much of a tool list to put
// in front of a model with the client, and a server that hands a client fewer
// definitions than it has has taken that decision away from it. A non-nil result
// announces exactly the names it holds. An empty non-nil result announces none —
// so a configuration that wrote an empty list and one that wrote no list at all
// are answered differently, which is the difference the two spellings mean.
//
// Every name is held to what this process can serve, and one outside it is
// refused rather than dropped. Dropping is the direction that fails quietly in
// the wrong way: whoever misspelled a tool asked for a particular surface and
// would be served a different one, with nothing about the running server saying
// so.
//
// commandTools widens the vocabulary a name is held to and never the
// announcement itself. A session reached over HTTP serves none of those tools,
// while one configuration file serves both transports — so refusing a generated
// tool's name on the transport that does not carry it would make a workspace's
// own statement of what it announces depend on how a client happened to reach
// the server, which is the coupling the setting exists to remove rather than to
// add.
func announcedToolNames(declared []string, commandTools []toolSpec) (map[string]bool, error) {
	if declared == nil {
		return nil, nil
	}
	announceable := announceableToolNames(commandTools)
	out := make(map[string]bool, len(declared))
	for _, name := range declared {
		if !announceable[name] {
			return nil, fmt.Errorf("mcp.announce_tools names %q, which is no tool this server has; "+
				"the tools it has are %s", name, strings.Join(sortedToolNames(announceable), ", "))
		}
		out[name] = true
	}
	return out, nil
}

// announceableToolNames is every name this process could announce on some
// transport: the tools this package declares statically, and the tools derived
// from the command surface it was handed.
//
// Both halves are read from the derivations that produce the tools themselves,
// so a tool added to a registry or a command added to the tree reaches this
// vocabulary with no edit here — and a name this refuses is a name no listing
// could have carried.
func announceableToolNames(commandTools []toolSpec) map[string]bool {
	out := make(map[string]bool, len(toolRegistry)+len(deferredToolRegistry)+len(commandTools))
	for _, name := range StaticToolNames() {
		out[name] = true
	}
	for i := range commandTools {
		out[commandTools[i].name] = true
	}
	return out
}

// sortedToolNames renders a name set for a refusal to enumerate, in a fixed
// order so one configuration earns the same message across runs.
func sortedToolNames(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// toolNotAnnounced is what a caller is told when it names a tool this server has
// and does not announce.
//
// It names the setting, because the person who runs this server is the one who
// can change it and the setting is the only thing anyone reading the refusal can
// act on. It carries no challenge and reports no scope: the bound is on what this
// server offers rather than on how far the caller may reach, so no credential
// changes it and naming one would be an instruction that cannot be followed.
func toolNotAnnounced(tool string) *rpcError {
	return &rpcError{
		Code: codeInvalidParams,
		Message: tool + " is a tool this server has and does not announce: its mcp.announce_tools setting " +
			"names the tools it offers, and this is not one of them. No credential changes that, because the " +
			"bound is on what this server offers rather than on how far you may reach. Whoever runs this " +
			"server can change that setting; until they do, the tool list this server answers you with " +
			"leaves this tool out.",
	}
}
