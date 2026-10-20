// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Provider renders one node's values dictionary into on-disk-style artifacts.
// Each returned RenderedFile carries a relative path (e.g., "Chart.yaml",
// "templates/deployment.yaml"), its file contents, and the producer-declared
// format the guest rendered it in (empty when the guest declares none).
// Per-node files are later prefixed by the orchestrator with the boundary path
// (and, for an engine whose provider declares that it nests per node, the
// per-node sub-directory prefix that provider declares — see [BoundaryNesting]).
// The per-file format
// travels with its file so a downstream consumer can prefer the producer's
// format over the path extension; it never affects the emitted bytes.
type Provider interface {
	Render(ctx context.Context, values map[string]any, nodeCtx NodeRenderContext) ([]RenderedFile, error)
	// Contract reports which Recipe input kinds this engine renders. It is
	// the Go analog of the Wasm provider plugin's describe() ABI — each engine
	// self-reports, so the central input-kind gate (orchestrator) reads a
	// provider's support from the engine itself rather than a hardcoded table.
	Contract() ProviderContract
}

// NodeInputValidator is the optional capability a provider engine implements to
// structurally validate a node's resolved render input before render dispatch.
// The central input-kind gate calls it once per node, immediately after the
// (mode, provider) capability check and before any Phase B render pass, so a
// provider that can detect a malformed input surfaces the defect as a Loud
// Guardrail before emission rather than producing broken output.
//
// The implementation returns the diagnostics the provider raised. An error
// return is an infrastructure failure (the guest could not run); a nil error
// with diagnostics is a completed check whose findings the caller routes by
// severity — a halt-severity diagnostic halts the build (ErrProviderNodeInputInvalid,
// naming the node and provider), a warning or info diagnostic surfaces to the
// build result without changing rendered output. An engine that does not
// advertise the capability returns no diagnostics and no error, so the gate is a
// no-op for it.
type NodeInputValidator interface {
	ValidateNodeInput(ctx context.Context, values map[string]any, nodeCtx NodeRenderContext) ([]Diagnostic, error)
}

// CodecProvider is the optional capability a Provider implements to supply a
// pipeline codec — how the provider's emitted output is carved into addressable
// units and reassembled byte-faithfully after a step edits some of them. An
// engine reports (codec, true) when its provider guest advertises the carve and
// reassemble exports; the host then drives carve and reassemble across the guest
// ABI so the provider's own output-shape knowledge (what is a PATCH target
// versus opaque) lives in the guest rather than the content-neutral core. An engine that
// reports (nil, false) — or does not implement the interface at all — borrows
// the core's stock YAML codec ([NewStockYAMLCodec]).
//
// The pipeline executor type-asserts this capability off the engine rendering
// the scope it is carving, exactly as it asserts [BoundaryAssembler] and the
// artifacts-placement capability elsewhere: own-scope units use the own
// engine's codec, host-scope units use the host engine's codec.
// The context is the render's own, and it is taken here rather than on the
// codec's own methods because the carve-and-reassemble contract is the guest
// export contract unchanged: a guest holds no host context, so that contract
// cannot take one. An engine that drives its codec across the guest ABI carries
// the context on the codec it returns.
type CodecProvider interface {
	PipelineCodec(ctx context.Context, yamlIndent int) (frp.Codec, bool)
}

// CrossBoundaryLinker is the optional capability a provider engine implements
// to wire a cross-boundary uses edge — an edge whose consuming and consumed
// ends assemble into different dispatch groups — into provider-native
// cross-reference wiring. It has two invoke halves that produce different
// artifacts applied to the two ends' assembled output, plus a capability query
// the host reads before it routes any link crossing to the engine.
//
//   - LinkPublish, given the consumed (target) end of a link, returns the
//     published identity the consumer resolves against and any producer-side
//     wiring to apply to the consumed end's assembled output.
//   - LinkResolve, given the consuming (source) end and the peer's published
//     identity handle, returns the consumer-side wiring to apply to the
//     consuming end's assembled output.
//
// Both invoke halves are advertised-only: an engine whose guest does not
// declare the matching export returns a zero result and no error, so the host
// applies no wiring. LinksCrossBoundary reports the guest's plugin-level claim
// (both link exports present) so the host engages a link crossing only when at
// least one end's provider advertises linking; an engine that does not
// implement this interface, or reports false, wires no cross-boundary edge and
// the existing per-node scalar value-pipe stands. The wiring itself lives in
// the provider guest; the host assembles the domain-agnostic per-edge payload,
// routes it, and applies the returned structural patches.
type CrossBoundaryLinker interface {
	LinksCrossBoundary() bool
	LinkPublish(ctx context.Context, in LinkPublishInput) (LinkPublishOutput, error)
	LinkResolve(ctx context.Context, in LinkResolveInput) (LinkResolveOutput, error)
}

