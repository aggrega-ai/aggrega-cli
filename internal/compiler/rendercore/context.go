// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// IREdgeInfo is the per-edge view passed to a BoundaryAssembler. Decoupled
// from types.IREdge so the package's BoundaryAssembler input does not depend
// on pkg/types directly.
type IREdgeInfo struct {
	Source string
	Target string
}

// NodeRenderContext provides identity metadata for manifest generation.
type NodeRenderContext struct {
	NodeID       string
	Annotations  map[string]string // merged release annotations
	BoundaryPath string
	SyncWave     int // deployment wave from the containing boundary's syncWave (signed integer; default 0)
	// Attachments is the node's post-cascade attachment set, keyed by the
	// instance name the author gave each one, exactly as the IR graph carries
	// it. It is the render surface's one source for an attachment's declared
	// facts: the per-node projection handed to a provider guest carries these
	// entries verbatim, including the derived sensitivity marking, so a guest
	// reads them without a host round-trip and the host derives nothing a
	// second time. Empty for a node with no attachments.
	Attachments types.OrderedMap[types.IRFeatureEntry]
	// DeclaredSensitivity carries the sensitivity declarations this node's render
	// joins against to name a rendered key. It is read only by the node
	// projection's own construction, which turns it into the per-node marking a
	// provider guest receives; nothing else consults it, and nothing derived from
	// it reaches an emitted byte. The zero value declares nothing and therefore
	// marks nothing, which is the safe direction here — see DeclaredSensitivity.
	DeclaredSensitivity DeclaredSensitivity
	// SensitiveValues reports that the values this node's render carries were
	// produced by a template — a values mapping — whose references reach a value an
	// author declared sensitive, read statically from the template over the
	// declarations and never from a value. No key of such a template's output can be
	// traced back to the datum it came from, so the fact covers the whole map: the
	// node projection carries it to the guest wherever it carries the values, and a
	// guest reads it to keep every one of those values out of a refusal or a warning.
	//
	// It is a statement about the template that produced the values, which is what
	// sets it apart from DeclaredSensitivity: that holds declarations alone, joined
	// to a key only where the join is exact, while this is derived from the
	// template's references and claims nothing about any one key. Its zero value
	// claims nothing either, and it never reaches an emitted byte — only the text a
	// guest composes when something fails.
	SensitiveValues bool
	// SensitiveWorkload reports the same fact about the template that produced the
	// workload this node's render synthesizes from, the same reading the host's own
	// checks of that workload are worded by. It is set on the context a workload
	// synthesis is handed and carried on the workload projection, which a guest
	// rebuilds it from.
	SensitiveWorkload bool
	// Source is what engines actually consume (via loader.Load or
	// equivalent): the resolved filesystem path for remote sources
	// (extracted directory under the source-artifact cache slot),
	// the verbatim path for local sources (Tier-1 filesystem paths,
	// Tier-2 workspace-relative paths), or nil for sourceless
	// Recipes. The orchestrator populates this before dispatch;
	// engines that receive a non-string value here treat it as an
	// orchestrator bug rather than authoring drift.
	Source any
	// OriginalSource carries the verbatim Recipe spec.source as
	// authored, regardless of whether Source was resolved through
	// the cache. Engines reference OriginalSource for human-readable
	// diagnostics ("chart load failed for oci://...") so error
	// messages match the operator's source code rather than the
	// opaque cache path. Engines NEVER call loader.Load on
	// OriginalSource — Source is the load-bearing path; the helper
	// sourceForDiagnostic centralises the diagnostic formatting.
	OriginalSource any
	// SourceIgnore is what the render reads a local source directory through
	// before any of it reaches a provider: the ignore rules the lock beside the
	// entity records, and the entity's directory they are spelled against. The
	// listing a provider is handed and every file it asks to have packed or read
	// are held to it, so a render reads exactly the files a publication of the
	// entity carries. Its zero value applies no rule. Host-only: it is not part of
	// any wire projection sent to a guest.
	SourceIgnore  SourceIgnore
	RecipeName    string // recipe metadata.name; empty when no Recipe selected for this node
	RecipeVersion string // recipe metadata.version; pairs with RecipeName to form the aggrega.ai/recipe-uri label
	// ProviderName is the bare provider engine kind rendering this node
	// (recipe.spec.capability with any version pin stripped). When set it is the
	// value of the aggrega.ai/provider label.
	ProviderName string
	// ProviderVersion is the resolved provider-rendering plugin version behind
	// ProviderName, or empty when no provider-rendering plugin version resolves
	// behind the node's provider binding. When set it is the value of the
	// aggrega.ai/provider-version label; empty omits that label.
	ProviderVersion string
	// LabelComponentID, when non-empty, overrides NodeID as the value of the
	// app.kubernetes.io/name and aggrega.ai/component-id labels — what a resource
	// this node emits BELONGS to, which is what provenance records and what a
	// selector queries. It is set for every resource an FR attachment emits,
	// whatever input kind delivers them, so an attachment's resources are
	// distinguishable from its host component's by label alone. Empty is the
	// default for a regular component node, and means "derive it from NodeID" —
	// which ENCODES rather than copies, because a node one aggregate composes into
	// another carries a qualified identity whose join no name may hold. Read
	// through [ProvenanceComponentID], never as a field: reading the field is how a
	// caller gets the unencoded form into a name.
	//
	// It is deliberately NOT the value a target derives its own names from; that is
	// DerivedNameBasis below, and the two answer different questions.
	LabelComponentID string
	// DerivedNameBasis, when non-empty, overrides NodeID as the identity a target
	// derives its OWN names from — the Helm release name a chart's templates read
	// as .Release.Name and that target names a component's synthesized workload
	// after, and the prefix on the files a source render emits. Empty means "use
	// NodeID directly". Read through [TargetNameBasis].
	//
	// The split from LabelComponentID above is the point rather than a compromise,
	// and the asymmetry runs both ways. A chart or fragment an FR attachment's
	// author supplied renders against the HOST component's name, because the
	// author's own templates name resources after it and changing it would rename
	// resources already deployed for a reason that has nothing to do with the
	// chart — so a per-component attachment leaves this empty and the host's NodeID
	// stands. A workload or artifact set built for an FR attachment rather than
	// rendered from its author's source has no author templates, so nothing renders
	// against its name and its resources are named after the identity instead,
	// which is what keeps an FR's synthesized workload from colliding with its
	// host's own.
	//
	// A boundary-scope attachment sets it, to the same composed identity it sets
	// LabelComponentID to. It has to: that node's NodeID is a dispatch marker
	// opening with a character the release-name alphabet refuses, so falling back
	// to NodeID there would produce a name no shortening repairs and halt the
	// build.
	DerivedNameBasis string
	// OutputLabels are the recipe-author-owned labels stamped onto every object
	// this node's rendered output produces: the pairs written literally on the
	// recipe (recipe.spec.outputLabels) together with the ones it promotes from
	// this node's component labels by key (recipe.spec.outputComponentLabels),
	// already resolved to their values. They ride the same rail as the fixed
	// provenance labels: merged into the set [BuildAggregaProvenanceLabels]
	// returns. Load-time validation guarantees the keys are disjoint from the
	// compiler-owned provenance keyspace (no aggrega.ai/ or app.kubernetes.io/
	// key), so the merge never collides.
	//
	// The map belongs to this node alone. Two nodes bound to one recipe hold
	// separate maps, because a promoted value is this node's own component
	// identity and must not reach a neighbour's output.
	OutputLabels map[string]string
	Provenance   ProvenanceContext
	// YAMLIndent is the indentation width for artifacts rendered from this
	// node (clamped to [2,9], default 2). Forwarded from RendererInput.
	YAMLIndent int
	// WarningSink, when non-nil, receives the non-fatal advisories a provider
	// engine raises while rendering this node — the warn- and info-severity
	// diagnostics the guest returns in its render output, plus any guest-emitted
	// warnings. The orchestrator wires it to the build result's warnings stream;
	// engines nil-check it before calling. Warnings never affect emitted bytes,
	// so byte-identity of rendered files is independent of whether a sink is
	// attached. Host-only: it is not part of any wire projection sent to a guest.
	//
	// It carries the whole diagnostic rather than a rendered line, which is what
	// lets an advisory reach a consumer that cannot read a terminal: a caller with
	// only text has to parse the node identity back out of it, and a caller
	// needing a position has nothing to parse it out of at all. Every display
	// surface renders an entry through [RenderDiagnosticText], so the text a
	// reader sees is composed once rather than at each sink.
	//
	// What separates it from UnsupportedFieldSink below is now the CLASS an entry
	// belongs to and not its shape: this is the stream a display prints
	// unconditionally, and that one is the stream a display may suppress by its
	// class code.
	WarningSink func(renderctx.Diagnostic)
	// UnsupportedFieldSink, when non-nil, receives the unsupported-field advisories a
	// provider engine raises while rendering this node — the structured
	// warn-severity diagnostics reporting an authored field the target could
	// not honor. It is routed separately from WarningSink so a display surface can
	// suppress the unsupported-field class alone by its stable code while other render
	// warnings still surface. Like WarningSink it never affects emitted
	// bytes and is host-only: not part of any wire projection sent to a guest.
	UnsupportedFieldSink func(renderctx.Diagnostic)
	// DerivedKeySink, when non-nil, receives the names a provider engine's guest
	// declares it derived while rendering this node, keyed by the guest's own key
	// space. A provider engine forwards whatever the guest declared, verbatim, and
	// the host reads a space name and a key only as strings to compare for
	// equality: it interprets nothing that arrives here and derives nothing from
	// it. The orchestrator wires it to the per-node accumulator whose contents are
	// checked for duplicates once every contributor to an assembled output has
	// rendered. A node may render across more than one crossing — a source render,
	// a workload synthesis, an artifacts placement — so the sink is called once per
	// crossing and the accumulator merges them.
	//
	// This sink carries a GUEST's declarations and only those. It is not the only
	// way a name reaches that accumulator: a name the compiler derives itself —
	// LabelComponentID above, composed from an identity the author wrote — is
	// declared into the same accumulator at the site that derives it, with no sink
	// involved, because a compiler derivation has no engine boundary to cross. So
	// the accumulator's contents are wider than this sink's traffic, while this
	// sink's own contract stays exactly what it says.
	//
	// Like WarningSink it never affects emitted bytes: a duplicate halts the build
	// and no declaration changes what is written, so rendered output is
	// byte-identical whether or not a sink is attached. Host-only: not part of any
	// wire projection sent to a guest.
	DerivedKeySink func(types.OrderedMap[[]string])
	// RenderFactSink, when non-nil, receives the facts a provider engine's guest
	// reports about what it emitted while rendering this node, in the order the
	// guest listed them. Each fact is a name and a value, and both are the guest's
	// own vocabulary: a provider engine forwards the report verbatim, the host reads
	// no name and parses no value, and nothing a provider's format means is learned
	// here. A node may render across more than one crossing, so the sink is called
	// once per crossing that reports anything; a crossing whose guest reports
	// nothing costs it no call.
	//
	// The orchestrator wires it to the node's own record of what its render
	// reported. A Feature attachment's render carries a copy of this context and
	// rewires the sink to a record of its own, because what an attachment emitted
	// is not what its host rendered.
	//
	// Like DerivedKeySink it never affects emitted bytes: the files written are the
	// files the guest returned, whether or not facts accompany them. Host-only: not
	// part of any wire projection sent to a guest.
	RenderFactSink func([]RenderFact)
	// Omits are the omit entries this node's source render hands its provider: the
	// entries of the rendering document's omits list whose when kept them, in the
	// order that document declares them, each resolved to plain strings. Empty for a
	// document declaring none, and then the render's input carries none either.
	//
	// They reach the source render and no other crossing: a workload synthesis and an
	// artifacts placement read nothing here, so what those emit is never filtered.
	// A Feature attachment's render carries a copy of this context and replaces the
	// entries with its own FeatureRecipe's, because entries an author wrote for the
	// host's source name nothing the attachment renders.
	Omits []ResolvedOmit
}

