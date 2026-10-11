// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package clisurface

import "github.com/aggrega-ai/aggrega-cli/internal/opsurface"

// OperationAvailability returns what the command line does with every
// operation: the command path it is reachable at, or the reason no command
// carries it.
//
// It is exported so the omissions are readable from outside this package. A
// declared absence is only declared while someone other than the derivation can
// enumerate it, which is the same reason the generated-tool exemptions are
// exported alongside the derivation that consumes them.
//
// It returns a fresh map, so a caller enumerating the availability cannot alter
// what the completeness checks read.
func OperationAvailability() map[string]opsurface.OperationAvailability {
	out := make(map[string]opsurface.OperationAvailability, len(operationAvailability))
	for id, a := range operationAvailability {
		out[id] = a
	}
	return out
}

// The reasons on which no command carries an operation.
//
// Each is a property of the command line a reader can check, not a judgement
// about the capability: a command-line invocation is one process per request,
// it holds no document open, and it has no editor view to keep in step with.
// They are named constants rather than repeated prose so that twenty rows
// resting on one property cannot drift into twenty descriptions of it.
//
// A reason here decides behaviour rather than annotating it: the registrar below
// refuses to attach a command for an operation whose row carries one, so the
// reason is what keeps the command off the tree.
const (
	// unavailabilityReasonNoOpenBuffer covers an operation whose request
	// addresses a cursor position inside a document the caller holds open. A
	// command line has no open document and no cursor, so there is no position
	// for an argument to name.
	unavailabilityReasonNoOpenBuffer = "the request addresses a cursor position inside a document the caller " +
		"holds open; a command-line invocation has neither an open document nor a cursor"

	// unavailabilityReasonNoSessionAcrossRequests covers session lifecycle. The
	// daemon holds parsed documents and their findings across requests; a
	// command-line invocation is one process per request, so there is no session
	// to establish or to invalidate.
	unavailabilityReasonNoSessionAcrossRequests = "it establishes or discards state that outlives one request; " +
		"a command-line invocation is one process per request and has no session to hold that state"

	// unavailabilityReasonNeedsAPriorRequest covers returning what an earlier
	// request recorded. Asking a fresh process for findings it never produced can
	// only re-run the check, which is what the validate command already is.
	unavailabilityReasonNeedsAPriorRequest = "it returns what an earlier request already recorded, and a fresh " +
		"process has recorded nothing; running the check is the validate command"

	// unavailabilityReasonEditorViewParity covers an operation that exists so a
	// caller holding its own view of the workspace can keep that view identical
	// to the compiler's, which is why it answers as a structured document rather
	// than as readable output. A command-line invocation holds no such view.
	unavailabilityReasonEditorViewParity = "it exists so a caller holding its own view of the workspace can keep " +
		"that view identical to the compiler's, which is why it answers as a structured document; " +
		"a command-line invocation holds no such view"

	// unavailabilityReasonNoCommandReadsTheMarker covers recording the Release a
	// workspace's active-Release marker names. The marker is read only where the
	// language server settles on a default Release for an editor, and no command
	// reads it, so a command writing one would record something no command
	// consults.
	unavailabilityReasonNoCommandReadsTheMarker = "it records the marker the language server reads to settle on a " +
		"default Release, and no command reads that marker"

	// unavailabilityReasonProtocolLocalMeta covers an operation that reports one
	// protocol's own capability surface. The command line withholds no command
	// from its help, so it has no withheld set to reveal.
	unavailabilityReasonProtocolLocalMeta = "it reveals definitions one protocol withholds from its default " +
		"listing; the command line withholds no command from its help, so there is nothing to reveal"
)

