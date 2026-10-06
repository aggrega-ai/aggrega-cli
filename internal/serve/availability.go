// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// methodBinding binds one wire method to the handler that answers it, and
// records whether that method's subject is a workspace.
type methodBinding struct {
	method  string
	handler methodHandler

	// workspaceFree marks a method whose subject is not a workspace, so no
	// `workspace` parameter is required of it and none is resolved for it. It is
	// routing information rather than a handler input — the routing path has to
	// know it before any params shape is decoded — which is why it sits on the
	// binding beside the handler rather than being reached for by the handler.
	//
	// The default is false, and that default is the safe direction: a method
	// added without a thought about its subject requires a workspace and refuses
	// a request that names none, which is loud. The opposite default would let
	// such a method answer from an empty workspace and report an empty result,
	// which reads exactly like a workspace with nothing in it.
	workspaceFree bool
}

// at binds a wire method to its handler, for one row of the table below. The
// method's subject is a workspace, so a request must name one.
func at(method string, handler methodHandler) methodBinding {
	return methodBinding{method: method, handler: handler}
}

// atWorkspaceFree binds a wire method whose subject is NOT a workspace: it
// requires no `workspace` parameter and none is resolved for it.
//
// Three methods qualify and each for its own reason. The schema report and the
// document skeleton are derived from the schema set compiled into the binary, so
// a workspace contributes nothing to producing either — which is what lets both
// answer in a directory nobody has prepared. The handshake DECLARES a workspace
// rather than acting on one, so requiring the parameter it exists to receive
// would be circular, and a client that declares none must still complete a
// handshake.
func atWorkspaceFree(method string, handler methodHandler) methodBinding {
	return methodBinding{method: method, handler: handler, workspaceFree: true}
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
	// modules it can load or is configured with. Every request here is about one
	// workspace — that workspace's documents and the caches that serve them — and
	// carries nothing about the binary running it, so such a subject is no nearer
	// here than to any other caller of it.
	//
	// It is deliberately about what a REQUEST is about rather than about where a
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
		"the guest modules it can load — and every request here is about a workspace, its documents " +
		"and the caches that serve them"

	// unavailabilityReasonAddressedByAHostPath covers an operation whose subject is
	// a file the request names by a filesystem path of its own, for the daemon to
	// locate and read. A path a request must locate is resolved inside the
	// workspace that request named, and every other form is refused — an absolute
	// value, or one resolving outside that root — so an operation constituted by a
	// path chosen outside that root asks for the addressing this surface declines.
	//
	// The workspace parameter is not an opening for it. That parameter names the
	// tree a request is about, and every path the request then carries is resolved
	// inside that tree, so naming a workspace widens which tree may be read and
	// never widens what may be read outside one.
	//
	// It rests on the addressing rather than on what the named file turns out to
	// be. This daemon already loads and runs guest modules — every build it
	// performs does, drawing on the shared compiled-module store this process fills
	// — so a reason resting on execution would be false of the surface it
	// describes. What it does not do is locate a file no workspace holds.
	//
	// Confining such an operation to the workspace would not narrow it but replace
	// it: a guest resolved through the workspace is what a build already runs, and
	// the capability here is the one addressed outside that resolution.
	unavailabilityReasonAddressedByAHostPath = "its subject is a file the request names by a filesystem path of " +
		"its own, and what a request reads is located inside the workspace it names"

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
	"session-initialize": servedAt(atWorkspaceFree("aggrega/initialize", (*Server).dispatchInitialize)),
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
	//
	// The writing method additionally takes a request field that resolves the
	// Release's dependencies first, and with it set the build reaches a registry
	// and writes the lockfile — on the same rule the resolution row below runs
	// under, which is about when an operation runs rather than which surface runs
	// it: a caller that asked for the fill asked for it by name. Unasked, the
	// build reaches no registry and writes only artifacts. The reporting method
	// refuses that field, because a request that writes nothing cannot fill a
	// lockfile and remain what it says it is.
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

	// The session resolves a Release's dependencies, which is also where the
	// external inputs its render consumes are pinned: one request, one acquisition
	// moment. It reaches registries and it writes — the lockfile, the entity and
	// source-artifact cache, and the input store — and it is not a question about
	// the workspace: a caller sending it has asked for the work, exactly as a
	// caller sending a build has asked for artifacts.
	//
	// That is the whole of what admits it here. The rule it runs under is a
	// property of when an operation runs rather than of which surface runs it — a
	// registry-reaching or state-writing operation runs on explicit invocation and
	// never while something is being validated or inspected — and it is stated over
	// the shared resolution layer's own invocation path rather than restated on
	// each row that happens to reach it.
	"pull": servedAt(at("aggrega/resolveDependencies", (*Server).dispatchResolveDependencies)),

	// The session also runs the entity author's own pin step, the one a
	// dependency resolution runs when asked to pin: it records, in the lock beside
	// the entity a request names, the pins for the inputs that entity reads. It
	// writes inside the workspace and reaches the hosts the entity's sources name,
	// which is the resolution's own reach, and it is admitted on the same rule: a
	// caller that sent it has asked for the pin.
	"pin": servedAt(at("aggrega/pin", (*Server).dispatchPin)),

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
	// file they wrote and whether it is the machine-wide one. A request names one
	// workspace, so an answer that said nothing would be read as being about that
	// workspace, and for the default target that reading is wrong.
	//
	// A refresh is the other way round: it reaches out to a registry, and what it
	// writes is the index under this workspace's own configured cache directory.
	// Nothing is prompted for — a
	// registry needing a credential no ambient source carries is refused with the
	// credential named, which is what makes it safe on a stream with nobody
	// watching.
	"registry-add":    servedAt(at("aggrega/registryAdd", (*Server).dispatchRegistryAdd)),
	"registry-remove": servedAt(at("aggrega/registryRemove", (*Server).dispatchRegistryRemove)),
	"registry-update": servedAt(at("aggrega/registryUpdate", (*Server).dispatchRegistryUpdate)),

	// The session also removes a stored registry credential. It changes state, and
	// it is admitted on the rule every changing method here runs under: a caller
	// that sent it has asked for the removal, exactly as a caller sending a build
	// has asked for artifacts. What stays out of bounds is a change performed while
	// validating or inspecting, where nobody asked, and this is not that.
	//
	// 🔴 The ground is that somebody asked, and it is worth separating from a
	// neighbouring ground it is easy to mistake it for. The rows below decline
	// acquiring a credential and acquiring an identity token because those need a
	// secret typed at a terminal, and this surface has none — a decision the surface
	// would have to obtain. Removal obtains nothing: the request carries the one
	// value it takes, and the client puts the choice in front of whoever is driving
	// it. So the two rows part on whether the operation needs INPUT from a person,
	// not on whether a person is involved — a person is involved in both, and in
	// this one their involvement is the whole reason it is carried.
	//
	// Its reach is not the argument either, and that is worth writing down because
	// the reach is the widest of any row here that stops at this machine. The store
	// is shared by every project on the machine and by every other tool addressing
	// the same registries. But scope has never been the line: the registration rows
	// above write the configuration file every project on the machine resolves
	// through, on this same rule. So the reach is REPORTED — every response carries
	// it, since a session-scoped answer would otherwise be read as denying it — and
	// it is not what admits the row.
	//
	// The credential store still holds nothing on this daemon's behalf, which is the
	// property the neighbouring declines actually protect. This removes what a
	// person already put there, at their request, so carrying it makes the daemon no
	// more a holder of credentials than the listing above already makes it a reader
	// of them.
	"logout": servedAt(at("aggrega/logout", (*Server).dispatchLogout)),

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

	// The session records a Release in the workspace's active-Release marker, the
	// writing pair of the row above. It writes, and it is admitted on the rule every
	// writing method here runs under: a caller that sent it has asked for the
	// change. It is carried here rather than left to the client because the marker
	// is a path inside the workspace, and a write the product makes there is held
	// to the one judgement every such write is held to; a client writing the file
	// itself would decide for itself where a link beneath the workspace leads.
	"active-release-set": servedAt(at("aggrega/setActiveRelease", (*Server).dispatchSetActiveRelease)),

	// The session applies one bounded structured edit to a document the
	// workspace carries, and it writes. It is admitted on the rule every
	// writing method here runs under: a caller that sent it has asked for the
	// edit, exactly as a caller sending a build has asked for artifacts.
	//
	// It is the counterpart to the two locating rows above, and the division
	// between them is deliberate rather than an accident of what shipped when.
	// Those answer where an entry belongs and leave composing and applying it to
	// the caller that owns the buffer; this composes and applies one, for a
	// caller that would rather the document be edited than be told where to edit
	// it. Both remain reachable, because a client holding an unsaved buffer and a
	// client editing what is on disk want different halves.
	//
	// What may be applied is fixed by the shared layer's own enumeration of the
	// edits, and it is not restated here: one call adds one entry under one
	// container or sets one scalar, and a shape absent from that set has no path
	// through it. The bound on the RESULT is the same one a hand-typed document
	// meets — the composed document is validated against its own kind's schema
	// and reaches disk only if that passes — so this cannot write what the
	// authoring schema refuses, and the refusal comes from where that rule
	// already lives rather than from a copy of it here.
	//
	// A refused edit leaves the file byte-for-byte as it was, so a caller can
	// retry a corrected request against the document it started with. After a
	// write the session's view of that document is dropped, so the next
	// validation, hover or graph request is answered from the bytes the edit
	// produced rather than from the version it replaced. It reaches no registry
	// and no network under either delivery.
	"document-edit": servedAt(at("aggrega/editDocument", (*Server).dispatchDocumentEdit)),

	// The session reports the configuration it resolved for the workspace a request
	// each setting came from. It reports no other part of this capability: the
	// command line's single-layer verbatim dumps preserve a file's own comments
	// and formatting, which is a reading of the bytes on disk rather than of the
	// resolution, and a caller wanting those has the file.
	"config-show": servedAt(at("aggrega/effectiveConfiguration", (*Server).dispatchEffectiveConfiguration)),

	// The session also seeds a configuration and freezes one. Both write, and they
	// are admitted on the rule every writing method here runs under: a caller that
	// sent one has asked for the change, exactly as a caller sending a build has
	// asked for artifacts. What stays out of bounds is a write performed while
	// validating or inspecting, where nobody asked, and neither of these is that.
	//
	// Both produce a configuration document, and each writes it rather than handing
	// the content back for the caller to place: the surface that produces a document
	// owes the document, so the answer names the file it wrote and opening it is the
	// client's follow-up. The seeding carries the one exception, as a request field
	// rather than as a default — a client that keeps its own document model can ask
	// for the content instead, because which of the two it wants is a property of
	// the caller rather than of this surface.
	//
	// The two differ in reach and each answer says which. Seeding writes the GLOBAL
	// configuration file by default, since a registry and the identities trusted on
	// it are machine-wide tooling, so its response carries the file it wrote and
	// whether that file is the shared one. Freezing has no such default and no
	// override: its whole purpose is to make one workspace self-contained, so it
	// always writes that workspace's own file and reaches nothing outside it.
	//
	// The seeding reaches a registry to read the trust policy it advertises; the
	// freeze reaches none. Nothing is prompted for on either: a registry that
	// advertises nothing is reported as advertising nothing and the seeding proceeds
	// on the built-in baseline.
	"config-init":    servedAt(at("aggrega/configInit", (*Server).dispatchConfigInit)),
	"config-capture": servedAt(at("aggrega/configCapture", (*Server).dispatchConfigCapture)),

	// The session converts a foreign source description into draft authoring
	// files. It writes, and it is admitted on the rule every writing method here
	// runs under: a caller that sent it has asked for the drafts, exactly as a
	// caller sending a build has asked for artifacts.
	//
	// It is the one writing method whose destination the CALLER names, and that is
	// the property worth stating rather than leaving to be inferred. Every other
	// write here lands somewhere the operation resolves — a configuration file, a
	// lockfile, the output root a build derives from the session — while an import
	// has no such place to derive: which directory a set of drafts belongs in is a
	// decision only whoever asked for them can make, so the request names it and
	// an absent value writes where the same import run in this workspace on the
	// command line would.
	//
	// Naming a destination is not a widening of what the daemon may reach.
	// The directory resolves inside the workspace the session opened, like every
	// other path a request names here; and the paths the IMPORTER returns are
	// separately required to stay under that directory, because those come from a
	// plugin rather than from the caller and a plugin's relative path can climb.
	// The refusal is the same one the rendered-artifact write is judged by, so the
	// two writes cannot come to disagree about what escaping means.
	//
	// A source it could not fully convert is an answer rather than a failure: the
	// drafts it did produce are written and the diagnostics say what it could not
	// convert, because those diagnostics are the operation's most useful output
	// and a response reducing them to a verdict would discard what the caller
	// needs. It reaches no registry: the importer and the parser it draws on come
	// from the workspace's own catalog through the shared plugin cascade, which
	// fetches nothing.
	"import": servedAt(at("aggrega/import", (*Server).dispatchImport)),

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
	//
	// It names no workspace, which follows from the subject: the skeleton is
	// derived from the compiled-in schema set, so there is nothing for a
	// workspace to contribute and nothing a request could point it at.
	"scaffold-entity": servedAt(atWorkspaceFree("aggrega/scaffoldEntity", (*Server).dispatchScaffoldEntity)),

	// The session checks catalog publishing conventions: one entity, every entity
	// a Release's closure reaches, or every entity under the configured catalog
	// directories. The same rules also run as a diagnostic layer on every
	// validation, behind a configuration dial that defaults off — but that layer
	// is not what this row records. A row records that the capability is
	// reachable, and it is reachable at this method whatever a workspace has
	// configured: a caller sending this request has asked for the check by name.
	//
	// It needs a method of its own, and the table is what forces that: two rows
	// cannot claim one method, and the validation method already answers the
	// correctness capability. Folding the conventions findings onto it as a third
	// diagnostic layer — which is exactly what the dial does — therefore leaves
	// this capability with no row it could legally occupy, and a capability with
	// no row is one this table says nothing about.
	//
	// The mode that compares an entity against its previously published version
	// reaches a registry and is carried here on the rule the writing methods above
	// run under: what admits such an operation is that a caller invoked it. It is
	// unreachable from the per-validation layer by construction, because a check
	// running on every edit is precisely where a registry must not be reached.
	// Nothing is written to disk under any mode.
	"lint": servedAt(at("aggrega/lint", (*Server).dispatchLint)),

	// The session configures the plugins the workspace attaches, aligns a plugin
	// entity's machine-owned records with its module, and generates Plugin catalog
	// entities from guest metadata plus an authored overlay.
	//
	// All four write, and they are admitted on the rule every writing method here
	// runs under: a caller that sent one has asked for the change, exactly as a
	// caller sending a build has asked for artifacts. What stays out of bounds is a
	// write, or a reach for a registry, performed while validating or inspecting,
	// where nobody asked, and none of these is that.
	//
	// Two of them write files an author owns and goes on editing, which is a
	// narrower thing than it sounds and is stated on each method: the record
	// alignment rewrites two machine-owned blocks and carries every other byte
	// through verbatim, and the generation writes documents that are
	// byte-identical when regenerated over unchanged inputs, or hands the content
	// back untouched when the request asks it to report instead.
	//
	// The addition reaches a registry to resolve the query to one plugin and to
	// pin a version; the other three reach none. Nothing is prompted for: the one
	// decision the command line obtains at a terminal — accepting a
	// command-extension plugin's declared capabilities — arrives as a request
	// field, so the client asks whoever is driving it and this surface never blocks
	// on a prompt nobody can answer.
	//
	// Because the command-extension type's default target is the global
	// configuration file every workspace on the machine resolves through, each of
	// these responses carries the file it wrote and whether that file is the shared
	// one. A request names one workspace, so an answer that said nothing would be
	// read as being about that workspace, and for that default the reading is
	// wrong.
	"plugin-add":         servedAt(at("aggrega/pluginAdd", (*Server).dispatchPluginAdd)),
	"plugin-remove":      servedAt(at("aggrega/pluginRemove", (*Server).dispatchPluginRemove)),
	"plugin-sync":        servedAt(at("aggrega/pluginSync", (*Server).dispatchPluginSync)),
	"plugin-gen-catalog": servedAt(at("aggrega/pluginGenCatalog", (*Server).dispatchPluginGenCatalog)),

	// The session expands a code a diagnostic carried into what the compiler
	// declares about that condition and the published entries that teach the
	// authoring fix.
	//
	// It is the one method here whose subject is a code this surface itself
	// emitted, and that is why it belongs here rather than being left to whichever
	// surface a reader happens to reach for. A diagnostic published from this
	// session carries the condition's quotable code and the protocol's own pointer
	// to where conditions are explained; a client that can follow that pointer
	// needs nothing more, and one that cannot — anything without a browser beside
	// it — asks here for the text. A surface that emits an identifier and cannot
	// expand it leaves its own reader with a code and nowhere to take it.
	//
	// The expansion is a request rather than something riding on each diagnostic,
	// and the diagnostics are why: one is emitted on every edit, so prose carried
	// on each would republish the same paragraphs on every keystroke to a reader
	// who has usually already read them. Asking once is the shape that matches how
	// often the question is actually asked.
	//
	// It reaches the documentation bundle and acquires it when the workspace has no
	// copy, and it is admitted on the rule every reaching method here runs under: a
	// caller that sent it has asked for the expansion. It is unreachable from a
	// validation by construction — no diagnostic path expands anything — so the
	// posture that keeps a registry out of an edit is untouched. Nothing is written
	// to the workspace, and a bundle that cannot be acquired still answers what the
	// binary declares and says why the prose is missing.
	//
	// Nothing about the answer is decided here. The composition is the shared one
	// every surface answering this question composes through, so a code expanded
	// here and the same code expanded at the command line are one answer in two
	// renderings rather than two answers that agree today.
	"explain-error": servedAt(at("aggrega/explainError", (*Server).dispatchExplainError)),

	// The session reports a kind's schema, and — when the request names none —
	// which kinds can be named: every token this binary answers to, and the
	// narrower set an author may write a document of. Its subject is the same one
	// the drafting method above answers from — a skeleton is derived from these
	// very bytes — so a surface carrying the derived document while declining its
	// source would be describing one subject two ways.
	//
	// The narrow set rides on this answer rather than on a method of its own for
	// the same reason. A client learns what it may name here, and which of those
	// names an author writes documents of is the same question at a second width;
	// two methods answering it would be two answers to keep in agreement, which is
	// the arrangement a single-sourced vocabulary exists to avoid.
	//
	// It is carried on the same ground, and that ground is about who is on the
	// other end. A client drawing an editor's schema association, or holding a
	// document to the contract the running binary carries, is asking the one
	// process that answers for THIS binary rather than for whichever copy is
	// installed elsewhere. Nothing is written, no registry is reached, and the
	// workspace is not consulted, so it answers where a session has nothing
	// prepared yet — which is also why it names no workspace: a request cannot
	// point at something the answer does not read.
	"get-schema": servedAt(atWorkspaceFree("aggrega/getSchema", (*Server).dispatchGetSchema)),

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

// methodIndex resolves a method name to its route in one lookup, and it is the
// whole of the daemon's routing. It is built from the availability table and
// from nothing else, so a method exists exactly because a row names it.
//
// The route carries the handler AND whether the method's subject is a workspace,
// because the routing path needs both: it resolves the named workspace before
// the handler runs, and only the row knows whether there is one to resolve.
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
//
// projection-omits Operation: the lookup is an existence test. What this reads is
// whether the row's key names an operation at all, which is the yes-or-no the
// first refusal turns on; the operation itself is not an answer being reported to
// anyone, and there is no reader here for one to be owed to.
func buildMethodIndex(table map[string]serveAvailability) map[string]methodBinding {
	idx := map[string]methodBinding{}
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
			idx[b.method] = b
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