// linksCrossBoundary reports whether engine advertises cross-boundary linking:
// it implements [CrossBoundaryLinker] and its guest declares the link export
// pair. It is the single seam the link pass reads, so an engine's linking
// capability resolves independently of its concrete type. A nil engine, or one
// that does not implement the interface, reports false.
func linksCrossBoundary(engine Provider) bool {
	if l, ok := engine.(CrossBoundaryLinker); ok {
		return l.LinksCrossBoundary()
	}
	return false
}

// ProviderContract declares which of the three Recipe input kinds a
// provider engine can render. This is the INPUT-KIND axis only — it is
// orthogonal to the output-assembly axis ([BoundaryAssembler] and
// [BoundaryAssemblyReporter], which describe how a node's output is assembled,
// not which input kinds it accepts). Conflating the two is the easy mistake:
// an engine that merges its output and an engine that renders sourced input are
// answering different questions.
//
//   - Sourced — renders a Recipe with a source: (sourced mode).
//   - Synthesized — renders a Recipe with a workload: (synthesized mode).
//   - ArtifactsStandalone — renders an artifacts-only Recipe (no source:, no
//     workload:) by emitting its declared artifacts as the node's output.
//
// Which kinds a given provider supports is the provider's own claim, read from
// its describe() self-report — never a table of provider names held in the
// core. A per-provider table here would put provider knowledge in the core,
// and it would be wrong the first time a provider's capabilities changed
// without the core being rebuilt.
//
// The central input-kind gate (gateInputKind) consumes these to accept or reject
// a (node-mode, provider) combination once per node, with a single uniform
// diagnostic. It is the ONE gate deciding whether a node may be dispatched, and
// it consults every kind the node declares rather than only the kind the node's
// mode is named after.
//
// The synthesis and artifacts dispatches type-assert the engine to
// [WorkloadSynthesizer] and [ArtifactsEmitter] before handing it a workload or an
// artifact set, and neither assertion is a second gate on what an author wrote:
// the host renders through a single provider adapter, which satisfies both
// interfaces by compile-time assertion whatever its guest's describe() declares,
// so no provider this host ships can fail either one. A provider declining an
// input kind is therefore refused at the gate above, by its own declared
// contract. The assertions' false branches survive as guards against an outcome
// worse than a loud refusal — a dispatch that emits nothing for a declared input
// and reports success.
// It is an ALIAS of [types.PluginInputKinds] rather than a distinct struct. The
// input-kind claim is one fact about one provider and has one home; an alias means
// a claim read off a Plugin entity reaches this gate without a field copy, so no
// copy can drop a kind between the entity and the gate that reads it. The local
// name is kept because the gate's vocabulary is a provider's contract rather than
// entity authoring.
//
// Adding a kind means adding a field to the home type. Do not declare a second
// three-boolean struct here or anywhere else: nothing makes that fail to compile,
// so the only thing standing between a new shape and a capability this gate
// silently stops reading is the field-set guard in internal/wasm and this comment.
type ProviderContract = types.PluginInputKinds

// Compiles only while ProviderContract really is an alias. Two DISTINCT named
// struct types are never assignable to one another in Go however identical their
// fields, so re-expanding the alias into a struct of its own breaks the build here
// — which makes this the one rung on this path that a reader cannot walk past.
var _ ProviderContract = types.PluginInputKinds{}

// EngineFactory returns the Provider implementation for a given provider name
// (from `recipe.spec.capability`). Returning an error halts the build.
// Engines may be cached/singleton'd by the factory; the orchestrator makes no
// assumption about instance identity.
//
// How often the factory is called is not bounded by the provider set. Some
// callers deliberately resolve each distinct provider once, but the render path
// calls the factory at every crossing it dispatches — per node, per attachment
// the render sequence admits, per boundary-scope FeatureRecipe, per codec
// selection. So a factory that returns a fresh Provider per call yields one
// Provider per crossing; a factory that memoizes on the identity it resolves
// collapses that to one Provider per distinct provider identity.
//
// The choice is not cost-neutral, because a Provider that reuses a guest instance
// across crossings holds that instance for its own lifetime, and nothing releases
// it when a crossing ends — closing the Provider is what releases it. So a factory
// that memoizes also exposes a way to close what it memoized, and its caller
// closes at build end. A factory that neither memoizes nor closes accumulates one
// such instance per crossing for the whole build.
type EngineFactory func(providerName string) (Provider, error)

