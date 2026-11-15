// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// OperationAvailability returns what the tool protocol does with every
// operation: the tool names it is reachable under, or the reason no tool
// carries it.
//
// Every operation a command carries is *derived* rather than restated: the tool
// name is computed from the command path the command line already declares, and
// an operation whose command is exempt from the generated surface carries that
// exemption's own reason. Restating either would put a second copy of the
// command surface here, and the copy is what drifts when a command is renamed.
// The command line takes its own paths from its own table, so a generated tool's
// name is bound to the operation table one step removed.
//
// What is authored here is the residue the derivation cannot reach: the curated
// tools that answer an operation under a goal-shaped name of their own, and the
// operations no command backs at all.
//
// It returns a fresh map, so a caller enumerating the availability cannot alter
// what the completeness checks read.
func OperationAvailability() map[string]opsurface.OperationAvailability {
	out := make(map[string]opsurface.OperationAvailability, len(curatedToolNames)+len(unbackedAvailability))

	for id, cli := range clisurface.OperationAvailability() {
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
		names = append(names, curatedToolNames[id]...)
		if len(names) > 0 {
			out[id] = opsurface.Exposed(names...)
			continue
		}
		if declined != "" {
			out[id] = opsurface.Unavailable(declined)
		}
	}

	for id, curated := range curatedToolNames {
		if _, ok := out[id]; ok {
			continue
		}
		out[id] = opsurface.Exposed(curated...)
	}

	for id, a := range unbackedAvailability {
		out[id] = a
	}

	return out
}