// ResolvedOmit is one active omit entry as a source render carries it: the entry the
// provider is handed, and what the host needs to report on the count the provider
// returns for it.
//
// The provider matches by Entry's fields and is told, through the entry's mark, whether
// Withheld names any of them; Position describes the entry to whoever reads a report
// about it and never reaches a guest.
type ResolvedOmit struct {
	// Entry is the entry's kind, name and namespace as its templates resolved them. A
	// namespace that resolved to the empty string is absent, which places it exactly
	// where an entry naming none is placed.
	//
	// Its Sensitive is never set on this side. Whether the entry is marked is Withheld's
	// to say, and [OmitEntriesOf] derives the mark from it when the wire list is built,
	// so the mark has one home here rather than two that could disagree.
	Entry OmitEntry
	// Position is the entry's index in the list its document declares, counting the
	// entries a falsy when dropped, so a report names the entry the author wrote
	// wherever it sits in that list.
	Position int
	// Withheld names the fields whose templates read a value an author declared
	// sensitive. A report about the entry quotes none of them. The provider is handed
	// them unchanged, because matching needs the resolved strings, and is told the
	// entry is marked when any of them is named here.
	Withheld OmitFieldsWithheld
}

// OmitFieldsWithheld names which of an omit entry's three resolved fields were
// produced by a template reading a value an author declared sensitive. It is read
// from each field's template over the declarations, never from what the template
// produced.
type OmitFieldsWithheld struct {
	Kind      bool
	Name      bool
	Namespace bool
}

