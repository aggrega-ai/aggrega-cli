// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package clisurface

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

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
	"skills-install":     opsurface.Exposed("aggrega skills install"),
	"skills-list":        opsurface.Exposed("aggrega skills list"),
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

	// The scaffold command only prints a starter document, which is the drafting
	// operation's answer; with --example it writes one of the documentation's
	// runnable examples into the new directory the caller names, as the stated
	// effect of that flag.
	"scaffold-entity": opsurface.ReachWorkspace,
}

// WorkspaceScope says whether what an operation does depends on which folder is
// the workspace root.
type WorkspaceScope string

const (
	// WorkspaceScoped is an operation whose result changes with the workspace
	// root: it reads the documents beneath it, writes beneath it, reads or fills
	// the artifact cache kept there, or serves it. Run in a folder that is no
	// workspace, it uses that folder as the root, so every surface carrying it says
	// so.
	WorkspaceScoped WorkspaceScope = "workspace"

	// MachineScoped is an operation whose result does not depend on which folder
	// is the root: it answers from the binary, from the machine's own
	// configuration and caches, or from a file or reference the call names. It may
	// still read the configuration a workspace layers on top, and no surface says
	// anything about workspaces when it runs outside one.
	MachineScoped WorkspaceScope = "machine"
)

// OperationWorkspaceScope returns the workspace scope declared for an operation,
// and false where the identifier names no declared operation.
//
// It is the one answer to whether an operation's result depends on the workspace
// root, read by every surface that says so: the command line for the command that
// carries the operation (CommandWorkspaceScope), and the assistant server for the
// tool that answers it. Every operation declares one, the operations no command
// carries included, so a surface carrying an operation the command line declines
// still reads its answer here rather than keeping one of its own.
func OperationWorkspaceScope(id string) (WorkspaceScope, bool) {
	scope, declared := operationWorkspaceScope[id]
	return scope, declared
}

// CommandWorkspaceScope returns the workspace scope of the operation the command
// at path carries (CommandOperation), and false where the command carries none — a
// command a configured plugin contributes, or one the command framework creates
// for itself. The caller decides what an undeclared command means.
func CommandWorkspaceScope(path string) (WorkspaceScope, bool) {
	id := CommandOperation(path)
	if id == "" {
		return "", false
	}
	return OperationWorkspaceScope(id)
}

// CommandOperation returns the operation the command at path CARRIES, empty when
// the availability table records none for it.
//
// A path can be claimed by more than one operation, and the distinction the
// answer turns on is between the capability a command IS and a capability it
// merely OFFERS. A command is the sole way to reach the first, so the operation
// naming that path and no other is the one it carries; a capability reachable at
// several commands — a document any of them can hand back under a flag — is
// offered by each and is none of their identities. So a record naming exactly
// this path wins over one naming it among others.
//
// That rule is read from the availability records rather than from any ordering,
// which is what keeps the answer independent of the order the operation set is
// written in. A tie broken by position would answer correctly only until the set
// was reordered, and a capability declared before the command it is offered by
// would then take the command's identity.
//
// The walk still follows the operation set's order, so among records that are
// equally specific the answer never depends on map iteration.
func CommandOperation(path string) string {
	fallback := ""
	for _, op := range opsurface.Operations() {
		a, ok := operationAvailability[op.ID]
		if !ok || !a.IsExposed() {
			continue
		}
		for _, p := range a.Names {
			if p != path {
				continue
			}
			if len(a.Names) == 1 {
				return op.ID
			}
			if fallback == "" {
				fallback = op.ID
			}
		}
	}
	return fallback
}

// NoWorkspaceWarning is what every surface says, in the same words, where a
// workspace-scoped operation ran in a folder no workspace was found from and
// nothing named one: that folder, and the command that makes it a workspace.
//
// The command is spelled from the path this table gives the seeding operation,
// so the sentence names the invocation the command line actually carries.
func NoWorkspaceWarning(folder string) string {
	return fmt.Sprintf("%s is not inside an Aggrega workspace. To make it one, run: %s --workspace",
		folder, operationAvailability["config-init"].Names[0])
}