// curatedToolNames names the curated and long-tail tools that answer an
// operation under a name of their own — the goal-shaped names the authoring loop
// lives in, which exist alongside any generated command tool rather than
// instead of it.
//
// An entry here is the one thing the derivation above cannot compute: a curated
// tool's name is chosen for the goal it serves, so nothing about a command path
// yields it. An operation no command backs appears here and nowhere else, which
// is how it reaches an availability record at all.
//
// It is also where a curated tool's name is written, once. The registries take
// each tool's name from here rather than from the tool's own definition, so a
// tool cannot be advertised under a name this table does not give, and a tool
// for an operation this table says nothing about cannot be registered at all.
// Several names for one operation are admissible and one is used: an operation
// backed by a command is reachable both as the generated command tool and, where
// a curated tool answers it too, under that tool's goal-shaped name.
//
// An entry lists the tool's name first. A name after it is an earlier spelling
// still served, as the same tool under a description that opens by naming the
// replacement and the release that removes it (deprecatedSpelling).
var curatedToolNames = map[string][]string{
	"validate": {"validate"},
	"build":    {"build"},
	"pull":     {"pull"},

	// The entity author's own pin step, run inside this server. It is also
	// reachable, for a caller at this machine's keyboard, as the tool generated
	// from its command; this one answers with what the step did to the lock rather
	// than with the text the command printed, and it is the one a caller reaching
	// a listener has, since that session derives no generated tools. The name is
	// the operation's own, which is the name the command line and the session
	// protocol both use for it.
	"pin":                    {"pin"},
	"inspect":                {"describe"},
	"get-schema":             {"get-schema"},
	"plugin-gen-catalog":     {"gen-catalog"},
	"lint":                   {"lint"},
	"explain-error":          {"explain-error"},
	"scaffold-entity":        {"draft", "scaffold"},
	"describe-capabilities":  {describeCapabilitiesName},
	"workspace-entity-index": {"list-entities"},
	"release-candidates":     {"list-releases"},

	// Adopting what already exists. It is also reachable, for a caller at this
	// machine's keyboard, as the tool generated from its command, which re-runs
	// that command in a separate process, writes into whatever directory that
	// process is sitting in, and answers with the text it printed; this runs the
	// import inside this server, writes into the workspace the call resolved, and
	// answers with the drafted paths, the drafted content under a dry run, and
	// what the importer could not translate — so a caller adopts a foreign
	// description and reads the outcome without parsing a transcript back into
	// the facts it was rendered from. The name is the operation's own, which is
	// the name the command line and the session protocol both use for it.
	"import": {"import"},

	"feature-attach-candidates": {"attach-candidates"},
	"connection-candidates":     {"connection-candidates"},
	"document-insertion-point":  {"insertion-point"},
	"attachment-container":      {"attachment-container"},

	// The step before the candidacy and container tools for a connection: a
	// caller holding a place in a document learns which connection is written
	// there. The name is the operation's own, which is the name the session
	// protocol's method is spelled from, because a caller reading both surfaces
	// should meet one concept under one name.
	"connection-at-position": {"connection-at-position"},

	// The applying half of structured authoring, whose reading half the names
	// above are. It is also reachable, for a caller at this machine's
	// keyboard, as the tool generated from its command, which re-runs that
	// command in a separate process and answers with the text it printed; this
	// runs the edit inside this server and answers with where the entry landed,
	// which container levels it had to create, and what reached disk — so a
	// caller applies an edit and reads the outcome without parsing a transcript
	// back into the facts it was rendered from. The name is the one the session
	// protocol gives the same capability, because a caller reading both surfaces
	// should meet one concept under one name.
	"document-edit": {"edit-document"},

	// The two inventories. Each is also reachable, for a caller at this machine's
	// keyboard, as the tool generated from its command, which returns that
	// command's rendered output; these carry the
	// values, so a caller totals sizes or picks a version without parsing a table
	// back into the numbers it was rendered from. The name is the operation's own,
	// which is the name the command line and the daemon use for the same concept.
	"cache-status":  {"cache-status"},
	"registry-list": {"registry-list"},
	"search":        {"search"},

	// The configuration reading, which joins those inventories on the same terms
	// and for the same reason: it is also reachable, for a caller at this
	// machine's keyboard, as the tool generated from its command, which returns
	// that command's rendered table; this carries the settings themselves, so a
	// caller reads whether a capability is attached without parsing a column
	// layout back into the values it was rendered from. What it adds to the
	// family is the reading that says whether a capability exists at all — a
	// provider, an importer or a governance check that is absent and one that is
	// merely not configured here look alike until this is read.
	"config-show": {"config-show"},

	// The plugin reading, which is the other half of that answer. The
	// configuration says which plugins are attached; this says whether an
	// attachment reaches a guest that resolves and describes, and what that
	// guest claims to serve — so a caller told a capability is configured can
	// tell an unusable attachment from a working one. It is also reachable, for
	// a caller at this machine's keyboard, as the tool generated from its
	// command, which returns that command's rendered table; this carries the
	// per-plugin values, so a caller picks a version or checks a served
	// capability without parsing a column layout back into them.
	"plugin-list": {"plugin-list"},

	// The four publication verbs. Each is also reachable, for a caller at this
	// machine's keyboard, as the tool generated from its command, which re-runs
	// that command in a separate process and
	// answers with the text it printed; these run the operation inside this server
	// and answer with the facts — the reference, the tag, the published digest,
	// what was signed, and how far the call reached. The names are the ones the
	// session protocol gives the same four capabilities, because a caller reading
	// both surfaces should meet one concept under one name, and neither surface's
	// caller ever typed a command.
	"push":          {"publish-entity"},
	"push-artifact": {"publish-artifact"},
	"sign":          {"sign-artifacts"},
	"deprecate":     {"deprecate-artifact"},
}

