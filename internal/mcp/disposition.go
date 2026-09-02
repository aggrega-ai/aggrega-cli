// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// Dispositions returns what the tool protocol does with every operation: the
// tool names it is reachable under, or the reason no tool carries it.
//
// Every operation a command carries is *derived* rather than restated: the tool
// name is computed from the command path the command line already declares, and
// an operation whose command is exempt from the generated surface carries that
// exemption's own reason. Restating either would put a second copy of the
// command surface here, and the copy is what drifts when a command is renamed.
//
// What is authored here is the residue the derivation cannot reach: the curated
// tools that answer an operation under a goal-shaped name of their own, and the
// operations no command backs at all.
//
// It returns a fresh map, so a caller enumerating the dispositions cannot alter
// what the completeness checks read.
func Dispositions() map[string]opsurface.Disposition {
	out := make(map[string]opsurface.Disposition, len(operationAliases)+len(unbackedDispositions))

	for id, cli := range clisurface.Dispositions() {
		if !cli.IsExposed() {
			continue
		}
		var names []string
		var declined string
		for _, path := range cli.Names {
			if exemption, exempt := CommandExempted(path); exempt {
				declined = "no tool is generated for `" + path + "`: " + exemption.Reason
				continue
			}
			names = append(names, commandToolName(path))
		}
		names = append(names, operationAliases[id]...)
		if len(names) > 0 {
			out[id] = opsurface.Exposed(names...)
			continue
		}
		if declined != "" {
			out[id] = opsurface.NotApplicable(declined)
		}
	}

	for id, alias := range operationAliases {
		if _, ok := out[id]; ok {
			continue
		}
		out[id] = opsurface.Exposed(alias...)
	}

	for id, d := range unbackedDispositions {
		out[id] = d
	}

	return out
}

// operationAliases names the curated and long-tail tools that answer an
// operation under a name of their own — the goal-shaped names the authoring loop
// lives in, which exist alongside any generated command tool rather than
// instead of it.
//
// An entry here is the one thing the derivation above cannot compute: a curated
// tool's name is chosen for the goal it serves, so nothing about a command path
// yields it. An operation no command backs appears here and nowhere else, which
// is how it reaches a disposition at all.
var operationAliases = map[string][]string{
	"validate":              {"validate"},
	"build":                 {"build"},
	"inspect":               {"describe"},
	"get-schema":            {"get-schema"},
	"plugin-gen-catalog":    {"gen-catalog"},
	"lint":                  {"lint"},
	"explain-error":         {"explain-error"},
	"scaffold-entity":       {"scaffold"},
	"describe-capabilities": {describeCapabilitiesName},
}

// The grounds on which no tool carries an operation.
//
// Each is a property of this protocol a reader can check against the package,
// not a judgement about the capability.
const (
	// groundNoOpenBuffer covers an operation whose request addresses a cursor
	// position inside a document the caller holds open. A tool call carries one
	// self-contained arguments object, and this protocol's method set has no
	// document-synchronisation surface at all, so there is no open document for
	// a position to be inside of.
	groundNoOpenBuffer = "the request addresses a cursor position inside a document the caller holds open, " +
		"and this protocol has no document-synchronisation surface: a tool call carries one " +
		"self-contained arguments object"

	// groundNoStateBetweenCalls covers session lifecycle. This protocol runs
	// its own handshake, and the tool surface holds nothing per-document that a
	// session would carry or that invalidation would discard.
	groundNoStateBetweenCalls = "it establishes or discards per-document state that outlives one call; this " +
		"protocol runs its own handshake and the tool surface holds no such state"

	// groundNothingRecorded covers returning what an earlier request recorded.
	// Nothing is recorded between calls here, so the only honest answer to such
	// a call is to run the check, which the validate tool already is.
	groundNothingRecorded = "it returns what an earlier request already recorded, and nothing is recorded " +
		"between calls here; running the check is the validate tool"

	// groundNoReleaseSelection covers an operation that reports which Release a
	// workspace-wide selection settled on. This protocol makes no such
	// selection: a call names the Release it means — the build tool requires
	// one — and each call resolves its own workspace from its own arguments, so
	// there is no selection outcome for a caller to ask about.
	groundNoReleaseSelection = "it reports which Release a workspace-wide selection settled on, and this " +
		"protocol makes no such selection: every call names the Release it means and resolves its " +
		"workspace from its own arguments"
)

// unbackedDispositions is what the tool protocol does with the operations no
// command backs — the ones the derivation above cannot reach and no curated
// tool answers.
//
// The operation set is deliberately not restated here: an operation absent from
// this map, from the alias map above, and from the command surface is reported
// by the completeness check rather than defaulting to anything.
var unbackedDispositions = map[string]opsurface.Disposition{
	"session-initialize":          opsurface.NotApplicable(groundNoStateBetweenCalls),
	"session-invalidate":          opsurface.NotApplicable(groundNoStateBetweenCalls),
	"cached-diagnostics":          opsurface.NotApplicable(groundNothingRecorded),
	"definition-at-position":      opsurface.NotApplicable(groundNoOpenBuffer),
	"references-at-position":      opsurface.NotApplicable(groundNoOpenBuffer),
	"implementations-at-position": opsurface.NotApplicable(groundNoOpenBuffer),
	"hover-at-position":           opsurface.NotApplicable(groundNoOpenBuffer),
	"completion-at-position":      opsurface.NotApplicable(groundNoOpenBuffer),
	"active-release":              opsurface.NotApplicable(groundNoReleaseSelection),
}

// StaticToolNames returns the tool names this package declares statically: the
// curated set advertised in every listing, followed by the long tail surfaced on
// demand.
//
// It excludes the generated command tools, which exist only for a session that
// was handed a command surface. A caller checking the whole tool surface builds
// the generated names from the command tree it holds and appends them.
func StaticToolNames() []string {
	out := make([]string, 0, len(toolRegistry)+len(deferredToolRegistry))
	for i := range toolRegistry {
		out = append(out, toolRegistry[i].name)
	}
	for i := range deferredToolRegistry {
		out = append(out, deferredToolRegistry[i].name)
	}
	return out
}

// GeneratedToolNames returns the names of the tools derived from the given
// command surface, in catalog order.
//
// It runs the same derivation a session runs, so the names it reports are the
// names a client would be offered — a command the derivation skips is absent
// here exactly as it is absent there.
func GeneratedToolNames(surface []CommandSurface) []string {
	specs := deriveCommandTools(surface)
	out := make([]string, 0, len(specs))
	for i := range specs {
		out = append(out, specs[i].name)
	}
	return out
}