// ProviderVersionResolver reports the resolved provider-rendering plugin version
// behind a provider binding (recipe.spec.capability, version pin included), or the
// empty string when no provider-rendering plugin version resolves behind that
// binding. It shares the provider→plugin resolution the engine factory performs,
// so the version reported here is the one behind the engine the factory returns
// for the same binding. A nil resolver reports every binding as version-less.
type ProviderVersionResolver func(providerRef string) string

// BoundaryAssembler is the optional interface a provider engine implements to
// assemble per-(evaluated_path, provider) artifacts from the per-node Render
// outputs at that dispatch path. The helm-chart provider uses this to emit an
// umbrella Chart.yaml + values.yaml listing each component as a sub-chart
// dependency; the compose provider uses it to merge per-node fragments into
// one docker-compose.yaml per dispatch path. Engines that only produce flat
// per-node output do not implement this.
//
// Per-(evaluated_path, provider) dispatch is the single assembly tier: the
// orchestrator groups contributors by dispatch key and invokes AssembleBoundary
// once per group.
type BoundaryAssembler interface {
	// AssembleBoundary returns assembled files relative to the dispatch path.
	// The orchestrator prefixes them with `boundaries/<evaluated_path>/`
	// (or, post path-template-evaluation, the operator-authored path).
	//
	// Where a contributor's own per-node files sit is deliberately NOT part of
	// this reply. It is a static property of the provider, read from its
	// self-description through [boundaryNestingOf] and so in hand before any node
	// renders — which is what lets the orchestrator place output whose position is
	// decided during render rather than after assembly.
	AssembleBoundary(ctx context.Context, info BoundaryInfo) (files map[string]AssembledFile, err error)
}

// AssembledFile is one output of a boundary assembly, keyed by its dispatch-path-
// relative path in the returned map. It pairs the assembled bytes with the
// producer-declared render format the assembler guest reported for them, so the
// format survives the merge boundary and reaches the render-policy parser seam
// alongside the bytes. Format is empty when the producer declared none, in which
// case a downstream consumer selects the format from the file extension. Format
// is additive metadata: it never affects the emitted bytes. The single-record
// shape keeps the assembled bytes and their format from drifting into parallel
// maps keyed by the same path.
type AssembledFile struct {
	Content []byte
	Format  string
}

// BoundaryNesting is how a dispatch group's per-node files are placed. PerNode
// says whether each contributor's files sit under a sub-directory of their own
// rather than flat at the dispatch path; Segment names the directory those
// per-node sub-directories sit under, and is empty when the provider names none.
//
// The two travel as one value because they answer one question — where a
// contributor's files go — and a caller reading the flag has to read the segment
// with it. Keeping them apart would let a segment reach a caller that never
// asked whether nesting was on, which is how the two would drift.
//
// Segment is the provider's own vocabulary: a provider that composes an
// enclosing chart names the directory that target calls its child units, one
// that composes a root module names the directory that target calls its own, and
// the orchestrator places the files without reading either word. What the
// orchestrator supplies is the bound that keeps a named segment inside the
// dispatch position.
//
// Both halves are read from the provider's static self-description rather than
// from the reply its assembler returns, through [boundaryNestingOf]. That is what
// makes the whole of a group's layout answerable before any node renders, which
// a reply cannot be: an attachment's files are placed during render, and a
// provider without an assembler export never replies at all.
type BoundaryNesting struct {
	PerNode bool
	Segment string
}

// boundaryNestingOf reads where a dispatch group's per-node files sit from the
// elected provider's own self-description. Only a provider that nests places its
// contributors under sub-directories, and the derivation is exactly that: nesting
// is the declared assembly mode, and the directory those sub-directories sit
// under is the segment the provider declares beside it.
//
// PerNode is derived rather than declared separately because a separate
// declaration would be a second statement of the mode, free to contradict it —
// and a provider claiming per-node placement while declaring that it folds its
// contributors into one document would be describing two layouts at once.
func boundaryNestingOf(engine Provider) BoundaryNesting {
	if !boundaryAssemblyNests(boundaryAssemblyOf(engine)) {
		return BoundaryNesting{}
	}
	return BoundaryNesting{PerNode: true, Segment: nestSegmentOf(engine)}
}