// curated names a tool spec after the operation it answers, and is the one way a
// curated or deferred tool reaches a registry.
//
// It sets the name from the table rather than reading one off the spec, so the
// name has a single home: a spec that carried its own name would be a second
// place to spell it, and the two would drift the moment one was renamed. It
// refuses an operation the table says nothing about. The name it sets is the
// first the table gives; a later name is an earlier spelling, which only
// deprecatedSpelling serves.
//
// It refuses a spec that declares no one-line summary, which is the field the
// tool index is projected from. The refusal sits here because this is the one
// door every statically declared tool enters a registry through, so a tool
// without a purpose written for it cannot be registered rather than being
// surfaced as an index entry with nothing beside its name.
//
// It records the operation alongside the name, from the same lookup, so a tool
// carries what it answers as well as what it is called. That is what lets a
// curated tool and the tool generated from the command carrying the same
// operation recognise one another: their names are chosen independently — one
// for the goal it serves, one for the command path it runs — and exist precisely
// so they can differ, so a name is the one thing that cannot pair them.
//
// It records the reach class from that same lookup, so the class a request is
// measured against is read once, where the tool enters the registry, rather than
// looked up again on every listing. A class is a property of the operation and
// the operation set is where it is declared, so reading it here is reading it
// from its one home; what would be a second home is a class authored beside a
// tool, which is why no definition below carries one.
//
// It carries the handler the spec already holds and constructs nothing the
// handler needs. A registry that assembled a handler's inputs would have to
// describe every tool's inputs in one shape, and the only shape that fits every
// tool is one that requires nothing — at which point each tool's own required
// inputs stop being required.
//
// It panics because the registries are package-level literals: a refusal here is
// a programming error a reader fixes by editing the table or the registry, and a
// server that started anyway would advertise a tool nobody declared.
//
// projection-omits internal/opsurface.Operation.Summary: the lookup here is an
// existence test. What this reads is whether the identifier names an operation at
// all, which is the yes-or-no the refusal above turns on, and the summary is a
// line written for a reader deciding whether a surface's availability of a
// capability is right — a question answered where the availability record is
// composed, not where a tool is registered. The class the operation declares is a
// different matter and this protocol does carry it: it is read where a request's
// credential is measured against what a call reaches.
func curated(id string, spec toolSpec) toolSpec {
	op, known := opsurface.Lookup(id)
	if !known {
		panic(fmt.Sprintf("mcp: %q is not an operation, so no tool can be registered for it: either the "+
			"operation was renamed or removed, or the identifier is misspelled", id))
	}
	names, declared := curatedToolNames[id]
	if !declared {
		panic(fmt.Sprintf("mcp: the tool protocol declares no curated name for the %q operation, so a tool "+
			"for it has nothing to be advertised as: add the name to the curated table, or reach the "+
			"operation through the tool generated from its command", id))
	}
	if len(names) == 0 {
		panic(fmt.Sprintf("mcp: the tool protocol gives the %q operation an empty list of curated names, so "+
			"a tool for it has nothing to be advertised as: write the tool's name in the curated table", id))
	}
	if strings.TrimSpace(spec.summary) == "" {
		panic(fmt.Sprintf("mcp: the tool for the %q operation declares no summary, so the tool index would "+
			"carry it as a blank line: declare a one-line purpose beside its description. The index is "+
			"what a client reads to decide which definition to load, and a tool with nothing written "+
			"against it is one a caller has no reason to ask about", id))
	}
	spec.name = names[0]
	spec.operation = id
	spec.reach = op.Reach
	return spec
}

// deprecatedSpelling returns the tool current is also served as under name, an
// earlier spelling of its name: the same arguments, the same handler and the
// same answers, under a description and summary that open by naming current's
// name and the release removedIn that removes this one.
//
// It derives the spelling from the registered spec rather than declaring a
// second one, so the two names cannot come to take different arguments or
// answer differently. The name must be one the curated table lists after
// current's own for the operation current answers; any other is a programming
// error, and it panics for the reason curated does.
func deprecatedSpelling(current toolSpec, name, removedIn string) toolSpec {
	names := curatedToolNames[current.operation]
	if len(names) < 2 || names[0] != current.name || !slices.Contains(names[1:], name) {
		panic(fmt.Sprintf("mcp: %q is not an earlier spelling the curated table lists for the %q operation "+
			"after %q (%v): list it there after the tool's name, or register it as a tool of its own",
			name, current.operation, current.name, names))
	}
	notice := "Deprecated: use `" + current.name + "`; this name is removed in " + removedIn + ". "
	spelling := current
	spelling.name = name
	spelling.summary = notice + current.summary
	spelling.description = notice + current.description
	return spelling
}

