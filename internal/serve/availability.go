// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// methodBinding binds one wire method to the handler that answers it.
type methodBinding struct {
	method  string
	handler methodHandler
}

// at binds a wire method to its handler, for one row of the table below.
func at(method string, handler methodHandler) methodBinding {
	return methodBinding{method: method, handler: handler}
}

// serveAvailability is what the daemon does with one operation: the request
// methods that carry it, each with the handler answering it, or the reason no
// method carries it.
//
// It carries the handler because the table registers the daemon rather than
// describing it: the routing index below is built from these rows and from
// nothing else, so a wire method exists because a row names it. What the table
// does not carry is anything a handler needs in order to run — the handler is a
// value the row holds, never a shape the row constructs. A row that assembled a
// handler's inputs would have to speak about every handler at once, which means
// an untyped bag, which means each handler's own required inputs stop being
// required.
type serveAvailability struct {
	methods              []methodBinding
	unavailabilityReason string
}

// servedAt declares an operation reachable at the given methods. Several are
// admissible: one capability can be reachable under more than one method, and
// the two forms of a build are.
func servedAt(bindings ...methodBinding) serveAvailability {
	return serveAvailability{methods: bindings}
}

// notServed declares an operation no method carries, carrying the reason.
func notServed(reason string) serveAvailability {
	return serveAvailability{unavailabilityReason: reason}
}

// OperationAvailability returns what the daemon does with every operation: the
// request method it is reachable at, or the reason no method carries it.
//
// It is the surface-neutral projection of the table below — the same rows with
// their handlers dropped — so it cannot disagree with what the daemon routes.
//
// It is exported so the omissions are readable from outside this package. A
// declared absence is only declared while someone other than the daemon can
// enumerate it.
//
// It returns a fresh map, so a caller enumerating the availability cannot alter
// what the completeness checks read.
func OperationAvailability() map[string]opsurface.OperationAvailability {
	out := make(map[string]opsurface.OperationAvailability, len(operationAvailability))
	for id, a := range operationAvailability {
		if a.unavailabilityReason != "" {
			out[id] = opsurface.Unavailable(a.unavailabilityReason)
			continue
		}
		names := make([]string, 0, len(a.methods))
		for _, b := range a.methods {
			names = append(names, b.method)
		}
		out[id] = opsurface.Exposed(names...)
	}
	return out
}

// The reasons on which no request method carries an operation.
//
// Each is a property of this daemon a reader can check against the package, not
// a judgement about the capability. They are named constants rather than
// repeated prose so that a dozen rows resting on one property cannot drift into
// a dozen descriptions of it.
//
// A reason here decides behaviour rather than annotating it: a row carrying one
// contributes no route, so the operation is unreachable because of what the
// reason says. That is why none of them may restate how the daemon happens to be
// wired — "no method carries it, therefore no method should" would justify
// itself, and the routing index would then be built from its own shape.
const (
	// unavailabilityReasonNoTerminal covers an operation that asks a person for
	// something. The daemon's only stream is its request channel, so there is
	// nothing to prompt on.
	unavailabilityReasonNoTerminal = "it asks a person for input, and the daemon's only stream is its request channel"

	// unavailabilityReasonAboutTheToolchain covers an operation whose subject is
	// the toolchain rather than the work: this binary's own identity, and the guest
	// modules it can load or is configured with. A session is opened over a
	// workspace to work on that workspace's documents and the caches that serve
	// them, and holds nothing about the binary running it — so such a subject is no
	// nearer here than to any other caller of it.
	//
	// It is deliberately about what a session HOLDS rather than about where a
	// subject sits on disk, and the two come apart in a way that decides real rows.
	// The compiled-module store sits outside every workspace and is nonetheless one
	// of the caches that serve the work — this daemon fills it, once per guest, for
	// every session on the machine — so reporting and trimming it are carried here.
	// A reason resting on the weaker "not in the workspace directory" would have
	// declined those too while the daemon was the very process populating them,
	// which is a reason justifying itself from an incidental fact rather than from
	// a decision.
	//
	// It deliberately claims nothing about where every method here is anchored.
	// Two methods are anchored to a kind named in the request rather than to the
	// workspace — the one that drafts a starter document and the one that reports a
	// kind's schema — so a reason resting on the stronger claim would be false
	// against the table below, and a reason that is false about the surface it
	// describes decides nothing, whatever it happens to decide today.
	//
	// The schema set compiled into this binary is deliberately absent from the
	// subjects named above, for the same reason. Both of those methods answer out
	// of it, so naming it here would decline a subject this surface carries.
	unavailabilityReasonAboutTheToolchain = "its subject is the toolchain rather than the work — this binary and " +
		"the guest modules it can load — and a session is opened over a workspace to work on that " +
		"workspace's documents and the caches that serve them"

	// unavailabilityReasonAddressedByAHostPath covers an operation whose subject is
	// a file the request names by a filesystem path of its own, for the daemon to
	// locate and read. A session resolves a path it must locate inside the
	// workspace it opened and refuses every other form — an absolute value, or one
	// resolving outside that root — so an operation constituted by a path chosen
	// outside that root asks for the addressing this surface declines.
	//
	// It rests on the addressing rather than on what the named file turns out to
	// be. This daemon already loads and runs guest modules — every build a session
	// performs does, drawing on the shared compiled-module store this process fills
	// — so a reason resting on execution would be false of the surface it
	// describes. What a session does not do is locate a file the workspace does not
	// hold.
	//
	// Confining such an operation to the workspace would not narrow it but replace
	// it: a guest resolved through the workspace is what a build already runs, and
	// the capability here is the one addressed outside that resolution.
	unavailabilityReasonAddressedByAHostPath = "its subject is a file the request names by a filesystem path of " +
		"its own, and a session locates what it reads inside the workspace it opened"

	// unavailabilityReasonIsAStreamServer covers an operation that is itself a
	// server owning a stream. The daemon already owns this one, and a second
	// stream server inside it would nest one protocol inside another.
	unavailabilityReasonIsAStreamServer = "it is itself a server owning a stream, and the daemon already owns this one"

	// unavailabilityReasonProtocolLocalMeta covers an operation that reports one
	// protocol's own capability surface. The daemon withholds no method, so it
	// has no withheld set to reveal.
	unavailabilityReasonProtocolLocalMeta = "it reveals definitions one protocol withholds from its default " +
		"listing; the daemon withholds no method, so there is nothing to reveal"
)

