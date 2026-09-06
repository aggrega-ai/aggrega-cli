// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package opsurface holds the operation set the compiler exposes, and the
// vocabulary each caller-facing surface uses to declare what it does with every
// one of them.
//
// An operation is a capability, not a command name. A capability reachable as a
// command line and a capability reachable only as a protocol method are the same
// kind of entry here, so the set is deduplicated by what the capability is
// rather than by how any one surface spells it.
//
// Each surface — the command line, the JSON-RPC daemon, the tool protocol —
// declares, per operation, either the names it exposes that operation under or
// the reason it does not expose it at all. That declaration is the surface's
// registration: a surface registers nothing it has not first named an operation
// for, and the name it registers under is the name the table gives. An entry
// recording that a surface does not carry an operation therefore registers
// nothing, which is what makes an unavailability reason decide behaviour rather
// than decorate a check.
//
// Both halves are still checked against this set by CheckAvailability and
// CheckMembers, which is what makes an omission declared rather than accidental:
// a capability a surface simply forgot has no entry, and the check names it.
//
// The package deliberately holds no dependency on any surface, and none on the
// operations layer that implements the capabilities. A surface reads the set;
// the set reads nothing. That is what lets a projection package with a handful
// of dependencies declare its availability without importing a compiler, and it
// is why the handler a surface binds to an operation stays in the surface's own
// table rather than here: this package carries the vocabulary, never a callable.
package opsurface

import (
	"fmt"
	"sort"
	"strings"
)

// Operation is one capability the compiler exposes to a caller.
//
// ID is a stable identifier in the descriptive register — lowercase words
// joined by dashes — chosen for what the capability does rather than for any
// surface's spelling of it, so a renamed command or a renamed method does not
// rename the operation. Summary states in one line what the capability does,
// for a reader deciding whether a surface's availability of it is right.
type Operation struct {
	ID      string
	Summary string
}

// Operations returns the operation set, in a fixed order.
//
// The order is the declaration order below, which groups the set by the surface
// that originated each capability. Nothing derives behaviour from the order; it
// is fixed so a check's findings are reported in the same sequence across runs.
//
// It returns a copy, so a caller walking the set cannot alter what the checks
// read.
func Operations() []Operation {
	return append([]Operation(nil), operations...)
}

// IDs returns every operation's ID, in Operations order.
func IDs() []string {
	out := make([]string, 0, len(operations))
	for _, op := range operations {
		out = append(out, op.ID)
	}
	return out
}

// Lookup returns the operation with the given ID.
func Lookup(id string) (Operation, bool) {
	for _, op := range operations {
		if op.ID == id {
			return op, true
		}
	}
	return Operation{}, false
}