// The reasons on which no tool carries an operation.
//
// Each is a property of this protocol a reader can check against the package,
// not a judgement about the capability. A reason here decides behaviour rather
// than annotating it: an operation carrying one appears in no registry, so the
// reason is what keeps the tool off the surface.
const (
	// unavailabilityReasonNoOpenBuffer covers an operation whose request
	// addresses a cursor position inside a document the caller holds open. A tool
	// call carries one self-contained arguments object, and this protocol's
	// method set has no document-synchronisation surface at all, so there is no
	// open document for a position to be inside of.
	unavailabilityReasonNoOpenBuffer = "the request addresses a cursor position inside a document the caller " +
		"holds open, and this protocol has no document-synchronisation surface: a tool call carries one " +
		"self-contained arguments object"

	// unavailabilityReasonNoStateBetweenCalls covers session lifecycle. This
	// protocol runs its own handshake, and the tool surface holds nothing
	// per-document that a session would carry or that invalidation would discard.
	unavailabilityReasonNoStateBetweenCalls = "it establishes or discards per-document state that outlives one " +
		"call; this protocol runs its own handshake and the tool surface holds no such state"

	// unavailabilityReasonNothingRecorded covers returning what an earlier
	// request recorded. Nothing is recorded between calls here, so the only
	// honest answer to such a call is to run the check, which the validate tool
	// already is.
	unavailabilityReasonNothingRecorded = "it returns what an earlier request already recorded, and nothing is " +
		"recorded between calls here; running the check is the validate tool"

	// unavailabilityReasonNoReleaseSelection covers an operation that reports
	// which Release a workspace-wide selection settled on. This protocol makes no
	// such selection: a call names the Release it means — the build tool requires
	// one — and each call resolves its own workspace from its own arguments, so
	// there is no selection outcome for a caller to ask about.
	unavailabilityReasonNoReleaseSelection = "it reports which Release a workspace-wide selection settled on, " +
		"and this protocol makes no such selection: every call names the Release it means and resolves its " +
		"workspace from its own arguments"

	// unavailabilityReasonNoReleaseSelectionToRecord covers the writing pair of the
	// operation above: recording the Release a workspace-wide selection starts
	// from. This protocol makes no such selection, so a tool recording one would
	// write a default no call here reads.
	unavailabilityReasonNoReleaseSelectionToRecord = "it records the Release a workspace-wide selection starts " +
		"from, and this protocol makes no such selection: every call names the Release it means and resolves " +
		"its workspace from its own arguments"
)

// unbackedAvailability is what the tool protocol does with the operations no
// command backs — the ones the derivation above cannot reach and no curated
// tool answers.
//
// The operation set is deliberately not restated here: an operation absent from
// this map, from the curated table above, and from the command surface is
// reported by the completeness check rather than defaulting to anything.
var unbackedAvailability = map[string]opsurface.OperationAvailability{
	"session-initialize":          opsurface.Unavailable(unavailabilityReasonNoStateBetweenCalls),
	"session-invalidate":          opsurface.Unavailable(unavailabilityReasonNoStateBetweenCalls),
	"cached-diagnostics":          opsurface.Unavailable(unavailabilityReasonNothingRecorded),
	"definition-at-position":      opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"references-at-position":      opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"implementations-at-position": opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"hover-at-position":           opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"completion-at-position":      opsurface.Unavailable(unavailabilityReasonNoOpenBuffer),
	"active-release":              opsurface.Unavailable(unavailabilityReasonNoReleaseSelection),
	"active-release-set":          opsurface.Unavailable(unavailabilityReasonNoReleaseSelectionToRecord),
}

// StaticToolNames returns the tool names this package declares statically: the
// curated set, followed by the tail declared behind it. Both are advertised in
// every listing.
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