// operationAvailability is what the command line does with every operation.
//
// An exposed row names the command path, which is the identifier the command
// catalog reports for that command. The registrar below takes the path from
// here, so the path exists once: a command whose invocation path is not the one
// this row gives cannot be attached, and neither can a command for an operation
// this table says nothing about.
//
// The operation set is deliberately not restated here: an operation absent from
// this map is reported by the completeness check rather than defaulting to
// anything.
var operationAvailability = map[string]opsurface.OperationAvailability{
	// Capabilities a command carries.
	"auth-token":     opsurface.Exposed("aggrega auth token"),
	"build":          opsurface.Exposed("aggrega build"),
	"cache-clear":    opsurface.Exposed("aggrega cache clear"),
	"cache-evict":    opsurface.Exposed("aggrega cache evict"),
	"cache-prune":    opsurface.Exposed("aggrega cache prune"),
	"cache-status":   opsurface.Exposed("aggrega cache status"),
	"config-capture": opsurface.Exposed("aggrega config capture"),
	"config-init":    opsurface.Exposed("aggrega config init"),
	"config-show":    opsurface.Exposed("aggrega config show"),
	"deprecate":      opsurface.Exposed("aggrega deprecate"),
	"document-edit":  opsurface.Exposed("aggrega edit-document"),
	"explain-error":  opsurface.Exposed("aggrega explain"),
	"get-schema":     opsurface.Exposed("aggrega get-schema"),
	"import":         opsurface.Exposed("aggrega import"),
	"inspect":        opsurface.Exposed("aggrega inspect"),
	// Three commands resolve a Release far enough to hold the graph, and each
	// hands it back under one flag. So this row names three paths — the record
	// admits several, because one capability can be reachable under more than one
	// name on the same surface — rather than picking one of the three and leaving
	// the other two undeclared.
	//
	// The paths are the commands rather than the flag spellings, because a path
	// here is compared against the command catalog: a flag-qualified string names
	// nothing the catalog carries, so it would report as an exposure the surface
	// does not have. The consequence is that these three paths are each claimed by
	// two operations — the capability the command IS, and this one it offers — and
	// which of the two a command CARRIES is settled where that question is asked
	// rather than by the order this table or the operation set happens to be
	// written in.
	"ir-graph":           opsurface.Exposed("aggrega build", "aggrega inspect", "aggrega validate"),
	"lint":               opsurface.Exposed("aggrega lint"),
	"login":              opsurface.Exposed("aggrega login"),
	"logout":             opsurface.Exposed("aggrega logout"),
	"mcp":                opsurface.Exposed("aggrega mcp"),
	"pin":                opsurface.Exposed("aggrega pin"),
	"plugin-add":         opsurface.Exposed("aggrega plugin add"),
	"plugin-gen-catalog": opsurface.Exposed("aggrega plugin gen-catalog"),
	"plugin-list":        opsurface.Exposed("aggrega plugin list"),
	"plugin-remove":      opsurface.Exposed("aggrega plugin remove"),
	"plugin-run-local":   opsurface.Exposed("aggrega plugin run-local"),
	"plugin-sync":        opsurface.Exposed("aggrega plugin sync"),
	"pull":               opsurface.Exposed("aggrega pull"),
	"push":               opsurface.Exposed("aggrega push"),
	"push-artifact":      opsurface.Exposed("aggrega push-artifact"),
	"registry-add":       opsurface.Exposed("aggrega registry add"),
	"registry-list":      opsurface.Exposed("aggrega registry list"),
	"registry-remove":    opsurface.Exposed("aggrega registry remove"),
	"registry-update":    opsurface.Exposed("aggrega registry update"),
	"scaffold-entity":    opsurface.Exposed("aggrega scaffold"),
	"search":             opsurface.Exposed("aggrega search"),
	"serve":              opsurface.Exposed("aggrega serve"),
	"sign":               opsurface.Exposed("aggrega sign"),
	"validate":           opsurface.Exposed("aggrega validate"),
	"version":            opsurface.Exposed("aggrega version"),

	// Capabilities no command carries, each on a property of the command line.
	"session-initialize":          opsurface.Unavailable(unavailabilityReasonNoSessionAcrossRequests),
	"session-invalidate":          opsurface.Unavailable(unavailabilityReasonNoSessionAcrossRequests),
	"cached-diagnostics":          opsurface.Unavailable(unavailabilityReasonNeedsAPriorRequest),
	"definition-at-position":      opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"references-at-position":      opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"implementations-at-position": opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"hover-at-position":           opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"completion-at-position":      opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"workspace-entity-index":      opsurface.Unavailable(unavailabilityReasonEditorViewParity),
	"feature-attach-candidates":   opsurface.Unavailable(unavailabilityReasonEditorViewParity),
	"connection-candidates":       opsurface.Unavailable(unavailabilityReasonEditorViewParity),
	"document-insertion-point":    opsurface.Unavailable(unavailabilityReasonEditorViewParity),
	"attachment-container":        opsurface.Unavailable(unavailabilityReasonEditorViewParity),
	"connection-at-position":      opsurface.Unavailable(unavailabilityReasonEditorViewParity),
	"release-candidates":          opsurface.Unavailable(unavailabilityReasonEditorViewParity),
	"active-release":              opsurface.Unavailable(unavailabilityReasonEditorViewParity),
	"active-release-set":          opsurface.Unavailable(unavailabilityReasonNoCommandReadsTheMarker),
	"describe-capabilities":       opsurface.Unavailable(unavailabilityReasonProtocolLocalMeta),
}

// CommandReach returns the reach class the command carrying an operation
// declares for itself, and false where it declares none — in which case the
// command reaches exactly what its operation declares.
//
// The class is the command line's to state because the difference is a property
// of the command rather than of the capability. An operation that answers with
// content reaches nothing by delivering it, which is why the operation set
// classes a render as none; a command that saves that content to disk writes
// it, and a surface running the command — a tool that runs it in a separate
// process — reaches whatever the command does. Stating the command's class here,
// once, leaves the operation set describing the capability on every surface
// while such a surface reads what the command actually reaches, with no second
// record of it kept beside any tool.
func CommandReach(operation string) (opsurface.Reach, bool) {
	class, declared := commandReach[operation]
	return class, declared
}

// commandReach is the class each command reaches where it reaches more than the
// operation it carries, keyed by that operation.
//
// A row is written only where a command saves what its operation only answers
// with, or writes as the stated effect of a flag it declares. A command that
// writes as its operation does needs none, because the operation's own class
// already says so. A store a command fills as a side effect that no operation's
// class counts — the compiled-module cache every guest load fills, say — is not
// a reason for a row either: the class reports what a call leaves behind as its
// result, not the caches that make the next call faster.
var commandReach = map[string]opsurface.Reach{
	// The build command writes the rendered artifacts beneath the workspace root,
	// under dist/, and a traced build's files under .trace/ beside it. With
	// --auto-pull or --upgrade it also writes the Release's lockfile and plugin
	// trust record and fills the workspace's cache, which is the class a
	// dependency resolution declares.
	"build": opsurface.ReachWorkspace,
}
