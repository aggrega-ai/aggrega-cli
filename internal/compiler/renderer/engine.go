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
)

// Provider renders one node's values dictionary into on-disk-style artifacts.
// Each returned RenderedFile carries a relative path (e.g., "Chart.yaml",
// "templates/deployment.yaml"), its file contents, and the producer-declared
// format the guest rendered it in (empty when the guest declares none).
// Per-node files are later prefixed by the orchestrator with the boundary path
// (and, for an engine whose assembler nests per node, the per-node sub-directory
// prefix that assembler names — see [BoundaryNesting]). The per-file format
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
// versus opaque) lives in the guest rather than the dumb core. An engine that
// reports (nil, false) — or does not implement the interface at all — borrows
// the core's stock YAML codec ([NewStockYAMLCodec]).
//
// The pipeline executor type-asserts this capability off the engine rendering
// the scope it is carving, exactly as it asserts [BoundaryAssembler] and the
// artifacts-placement capability elsewhere: own-scope units use the own
// engine's codec, host-scope units use the host engine's codec.
type CodecProvider interface {
	PipelineCodec(yamlIndent int) (frp.Codec, bool)
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
// the provider guest; the host assembles the domain-blind per-edge payload,
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
//   - Synthesized — renders a Recipe with a workload: (synthesised mode).
//   - ArtifactsStandalone — renders an artifacts-only Recipe (no source:, no
//     workload:) by emitting its declared artifacts as the node's output.
//
// Which kinds a given provider supports is the provider's own claim, read from
// its describe() self-report — never a table of provider names held in the
// core. A per-provider table here would put provider knowledge in the core,
// and it would be wrong the first time a provider's capabilities changed
// without the core being rebuilt.
//
// The central input-kind gate consumes these to accept or reject a
// (node-mode, provider) combination once per node, with a single uniform
// diagnostic. Synthesis is gated twice over, at two different layers: this
// report decides whether the node may be dispatched at all, and the synthesis
// dispatch separately requires the engine to implement [WorkloadSynthesizer],
// halting with a catalog error naming the provider when it does not.
type ProviderContract struct {
	Sourced             bool
	Synthesized         bool
	ArtifactsStandalone bool
}

// EngineFactory returns the Provider implementation for a given provider name
// (from `recipe.spec.capability`). Returning an error halts the build.
// Engines may be cached/singleton'd by the factory; the orchestrator makes no
// assumption about instance identity.
//
// How often the factory is called is not bounded by the provider set. Some
// callers deliberately resolve each distinct provider once, but the render path
// calls the factory at every crossing it dispatches — per node, per
// resource-generating attachment, per boundary-scope FeatureRecipe, per codec
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
	// The returned [BoundaryNesting] says whether each contributor's per-node
	// files are nested under a per-node sub-directory, and which directory
	// those sub-directories sit under.
	AssembleBoundary(ctx context.Context, info BoundaryInfo) (files map[string]AssembledFile, nesting BoundaryNesting, err error)
}

// AssembledFile is one output of a boundary assembly, keyed by its dispatch-path-
// relative path in the returned map. It pairs the assembled bytes with the
// producer-declared render format the assembler guest reported for them, so the
// format survives the merge boundary and reaches the render-policy parser seam
// alongside the bytes. Format is empty when the producer declared none, in which
// case a downstream consumer selects the format from the file extension. Format
// is additive metadata: it never affects the emitted bytes. The single-carrier
// shape keeps the assembled bytes and their format from drifting into parallel
// maps keyed by the same path.
type AssembledFile struct {
	Content []byte
	Format  string
}

// BoundaryNesting is how a boundary assembler wants its dispatch group's
// per-node files placed. PerNode says whether each contributor's files sit under
// a sub-directory of their own rather than flat at the dispatch path; Segment
// names the directory those per-node sub-directories sit under, and is empty when
// the assembler names none.
//
// The two travel as one carrier because they answer one question — where a
// contributor's files go — and a caller reading the flag has to read the segment
// with it. Keeping them apart would let a segment reach a caller that never
// asked whether nesting was on, which is how the two would drift.
//
// Segment is the assembler's own vocabulary: an assembler that composes an
// enclosing chart names the directory that target calls its child units, one
// that composes a root module names the directory that target calls its own, and
// the orchestrator places the files without reading either word. What the
// orchestrator supplies is the default for an assembler that names nothing, and
// the bound that keeps a named segment inside the dispatch position.
type BoundaryNesting struct {
	PerNode bool
	Segment string
}