// operations is the operation set.
//
// Three groups, in order: the capabilities a command line originated, the
// capabilities the JSON-RPC daemon originated, and the capabilities the tool
// protocol originated. The grouping is a reading aid — every entry is one kind
// of thing, and which surface first carried a capability constrains nothing
// about which surfaces may expose it.
var operations = []Operation{
	// Capabilities the command line originated.
	{"acquire", "Pin the external inputs a Release's render consumes."},
	{"auth-token", "Acquire an identity token interactively and print it."},
	{"build", "Compile an Aggregate and Release into provider-native artifacts."},
	{"cache-clear", "Wipe every on-disk cache, compiled modules included."},
	{"cache-evict", "Trim the compiled-module cache to a size cap, oldest first."},
	{"cache-prune", "Remove artifact-cache entries no lockfile references."},
	{"cache-status", "Report the on-disk caches' contents and total size."},
	{"config-capture", "Freeze the effective build-critical configuration into a workspace."},
	{"config-init", "Seed a working configuration with a registry and its trust policy."},
	{"config-show", "Report the effective configuration and where each setting came from."},
	{"deprecate", "Attach a signed deprecation marker to an already-published artifact."},
	{"get-schema", "Report the JSON Schema for an entity or contract kind."},
	{"import", "Turn a foreign source description into draft authoring files."},
	{"inspect", "Report an entity's resolved definition, or a Release's dependency closure, without rendering."},
	{"lint", "Check catalog publishing conventions across a catalog, a Release, or one entity."},
	{"login", "Authenticate to an OCI registry and store the credential."},
	{"logout", "Remove a stored registry credential."},
	{"mcp", "Serve the tool protocol over a stream for assistant integration."},
	{"plugin-add", "Search a registry and add a plugin of a named type."},
	{"plugin-gen-catalog", "Generate Plugin catalog entities from guest metadata plus an authored overlay."},
	{"plugin-list", "Report the plugins resolvable here, with their declared metadata."},
	{"plugin-remove", "Remove a configured plugin of a named type."},
	{"plugin-run-local", "Load a local guest module and call one of its exports."},
	{"plugin-sync", "Sync a plugin's machine-owned records in its entity file with its module."},
	{"pull", "Resolve catalog and source dependencies into the cache and write the per-Release lockfile."},
	{"push", "Bundle and publish a catalog entity to an OCI registry."},
	{"push-artifact", "Publish a standalone data artifact to an OCI registry."},
	{"registry-add", "Register a registry and its signature trust policy."},
	{"registry-list", "Report the configured registries and their trust posture."},
	{"registry-remove", "Remove a registry and its trust policy from a configuration file."},
	{"registry-update", "Build the local searchable index of configured registries."},
	{"search", "Search the local index of configured registries."},
	{"serve", "Serve the compiler over JSON-RPC on a stream."},
	{"sign", "Sign already-published artifacts and attach their signatures."},
	{"validate", "Check correctness across a catalog, a Release, or one entity."},
	{"version", "Report the binary's own version."},

	// Capabilities the JSON-RPC daemon originated.
	{"session-initialize", "Establish a session over a workspace and negotiate its position encoding."},
	{"session-invalidate", "Discard the session's cached parse and findings for a document."},
	{"cached-diagnostics", "Return the findings already recorded for a document, without re-checking it."},
	{"ir-graph", "Return the serialized IR graph a Release resolves to."},
	{"definition-at-position", "Locate the definition site the reference under a cursor names."},
	{"references-at-position", "Locate every reference to the entity name under a cursor."},
	{"implementations-at-position", "Locate every implementation of the entity under a cursor."},
	{"hover-at-position", "Render a readable summary of the entity under a cursor."},
	{"completion-at-position", "Propose the values admissible at a cursor."},
	{"workspace-entity-index", "Report every workspace-local entity's kind, name, description and source file."},
	{"feature-attach-candidates", "Report the Features whose declared applicability an entity's labels satisfy."},
	{"connection-candidates", "Report an entity's dependency sockets and the components whose declared labels satisfy each one."},
	{"document-insertion-point", "Report where in an authored document a new entry under a named container belongs."},
	{"release-candidates", "Report every Release-kind document under the workspace root, by content."},
	{"active-release", "Report the Release the workspace's disambiguation cascade selected."},

	// Capabilities the tool protocol originated.
	{"explain-error", "Explain a diagnostic a build emitted, and what to change."},
	{"scaffold-entity", "Generate a minimal starter skeleton for an entity kind from its schema."},
	{"describe-capabilities", "Reveal the tool definitions the default listing withholds."},
}

// Surface names one caller-facing surface, for the findings a check reports.
type Surface string

// The caller-facing surfaces. Each names itself as a caller would, so a finding
// reads as a sentence about something the reader can go and look at.
const (
	SurfaceCLI   Surface = "the command line"
	SurfaceServe Surface = "the JSON-RPC daemon"
	SurfaceMCP   Surface = "the tool protocol"
)