// operationAvailability is what the daemon does with every operation, and it is
// the one place the daemon's wire surface is stated: adding a method means
// adding a binding to the row of the operation it carries.
//
// An exposed row names the wire method and supplies the handler, so the method
// name exists once. A row carrying a reason supplies no handler and produces no
// route: the reason is what makes the operation unreachable.
//
// The operation set is deliberately not restated here: an operation absent from
// this map is reported by the completeness check rather than defaulting to
// anything, and an identifier here that is not an operation is refused when the
// routing index is built.
var operationAvailability = map[string]serveAvailability{
	// Capabilities a request method carries.
	"session-initialize": servedAt(at("aggrega/initialize", (*Server).dispatchInitialize)),
	"validate":           servedAt(at("aggrega/validate", (*Server).dispatchValidate)),
	"session-invalidate": servedAt(at("aggrega/invalidate", (*Server).dispatchInvalidate)),
	"cached-diagnostics": servedAt(at("aggrega/diagnostics", (*Server).dispatchDiagnostics)),
	"ir-graph":           servedAt(at("aggrega/irGraph", (*Server).dispatchIrGraph)),

	// The compile runs here in full, under the posture of a surface that emits an
	// artifact: every gate on, every divergence a halt. Two methods carry it and
	// the difference between them is what happens to the result — one writes the
	// artifacts into the workspace, the other reports what writing them would have
	// produced. Both are named, because a caller enumerating this table is asking
	// what the daemon can be asked for, and a capability reachable two ways under
	// one name would be described as reachable one way.
	"build": servedAt(
		at("aggrega/build", (*Server).dispatchBuild),
		at("aggrega/buildPreview", (*Server).dispatchBuildPreview),
	),

	// The session reports what an entity resolves to and what a Release depends
	// on, and two methods carry it because the two are asked from different
	// coordinates: one from a name, one from a Release. Both are named, because a
	// caller enumerating this table is asking what the daemon can be asked for,
	// and a capability reachable two ways under one name would be described as
	// reachable one way.
	//
	// Neither renders. The Release half runs the same inspection the graph
	// request runs and reports the closure that resolution reached rather than
	// the graph derived from it — a different document about the same Release,
	// which is why it is this operation and not the graph one.
	"inspect": servedAt(
		at("aggrega/entityDefinition", (*Server).dispatchEntityDefinition),
		at("aggrega/releaseClosure", (*Server).dispatchReleaseClosure),
	),

	// The session resolves a Release's dependencies and pins the external inputs
	// its render consumes. Both reach registries and both write — the lockfile and
	// the content-addressed cache for the first, the input store and the lockfile's
	// external-input pins for the second — and neither is a question about the
	// workspace: a caller sending either has asked for the work, exactly as a
	// caller sending a build has asked for artifacts.
	//
	// That is the whole of what admits them here. The rule they run under is a
	// property of when an operation runs rather than of which surface runs it — a
	// registry-reaching or state-writing operation runs on explicit invocation and
	// never while something is being validated or inspected — and it is stated over
	// the shared resolution layer's own invocation path rather than restated on
	// each row that happens to reach it.
	"pull":    servedAt(at("aggrega/resolveDependencies", (*Server).dispatchResolveDependencies)),
	"acquire": servedAt(at("aggrega/acquireExternalInputs", (*Server).dispatchAcquireExternalInputs)),

	// The session reports and maintains the caches that serve the work. One of
	// them is the workspace's own artifact cache; two are shared across every
	// project on the machine, and this daemon is what fills the compiled-module
	// one — once per guest, for every session on the machine. A surface that
	// populates a store is the surface that owes a way to read and reclaim it.
	//
	// Three of the four write, and they are admitted on the same rule the
	// resolution above runs under: a caller that sent one has asked for the
	// reclamation. What stays out of bounds is a write performed while validating
	// or inspecting, where nobody asked, and none of these is that.
	//
	// Because two of the classes are machine-wide, every one of these responses
	// carries a scope naming the machine-wide classes it touched. A caller that
	// opened a session over a workspace would otherwise read a reclamation as
	// being about that workspace, and for those classes that reading is wrong.
	"cache-status": servedAt(at("aggrega/cacheStatus", (*Server).dispatchCacheStatus)),
	"cache-prune":  servedAt(at("aggrega/cachePrune", (*Server).dispatchCachePrune)),
	"cache-evict":  servedAt(at("aggrega/cacheEvict", (*Server).dispatchCacheEvict)),
	"cache-clear":  servedAt(at("aggrega/cacheClear", (*Server).dispatchCacheClear)),

	// The session reports the registries the workspace reaches and what their
	// local index offers. Both are readings of what the session already holds: the
	// configured set is the merged effective configuration whose provenance this
	// surface already reports, and the index is written under the workspace's own
	// configured cache directory. Neither contacts a registry.
	//
	// The listing additionally reports whether a credential is present for each
	// host, which is a machine-wide fact and is marked as one in the response. It
	// reports presence only: never the credential, never its shape, and never
	// whether the registry would accept it, since establishing that would be the
	// network round trip a listing does not make.
	"registry-list": servedAt(at("aggrega/registryList", (*Server).dispatchRegistryList)),
	"search":        servedAt(at("aggrega/search", (*Server).dispatchRegistrySearch)),

	// The session also changes that set and refreshes what it is searched
	// against. All three write, and they are admitted on the same rule the
	// resolution and the cache maintenance above run under: a caller that sent
	// one has asked for it. What stays out of bounds is a write performed while
	// validating or inspecting, where nobody asked, and none of these is that.
	//
	// Two properties separate them from everything else here, and each response
	// states its own rather than leaving a caller to infer it.
	//
	// A registration and a removal write the GLOBAL configuration file by
	// default — a registry is machine-wide tooling, so that is the operation's
	// own default rather than something a request opted into. Every workspace on
	// the machine resolves through the entry, so those two responses name the
	// file they wrote and whether it is the machine-wide one. A session is opened
	// over a workspace, so an answer that said nothing would be read as being
	// about that workspace, and for the default target that reading is wrong.
	//
	// A refresh is the other way round: it is the only method the daemon carries
	// that goes out to a registry, and what it writes is the index under this
	// workspace's own configured cache directory. Nothing is prompted for — a
	// registry needing a credential no ambient source carries is refused with the
	// credential named, which is what makes it safe on a stream with nobody
	// watching.
	"registry-add":    servedAt(at("aggrega/registryAdd", (*Server).dispatchRegistryAdd)),
	"registry-remove": servedAt(at("aggrega/registryRemove", (*Server).dispatchRegistryRemove)),
	"registry-update": servedAt(at("aggrega/registryUpdate", (*Server).dispatchRegistryUpdate)),

	// The session publishes to a registry, signs what is already published there,
	// and withdraws it. These four write further than anything else here: what
	// they land is what every consumer of that registry resolves, and neither a
	// published version nor a signature can be recalled from anyone who already
	// fetched it.
	//
	// They are admitted on the same rule the resolution and the cache maintenance
	// above run under, and the rule is about WHEN an operation runs rather than
	// about which surface runs it: a caller that sent one of these has asked for
	// the publication, exactly as a caller sending a build has asked for
	// artifacts. What stays out of bounds is a write performed while validating or
	// inspecting, where nobody asked, and none of these is that.
	//
	// Two properties separate them from every other writing method here, and each
	// response states its own rather than leaving a caller to infer it. Their reach
	// is beyond this machine entirely — the registry methods above reach the
	// machine's own configuration, and these reach everybody resolving from that
	// registry — so every response names the host it addressed and whether the call
	// landed anything. And nothing is prompted for: identity resolves from what the
	// request supplied or what the environment already carries, and a credential
	// none of them yields is refused with what to supply named, which is what makes
	// them safe on a stream with nobody watching.
	//
	// A request names no signing key, and that is an addressing decision rather
	// than an omission: its ground is unavailabilityReasonAddressedByAHostPath,
	// and the reasoning is stated with the methods themselves.
	"push":          servedAt(at("aggrega/publishEntity", (*Server).dispatchPublishEntity)),
	"push-artifact": servedAt(at("aggrega/publishArtifact", (*Server).dispatchPublishArtifact)),
	"sign":          servedAt(at("aggrega/signArtifacts", (*Server).dispatchSignArtifacts)),
	"deprecate":     servedAt(at("aggrega/deprecateArtifact", (*Server).dispatchDeprecateArtifact)),

	"definition-at-position":      servedAt(at("aggrega/resolve", (*Server).dispatchResolve)),
	"references-at-position":      servedAt(at("aggrega/findReferences", (*Server).dispatchFindReferences)),
	"implementations-at-position": servedAt(at("aggrega/findImplementations", (*Server).dispatchFindImplementations)),
	"hover-at-position":           servedAt(at("aggrega/hover", (*Server).dispatchHover)),
	"completion-at-position":      servedAt(at("aggrega/completion", (*Server).dispatchCompletion)),
	"workspace-entity-index":      servedAt(at("aggrega/catalogIndex", (*Server).dispatchCatalogIndex)),
	"feature-attach-candidates":   servedAt(at("aggrega/attachCandidates", (*Server).dispatchAttachCandidates)),
	"connection-candidates":       servedAt(at("aggrega/connectionCandidates", (*Server).dispatchConnectionCandidates)),
	"document-insertion-point":    servedAt(at("aggrega/insertionPoint", (*Server).dispatchInsertionPoint)),
	"attachment-container":        servedAt(at("aggrega/attachmentContainer", (*Server).dispatchAttachmentContainer)),
	"release-candidates":          servedAt(at("aggrega/releaseCandidates", (*Server).dispatchReleaseCandidates)),
	"active-release":              servedAt(at("aggrega/activeRelease", (*Server).dispatchActiveRelease)),

	// The session reports the configuration it resolved at startup and the layer
	// each setting came from. It reports no other part of this capability: the
	// command line's single-layer verbatim dumps preserve a file's own comments
	// and formatting, which is a reading of the bytes on disk rather than of the
	// resolution, and a caller wanting those has the file.
	"config-show": servedAt(at("aggrega/effectiveConfiguration", (*Server).dispatchEffectiveConfiguration)),

	// The session drafts a starter document for an entity kind the request
	// names. Its subject is not the workspace: the skeleton is derived from the
	// schema set compiled into the binary, so the session contributes nothing to
	// producing it, and the rows below decline several capabilities for exactly
	// that property.
	//
	// It is carried anyway, and the reason is about who is on the other end
	// rather than about where the subject lives. This is the surface a person
	// drives, starting a new document is something a person does, and the answer
	// lands in a buffer they can read before it is anything else. Nothing is
	// written: the answer is text, and where it goes is the client's.
	"scaffold-entity": servedAt(at("aggrega/scaffoldEntity", (*Server).dispatchScaffoldEntity)),

	// The session reports a kind's schema, and the set of kinds when the request
	// names none. Its subject is the same one the drafting method above answers
	// from — a skeleton is derived from these very bytes — so a surface carrying
	// the derived document while declining its source would be describing one
	// subject two ways.
	//
	// It is carried on the same ground, and that ground is about who is on the
	// other end. A client drawing an editor's schema association, or holding a
	// document to the contract the running binary carries, is asking the one
	// process that answers for THIS binary rather than for whichever copy is
	// installed elsewhere. Nothing is written, no registry is reached, and the
	// workspace is not consulted, so it answers where a session has nothing
	// prepared yet.
	"get-schema": servedAt(at("aggrega/getSchema", (*Server).dispatchGetSchema)),

	// Capabilities no method carries, each on a property of this daemon.
	//
	// The set is smaller than the capabilities the daemon lacks a method for,
	// and deliberately so. A row here asserts that a property of the daemon
	// declines the capability; a capability nobody has ruled on is named in the
	// bound the availability check reads instead, where it reads as the question
	// it is rather than as a decision.
	"plugin-list":           notServed(unavailabilityReasonAboutTheToolchain),
	"version":               notServed(unavailabilityReasonAboutTheToolchain),
	"plugin-run-local":      notServed(unavailabilityReasonAddressedByAHostPath),
	"auth-token":            notServed(unavailabilityReasonNoTerminal),
	"login":                 notServed(unavailabilityReasonNoTerminal),
	"mcp":                   notServed(unavailabilityReasonIsAStreamServer),
	"serve":                 notServed(unavailabilityReasonIsAStreamServer),
	"describe-capabilities": notServed(unavailabilityReasonProtocolLocalMeta),
}