// operationWorkspaceScope is the workspace scope of every operation, keyed by its
// identifier.
//
// It is keyed by operation rather than by command so the command line and every
// other surface read one declaration: a command finds its row through the
// operation it carries, and a tool through the operation it answers. Like the
// availability table above it covers the operations no command carries, because
// another surface may carry them. An operation added to the set and given no row
// here fails the completeness check over this table, so whether a new
// operation's result depends on the workspace is a question its author answers
// rather than a default.
var operationWorkspaceScope = map[string]WorkspaceScope{
	// Operations that read or write the workspace's documents.
	"build":         WorkspaceScoped,
	"document-edit": WorkspaceScoped,
	"import":        WorkspaceScoped,
	"inspect":       WorkspaceScoped,
	"ir-graph":      WorkspaceScoped,
	"lint":          WorkspaceScoped,
	"pin":           WorkspaceScoped,
	"pull":          WorkspaceScoped,
	"push":          WorkspaceScoped,
	"validate":      WorkspaceScoped,

	// Operations that read or fill the artifact cache and the registry index kept
	// beneath the workspace root.
	"cache-clear":     WorkspaceScoped,
	"cache-evict":     WorkspaceScoped,
	"cache-prune":     WorkspaceScoped,
	"cache-status":    WorkspaceScoped,
	"registry-update": WorkspaceScoped,
	"search":          WorkspaceScoped,

	// A capture writes into the workspace's own configuration file, and a skills
	// install writes beneath the workspace root unless told to install for the
	// whole machine.
	"config-capture": WorkspaceScoped,
	"skills-install": WorkspaceScoped,

	// The two servers answer, by default, in the workspace of the folder they are
	// started in, and a session they hold is a session over that workspace.
	"mcp":                WorkspaceScoped,
	"serve":              WorkspaceScoped,
	"session-initialize": WorkspaceScoped,
	"session-invalidate": WorkspaceScoped,
	"cached-diagnostics": WorkspaceScoped,

	// Operations that read the workspace's documents for an editor or an
	// assistant: what it holds, which Releases it carries, and what a position or
	// an attachment in one of its documents means.
	"workspace-entity-index":      WorkspaceScoped,
	"release-candidates":          WorkspaceScoped,
	"active-release":              WorkspaceScoped,
	"active-release-set":          WorkspaceScoped,
	"feature-attach-candidates":   WorkspaceScoped,
	"connection-candidates":       WorkspaceScoped,
	"document-insertion-point":    WorkspaceScoped,
	"attachment-container":        WorkspaceScoped,
	"connection-at-position":      WorkspaceScoped,
	"definition-at-position":      WorkspaceScoped,
	"references-at-position":      WorkspaceScoped,
	"implementations-at-position": WorkspaceScoped,
	"hover-at-position":           WorkspaceScoped,
	"completion-at-position":      WorkspaceScoped,

	// Operations that answer from the binary itself.
	"describe-capabilities": MachineScoped,
	"explain-error":         MachineScoped,
	"get-schema":            MachineScoped,
	"scaffold-entity":       MachineScoped,
	"skills-list":           MachineScoped,
	"version":               MachineScoped,

	// Operations that read or write the machine's own configuration and
	// credentials. Each writes the global configuration file by default; a
	// --workspace flag, where one is offered, is the reader choosing otherwise.
	"auth-token":      MachineScoped,
	"config-init":     MachineScoped,
	"config-show":     MachineScoped,
	"login":           MachineScoped,
	"logout":          MachineScoped,
	"registry-add":    MachineScoped,
	"registry-list":   MachineScoped,
	"registry-remove": MachineScoped,

	// Plugin configuration. A command plugin is added to, and removed from, the
	// global configuration by default, and the listing reads every layer; a socket
	// attachment goes to the workspace's configuration file by default, which
	// follows from the type the reader names rather than from the operation.
	"plugin-add":    MachineScoped,
	"plugin-list":   MachineScoped,
	"plugin-remove": MachineScoped,

	// Operations that act on a file or a published reference the call names.
	"deprecate":          MachineScoped,
	"plugin-gen-catalog": MachineScoped,
	"plugin-run-local":   MachineScoped,
	"plugin-sync":        MachineScoped,
	"push-artifact":      MachineScoped,
	"sign":               MachineScoped,
}