// OperationAvailability is what one surface does with one operation.
//
// Exactly one half is filled. Names carries the identifiers the surface exposes
// the operation under — a command path, a wire method, a tool name — and may
// carry several, because one capability can be reachable under more than one
// name on the same surface. UnavailabilityReason states why the surface does not
// expose the operation at all.
//
// Both halves are load-bearing at run time, not only at check time. A surface
// registers an operation under the names this record gives and under no others,
// and a record carrying an unavailability reason registers nothing — so the
// reason is what stops the operation reaching the surface rather than a note
// beside the fact that it did not.
//
// There is deliberately no third state. A capability a surface has simply not
// got round to is indistinguishable, in a table, from one it declines on
// purpose — so a value meaning "not yet" would turn this table into a backlog,
// and a bound that reads as backlog gets closed by someone who does not know
// why it was a bound. An operation nobody has ruled on therefore has no entry,
// and the checks below name it.
type OperationAvailability struct {
	Names                []string
	UnavailabilityReason string
}

// Exposed reports availability under the names given. Passing no name is a
// programming error the checks report, not an exposure.
func Exposed(names ...string) OperationAvailability {
	return OperationAvailability{Names: names}
}

// Unavailable reports an operation the surface declines to expose, carrying the
// reason it declines.
func Unavailable(reason string) OperationAvailability {
	return OperationAvailability{UnavailabilityReason: reason}
}

// IsExposed reports whether the surface exposes the operation.
func (a OperationAvailability) IsExposed() bool { return len(a.Names) > 0 }

// CheckAvailability reports every way a surface's availability table disagrees
// with the operation set: an operation the table says nothing about, an entry
// naming an operation that no longer exists, and an entry that is neither an
// exposure nor a declined one.
//
// An operation-and-surface pair named on the bound is excused, and the pair is
// named in the surface's own census rather than passed over in silence. The
// bound is held from both ends: a pair on it that has since been disposed of is
// refused as a stale row, and so is a row naming something that is not an
// operation. So the bound can only ever shrink by being repaired, and it cannot
// quietly excuse a capability that was ruled on after the row was written.
//
// Each finding is a full sentence naming the operation, the surface and what to
// do about it, because the reader of a failure is deciding between exposing the
// capability and stating why not, and a finding that only reports a mismatch
// leaves them to work out which.
//
// The findings are ordered by the operation set for the undeclared and
// malformed classes and lexically for the stale classes, so a failing check
// reports the same text across runs.
func CheckAvailability(surface Surface, table map[string]OperationAvailability) []string {
	return checkAvailability(surface, table, openQuestions)
}