// DefaultNestSegment is the directory the orchestrator nests per-node files under
// when a nesting assembler names no segment of its own. It carries no claim about
// any target: it is the layout nesting assemblers already emitted before the
// segment could be named, retained so an assembler that names nothing keeps
// emitting exactly what it emitted before.
const DefaultNestSegment = "charts"

// resolvedSegment returns the directory to nest per-node files under: the
// segment the assembler named, or [DefaultNestSegment] when it named none.
//
// It refuses a segment that is not a single directory name. The orchestrator
// composes the node's identity into the segment, so a value carrying a path
// separator — or naming the current or the parent directory — would place a
// node's files somewhere other than beneath its own position in the dispatch
// group. provider names the provider whose assembler returned the value, so the
// refusal says which one to fix and what it returned.
func (n BoundaryNesting) resolvedSegment(provider string) (string, error) {
	if n.Segment == "" {
		return DefaultNestSegment, nil
	}
	if strings.ContainsAny(n.Segment, `/\`) || n.Segment == "." || n.Segment == ".." {
		return "", fmt.Errorf(
			"%w: provider %q returned %q; the value must name one directory, carrying no path separator and naming neither the current nor the parent directory",
			ErrNestSegmentNotOneDirectory(), provider, n.Segment)
	}
	return n.Segment, nil
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

// AssemblyNameAnnotationReporter is the optional interface an engine implements
// to report a boundary annotation whose resolved value names the assembled
// artifact of a dispatch group. A provider that folds a dispatch group's
// contributing boundaries into one named assembly reports the annotation key it
// reads and whether every contributing boundary must agree on that annotation's
// resolved value. The orchestrator reads the resolved value from each
// contributing boundary's annotations: when require-unique it halts a group whose
// boundaries disagree, and it cascades the resolved value into the assembler. An
// engine that reports an empty annotation gets neither the agreement check nor
// the cascade. A provider routed to a Wasm guest reports the value from the
// guest's live describe() self-report.
type AssemblyNameAnnotationReporter interface {
	AssemblyNameAnnotation() (annotation string, requireUnique bool)
}

// assemblyNameAnnotationOf returns engine's reported assembly-name annotation and
// require-unique flag, or ("", false) when engine reports none. It is the single
// seam the guard and cascade read, so a group's assembly-name rule resolves from
// the engine's own claim independently of its concrete type. A nil engine reports
// none.
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
	return boundaryAssemblyOf(engine) == wasm.BoundaryAssemblyMerge
}

// AssemblesBoundary reports whether an engine assembles its dispatch group's
// per-node output into a combined artifact — true for a provider that folds
// fragments into one document ("merge") or nests each node under its own umbrella
// position ("nest"), false for a flat-output provider ("none") or an engine that
// reports no assembly claim. The assembler election and the artifacts classifier
// read it to decide whether a dispatch group has an assembler.
func AssemblesBoundary(engine Provider) bool {
	switch boundaryAssemblyOf(engine) {
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
// assembler names. Together the two maps are every file per-node Render
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
	NodeSyncWave           map[string]int
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
	WarningSink func(string)
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
// the human-readable messages of its halt-severity entries and the messages of
// its warn- and info-severity entries, each list in the guest's emission order.
// It is the single classification the render, assemble, and input-validation
// surfaces share, so a diagnostic's severity means the same thing on every
// provider-socket crossing: the halting severity stops the build, and the two
// advisory severities advise.
//
// The severity a diagnostic carries is drawn from a closed set the shared
// diagnostic contract declares, and a guest that returns anything outside it
// halts at the socket boundary — see [wasm.GuestDiagnosticSeverities], which
// every socket calls as it decodes a guest's output. So the non-halting branch
// here covers the two advisory severities and nothing else: this classification
// never has to decide what an unroutable value means, because such a value never
// reaches it.
func SplitDiagnostics(diags []Diagnostic) (errs []string, advisories []string) {
	for _, d := range diags {
		if d.Severity == abi.SeverityHalt {
			errs = append(errs, d.Message)
			continue
		}
		advisories = append(advisories, d.Message)
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