// Any reports whether any of the three fields is withheld, which is what marks the
// whole entry sensitive on a render's input: the wire mark covers the entry rather
// than naming a field.
func (w OmitFieldsWithheld) Any() bool {
	return w.Kind || w.Name || w.Namespace
}

// OmitFieldText renders one resolved field of an omit entry for a text a reader is
// shown: quoted as it resolved, or the placeholder where the field is withheld. The
// placeholder is never quoted, so it cannot be mistaken for a value the entry
// resolved to.
//
// It is the one rendering of an entry's field for every report that names one —
// the host's report on the counts a provider returned and a provider's own refusal
// of an entry alike — so a withheld field reads the same wherever it is reported.
func OmitFieldText(resolved string, withheld bool) string {
	if withheld {
		return redact.Placeholder()
	}
	return strconv.Quote(resolved)
}

// omitFieldsWithheldByMark is what a marked entry says about its fields to the side
// that only receives the mark: every field may have been produced from a value
// declared sensitive, because the mark does not name which one was.
func omitFieldsWithheldByMark(marked bool) OmitFieldsWithheld {
	return OmitFieldsWithheld{Kind: marked, Name: marked, Namespace: marked}
}

// OmitEntriesOf returns the omits list a render input carries for a set of resolved
// entries, in the order they were resolved, each marked sensitive where Withheld
// names any of its fields. It is nil for none, so an input built for a document
// declaring no entries carries no list at all and its payload is the one it would be
// without the key; an entry no declared value produced carries no mark, so its bytes
// are the ones it would have without the mark.
//
// It and [ResolvedOmitsOf] are the two directions of one correspondence — the host
// building the wire list from what it resolved, a guest rebuilding the entries it
// was handed — so each direction has one home.
func OmitEntriesOf(resolved []ResolvedOmit) []OmitEntry {
	if len(resolved) == 0 {
		return nil
	}
	out := make([]OmitEntry, len(resolved))
	for i, r := range resolved {
		out[i] = r.Entry
		out[i].Sensitive = r.Withheld.Any()
	}
	return out
}