// checkAvailability is the whole of CheckAvailability with the bound supplied
// rather than read from the package.
//
// The split exists so each failure class the bound introduces can be driven
// against a bound built for it. The exported form above is a one-line
// delegation passing the real bound, which is what makes a test of this
// function a test of what the surfaces actually call.
func checkAvailability(surface Surface, table map[string]OperationAvailability, bound []OpenQuestion) []string {
	var findings []string

	for _, op := range operations {
		a, ok := table[op.ID]
		if isOpen(bound, surface, op.ID) {
			if ok {
				findings = append(findings, fmt.Sprintf(
					"%s declares the availability of the %q operation, and %s names it as undecided. One of the "+
						"two is wrong. If the declaration stands, delete the bound row: a capability that has "+
						"been ruled on reads, from the bound, as a question nobody need answer, and the bound "+
						"only shrinks.",
					surface, op.ID, boundLocation))
			}
			continue
		}
		if !ok {
			findings = append(findings, fmt.Sprintf(
				"%s declares nothing about the %q operation (%s). Declare its availability: "+
					"Exposed(name…) with the names it is reachable under, or Unavailable(reason) "+
					"stating why this surface does not carry it. Naming it in %s instead is not a "+
					"declaration — it records that nobody has decided, and it costs a deliberate second "+
					"edit to the bound's own ceiling.",
				surface, op.ID, op.Summary, boundLocation))
			continue
		}
		switch {
		case a.IsExposed() && a.UnavailabilityReason != "":
			findings = append(findings, fmt.Sprintf(
				"%s declares the %q operation both exposed and unavailable. "+
					"An availability record carries names or an unavailability reason, never both.",
				surface, op.ID))
		case !a.IsExposed() && a.UnavailabilityReason == "":
			findings = append(findings, fmt.Sprintf(
				"%s declares the %q operation with neither a name nor an unavailability reason. "+
					"Use Exposed(name…) or Unavailable(reason).", surface, op.ID))
		case a.IsExposed():
			for _, n := range a.Names {
				if strings.TrimSpace(n) == "" {
					findings = append(findings, fmt.Sprintf(
						"%s exposes the %q operation under an empty name.", surface, op.ID))
				}
			}
		}
	}

	stale := make([]string, 0, len(table))
	for id := range table {
		if _, ok := Lookup(id); !ok {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	for _, id := range stale {
		findings = append(findings, fmt.Sprintf(
			"%s declares the availability of %q, which is not an operation. Either the operation was "+
				"renamed or removed and this entry is stale — delete it — or the identifier is misspelled.",
			surface, id))
	}

	// The bound's other stale direction. A row naming something that is not an
	// operation excuses nothing and cannot be repaired by whoever reads the
	// row, because there is no capability to rule on — so it is refused here
	// rather than counted as a bound the check honoured.
	staleBound := make([]string, 0, len(bound))
	for _, q := range bound {
		if q.Surface != surface {
			continue
		}
		if _, ok := Lookup(q.ID); !ok {
			staleBound = append(staleBound, q.ID)
		}
	}
	sort.Strings(staleBound)
	for _, id := range staleBound {
		findings = append(findings, fmt.Sprintf(
			"%s names %q as undecided in %s, and that is not an operation. Either the operation was "+
				"renamed or removed and the row is stale — delete it — or the identifier is misspelled.",
			surface, id, boundLocation))
	}

	return findings
}

// CheckMembers reports every way a surface's exposed names disagree with what
// the surface actually carries: a member no operation claims, and a name an
// availability record claims that the surface does not carry.
//
// It is the half of the check that notices growth. CheckAvailability asks
// whether every known capability was ruled on, and structurally cannot notice a
// capability nobody added to the set — a new wire method or a new command
// reaches no operation, so no availability record is missing and nothing fails.
// This asks the question from the surface's end, where the new member is
// visible.
//
// Registering from the table closes the first half of that gap on its own: a
// surface that can only register what the table names cannot grow a member the
// table never claimed. This still runs, because a surface carries members the
// registration path does not mint — a protocol's own built-ins, and the members
// a third party contributes at run time — and those are exactly the ones no
// authoring-time reading would catch.
//
// members are the identifiers the surface carries, in whatever order it
// advertises them. Passing an empty members list reports every exposed name as
// absent, which is the honest answer for a caller that could not enumerate the
// surface: enumerate it, or do not call this.
func CheckMembers(surface Surface, table map[string]OperationAvailability, members []string) []string {
	carried := make(map[string]bool, len(members))
	for _, m := range members {
		carried[m] = true
	}

	claimed := map[string]string{}
	var findings []string

	for _, op := range operations {
		a, ok := table[op.ID]
		if !ok || !a.IsExposed() {
			continue
		}
		for _, n := range a.Names {
			claimed[n] = op.ID
			if !carried[n] {
				findings = append(findings, fmt.Sprintf(
					"%s claims to expose the %q operation as %q, which it does not carry. "+
						"Either the name changed — update the availability record — or the exposure was "+
						"removed, in which case state why with Unavailable(reason).",
					surface, op.ID, n))
			}
		}
	}

	uncovered := make([]string, 0, len(members))
	for _, m := range members {
		if _, ok := claimed[m]; !ok {
			uncovered = append(uncovered, m)
		}
	}
	sort.Strings(uncovered)
	for _, m := range uncovered {
		findings = append(findings, fmt.Sprintf(
			"%s carries %q, which no operation claims. Add the capability to the operation set "+
				"and declare its availability on every surface, or name it in the availability record of "+
				"the operation it already implements.", surface, m))
	}

	return findings
}