// methodIndex resolves a method name to its handler in one lookup, and it is the
// whole of the daemon's routing. It is built from the availability table and
// from nothing else, so a method exists exactly because a row names it.
var methodIndex = buildMethodIndex(operationAvailability)

// buildMethodIndex projects the availability table into the routing index,
// refusing at startup rather than routing a table that cannot be right.
//
// Three refusals, and each covers a way a row could otherwise reach the wire
// unexamined: a row keyed on something that is not an operation would register a
// method no capability claims; an exposed row with an empty method or a nil
// handler would register a route that cannot answer; and two rows naming one
// method would have one silently win. A row carrying an unavailability reason
// contributes nothing, which is the whole of what makes the reason operative.
//
// It panics because the table is a compile-time literal: every refusal here is a
// programming error a reader can fix by editing the row, and a daemon that
// started anyway would serve a surface nobody declared.
func buildMethodIndex(table map[string]serveAvailability) map[string]methodHandler {
	idx := map[string]methodHandler{}
	claimed := map[string]string{}

	for _, id := range sortedOperationIDs(table) {
		if _, known := opsurface.Lookup(id); !known {
			panic(fmt.Sprintf("serve: the availability table declares %q, which is not an operation: "+
				"either the operation was renamed or removed and this row is stale, or the identifier "+
				"is misspelled", id))
		}
		a := table[id]
		if a.unavailabilityReason != "" {
			continue
		}
		if len(a.methods) == 0 {
			panic(fmt.Sprintf("serve: the availability table exposes %q under no method and gives no "+
				"unavailability reason: use servedAt(at(method, handler)…) or notServed(reason)", id))
		}
		for _, b := range a.methods {
			switch {
			case b.method == "":
				panic(fmt.Sprintf("serve: the availability table exposes %q under an empty method name", id))
			case b.handler == nil:
				panic(fmt.Sprintf("serve: the availability table exposes %q at %q with no handler", id, b.method))
			}
			if other, dup := claimed[b.method]; dup {
				panic(fmt.Sprintf("serve: the availability table binds %q to both the %q and %q operations; "+
					"one wire method answers one capability", b.method, other, id))
			}
			claimed[b.method] = id
			idx[b.method] = b.handler
		}
	}
	return idx
}