// namesOneDirectory reports whether a provider-declared value is a single
// directory name rather than a path.
//
// It is the one home of what that means, because both declared directories are
// bounded by the same rule for the same reason — the host composes an identity
// into each, so a value carrying a path separator, or naming the current or the
// parent directory, would place files somewhere other than beneath the position
// they belong to. A second spelling of the comparison would agree on the day it
// was written and be free to drift afterwards, silently, since nothing compares
// two spellings of one rule.
func namesOneDirectory(declared string) bool {
	return !strings.ContainsAny(declared, `/\`) && declared != "." && declared != ".."
}

// notOneDirectoryErr wraps condition into the refusal both declared directories
// share: which provider declared the value, which declaration it was, what the
// value was, and what a conforming one looks like.
//
// The condition is an ARGUMENT rather than a field on a shared descriptor, and
// that is deliberate rather than incidental. A condition reached through a value
// held in a struct is raised by nothing any census of raise sites can see, and
// this project's published condition reference is built from exactly such a
// census — so a shared descriptor holding the two conditions would quietly drop
// both of them from the reference while every test still passed. Passing the
// condition in keeps each raise a call the census reads, and still leaves the
// sentence with one home.
func notOneDirectoryErr(condition error, provider, declaration, declared string) error {
	return fmt.Errorf(
		"%w: provider %q declared %s %q; the value must name one directory, carrying no path separator and naming neither the current nor the parent directory",
		condition, provider, declaration, declared)
}

// resolvedPrefix returns the path prefix each contributor's own sub-directory
// sits beneath, empty for a group whose provider names no enclosing directory.
func (n BoundaryNesting) resolvedPrefix(provider string) (string, error) {
	if n.Segment == "" {
		return "", nil
	}
	if !namesOneDirectory(n.Segment) {
		return "", notOneDirectoryErr(ErrNestSegmentNotOneDirectory(), provider, "nest_segment", n.Segment)
	}
	return n.Segment + "/", nil
}

// BoundaryAssemblyReporter is the optional interface an engine implements to
// report how it assembles a dispatch group's per-node output: "merge" folds the
// per-node fragments into one document, "nest" places each node's output under its
// own position in an umbrella structure, and "none" writes each node's output
// directly at the dispatch path. An empty value carries no assembly claim. A
// provider routed to a Wasm guest reports the value from the guest's live
// describe() self-report, so the host classifies render output from the same claim
// the guest advertises rather than from a host-side marker.
type BoundaryAssemblyReporter interface {
	BoundaryAssembly() string
}

// boundaryAssemblyOf returns engine's reported boundary-assembly mode, or the
// empty string when engine reports none. It is the single seam the classification
// helpers read, so an engine's assembly behaviour resolves independently of its
// concrete type.
func boundaryAssemblyOf(engine Provider) string {
	if r, ok := engine.(BoundaryAssemblyReporter); ok {
		return r.BoundaryAssembly()
	}
	return ""
}

// NestSegmentReporter is the optional interface an engine implements to report
// the directory its target places each contributor's own sub-directory under. A
// provider routed to a Wasm guest reports the value from the guest's live
// describe() self-report, so the directory is the target's contract term stated
// once, in the guest that serves that target.
type NestSegmentReporter interface {
	NestSegment() string
}

// nestSegmentOf returns engine's reported nesting directory, or the empty string
// when engine reports none. It is the single seam every reader of that report
// goes through, so the value resolves from the engine's own claim independently
// of its concrete type.
func nestSegmentOf(engine Provider) string {
	if r, ok := engine.(NestSegmentReporter); ok {
		return r.NestSegment()
	}
	return ""
}

// OmitsReporter is the optional interface an engine implements to report whether
// its provider honours the omit entries a source render carries: that it leaves
// out every rendered document an entry matches, and returns one count per entry it
// was handed. A provider routed to a Wasm guest reports the claim from the guest's
// live describe() self-report, so whether a provider leaves documents out is the
// provider's own statement rather than anything the host decides about it.
type OmitsReporter interface {
	HonoursOmits() bool
}

// honoursOmits reports whether engine declares that it honours omit entries. It is
// the single seam the claim is read through — the described presentation's facts
// read it, and the rules refusing a document whose provider does not honour its
// entries read those facts — so the claim resolves from the engine's own report
// independently of its concrete type. An engine that does not implement the
// interface makes no such claim, which is the safe answer: a document declaring
// omits is then refused rather than rendered with nothing left out.
func honoursOmits(engine Provider) bool {
	if r, ok := engine.(OmitsReporter); ok {
		return r.HonoursOmits()
	}
	return false
}

// BoundaryArtifactsReporter is the optional interface an engine implements to
// report whether its provider accepts the files a Recipe contributes to the shared
// output of the boundary it renders in: whether its target treats a file the host
// writes in the dispatch group's own directory, beside what its assembly writes
// there, as part of that output. A provider routed to a Wasm guest reports the
// claim from the guest's live describe() self-report, so whether such a file means
// anything to a target is the provider's own statement.
type BoundaryArtifactsReporter interface {
	AcceptsBoundaryArtifacts() bool
}

// acceptsBoundaryArtifacts reports whether engine declares that it accepts the
// files a Recipe contributes to its boundary's shared output. It is the single
// seam the claim is read through — the described presentation's fact reads it,
// and the rule refusing a Recipe whose provider does not accept the files reads
// that fact. An engine that does not implement the interface makes no such claim,
// which is the safe answer: a Recipe declaring the files is then refused rather
// than rendered into an output its target would ignore them in.
func acceptsBoundaryArtifacts(engine Provider) bool {
	if r, ok := engine.(BoundaryArtifactsReporter); ok {
		return r.AcceptsBoundaryArtifacts()
	}
	return false
}

// AssemblyNameAnnotationReporter is the optional interface an engine implements
// to report a boundary annotation whose resolved value names the assembled
// artifact of a dispatch group. A provider that folds a dispatch group's
// contributing boundaries into one named assembly reports the annotation key it
// reads and whether every contributing boundary must agree on that annotation's
// resolved value.
//
// Two things read that report, and both read the value each contributing
// boundary's annotations resolved to. The render records the report and those
// resolved values as facts about the group, which is where a rule about the
// group's naming is invoked and halts a group whose boundaries disagree while
// agreement is required. The render separately cascades the first resolved value
// it finds into the assembler, which is what names the artifact. An engine that
// reports an empty annotation gets neither. A provider routed to a Wasm guest
// reports the value from the guest's live describe() self-report.
type AssemblyNameAnnotationReporter interface {
	AssemblyNameAnnotation() (annotation string, requireUnique bool)
}

// assemblyNameAnnotationOf returns engine's reported assembly-name annotation and
// require-unique flag, or ("", false) when engine reports none. It is the single
// seam every reader of that report goes through — the record the render takes of
// each group's election, which is what the rule about the group's naming reads its
// facts from, and the cascade that names the assembled artifact — so both resolve
// from the engine's own claim independently of its concrete type. A nil engine
// reports none.
func assemblyNameAnnotationOf(engine Provider) (annotation string, requireUnique bool) {
	if r, ok := engine.(AssemblyNameAnnotationReporter); ok {
		return r.AssemblyNameAnnotation()
	}
	return "", false
}

// RenderIsIntermediate reports whether an engine's per-node render/synthesis
// output is intermediate input to a per-(path, provider) boundary assembly pass
// rather than a file written directly at the dispatch path. It is true only for a
// provider that folds its dispatch group's per-node fragments into one document
// (boundary assembly "merge"); a provider that nests each node under its own
// umbrella position ("nest") and a flat-output provider ("none") write per-node
// content directly and are not intermediate. It reads the engine's boundary-
// assembly self-report, so a provider routed to a Wasm guest classifies from the
// guest's live describe() claim. This is the single home of the
// intermediate-marking rule; every classification site reads it here.
func RenderIsIntermediate(engine Provider) bool {
	return boundaryAssemblyFolds(boundaryAssemblyOf(engine))
}

// boundaryAssemblyFolds reports whether a declared boundary-assembly mode folds
// a dispatch group's per-node output into ONE document. Only the fragment-merging
// mode does. A nesting mode combines a group's output without folding it — each
// node keeps a document of its own under its own position — and a flat-output
// mode and an absent claim combine nothing.
//
// It is separated from [boundaryAssemblyCombines] because the two answer
// different questions and the difference is exactly one mode. Whether a group HAS
// an assembler is what the election and the artifacts classifier ask; whether a
// group has one document in which two contributors' names can displace each other
// is what the derived-name comparison asks, and nesting answers the first yes and
// the second no.
//
// It is one helper rather than the same comparison written at each reader, for
// the reason the sibling above is: the two readers hold the claim in different
// forms — the classification above holds an engine and asks it, while a rule
// declared over a dispatch group holds the elected provider's claim as a declared
// value and has no engine to ask.
func boundaryAssemblyFolds(mode string) bool {
	return mode == wasm.BoundaryAssemblyMerge
}

// boundaryAssemblyNests reports whether a declared boundary-assembly mode places
// each of a dispatch group's contributors under a position of its own inside an
// enclosing structure. Only the nesting mode does; folding produces one document
// with no per-contributor position, and a flat mode and an absent claim produce
// no enclosing structure at all.
//
// It is the third of the mode predicates rather than a negation of the other two,
// for the reason they are separate from each other: each answers a different
// question, and the three questions do not partition the modes the same way.
func boundaryAssemblyNests(mode string) bool {
	return mode == wasm.BoundaryAssemblyNest
}

// AssemblesBoundary reports whether an engine assembles its dispatch group's
// per-node output into a combined artifact — true for a provider that folds
// fragments into one document ("merge") or nests each node under its own umbrella
// position ("nest"), false for a flat-output provider ("none") or an engine that
// reports no assembly claim. The assembler election and the artifacts classifier
// read it to decide whether a dispatch group has an assembler.
func AssemblesBoundary(engine Provider) bool {
	return boundaryAssemblyCombines(boundaryAssemblyOf(engine))
}

// boundaryAssemblyCombines reports whether a declared boundary-assembly mode
// names an assembly at all: folding the per-node fragments into one document, or
// nesting each node under its own position. A flat-output mode and an absent
// claim both name none.
//
// It is one helper rather than the same two-value comparison written at each
// reader, and it is separated from [AssemblesBoundary] because the two readers
// hold the claim in different forms: the classification above holds an engine and
// asks it, while a rule declared over a dispatch group holds the elected
// provider's claim as a declared value and has no engine to ask. Which modes
// combine is one decision, and a second copy of it would agree on the day it was
// written and be free to drift the moment a mode were added — at which point the
// stale copy would go quiet rather than fail.
func boundaryAssemblyCombines(mode string) bool {
	switch mode {
	case wasm.BoundaryAssemblyMerge, wasm.BoundaryAssemblyNest:
		return true
	default:
		return false
	}
}

// BoundaryInfo carries everything the BoundaryAssembler needs.
//
// BoundaryPath is the dispatch path the assembler writes into.
// AggregateName is the top-level Aggregate's name (for output identity).
// SubCharts is the per-sub-chart payload list (helm-chart umbrellas
// consume this to emit dependencies + sub-chart values; compose ignores).
//
// IntermediatesByNode carries the per-contributor Intermediate=true
// EmittedFiles produced by per-node Render. Compose reads these as
// fragments and merges into the assembled docker-compose.yaml; helmchart
// ignores (umbrella content comes from SubCharts).
//
// PlacedByNode is its exact complement: the per-contributor
// Intermediate=false EmittedFiles produced by per-node Render — the files
// the orchestrator places itself, at the per-node position a nesting
// provider declares. Together the two maps are every file per-node Render
// produced for the group, which is what an assembler is handed as its
// contributor list; a fragment-merging assembler's contributors are all in
// IntermediatesByNode and a nesting assembler's are all in PlacedByNode, so
// neither map alone sees a whole dispatch group.
//
// NodeAnnotations is per-node annotation map (NodeID → annotation map).
// Compose reads `aggrega.ai/namespace` from these to derive a docker
// network name per contributing boundary.
//
// NodeComponent maps NodeID → resolved component-type URI. Used by
// assemblers that need per-node component identity for output metadata.
//
// NodeValues is the resolved values dictionary of each contributing node whose
// files were rendered from a source — for an attached Feature, only where its
// FeatureRecipe's values mapping produced them — and of no other: a node that
// rendered its own files resolved its values into them and has no entry. An assembler whose
// output is the only position its target evaluates carries these into the
// assembled artifact itself; one whose contributors already carry their own
// inputs ignores it.
//
// ContributingBoundaries lists every boundary path whose components fed
// into this dispatch group. Used for diagnostic context in error messages
// and for chart-name cascade lookups.
//
// Edges carries the IR's directed `uses` edges across the entire graph.
// Compose uses this to infer `depends_on` between services. Assemblers
// filter to edges where both endpoints are contributing-service names.
//
// ChartName is the cascaded `aggrega.ai/chart-name` annotation value
// when present (set by the orchestrator from the resolver's per-boundary
// annotation cascade for the helm-chart provider's merged-path case).
// Empty when no cascade value resolves; helmchart umbrella falls back to
// path-derived naming.
type BoundaryInfo struct {
	BoundaryPath        string
	AggregateName       string
	SubCharts           []SubChartInfo
	IntermediatesByNode map[string][]EmittedFile
	PlacedByNode        map[string][]EmittedFile
	NodeAnnotations     map[string]map[string]string
	NodeComponent       map[string]string
	// NodeSyncWave is each contributing node's deployment wave, keyed by node
	// id exactly as NodeAnnotations and NodeComponent are. It travels per node
	// rather than once per BoundaryInfo because a dispatch group can merge
	// contributors from several boundaries, each with its own wave, which is
	// precisely the case where one assembled artifact holds more than one. A
	// node whose boundary declares no wave carries 0, the field's documented
	// default, so a missing entry and a declared zero are the same state.
	NodeSyncWave map[string]int
	// NodeValues is each contributing node's resolved values dictionary, keyed
	// by node id exactly as NodeAnnotations, NodeComponent and NodeSyncWave are,
	// and populated at the same site from the same node render. It travels per
	// node for the same reason the wave does: one assembled artifact can hold
	// contributors from several boundaries, each with its own values. A node
	// whose render produced no values carries a nil map, which is the same state
	// as an empty one — an assembler reads "no inputs" from both.
	//
	// It holds an entry only for a node rendered from a source. That is the one
	// case where the compiler supplies inputs the node's own files do not already
	// carry: the files came in verbatim, so the values could not be written into
	// them and an outer position has to supply them. A node whose render produced
	// its own files resolved the values into those files, so it has no entry here
	// and an assembler supplies it nothing — offering inputs a node's files never
	// declare is an assembled artifact its target may reject. A contributor that is
	// an attached Feature holds an entry only where, besides rendering from a
	// source, its FeatureRecipe's values mapping produced the values.
	NodeValues map[string]map[string]any
	// NodeValuesMapped reports, per node id, that the NodeValues entry for that
	// node is the output of its Recipe's or its FeatureRecipe's values mapping — the
	// same mark the node's own render projection carried. An entry is present only
	// beside a NodeValues entry and only where it is true, so an absent entry reads
	// as "not mapped", which is also what a node carrying no values reads as.
	NodeValuesMapped map[string]bool
	// NodeNameBasis is the identity each contributor derives its own names from
	// inside the assembled artifact, keyed by node id exactly as the maps above are.
	// An entry is present only where that identity is not the node id itself, which
	// is the case for every contributor that is not one of the graph's nodes: an
	// attached Feature reaches its group under a dispatch identity the compiler
	// built, and no target's name alphabet accepts it. Both attachment scopes do,
	// and each carries the identity its own render derived names from — a
	// boundary-scope one the composed identity settled for it, a component-scope one
	// the host component it renders against.
	//
	// It travels to the assembler because the alternative is an assembler deriving
	// its names from the dispatch identity while the render that produced the content
	// declared them from the composed one — two derivations of one name from two
	// identities, where the name the assembled artifact addresses content by is not
	// the name anything declared. Nothing fails on that: each side is
	// self-consistent, and only a reader holding both sees the mismatch.
	NodeNameBasis map[string]string
	// NodeOutputDirectory is the directory each contributor's own files sit in,
	// relative to the assembled artifact's own position and carrying no trailing
	// separator, keyed by node id exactly as the maps above are. It is populated
	// only for a group whose provider places each contributor under a position of
	// its own; a provider that folds its contributors into one document places no
	// per-contributor directory, so the map is empty and every contributor reads
	// no position.
	//
	// It is the group's own placement that fills this, from the same composition
	// that writes the files, so an assembler naming a contributor's files from
	// inside the assembled artifact names the directory they were written to. The
	// alternative is an assembler composing a position from the contributor's
	// identity while the placement composed one from the contributor's enclosing
	// key — two derivations that agree for a contributor placed directly beneath
	// the assembly and diverge for one placed inside another contributor's
	// position, where the assembled artifact would point at a directory holding
	// none of that contributor's files.
	//
	// It also carries the group's whole contributor set for an assembler that
	// nests: a contributor whose files the group keyed into another contributor's
	// map appears in neither of the two file maps above, so this is where such a
	// contributor is named at all.
	NodeOutputDirectory    map[string]string
	ContributingBoundaries []string
	Edges                  []IREdgeInfo
	ChartName              string
	// YAMLIndent is the indentation width for the assembled artifact (clamped
	// to [2,9], default 2). Forwarded from RendererInput.
	YAMLIndent int
	// WarningSink, when non-nil, receives the non-fatal advisories a boundary
	// assembler raises while merging a dispatch group — the warning- and
	// info-severity diagnostics the guest returns in its assemble output, plus
	// any guest-emitted warnings. The orchestrator wires it to the build result's
	// warnings stream; assemblers nil-check it before calling. Warnings never
	// affect emitted bytes. Host-only: not part of any wire projection.
	//
	// It carries the whole diagnostic for the reason the per-node sink does: an
	// entry travels past the terminal to a consumer that reads none, so the
	// severity and the class code stay fields rather than becoming substrings of
	// a rendered line.
	WarningSink func(Diagnostic)
}

// SubChartInfo describes one sub-chart contributing to an umbrella chart.
type SubChartInfo struct {
	InstanceID string         // dot-notation node ID
	Version    string         // resolved version
	Values     map[string]any // per-sub-chart values for umbrella values.yaml
	Source     any            // forwarded recipe.spec.source; nil for generative sub-charts
}

// SubChartProjection is the filesystem-free view of one sub-chart an
// umbrella-assembling engine hands to a Wasm guest. It carries the sub-chart's
// instance identity, the version the umbrella pins, the per-instance values the
// umbrella scopes under the sub-chart's alias, and — for a sub-chart vendored
// from a chart source — the pristine bytes of that chart's Chart.yaml, read from
// the local filesystem at compile time. A generative sub-chart carries no
// ChartYAML. A guest assembles the umbrella from these views alone and touches no
// filesystem.
type SubChartProjection struct {
	InstanceID string
	Version    string
	Values     map[string]any
	ChartYAML  []byte
}

// SplitDiagnostics partitions a provider guest's diagnostics by severity into
// the human-readable messages of its halt-severity entries and the whole
// diagnostics of its warn- and info-severity entries, each list in the guest's
// emission order. It is the single classification the render, assemble, and
// input-validation surfaces share, so a diagnostic's severity means the same
// thing on every provider-socket crossing: the halting severity stops the build,
// and the two advisory severities advise.
//
// The two halves are returned in different shapes because they are consumed for
// different things. A halting entry is joined into an error's text, so nothing
// but its message survives the join and returning more would be returning
// something no caller can carry. An advisory entry travels onward to a display
// and to a consumer that reads no display, so it keeps every field the guest
// populated — the code a class is suppressed by, the element it names, and the
// position it points at, none of which is recoverable from a sentence.
//
// The severity a diagnostic carries is drawn from a closed set the shared
// diagnostic contract declares, and a guest that returns anything outside it
// halts at the socket boundary — see [wasm.GuestDiagnosticSeverities], which
// every socket calls as it decodes a guest's output. So the non-halting branch
// here covers the two advisory severities and nothing else: this classification
// never has to decide what an unroutable value means, because such a value never
// reaches it.
func SplitDiagnostics(diags []Diagnostic) (errs []string, advisories []Diagnostic) {
	for _, d := range diags {
		if d.Severity == abi.SeverityHalt {
			errs = append(errs, d.Message)
			continue
		}
		advisories = append(advisories, d)
	}
	return errs, advisories
}

// SourceForDiagnostic returns a human-readable string identifying
// nodeCtx's source for use in engine error messages. Prefers
// OriginalSource (the verbatim Recipe spec.source — what the
// operator authored, e.g. "oci://hub.aggrega.ai/charts/mariadb")
// over Source (the resolved cache path, opaque to operators).
// Falls back to Source when OriginalSource is unset (sourceless
// recipes, local-path recipes, legacy code paths).
//
// Returns "<sourceless>" when both fields are nil so error
// messages stay self-describing.
func SourceForDiagnostic(nodeCtx NodeRenderContext) string {
	if nodeCtx.OriginalSource != nil {
		if s, ok := nodeCtx.OriginalSource.(string); ok {
			return s
		}
		return fmt.Sprintf("%+v", nodeCtx.OriginalSource)
	}
	if nodeCtx.Source != nil {
		if s, ok := nodeCtx.Source.(string); ok {
			return s
		}
		return fmt.Sprintf("%+v", nodeCtx.Source)
	}
	return "<sourceless>"
}