// ResolvedOmitsOf rebuilds the entries a render input carries into the shape a
// render core reads them in, each positioned at its place in the list it arrived in.
// A marked entry has every field withheld, because the mark covers the entry without
// naming the field a declared value produced, so a render core reporting on the entry
// quotes none of them. It is nil for an input carrying none.
func ResolvedOmitsOf(entries []OmitEntry) []ResolvedOmit {
	if len(entries) == 0 {
		return nil
	}
	out := make([]ResolvedOmit, len(entries))
	for i, e := range entries {
		withheld := omitFieldsWithheldByMark(e.Sensitive)
		e.Sensitive = false
		out[i] = ResolvedOmit{Entry: e, Position: i, Withheld: withheld}
	}
	return out
}

// SourceIgnore is the recorded ignore rule list a render applies to an entity's
// local source directory, with the directory holding the entity's document,
// which every rule's scope is spelled relative to. It is plain data: the render
// host matches it, and nothing a guest imports reads it.
type SourceIgnore struct {
	// EntityDirectory is the absolute directory holding the entity's document,
	// or empty where the source is not a local one.
	EntityDirectory string
	// Rules is the composed rule list the entity's lock records, in the order it
	// applies.
	Rules []types.IgnoreRule
}

// ProvenanceContext carries build identity for resource labels.
type ProvenanceContext struct {
	CompilerVersion  string
	AggregateName    string
	AggregateVersion string
	ReleaseName      string
	Environment      string
}