// sortedOperationIDs returns the table's keys in operation-set order, with any
// key that is not an operation last and in lexical order.
//
// The order matters only so that a refusal above and the method census below
// report the same sequence across runs; nothing routes on it. An unknown key
// sorts last rather than being dropped, because dropping it is how a stale row
// would reach the refusal as an absence instead of as itself.
func sortedOperationIDs(table map[string]serveAvailability) []string {
	out := make([]string, 0, len(table))
	for _, op := range opsurface.Operations() {
		if _, ok := table[op.ID]; ok {
			out = append(out, op.ID)
		}
	}
	unknown := make([]string, 0, len(table))
	for id := range table {
		if _, ok := opsurface.Lookup(id); !ok {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	return append(out, unknown...)
}

// Methods returns every request method the daemon routes, in operation-set order
// and, within one operation, in the order its row binds them.
//
// It is exported so what the daemon carries is readable from outside this
// package, which is what lets the operation set be checked against the wire
// surface rather than against someone's recollection of it. It reads the same
// table the routing index is built from, so the census and the routing cannot
// disagree.
func Methods() []string {
	out := make([]string, 0, len(methodIndex))
	for _, id := range sortedOperationIDs(operationAvailability) {
		for _, b := range operationAvailability[id].methods {
			out = append(out, b.method)
		}
	}
	return out
}
