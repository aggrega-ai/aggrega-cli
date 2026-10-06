// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pipeline orchestrates the 5-step compiler pipeline from already-
// loaded inputs to in-memory artifacts. It carries no CLI dependency: no
// flag parsing, no dist/ writing, no .trace/ writing. The CLI wrapper at
// cmd/aggrega/build.go composes catalog loading and dist/ writing around
// RunPipeline; a long-running daemon invokes RunPipeline directly with its
// own in-memory inputs (parse-cached Release and Manifest, cached raw
// Aggregate bytes, pre-constructed catalog lookup callbacks).
//
// RunPipeline performs no filesystem reads beyond the operations the
// caller-supplied lookup callbacks perform. The Aggregate path on Inputs
// is a source-location label only; it is not re-read.
package pipeline

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Inputs carry the caller-supplied state RunPipeline needs to execute the
// 5-step pipeline. Release and Manifest are pre-parsed typed values;
// AggregateRawBytes is the raw YAML the resolver interpolates with the
// merged parameter map. AggregatePath is a source-location label used in
// resolver errors and annotation-provenance entries; RunPipeline performs
// no filesystem operations against it.
type Inputs struct {
	Release           *types.Release
	Manifest          *types.Manifest
	AggregateRawBytes []byte
	AggregatePath     string
	// ReleasePath is the path of the Release document the run compiles, as the
	// caller holds it: the path a command was given, or the one a request named —
	// a path this process opens the Release by. The Release itself is not read
	// through it; the one file read beside it is the parameters file the Release
	// imports, which is resolved from the directory holding it and held to
	// WorkspaceRoot — or, for a Release named outside the workspace, to that
	// directory.
	//
	// The run renders it against WorkspaceRoot through catalog.RelativeToWorkspace,
	// the rule every other authored document's path in the graph is rendered by,
	// and that rendering is what names the Release wherever the run names it: the
	// file of a finding about what the Release wrote — an attachment it fills, a
	// connection it draws, a boundary it declares — and the source an annotation
	// written in it is traced to in the graph. A caller holding an absolute path and
	// one holding a path relative to the workspace therefore produce the same
	// graph, byte for byte, on any machine holding the same workspace layout.
	//
	// Every surface that runs the pipeline over a Release supplies it. A run that
	// leaves it empty names no file for the Release: a finding about what the
	// Release wrote carries its authored key alone, and an annotation written there
	// is traced to its key path with no file before it.
	ReleasePath string
	// RootAggregateFile is the source-location label stamped onto the flattened
	// root-tier topology node (and carried to IRNode.SourceFile) — a label only,
	// no I/O. It is distinct from AggregatePath: AggregatePath drives the
	// resolver's annotation source-location strings, while this drives the node
	// source stamp, and a caller may need different representations for the two
	// (a release inspection uses a sibling-resolved AggregatePath for its
	// annotation brackets but a workspace-relative RootAggregateFile for a
	// machine-stable node stamp). Optional: when empty, the flatten defaults it
	// to AggregatePath, so callers that supply a single workspace-relative path
	// are unaffected.
	RootAggregateFile string

	// WorkspaceConfig is the workspace configuration this run is composed
	// against. It is REQUIRED by the graph entry, which refuses a nil one rather
	// than reading a zero value in its place.
	//
	// A pointer, and the pointer is the whole point: an empty configuration and an
	// unsupplied one are different facts, and the zero value of a configuration
	// struct reports the second as the first. The governance attachments live
	// under it, and two of the roles attached there SHAPE THE GRAPH — the
	// label-transform role that runs between the flatten and the build, and the
	// annotation-transform role that runs inside the resolve. Each reads its
	// attachment list off this field and returns immediately when the list is
	// empty, so reading a zero value in place of an absent one skips both roles in
	// silence: the run is then indistinguishable from a workspace that attached
	// nothing, and the graph it produces is not the graph a build of the same
	// documents produces. A plausible zero value that is wrong is indistinguishable
	// from a real answer, which is why this one is refused rather than read — the
	// same reason a facts presentation refuses to answer a question it cannot see
	// rather than returning a confident "no".
	//
	// The pointed-to value is the pipeline's to READ, never to write: a caller
	// hands over a pointer into its own configuration, and a long-lived surface
	// serving many runs hands the same one to each. A surface that needs to run
	// against an altered configuration copies it and points the field at the copy,
	// which is what the governance composition does when it rewrites the
	// attachment list.
	//
	// That refusal is what every read of this field below the entry rests on:
	// those reads carry no nil test of their own, deliberately, because a read
	// substituting an empty configuration for an absent one would restore at each
	// site exactly the plausible-but-wrong zero the refusal exists to stop — and it
	// would restore it at the sites the refusal was put there to protect. A caller
	// reaching one of those helpers directly rather than through the entry therefore
	// supplies a configuration as well, an explicitly empty one where that is what
	// it means.
	WorkspaceConfig *workspaceconfig.Config

	// CompilerVersion is the build identity stamped into IR provenance
	// (IRGraph.Spec.Provenance.CompilerVersion). The command layer supplies the
	// binary's version string — the pipeline pipes it through to buildProvenance
	// without deriving it, so a build-from-source and a released build stamp
	// distinct identities while the value stays injectable for deterministic
	// tests. Empty when a caller does not stamp provenance (the field carries no
	// default; an unset value renders as an empty compiler-version).
	CompilerVersion string

	// Lockfile and WorkspaceRoot, together with WorkspaceConfig's resolved
	// cache directory, enable build-time source-artifact resolution: when
	// Lockfile is non-nil and WorkspaceRoot is set, RunPipeline populates the
	// renderer's source-resolution inputs so a remote Recipe (or FeatureRecipe)
	// source resolves to its lockfile-pinned, content-addressed cache entry and
	// renders. When Lockfile is nil or WorkspaceRoot is empty, the renderer
	// forwards each source verbatim — the sourceless and local-path paths.
	//
	// WorkspaceRoot is also what ReleasePath is rendered against, so a run
	// supplying no root names the Release by its path unchanged.
	Lockfile      *lockfile.Lockfile
	WorkspaceRoot string

	// PluginPins is where this run's plugin pins come from. It is REQUIRED by both
	// exported entries that can reach the plugin resolver — the graph entry and
	// the resolve-and-validate entry — each of which refuses an unset one rather
	// than reading a zero value in its place. Both refuse, because a mandate held
	// at one of two entries is one a caller escapes by picking the other.
	//
	// A pointer, and the pointer is the whole point: a caller that has no pin
	// source and a caller that forgot to name one are different facts, and an
	// empty pin map reports the second as the first. The pins address the
	// VERSIONED cache slot a plugin's bytes live in, so a construction holding
	// none probes the unversioned slot and misses a guest that is on disk — and
	// under a pinned posture it does so while asserting that a recorded identity
	// governs what it resolves. A caller with genuinely nothing to pin against
	// says so with pluginpath.NoPins, which is a value it WRITES; the omission it
	// used to be indistinguishable from is now refused.
	//
	// It sits beside Lockfile rather than being derived from it because the two
	// answer different questions. Three other readers need the record itself —
	// build-time source-artifact resolution, the transform tier's cache
	// fall-through, and the reconstruction of the recorded transform attachments —
	// and none of them is a pin. A surface whose pins come from the record derives
	// them with lockfile.PluginPins and hands the result to
	// pluginpath.RecordedPins, so a record that pins nothing yet still declares
	// that pins govern, which is exactly the fact a pin-free source does not
	// state.
	//
	// Those refusals are what the read below them rests on: it carries no nil
	// test of its own, deliberately, because substituting a pin-free source for an
	// unset one would restore the plausible-but-wrong zero the refusals exist to
	// stop. A caller reaching one of those helpers directly rather than through
	// an entry therefore supplies a pin source as well, an explicitly pin-free
	// one where that is what it means.
	PluginPins *pluginpath.PinSource

	// MappingLibraries is the resolved MappingLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. It
	// flows to the renderer so a Recipe / FeatureRecipe valuesMapping's
	// bare-name `$import: <Library>#/mappings/<entry>` resolves at render. nil
	// when no MappingLibraries are present.
	MappingLibraries map[string]types.MappingLibrary

	// WorkloadLibraries is the resolved WorkloadLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. It
	// flows to the renderer so a Recipe workload's bare-name
	// `$import: <Library>#/workloads/<entry>` resolves at render. nil when no
	// WorkloadLibraries are present.
	WorkloadLibraries map[string]types.WorkloadLibrary

	// ArtifactLibraries is the resolved ArtifactLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. It flows
	// to the renderer so a Recipe / FeatureRecipe artifacts entry's bare-name
	// `artifacts.<name>: { $import: <Library>#/artifacts/<entry> }` resolves at
	// render. nil when no ArtifactLibraries are present.
	ArtifactLibraries map[string]types.ArtifactLibrary

	// RecordedPins are the content pins the load recorded for each pin-bearing
	// entity, keyed by the document that entity was read from. They flow to the
	// renderer, which compares what a build acquired against them and halts where
	// the two disagree. nil records nothing for every entity, which leaves those
	// comparisons with nothing to conclude rather than approving.
	RecordedPins recipepin.Lookup

	// TolerateCatalogViewInconsistency opts a caller out of the guard-layer
	// halt for a component type that resolved AS A CATALOG REFERENCE yet is
	// absent from the guard's catalog view (the resolved-set / view divergence
	// that ErrGuardEntityViewInconsistency reports). Default false: the
	// divergence halts loudly — the build's safety net. A resilient-inspection
	// caller working against a lazily-populated view (where a cache slot may be
	// skipped as drifted, leaving an in-closure type momentarily absent) sets
	// it true: the divergence is dropped from the halt set so the IR still
	// builds with the unavailable entity skipped, and the condition surfaces as
	// a diagnostic rather than failing the inspection. Opt-out, not opt-in, so a
	// caller that forgets the flag halts (safe) rather than silently skips.
	TolerateCatalogViewInconsistency bool

	// VerdictPluginTrust carries the digests recorded as trusted for the plugins
	// that return an accept-or-reject verdict, keyed by plugin identity. The
	// verdict sockets thread it into their cache-not-pin resolution, where a
	// recorded digest is compared against the digest the plugin's cached manifest
	// reports and a disagreement halts.
	//
	// It is an input rather than something the pipeline reads for itself because
	// reading the record is the composing surface's concern: the record sits beside
	// the Release, and the pipeline is handed a resolved Release rather than a path
	// to look beside. The composition that supplies it supplies the governance
	// attachment list in the same act, so a surface cannot acquire one without the
	// other.
	//
	// The zero value means NOTHING IS RECORDED, which is the ordinary first-run
	// state and not a pin of zero: resolution stays exactly as it is without a
	// record, governed by the manifest-derived digest alone. That is why the field
	// is supplied through one composition rather than assigned per surface — an
	// omitted assignment is indistinguishable, to every type checker and to the
	// resolution itself, from a workspace that has recorded nothing.
	VerdictPluginTrust pluginpath.PinMaps

	// PluginPathLookup resolves a pipeline step's plugin URI to its verified
	// .wasm bytes. When nil, RunPipeline binds the shared plugin-resolution
	// cascade (embed → colocated → local → cache) configured from the
	// build context — the lockfile digest pins, the workspace root + cache dir,
	// and the catalog's co-located artifact directories; every tier reads bytes
	// already on the machine. A caller may override it to point a fixture's plugin URI at a
	// built artifact. The renderer's per-node pipeline executor uses it to load
	// each step's guest; a nil lookup reaching the renderer leaves pipelines
	// validated-but-not-executed.
	PluginPathLookup pluginpath.Lookup

	// AdvisoryLog collects the exit-code-neutral advisories the phases raise, in
	// the order they raise them. It is an OUT channel carried in with the inputs,
	// which is what lets one log span phases whose signatures are otherwise
	// unrelated: the advisories are produced inside the resolve scope, after static
	// analysis, around the governance sockets and after the render, and threading a
	// parameter through every one of those would put the same value in four
	// signatures for one purpose.
	//
	// The entry points set it for themselves and surface the result — RunPipeline
	// on Result.Advisories, RunTier4Pipeline on Tier4Result.Advisories — so an
	// ordinary caller never assigns it. A caller driving a single phase directly
	// leaves it nil, which records nothing and changes no display.
	AdvisoryLog *AdvisoryLog
}

// Options carries per-invocation flags. IncludeTrace, when true, causes
// RunPipeline to construct an in-memory trace writer and populate
// Result.Trace with the collected per-pass entries; when false,
// Result.Trace stays nil and the trace writer is a no-op.
type Options struct {
	IncludeTrace bool

	// TraceUnredacted turns off the per-pass capture's withholding of
	// declared-sensitive data, and stamps every captured file with a header
	// announcing that. It is meaningful only alongside IncludeTrace: with the
	// capture off there is nothing to withhold from. Left false — the default —
	// declared-sensitive data is withheld.
	//
	// It reaches ONLY the capture surface. The key set it empties is consumed by
	// the per-pass projection and by nothing else, so the `inspect` layouts and
	// the diagnostic bodies — which reach the replacement text on their own,
	// without a key set — are unaffected in either state.
	TraceUnredacted bool

	// SkipRender, when true, stops the pipeline after Step 4b (once the IR
	// graph is finalised and Component/Feature assertions have run) and
	// returns the graph with no Artifacts. Step 5 render — the sole path that
	// loads rendering plugins — never runs, so an IR-inspection caller that
	// needs only the graph avoids the render cost entirely. The returned graph
	// is byte-identical to a full-render run's graph, because render never
	// mutates it. Left false — the common case — the pipeline renders as usual.
	SkipRender bool
	// RecipeResolutionVerifier, when set, runs after IR-build and before render
	// to assert that every attachment resolves to exactly one recipe (halting
	// loudly on a no-recipe attachment or an unmapped ambiguously-implemented
	// type). Left nil — e.g. by callers that build the IR for inspection rather
	// than render — it is skipped. Catalog supplies VerifyRecipeResolution,
	// whose signature this matches.
	//
	// It no longer answers for the component instances themselves. That axis is
	// a rule declared over the flattened composition, asked where the node set
	// is complete, because the instance list this ran over omits a component a
	// sub-aggregate contributes — so the two together cover what this alone
	// once claimed to.
	//
	// 🔴 Setting it also declares that this run EMITS something a person will
	// apply, which is a second job its name does not carry and a caller cannot
	// discover from the signature. It is the only declaration a caller of this
	// entry makes that separates a run producing artifacts from one whose whole
	// product is a picture of the same documents, so emitsSomethingToApply
	// reads it, and a rule declaring the severity that refuses only an emitting
	// run refuses on exactly the runs that set this. The bound that carries
	// with that reading is stated on emitsSomethingToApply: a second such rule
	// inherits the answer callers gave for this one, and giving it an answer of
	// its own means a declaration of its own here.
	RecipeResolutionVerifier func(*types.Manifest, *types.IRGraph) error

	// NodeProgress, when set, is invoked once per node during Step 5 render as
	// each node completes: done is the 1-based count of nodes rendered so far,
	// total is the node count of the graph, and nodeID names the node just
	// finished. It is a diagnostic hook for a progress surface; the callback must
	// write only to a diagnostic sink (never stdout) so rendered output stays
	// byte-deterministic. Left nil — the common case, and every non-CLI caller —
	// the render loop skips the call. Mirrors IncludeTrace: an opt-in per-node
	// observation point that is a no-op when unset.
	NodeProgress func(done, total int, nodeID string)

	// PluginCompileBegin, when set, turns on the eager provider-guest compilation
	// pass: before the render loop, RunPipeline force-compiles the distinct
	// provider guests the render needs, so a cold guest compilation is a discrete,
	// attributed phase rather than a freeze inside the first node's render. Begin
	// is invoked once with the number of guests that will be compiled, before the
	// first is compiled. Left nil — the common case, and every non-CLI caller —
	// the eager pass is skipped and guests compile lazily on first per-node load,
	// as before. It is a diagnostic hook for a progress surface; the callback must
	// write only to a diagnostic sink (never stdout) so rendered output stays
	// byte-deterministic, and it carries no authority over the compile.
	PluginCompileBegin func(guests int)

	// PluginCompileGuestDone, when set, is invoked after each provider guest
	// finishes compiling in the eager pass, with the running count of guests
	// compiled so far. Paired with PluginCompileBegin; nil when the eager pass is
	// off. Like PluginCompileBegin it is a diagnostic hook that must write only to
	// a diagnostic sink and cannot alter output.
	PluginCompileGuestDone func(done int)

	// PluginCompileRunning, when set, is invoked the first time a provider guest
	// in the eager pass is observed to be compiling rather than served from the
	// cache — a warm still running after a short cache-hit window. It lets a
	// progress surface reveal the compilation step only for a build that truly
	// recompiles a guest, including a build whose cache directory already existed
	// but lacked the exact guest this build needs. Paired with PluginCompileBegin;
	// nil leaves the step hidden. Like the other compile hooks it must write only
	// to a diagnostic sink and carries no authority over the compile. It may fire
	// from a timer goroutine, so the callback must be safe to call concurrently
	// with the pass.
	PluginCompileRunning func()

	// SuppressUnsupportedFieldWarnings, when true, silences the unsupported-field
	// warning class on the render-warning surface — the per-authored-field notices
	// a lower-fidelity target emits for fields it cannot honor. It matches on that
	// class's own code, so other render warnings and the other coded diagnostic
	// classes still surface. It is a pure display filter over structured
	// diagnostics carried out-of-band; it never re-renders and never changes the
	// emitted bytes. Left false — the default — the class surfaces.
	SuppressUnsupportedFieldWarnings bool
}

// emitsSomethingToApply reports whether this run produces something a person
// will apply, which is what decides whether a finding raised at
// [constraint.SeverityEmissionFatal] refuses the run or travels out of it.
//
// It is read here, once, rather than at the gate that acts on it, because the
// question is about the RUN and not about any one condition: this entry serves an
// artifact-producing surface and a surface that draws a picture of the same
// documents, and only the caller knows which it is.
//
// # Why it is read off the resolution gate, and the bound that carries
//
// The resolution gate is the one thing a caller of this entry declares that
// separates the two postures: an artifact-producing surface asks for it and a
// surface that draws a picture leaves it off, deliberately, so that a graph still
// comes back over an incomplete resolution. Reading the posture from that
// declaration is what keeps every such surface behaving exactly as it did before
// a rule was expressed at this severity.
//
// 🔴 The bound: a caller cannot yet declare emission INDEPENDENTLY of that gate.
// So a second condition expressed at this severity inherits the answer callers
// gave for recipe resolution, which is right only while the two postures coincide
// — and giving it an answer of its own means a declaration of its own here, not a
// second reading of this one.
//
// The render-skip option is deliberately NOT what is read. A surface that draws a
// picture may still run the render — the daemon's inspection does, when its
// build-on-inspect dial is on, and that dial's own contract is that turning it on
// adds the render's advisories and never a check that would refuse. Keying on
// render-skip would break exactly that.
func (o Options) emitsSomethingToApply() bool {
	return o.RecipeResolutionVerifier != nil
}

// emitRenderWarnings writes the render step's advisory warnings to w: first the
// stream no class code governs, then the class-coded diagnostics, each entry
// attributed to its node and provider from its location. Suppression is keyed on
// the diagnostic's own code and drops the unsupported-field class alone — every
// entry of the first stream and every other coded class still surface, which is
// what keeps a target-fidelity preference from silencing an unrelated advisory.
// This is a pure display pass over out-of-band diagnostics and never touches the
// rendered bytes.
//
// Both streams render through [renderer.RenderDiagnosticText], the one formatter
// every surface that displays this channel reads, so an advisory has one wording
// in a terminal and in an editor. The two are printed as two groups rather than
// as one because a display treats them differently — the first unconditionally,
// the second subject to the suppression above — and because the carrier's own
// order is this order.
func emitRenderWarnings(w io.Writer, result *renderer.RenderResult, suppressUnsupportedFields bool) {
	for _, warning := range result.Warnings {
		fmt.Fprintf(w, "  warning: %s\n", renderer.RenderDiagnosticText(warning))
	}
	for _, d := range result.Diagnostics {
		if suppressUnsupportedFields && d.Code == renderer.UnsupportedFieldCode {
			continue
		}
		fmt.Fprintf(w, "  warning: %s\n", renderer.RenderDiagnosticText(d))
	}
}

// mergeRenderDiagnostics joins the render's two advisory streams into the single
// list a caller that reads no terminal consumes, uncoded entries first and coded
// entries second.
//
// The render keeps them apart because a display treats them differently, and a
// caller reading the result needs that distinction for nothing — so merging here
// is what stops every such caller either re-implementing the join or reading one
// stream and silently missing the other.
//
// The ORDER is the contract, and it is the display's order: whoever compares what
// a build printed against what a build returned must find the same entries in the
// same sequence. That is why this is a named function rather than two appends at
// the call site — an order with a reason needs somewhere for the reason to live
// and something to assert against.
//
// The result is a fresh slice. Appending onto either input would be free to write
// into a backing array the render still holds, and the render's result outlives
// this call.
func mergeRenderDiagnostics(result *renderer.RenderResult) []renderer.Diagnostic {
	merged := make([]renderer.Diagnostic, 0, len(result.Warnings)+len(result.Diagnostics))
	merged = append(merged, result.Warnings...)
	merged = append(merged, result.Diagnostics...)
	return merged
}

// guestWarmer is the compile-only capability the eager plugin-compilation pass
// drives on a provider engine that renders through a Wasm guest: it compiles the
// guest into the shared compilation cache without rendering a node, so the render
// loop's per-node loads are cache hits. An engine that does not implement it is
// skipped by the pass.
type guestWarmer interface {
	Warm(ctx context.Context) error
}

// Result carries the pipeline's outputs. Artifacts is the rendered file
// map the CLI wrapper writes to dist/. IRGraph is the step-3 output the
// daemon's irGraph operation returns directly. Trace is the per-pass
// intermediate state, empty unless Options.IncludeTrace is true.
type Result struct {
	Artifacts map[string][]byte
	IRGraph   *types.IRGraph
	Trace     map[string][]byte
	// Advisories carries EVERY exit-code-neutral advisory the run raised — the
	// binding-lint findings, the static-analysis advisory stream, the governance
	// sockets' warn and info verdicts, and the render's own warnings — so a caller
	// that reads no terminal receives all of them as structured entries rather
	// than depending on a redirect of the process diagnostic stream. Out-of-band
	// in every case: no entry affects Artifacts, and no entry moves a verdict.
	//
	// This is ONE channel and deliberately not one field per class. A second
	// advisory field beside this one would leave every consuming surface deciding
	// which to read, and the two would disagree the first time an advisory moved
	// between them. A consumer that wants to tell the classes apart reads each
	// entry's own severity and code.
	//
	// # The order is the display's order, and it is a contract
	//
	// Entries arrive in the sequence a terminal prints them, because each is
	// recorded beside the display that shows it rather than assembled afterwards.
	// Whoever compares what a run printed against what it returned finds the same
	// entries in the same sequence.
	//
	// # What an entry carries, and what it deliberately does not
	//
	// An entry carries a class code where its raising site had one and none where
	// it did not, so a consumer matching on code matches the classes that exist
	// rather than a code invented at this boundary. An entry carries a LOCATION
	// where its producer had one to carry: a finding about an authored document
	// carries the path into that document, leaving the position to be recovered by
	// the surface holding the parsed file, and a finding about a graph coordinate
	// carries the node it names. An entry whose producer has neither carries no
	// location at all, and a surface that must show one places it at the head of
	// the file.
	//
	// A trace-file close failure is NOT here, and that is deliberate rather than an
	// omission: it reports that a side effect the caller asked for did not
	// complete, which is housekeeping rather than a finding about the author's
	// Release. An advisory list an author reads must not fill with entries they
	// cannot act on.
	Advisories []renderer.Diagnostic
}

// Outcome is what a run hands back: its payload together with the verdict on
// whether the run finished. The payload is unexported, so a consumer reaches it
// only through the accessors below and cannot read it without having said which
// verdict it is reading under.
//
// # Why the payload is not a field
//
// A run that stops partway still holds findings worth reporting, so the
// advisories have to survive a failure. Handing back the whole payload beside
// the error would make a partial one indistinguishable from a finished one at
// every call site, and a site that branched on the value rather than on the
// error would read a half-built run as a complete one — writing an empty
// artifact set, or dereferencing a graph the run never reached. Naming the
// accessor is what closes that: there is no spelling that yields the payload
// without stating the verdict, and no second return value to discard.
//
// # The zero value is the safe one
//
// An Outcome nobody filled reports no completed payload and no advisories, so a
// caller that constructs one and forgets to set it is refused rather than
// trusted. The unsafe default — a value whose zero state reads as "finished" —
// is unrepresentable here.
type Outcome struct {
	// result is what the run produced. On a finished run it is the whole
	// payload; on a run that stopped it carries the findings the phases that DID
	// run raised, and nothing else, because nothing else is reachable from a
	// partial and dead data on a partial is worse than none.
	result *Result

	// finished records that the run reached its end. Only a finished run's
	// payload is handed out as a Result, because artifacts and a graph are
	// meaningful only from a run that produced them.
	finished bool

	// reachedRender records that the run began its render step, whether or not
	// it finished it.
	reachedRender bool
}

// ReachedRender reports whether the run began its render step — the step that
// loads the rendering plugins and renders each node — whether or not it finished
// it. A run that stopped ahead of the render (in the composition of its graph, or
// at the assertions a Component or a Feature declares) reports false, and so does
// a run told to skip the render. It is how a surface reporting a refusal says
// truthfully whether the refusal was raised while rendering, without reading the
// refusal's text. The zero Outcome reports false.
func (o Outcome) ReachedRender() bool {
	return o.reachedRender
}

// Completed returns the finished payload, or nil when the run stopped before it
// ended. Artifacts, the IR graph and the trace are read from here and nowhere
// else, so a run that produced none cannot supply them.
func (o Outcome) Completed() *Result {
	if !o.finished {
		return nil
	}
	return o.result
}

// Advisories returns every exit-code-neutral advisory the run raised, whether or
// not it finished. It is the one thing a run that stopped still reports in full:
// the phases ahead of the halt found what they found, and a reader who loses
// those findings has to re-run to see them.
//
// The order is the order a terminal printed them, for the reason
// [Result.Advisories] gives. A finished run's entries are the same slice that
// field carries; there is one accumulation and this is the route to it that does
// not depend on the verdict.
func (o Outcome) Advisories() []renderer.Diagnostic {
	if o.result == nil {
		return nil
	}
	return o.result.Advisories
}

// Tier4Result carries the outputs of Steps 1-4 of the compiler pipeline,
// produced by RunTier4Pipeline. The daemon's runTier4Validation consumes
// this shape to surface structured per-violation diagnostics through
// aggrega/validate. IRGraph is populated when Step 3 ran; Analysis is
// populated when Step 4 ran. Earlier phases that produced fatal errors
// leave later phases skipped (the returned error is non-nil), but the
// partial results from the phases that ran are still surfaced so the
// caller can construct diagnostics from whichever phases completed.
//
// ResolveDiagnostics carries the per-violation slice extracted from the
// resolver's error return, followed by the findings a rule raised at the
// severity that refuses only a run emitting something a person will apply.
// The extraction is populated for both single-violation and
// multi-violation cases: single-violation wraps the embedded
// *ResolverError as a one-element slice; multi-violation pulls the
// preserved Violations slice from MultiResolverError. Tier-4
// position-recovery walks the slice with a uniform code path regardless of
// cardinality.
//
// 🔴 A populated slice does NOT imply a failing run, and reading it as one is
// the mistake this sentence exists to prevent. This entry emits nothing, so a
// finding of that severity is reported here and returns no error — which is
// exactly what makes a validation surface able to describe a composition it
// would refuse to build from. The run's verdict is the returned error and
// never the length of this slice.
//
// Advisories carries the exit-code-neutral advisories the phases raised, in the
// order they raised them, and never contributes to a verdict: a caller renders
// them as warnings and proceeds. It is the SAME channel and the same element type
// RunPipeline returns on Result.Advisories, so a surface that consumes advisories
// from a validation and from a build reads one shape rather than two.
//
// An advisory about an authored document carries the path into that document on
// its location, so the position is recovered by the surface holding the parsed
// file — the same recovery ResolveDiagnostics gets, which is what keeps an
// advisory on its authoring key rather than at the head of the file.
type Tier4Result struct {
	IRGraph            *types.IRGraph
	Analysis           *analyzer.AnalysisResult
	ResolveDiagnostics []*resolver.ResolverError
	Advisories         []renderer.Diagnostic
}

// stepsOneToFourOutput bundles the outputs runStepsOneToFour produces inside
// the resolve scope. The scope is owned by resolver.ResolveAndUse (which
// runStepsOneToFour routes through) and closes only after the consumer's
// continuation — RunPipeline's render or RunTier4Pipeline's extraction —
// returns, so the outputs, the merged parameter map among them, are valid for
// the whole continuation.
type stepsOneToFourOutput struct {
	// GraphScope is the scope the graph construction produced, embedded rather
	// than copied field by field: every product this bundle adds to is one of
	// that scope's, so re-declaring them here would be a second set of fields
	// holding one set of values, free to be filled from somewhere else.
	//
	// Among them are the per-attachment-leaf sites, the origins paired with
	// each connection's authored delivery, the presentation of the finalised
	// graph and the enumeration of it — each carried alongside the graph
	// because the graph itself cannot answer what they answer.
	*GraphScope

	Analysis *analyzer.AnalysisResult

	// AssertionErr carries the first failing Component or Feature assertion, or
	// nil when every assertion passed. It is produced inside runStepsOneToFour
	// rather than by each consumer, because the rule's inputs — the finalised IR
	// graph and the catalog — are complete once the graph is built, so every
	// caller of Steps 1-4 is entitled to the verdict. What differs per caller is
	// only how the failure is surfaced: the render path wraps it as a fatal halt,
	// the validation path returns it as the run error alongside the graph it
	// already captured.
	AssertionErr error
}

// runStepsOneToFour executes the pre-render phases of the compiler pipeline
// (resolve, topology, IR finalisation, analyse, and Component / Feature
// assertion evaluation) and invokes use with the phase outputs while still
// inside the resolve scope. It routes through the graph entry →
// resolver.ResolveAndUse, so use runs while the parameter map is live and the
// resolved view cannot outlive the scope. Used by both RunTier4Pipeline
// (which renders nothing) and RunPipeline (which renders from the same outputs).
//
// Every check whose inputs are complete before render runs before use, either
// in the graph entry or here, rather than on one consumer's path. That is what
// makes the two entry points agree: a rule placed on the render path alone
// would let a validation surface report a clean verdict on a document a build
// rejects.
//
// Behavior on phase failure: a resolve, topology or IR failure returns the error
// without invoking use. An analyzer error is non-fatal — the structured
// AnalysisResult.Errors slice rides on out.Analysis and use IS invoked, so
// callers decide whether to treat analyzer errors as fatal (RunPipeline joins
// them) or as diagnostics (RunTier4Pipeline). This preserves the daemon's
// Tier-4 contract. An assertion failure likewise rides on out.AssertionErr
// rather than aborting the callback, so a caller still receives the graph.
func runStepsOneToFour(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
	subAggregateLoader topology.SubAggregateLoader,
	use func(*stepsOneToFourOutput) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Steps 1-3: resolve, topology flatten, label transform, graph build, and
	// the rules declared over the finalised graph — through the graph entry,
	// the one place that produces a graph. The release inspection and `aggrega
	// pull`'s per-component source resolution reach it too, so what those
	// surfaces hold is the graph a build would render rather than one assembled
	// beside it.
	//
	// The collecting entry is the one taken here, because this path has a
	// reporting channel of its own: what the rules raised joins the same
	// violation slice the governance verdicts below join, so one run reports
	// both rather than stopping at whichever came first.
	return buildGraphCollectingFindings(ctx, inputs, catalogLookup, recipeFor, subAggregateLoader, func(gs *GraphScope) error {
		graph := gs.IRGraph

		out := &stepsOneToFourOutput{GraphScope: gs}

		if err := ctx.Err(); err != nil {
			return err
		}

		// Every workspace provider binding is judged here, whether or not a Recipe
		// in this Release asks for its capability — a binding nothing consults is
		// otherwise read by nothing. Here rather than on one entry's path, so a
		// build and a validation of the same documents report a binding alike; and
		// below the graph entry's refusal of an absent configuration, which the
		// judgement reads.
		if err := judgeProviderBindings(inputs, catalogLookup); err != nil {
			return err
		}

		// Step 4: Analyze. Analyser errors surface as structured
		// AnalysisResult.Errors; the function-level error stays nil for
		// analyzer-only failures so Tier-4 callers can consume them as
		// diagnostics. Build / irgraph callers convert AnalysisResult.Errors
		// to the joined-error shape via RunPipeline's wrapper logic.
		analysis, err := analyzer.AnalyzeContext(ctx, graph)
		if err != nil {
			return err
		}
		out.Analysis = analysis

		// What the rules declared over the finalised graph raised, folded onto
		// the two streams this path reports on: a violation joins the slice the
		// build gates on and the Tier-4 surface reads as a diagnostic, and an
		// advisory joins the warning stream displayed below. The fold is here
		// rather than at the raise because which stream a finding belongs on is
		// a property of this path's reporting rather than of the rule, and a
		// surface with no such streams is handed the findings themselves.
		analysis.Errors = append(analysis.Errors, analyzer.ErrorsFromObjections(gs.Violations)...)
		analysis.Warnings = append(analysis.Warnings, analyzer.AdvisoriesFromObjections(gs.Advisories)...)

		// Step 4b: ir-verdict governance role. Config-driven off the ir-verdict
		// attachments in WorkspaceConfig.Plugins.Governance — a no-op when
		// unconfigured. When configured, each plugin evaluates the full IR graph
		// with its declared-sensitive leaves withheld, and its failed verdicts
		// append to analysis.Errors, so a verdict rejection halts the build through
		// the same gate selector/appliesTo violations use (RunPipeline joins
		// Errors; the Tier-4 path surfaces them as diagnostics). A verdict role
		// never mutates the graph, and the withholding works on a copy, so the
		// graph read below is the one the analysis was handed.
		if err := runPolicySocket(ctx, inputs, catalogLookup, analysis); err != nil {
			return err
		}

		// Component / Feature assertions. The rule reads the finalised IR graph and
		// the catalog and nothing else, so it belongs here — shared by every caller
		// of Steps 1-4 — rather than on one consumer's path, where a validation
		// surface would report a clean verdict on a document the render path
		// rejects. Skipped when an earlier violation already exists, so the
		// reported set is the earliest failing class rather than a mixture.
		if len(analysis.Errors) == 0 {
			out.AssertionErr = renderer.EvaluateComponentAndFeatureAssertions(graph, catalogLookup)
		}

		// The advisory stream static analysis produced: displayed, and recorded on
		// the one advisory channel in the same loop so the recorded order is the
		// displayed order by construction.
		//
		// The stream already carries the channel's own element type, so an entry is
		// recorded as it arrived and nothing is converted here. The display writes
		// the entry's message and nothing else, which is what keeps the printed line
		// byte-identical whatever else the entry carries: a producer that computed an
		// authoring key puts it on the entry's location, where a surface holding the
		// parsed document reads it, and leaves the message alone.
		for _, w := range analysis.Warnings {
			fmt.Fprintf(os.Stderr, "  %s\n", w.Message)
			inputs.AdvisoryLog.record(w)
		}
		return use(out)
	})
}

// wrapAssertionErr gives a Component / Feature assertion failure its user-facing
// shape. Single-sourced so the render path and the validation path present one
// failure identically — a diagnostic that reads differently depending on which
// command surfaced it would suggest two different rules.
func wrapAssertionErr(err error) error {
	if err == nil {
		return nil
	}
	return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, "Component/Feature assertions")
}

// dropCatalogViewInconsistencies returns the guard-violation slice with every
// ErrGuardEntityViewInconsistency finding removed, preserving order. It backs
// Inputs.TolerateCatalogViewInconsistency: a resilient-inspection caller drops
// the resolved-set / view divergence from the halt set while every other guard
// violation still halts.
func dropCatalogViewInconsistencies(errs []*resolver.ResolverError) []*resolver.ResolverError {
	kept := errs[:0:0]
	for _, e := range errs {
		if stderrors.Is(e, resolver.ErrGuardEntityViewInconsistency()) {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// ResolveAndValidate runs Step 1 of the compiler pipeline — resolve (parameter
// interpolation) + annotation binding validation + expansion + the full guard
// suite — against caller-supplied inputs, then invokes use with the resolved
// per-component view (ResolveResult carrying per-component BoundaryInfra)
// WITHOUT flattening topology. It is the single source of the build's
// pre-topology validation: resolveAndFlatten runs it and then flattens, so what a
// build validates before topology has one implementation rather than two. The
// guard suite operates on the resolved (post-parameter-interpolation) aggregate,
// so templated `{{ .parameters.* }}` values are validated as resolved.
//
// The continuation runs inside resolver.ResolveAndUse's scope, so the resolved
// view is live throughout it and reachable nowhere else. A resolve, annotation,
// or guard failure returns the error without invoking use.
//
// recipeFor is the component-instance-to-Recipe binding the caller will render
// with. It is read by the advisory suite below and by nothing else here, so a
// caller with no binding to offer passes nil and the one rule keyed inside a bound
// Recipe is asked about nothing while every other check is unaffected.
func ResolveAndValidate(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
	use func(*resolver.ResolveResult) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The same refusal the graph entry makes, made here for the same reason: this
	// is the other entry a caller can reach the plugin resolver through, by way of
	// the annotation-transform role below. The pins address the versioned cache
	// slot a plugin's bytes live in, so a run that names no pin source resolves
	// against an empty address and misses a guest that is on disk — while a pinned
	// posture over that emptiness states the opposite, that a recorded identity
	// governs here. A caller with nothing to pin against says so, and saying so is
	// what this refusal asks for. Refusing at both entries rather than at one is
	// what keeps the obligation from ending at whichever entry a caller happened to
	// pick.
	if inputs.PluginPins == nil {
		return stderrors.New("resolve: no plugin pin source was supplied; " +
			"the pins address the versioned cache slot a plugin's bytes are read from, " +
			"so a run without one resolves plugins against an empty address")
	}

	return resolver.ResolveAndUse(ctx, resolver.ResolveInputs{
		Release:           inputs.Release,
		Manifest:          inputs.Manifest,
		AggregateRawBytes: inputs.AggregateRawBytes,
		AggregatePath:     inputs.AggregatePath,
		ReleasePath:       releaseSourcePath(inputs),
		// The Release's file as the caller holds it, which is a path this process
		// opens, so a parameters import the Release names is read beside it; and
		// the workspace it is read in, which that import is held to.
		ReleaseFile:   inputs.ReleasePath,
		WorkspaceRoot: inputs.WorkspaceRoot,
	}, func(resolveResult *resolver.ResolveResult) error {
		// The annotation-transform governance role runs first in the resolve
		// continuation: it injects annotations onto resolved components before
		// binding validation and expansion, so a bound injection is validated and
		// expanded into a Feature attachment with no re-run. Unconfigured, it is a
		// byte-identical no-op.
		if err := runAnnotationSocket(ctx, inputs, catalogLookup, resolveResult); err != nil {
			return err
		}
		if err := resolver.ValidateAnnotationBindingTargets(resolveResult, catalogLookup); err != nil {
			return fmt.Errorf("annotation binding validation: %w", err)
		}
		// The rules over what an author WROTE in an annotation, asked after the
		// rule over what the Manifest's bindings declare and before expansion
		// consumes either. The order is the same one the binding rule's own
		// position rests on: a broken binding is reported against the Manifest
		// first, so an annotation routed through it is not reported against the
		// document that wrote the value.
		if err := resolver.ValidateAuthoredAnnotations(
			ctx.Err, resolveResult, catalogLookup); err != nil {
			return fmt.Errorf("annotation validation: %w", err)
		}
		// The binding-overlap lint is collected, never printed here: it joins the
		// one advisory drain below, which is what carries a finding to both front
		// ends. A finding written straight to the stream reaches the terminal and
		// nothing else, so the editor never sees it and no consumer can match it.
		// APPEND for the reason stated at the drain — the resolve already carries
		// the advisories from the checks that needed only the Aggregate.
		resolveResult.LintAdvisories = append(resolveResult.LintAdvisories,
			resolver.LintAnnotationBindingMaps(resolveResult)...)
		if err := resolver.ExpandAnnotations(resolveResult, catalogLookup); err != nil {
			return fmt.Errorf("annotation expansion: %w", err)
		}

		normalized := types.Normalize(resolveResult.Aggregate)

		// The catalog-aware advisory checks run in the same scope as the halting
		// guards and read the same normalized aggregate and catalog view. This is
		// the single DRAIN both front ends consume: the build renders every
		// advisory to stderr below, the daemon maps them to editor warnings off
		// Tier4Result. They are computed before the guard verdict so an advisory is
		// not lost when an unrelated guard halts.
		//
		// The declared advisory rules are asked through RunAdvisories, which is a
		// separate entry point from the halting suite and returns no collection a
		// build can halt on. That separation is what keeps an advisory advisory:
		// were these rules asked through RunGuards, whose return IS the verdict,
		// a near miss would refuse authoring the compiler deliberately permits.
		//
		// APPEND, never assign: the resolve arrives already carrying the advisories
		// from the checks that needed only the Aggregate, and assigning here would
		// silently drop them.
		declaredAdvisories, err := resolver.RunAdvisories(
			ctx.Err, normalized, resolveResult.Release, catalogLookup, recipeFor, resolveResult.Parameters)
		if err != nil {
			return err
		}
		resolveResult.LintAdvisories = append(resolveResult.LintAdvisories, declaredAdvisories...)
		// The drain: displayed, and recorded on the one advisory channel in the same
		// loop so the recorded order is the displayed order by construction.
		//
		// The display shows the finding's subject-bearing rendering because a
		// terminal has nowhere else to put the subject; the recorded entry carries
		// the subject-free rendering plus the subject structurally, so a surface that
		// draws the finding on its authoring key does not spell the same path twice.
		// Both renderings come from one composer in the resolver, so a condition
		// keeps one wording however many surfaces report it.
		for _, a := range resolveResult.LintAdvisories {
			fmt.Fprintf(os.Stderr, "  lint: %s\n", a)
			inputs.AdvisoryLog.record(advisoryFromResolverFinding(a, abi.SeverityWarn))
		}

		// ctx.Err is the cancellation signal the rule walks read once per
		// component and once per declared scope: a caller that has given up on
		// this build stops paying for the rest of it, and a walk that stops hands
		// back nothing rather than a partial set that would read as a document
		// satisfying rules nobody asked.
		guardErrs, err := resolver.RunGuards(ctx.Err, normalized, resolveResult.Release, resolveResult.ReleasePath, catalogLookup, resolveResult.BoundaryInfra, resolveResult.Parameters)
		if err != nil {
			return err
		}
		if inputs.TolerateCatalogViewInconsistency {
			// Resilient inspection against a lazily-populated view: a resolved
			// catalog reference momentarily absent from the view (a drifted cache
			// slot skipped on read) is a drift signal, not an authoring fault.
			// Drop it from the halt set so the IR still builds with the entity
			// skipped; the condition surfaces as a diagnostic. Every other guard
			// violation still halts.
			guardErrs = dropCatalogViewInconsistencies(guardErrs)
		}
		if len(guardErrs) > 0 {
			for _, e := range guardErrs {
				fmt.Fprintf(os.Stderr, "  guard: %s\n", e)
			}
			// Carry the per-violation slice on the returned error so a caller can
			// errors.Is the specific guard sentinel at the pipeline boundary
			// (parity with the analyzer path, which joins). The summary text is
			// unchanged: the rendered summary reads "resolution: N guard
			// violation(s)".
			return &resolver.MultiResolverError{
				Entity:     "resolution",
				Summary:    fmt.Sprintf("%d guard violation(s)", len(guardErrs)),
				Violations: guardErrs,
			}
		}
		return use(resolveResult)
	})
}

// releaseSourcePath is the Release's path as everything this run produces names
// it: the path the caller supplied, rendered against the workspace root by
// catalog.RelativeToWorkspace.
//
// It is the one place a run puts the Release's path into that form, and it is
// read at the one site the path enters the compilation, so the form cannot depend
// on which surface ran the pipeline. Surfaces hold the path differently — an
// absolute path where a request or a core resolved one, the path as typed where a
// command was given one — and leaving each to convert would let one of them hand
// the graph a machine-specific path while the others did not, which is the
// difference the graph's byte-identity across machines rules out.
func releaseSourcePath(in Inputs) string {
	return catalog.RelativeToWorkspace(in.WorkspaceRoot, in.ReleasePath)
}

// resolveAndFlatten runs Steps 1-2 of the compiler pipeline — resolve
// (parameter interpolation, annotation binding validation + expansion, guards)
// then topology flatten — and invokes use with the resolved per-component view
// (ResolveResult carrying per-component BoundaryInfra) and the flattened
// TopologyResult (nodes with InstanceID / ComponentType / Values).
//
// It is the single source of the resolve+flatten wiring, and it is unexported
// so that it stays one. A flattened topology is the input a graph build takes,
// so a caller holding one is one short step from assembling a graph of its own
// — which is how two command-layer surfaces came to build graphs outside the
// sequence the pipeline runs around that step. BuildGraph is the exported
// continuation of this function and the only way to reach a graph from outside
// this package; keeping this one private is what leaves no second route to the
// same place.
//
// The continuation runs inside resolver.ResolveAndUse's scope (threaded through
// ResolveAndValidate), so the resolved view is live throughout it; a resolve,
// guard, or topology failure returns the error without invoking use.
func resolveAndFlatten(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
	subAggregateLoader topology.SubAggregateLoader,
	use func(*resolver.ResolveResult, *topology.TopologyResult, []*resolver.ResolverError) error,
) error {
	return ResolveAndValidate(ctx, inputs, catalogLookup, recipeFor, func(resolveResult *resolver.ResolveResult) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		// The root-tier node source stamp defaults to AggregatePath but may be
		// supplied separately (RootAggregateFile) when a caller's annotation-source
		// label and node-source label need different path representations.
		rootAggregateFile := inputs.RootAggregateFile
		if rootAggregateFile == "" {
			rootAggregateFile = inputs.AggregatePath
		}
		topoResult, err := topology.BuildContext(ctx, topology.BuildConfig{
			ResolveResult:     resolveResult,
			CatalogLookup:     catalogLookup,
			LoadSubAggregate:  subAggregateLoader,
			RootAggregateFile: rootAggregateFile,
		})
		if err != nil {
			if inputs.TolerateCatalogViewInconsistency {
				// The sub-aggregate guard reaches the same shared uses-socket
				// validator as Step 1, so the view-inconsistency class can surface
				// here too. Apply the same tolerance: if every Step-2 violation is
				// a catalog-view inconsistency, suppress the halt and proceed with
				// the graph BuildContext built up to the guard (uses-conformance for
				// the unavailable node skipped). A remaining real violation halts.
				var multi *resolver.MultiResolverError
				if stderrors.As(err, &multi) && len(dropCatalogViewInconsistencies(multi.Violations)) == 0 {
					// The composition stopped inside the guard, so the rules over it
					// were never asked and there is nothing for a later entry to carry.
					return use(resolveResult, topoResult, nil)
				}
			}
			return fmt.Errorf("topology construction: %w", err)
		}

		// The rules declared over the flattened composition are invoked here,
		// against the presentation and the enumeration that stage produced and
		// carries. This is the site that holds both, and it sits beside the
		// flattening rather than inside it so that the package producing the
		// composition does not have to reach into the package a rule over it is
		// declared in.
		//
		// It runs for every caller of this scope rather than on the build path
		// alone, which is the same reason the checks before the render sit in one
		// place: a rule asked on one consumer's path would let another report a
		// clean verdict on a document the build rejects.
		//
		// What a rule declaring the emission severity raised is handed on rather
		// than halting here. This site is inside the one entry every surface
		// reaches a graph through, so refusing here would refuse an inspection and
		// an acquisition exactly as it refuses a build — which is the distinction
		// that severity exists to keep.
		carried, err := runTopologyStageRules(ctx, topoResult)
		if err != nil {
			return err
		}

		return use(resolveResult, topoResult, carried)
	})
}

// GraphScope carries the products of one graph construction. Every field is
// valid for the duration of the callback BuildGraph hands it to and no longer:
// the resolved view — and with it the merged parameter map — is owned by
// resolver.ResolveAndUse's scope, which closes when that callback returns.
//
// It is a struct rather than a parameter list because the products are
// co-derived and a consumer that wants one of them almost always wants
// another. Handing them over one at a time invites a caller to keep the one it
// was given and re-derive the rest, which is the second answer this whole entry
// exists to prevent.
type GraphScope struct {
	// ResolveResult is the resolved per-component view, carrying per-component
	// BoundaryInfra and the post-cascade boundary annotations.
	ResolveResult *resolver.ResolveResult

	// Topology is the flattened composition the graph was built from: nodes
	// with InstanceID / ComponentType / Values, in author order.
	Topology *topology.TopologyResult

	// IRGraph is the finalised graph.
	IRGraph *types.IRGraph

	// FeatureOrder is the per-node Feature execution order the graph build
	// derived alongside the graph.
	FeatureOrder ir.FeatureOrder

	// AttachmentSites is the record of where the cascade's contributions were
	// written: per attachment leaf, which scope's contribution won that leaf; and
	// per attachment, every place that contributed it. Both are settled while the
	// cascade folded.
	AttachmentSites ir.AttachmentSites

	// OriginatedConnections pairs every connection the composition drew with the
	// surface that drew it and with the delivery that connection dictates as
	// authored. It is carried alongside the graph because the graph cannot
	// answer either question: the graph records which attachment instance drew
	// a connection and never which surface declared that attachment, and it
	// stores a delivery as one string per delivery key, so the authored
	// distinction between a literal and a reference to a published key is gone
	// from it.
	OriginatedConnections *ir.OriginatedConnections

	// GraphView presents the finalised graph to a check as the named facts
	// complete once the whole attachment cascade and the annotation-driven
	// expansion have run, with every fact complete earlier answered by the
	// presentation the flattening produced.
	//
	// It is built here because this is the one place holding both products at
	// once: a presentation of a stage embeds the presentation of the stage below
	// it rather than re-reading that stage's own inputs, so it can only be built
	// where both are in hand. A rule placed at this stage reads it; a rule placed
	// later reads a presentation that embeds it in turn.
	//
	// Nil when either product is absent.
	GraphView constraint.GraphView

	// GraphCoordinates is the enumeration the finalised graph carries: the node
	// set, the connections, each node's merged attachment set, and the axes a
	// rule at this stage is invoked once per.
	//
	// It is built here for the reason the presentation beside it is, and it is
	// carried rather than rebuilt by each consumer: the rules at this stage and
	// the rules over what the render's plugins report are invoked over the same
	// node and attachment axes, and a second derivation of either would be free
	// to disagree with the one the earlier rules were invoked over.
	//
	// Nil when either product the enumeration reads is absent.
	GraphCoordinates *ir.GraphStageCoordinates

	// Violations holds every finding the rules declared over the finalised
	// graph raised at halting severity, in the order they were raised, and
	// Advisories holds the ones they raised at advisory severity.
	//
	// They are slices on the scope rather than a returned error because a rule
	// raising is not a reason to stop asking the others: the suite walks its
	// whole axis and every rule in it, so one offending connection costs a
	// document neither the rest of that rule's axis nor anything the rules
	// beside it would have reported. A surface that gates on findings reads the
	// first slice; a surface that surfaces warnings reads the second.
	Violations []constraint.Objection
	Advisories []constraint.Objection

	// EmissionViolations holds the findings raised at the severity that refuses a
	// run emitting something a person will apply, already converted to the shape
	// this compiler's positioned diagnostics take.
	//
	// It is a third slice rather than a flag on the first, because the two answer
	// different questions and a reader of either must not have to consult the
	// other: what is in Violations refuses whoever built this graph, and what is
	// here refuses only a caller that says its own run emits. Folding them would
	// make the first slice's meaning depend on a value beside it.
	//
	// It carries converted findings where the two slices above carry raw
	// objections, and the reason is the document each finding is attributed to: a
	// rule's findings name the authoring surface whose author fixes them, which is
	// known at the site that invoked the rule and nowhere afterwards. Converting
	// at the invocation is what lets a mapping rule report against the Manifest
	// while the rules beside it report against the Aggregate, and a slice of raw
	// objections would have lost that by the time anything read it.
	//
	// 🔴 A caller that carries this slice and never reads it reports the finding to
	// NOBODY. That is the honest state of the two entries that produce a graph for
	// a caller which renders nothing: they have no finding channel, so the
	// objection reaches them and stops. Stated because a slice named on a scope
	// reads as one somebody surfaces.
	EmissionViolations []*resolver.ResolverError
}

// BuildGraph is the one entry that produces a compiler graph, and every surface
// that needs one goes through it — the render pipeline, the release inspection,
// and the artifact acquisition alike.
//
// It exists because there was no chokepoint. Resolve, flatten and graph build
// were three separately reachable steps, so each surface assembled its own
// sequence and each assembly was free to differ: two of them stamped the
// compiler version from their own command layer while the pipeline read it off
// its inputs, and neither ran the label-transform role that sits between the
// flatten and the build. Nothing failed, because every assembly produced a
// graph; what differed was what the graph said. Routing them all through one
// function is what makes "which sequence produced this graph" have one answer.
//
// The sequence, and why it is here rather than at each caller:
//
//   - resolve and flatten, which runs the guard suite and the rules declared
//     over the flattened composition;
//   - the label-transform governance role, which sits between the flatten and
//     the build precisely so its tags reach the graph. With a workspace that
//     attaches no such plugin it returns before resolving anything, so an
//     unattached build is byte-identical to a build without the role;
//   - the graph build itself.
//
// # A caller that supplied no workspace configuration is refused
//
// The transform roles above read their attachment lists off the inputs'
// workspace configuration, so a caller that never supplied one gets them
// skipped — silently, and producing a graph no build of the same documents
// would produce. That is refused here rather than tolerated: the attachments
// are the caller's to supply, so the data stays per-caller, but the obligation
// to supply them is checked once, at the one entry every graph goes through.
//
// use is invoked with the products while the resolve scope is still open, so a
// consumer reads the parameter map and the resolved view live. Nothing here
// escapes that scope; a caller that needs a product afterwards copies what it
// needs out of the callback.
//
// A resolve, guard, topology, transform or build failure returns the error
// without invoking use.
//
// # A graph the declared rules reject is not handed over
//
// The rules declared over the finalised graph are asked here, and a graph any
// of them rejected is refused before use sees it. That is what makes this entry
// worth going through for a surface that renders nothing: an inspection that
// drew the graph and published it, or an acquisition that pinned it and wrote a
// lockfile, would be publishing a composition a build of the same documents
// rejects.
//
// The whole suite runs before the refusal, so the refusal names every finding
// rather than the first: a caller fixing one authoring mistake is shown the
// others in the same run.
//
// A surface with a reporting channel of its own takes
// [buildGraphCollectingFindings] instead and surfaces the findings on that
// channel, alongside whatever else it found.
//
// # What is carried rather than refused, and what that costs here
//
// A finding raised at the severity that refuses only a run emitting something a
// person will apply is NOT refused here, and this entry is the reason that
// severity exists: it is the one entry an inspection, an acquisition and a build
// all reach, so refusing here would refuse the first two for a fault that only
// makes the third's output wrong. Such findings arrive on
// [GraphScope.EmissionViolations] for a caller that knows what its own run is.
//
// 🔴 This entry itself reads them nowhere, so a surface that stops here is told
// nothing about them. That is what the two surfaces taking this entry already do
// with the conditions concerned, and it is stated rather than left to be
// inferred from a slice being present on the scope.
func BuildGraph(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
	subAggregateLoader topology.SubAggregateLoader,
	use func(*GraphScope) error,
) error {
	return buildGraphCollectingFindings(ctx, inputs, catalogLookup, recipeFor, subAggregateLoader,
		func(gs *GraphScope) error {
			if err := joinedAnalysisHalt(analyzer.ErrorsFromObjections(gs.Violations)); err != nil {
				return err
			}
			return use(gs)
		})
}

// buildGraphCollectingFindings is [BuildGraph] for a caller that reports the
// findings itself: it asks the same rules and hands the scope over with what
// they raised on it, rather than refusing.
//
// The two exist separately because the refusal is the only thing that differs,
// and it is a caller's property rather than the graph's. A surface that renders
// nothing has no channel of its own to report on, so a finding it was handed
// would have nowhere to go and the offending document would pass; a surface
// that already joins violations from several sources into one verdict has one,
// and refusing here would cost it the findings its other sources were about to
// contribute to the same run.
//
// Everything else — the sequence, the scope the products are valid for, and the
// failure behaviour — is [BuildGraph]'s, because this is the body it delegates
// to.
func buildGraphCollectingFindings(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
	subAggregateLoader topology.SubAggregateLoader,
	use func(*GraphScope) error,
) error {
	// Before anything resolves. The roles that shape the graph read their
	// attachment lists off the workspace configuration and treat an empty list as
	// "nothing attached", so an absent configuration and an empty one produce the
	// same run — and only one of them is a run anybody asked for. Refusing here
	// rather than at each caller is what makes a caller added later inherit the
	// obligation instead of having to remember it.
	if inputs.WorkspaceConfig == nil {
		return stderrors.New("graph build: no workspace configuration was supplied; " +
			"the label-transform and annotation-transform roles are read from it, so a " +
			"graph built without one silently skips them")
	}
	// The same shape one field over, and refused for the same reason. The pins
	// address the versioned cache slot a plugin's bytes live in, so a run that
	// names no pin source resolves against an empty address and misses a guest
	// that is on disk — while a pinned posture over that emptiness states the
	// opposite, that a recorded identity governs here. A caller with nothing to
	// pin against says so, and saying so is what this refusal asks for.
	if inputs.PluginPins == nil {
		return stderrors.New("graph build: no plugin pin source was supplied; " +
			"the pins address the versioned cache slot a plugin's bytes are read from, " +
			"so a graph built without one resolves plugins against an empty address")
	}
	return resolveAndFlatten(ctx, inputs, catalogLookup, recipeFor, subAggregateLoader,
		func(
			resolveResult *resolver.ResolveResult,
			topoResult *topology.TopologyResult,
			carried []*resolver.ResolverError,
		) error {
			// Step 2b: label-transform governance role. Config-driven off the
			// label-transform attachments in WorkspaceConfig.Plugins.Governance — a
			// no-op when the workspace attaches none, so an unattached build is
			// byte-identical to a build without the role. When configured, each
			// plugin auto-tags node labels via JSON Patch (the narrow mutation
			// exception); the tags flow into the graph because the build below reads
			// its labels from this same topoResult.
			if err := runMutatorSocket(ctx, inputs, catalogLookup, topoResult); err != nil {
				return err
			}

			// Step 3: IR.
			graph, featureOrder, attachmentSites, err := ir.BuildIRContext(ctx, ir.BuildIRConfig{
				Topology:      topoResult,
				ResolveResult: resolveResult,
				CatalogLookup: catalogLookup,
				// Read off the inputs rather than taken as a parameter of its own:
				// the stamp is a property of the build being run, and a surface that
				// supplied it separately from the rest of its inputs is a surface
				// that can supply a different one.
				CompilerVersion: inputs.CompilerVersion,
			})
			if err != nil {
				return fmt.Errorf("IR graph construction: %w", err)
			}

			gs := &GraphScope{
				ResolveResult:   resolveResult,
				Topology:        topoResult,
				IRGraph:         graph,
				FeatureOrder:    featureOrder,
				AttachmentSites: attachmentSites,
				// What the rules over the flattened composition raised at the
				// severity that refuses only an emitting run, carried forward
				// unchanged. It is filled here rather than accumulated below because
				// it was produced one stage earlier, and the scope is the first thing
				// a consumer of either stage holds.
				EmissionViolations: carried,
			}

			// The origins and the authored deliveries, paired with the connection
			// set the composition produced. Built from that set rather than from the
			// authoring documents, so the connections a rule is invoked for are the
			// connections the build actually drew.
			gs.OriginatedConnections = ir.NewOriginatedConnections(graph, topoResult, resolveResult)

			// The presentation of the graph just finalised, extending the one the
			// flattening produced. Built here because this is where every product it
			// takes is in hand — the graph, the presentation below it, and the
			// per-leaf sites: a stage's presentation embeds the stage below it rather
			// than re-reading that stage's inputs, so building it anywhere else would
			// mean producing a second answer to facts this one already answers.
			gs.GraphView = ir.NewGraphView(
				graph, topoResult.StageView, gs.AttachmentSites, gs.OriginatedConnections)

			// The enumeration of the same graph, built beside the presentation
			// because it reads that presentation for the axes a fact already names.
			// The Release's file comes from the resolve result, which already
			// carries it so downstream phases can build source locations without
			// re-discovering it. The per-leaf sites come from the fold that produced
			// them rather than being recovered here: which scope's contribution won
			// any one leaf is settled while the cascade folds, and re-running that
			// precedence would be a second answer to a question the build already
			// answered.
			gs.GraphCoordinates = ir.NewGraphStageCoordinates(
				graph, gs.GraphView, catalogLookup, releaseFileOf(gs.ResolveResult),
				gs.AttachmentSites, gs.OriginatedConnections,
			)

			if err := ctx.Err(); err != nil {
				return err
			}

			// The rules declared over the finalised graph are asked here, against
			// the presentation and the enumeration built above. This is the site
			// that holds both, and it sits in the graph entry rather than on one
			// consumer's path because every surface that holds a graph holds one
			// these rules have an opinion about: a rule asked on the render path
			// alone would let an inspection publish, and an acquisition pin, a
			// composition the build of the same documents refuses.
			if err := runGraphStageRules(ctx, gs); err != nil {
				return err
			}

			return use(gs)
		})
}

// joinedAnalysisHalt renders the violations a run accumulated into the joined
// halt its callers already read, or nil when nothing was raised.
//
// It is one function rather than a loop at each halting surface because the
// shape is the contract: an existing caller matches the class of a violation
// through the joined error, so two renderings of the same slice would be two
// shapes a caller has to recognise, and the second one would be free to drift
// from the first.
func joinedAnalysisHalt(violations []analyzer.AnalysisError) error {
	if len(violations) == 0 {
		return nil
	}
	errs := make([]error, len(violations))
	for i := range violations {
		errs[i] = violations[i].ToCompilerError()
	}
	return stderrors.Join(errs...)
}

// emissionRefusal renders the findings that refuse an emitting run into the
// grouped halt this compiler's surfaces already read, or nil when none was
// raised.
//
// The group is returned even for a single finding, where the resolve's own
// aggregation hands the finding back bare. The two differ because the readers
// differ: a surface reading this one walks the violation slice to position each
// finding on its own authoring key, and a bare finding gives it nothing to walk,
// so the cardinality would decide whether the positions arrive.
//
// The summary is the per-finding lines rather than a count of them, which is what
// the field is for. A count reads correctly and is strictly worse where it
// matters: a caller that flattens an error and its causes into one line stops at
// a grouped error, because the group unwraps to a slice rather than to a single
// cause — so a summary carrying only a count drops every finding's text at the
// one surface that has nowhere else to put it.
//
// The group names no document of its own, and that is not an omission. Each
// finding names the authoring surface whose author fixes it, and the findings
// that reach this severity need not agree: a group naming one document would be
// wrong for every finding that named another, silently, at the head of the
// report.
func emissionRefusal(findings []*resolver.ResolverError) error {
	if len(findings) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d instance(s) the emitted output would be wrong for:", len(findings))
	for _, f := range findings {
		fmt.Fprintf(&b, "\n  - %s: %s", f.Field, f.MessageWithoutSubject())
	}
	return &resolver.MultiResolverError{
		Summary:    b.String(),
		Violations: findings,
	}
}

// RunTier4Pipeline executes the pre-render phases of the compiler pipeline
// (resolve, topology, IR finalisation, analyse, Component / Feature assertions)
// against caller-supplied inputs. The daemon's runTier4Validation consumes this
// to surface structured per-violation diagnostics through aggrega/validate
// without rendering, which is expensive and not needed for semantic validation.
//
// It runs every pre-render check the render path runs, because both entry points
// share one implementation of those phases. A rule reachable from one and not the
// other would make "this document validates" mean something different from "this
// document builds".
//
// Unlike RunPipeline's first-error-fatal semantic for terminal phases,
// the returned Tier4Result carries whichever phases completed even when
// a later phase produced an error. Callers consume the analyser's
// structured errors (Analysis.Errors) directly rather than the joined
// error return. The returned error is non-nil iff a phase produced a
// fatal error that blocked downstream phases; analyzer errors are NOT
// returned as the function-level error because the caller wants them
// as structured diagnostics, not opaque error text.
func RunTier4Pipeline(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
	subAggregateLoader topology.SubAggregateLoader,
) (*Tier4Result, error) {
	// The entry point owns the log, for the reason RunPipeline's does: one log per
	// run, so a caller cannot hand two runs the same accumulator.
	inputs.AdvisoryLog = &AdvisoryLog{}
	out := &Tier4Result{}
	var assertionErr error
	var emissionViolations []*resolver.ResolverError
	err := runStepsOneToFour(ctx, inputs, catalogLookup, recipeFor, subAggregateLoader, func(stepsOut *stepsOneToFourOutput) error {
		// Capture the phase outputs inside the resolve scope, which
		// resolver.ResolveAndUse closes once this returns. Tier-4 runs no
		// further work in the scope — analyzer errors ride on
		// Analysis.Errors, consumed below.
		out.IRGraph = stepsOut.IRGraph
		out.Analysis = stepsOut.Analysis
		assertionErr = stepsOut.AssertionErr
		emissionViolations = stepsOut.EmissionViolations
		return nil
	})

	// The advisories are read off the log OUTSIDE the resolve scope, and that is
	// safe where reading the phase outputs above is not: the log holds recorded
	// entries — copies of what each display showed — rather than a view the resolve
	// scope owns and closes.
	//
	// Read unconditionally, including when a phase failed. The phases that ran
	// before the failure still found what they found, and a reader who loses those
	// findings has to fix the failure and re-run to see them.
	out.Advisories = inputs.AdvisoryLog.Entries()

	// A failing Component or Feature assertion is a fatal verdict on this
	// document, so it surfaces here as the run error — the same class the render
	// path halts on, reported by a surface that never renders. The captured
	// IRGraph and Analysis stay populated: this function's contract is that the
	// phases which completed are returned even when a later one failed, and the
	// graph is final before assertions evaluate.
	if err == nil {
		err = wrapAssertionErr(assertionErr)
	}

	// Extract the structured per-violation slice into a uniform shape so
	// downstream consumers iterate ResolveDiagnostics with one code path
	// regardless of single-vs-multi-violation cardinality.
	//
	// Single-violation case: extract the embedded *ResolverError directly
	// and wrap as a one-element slice. Multi-violation case: pull the
	// preserved Violations slice from MultiResolverError. The two
	// extractions are mutually exclusive — MultiResolverError contains
	// ResolverErrors and the multi check runs first; the single check
	// never matches inside a multi.
	if err != nil {
		var multi *resolver.MultiResolverError
		var single *resolver.ResolverError
		switch {
		case stderrors.As(err, &multi):
			out.ResolveDiagnostics = multi.Violations
		case stderrors.As(err, &single):
			out.ResolveDiagnostics = []*resolver.ResolverError{single}
		}
	}

	// The findings that refuse only an emitting run join the same slice, and this
	// entry emits nothing — it produces diagnostics for a surface that draws a
	// picture of the documents. So they are surfaced rather than refused, which is
	// what that severity asks of an entry like this one, and they are surfaced on
	// the channel the rest of the per-violation findings arrive on so a consumer
	// positions all of them with one code path.
	//
	// Appended unconditionally rather than only on a failing run: this entry
	// returns no error for them, so gating the append on one would surface them
	// exactly when something ELSE had already gone wrong.
	out.ResolveDiagnostics = append(out.ResolveDiagnostics, emissionViolations...)

	return out, err
}

// buildPluginResolver constructs the hermetic build-time plugin-resolution
// cascade from the pipeline inputs: the catalog's co-located artifact
// directories, the workspace root + resolved cache directory, and the pin source
// the caller wrote onto the inputs. The cascade reads only bytes already on the
// machine — the build never fetches from a registry (AOT purity) — so an artifact
// no tier holds is a loud halt rather than a network round-trip.
//
// The pins and the posture both come from that source and are never derived here,
// so this function cannot turn a caller that supplied no source into one that
// pins nothing. Where the source is a recorded closure, its pins make every
// tier's resolved bytes verify against the recorded digest, so a drifted or
// substituted artifact halts on the pin regardless of which tier produced it, and
// a row carrying a verified signature fact also carries the recorded
// signature-bundle digest, which the resolver re-hashes the cached bundle blob
// against before the guest loads — an offline integrity gate that re-runs no
// signature verification. Where the source is pin-free, none of that is in force
// and the site has declared as much.
//
// It reads the pin source with no nil test, deliberately: both entries that can
// reach it refuse an unset one, and a test here would substitute a pin-free
// source for an unset one at exactly the site those refusals protect.
//
// colocatedDirs is the catalog's artifact-reference → catalog-
// directory map, feeding the co-located tier so a binary that embeds no core
// guests resolves them from beside their catalog entities; nil at a socket with
// no catalog access, which disables that tier.
func buildPluginResolver(inputs Inputs, colocatedDirs map[string]string) pluginpath.Resolver {
	return PluginResolver(inputs.PluginPins, inputs.WorkspaceRoot, *inputs.WorkspaceConfig, colocatedDirs)
}

// PluginResolver constructs the plugin-resolution cascade a build loads plugin
// bytes through, over the tier sources a workspace supplies: the catalog's
// co-located artifact directories, and the content cache addressed at the
// workspace root. It is the one assembly of those sources, so a caller asking the
// build's question outside a build — what the artifact of a Plugin entity
// authored in a catalog directory resolves to — asks the same cascade over the
// same sources rather than a second reading of either. pins states where the
// construction's recorded digests come from and fixes its posture.
func PluginResolver(pins *pluginpath.PinSource, workspaceRoot string, wsCfg workspaceconfig.Config, colocatedDirs map[string]string) pluginpath.Resolver {
	return pins.Resolver(pluginpath.ResolutionContext{
		ColocatedDirs: colocatedDirs,
		WorkspaceRoot: workspaceRoot,
		CacheDir:      wsCfg.ResolvedArtifactCacheDir(),
	})
}

// CatalogPluginArtifacts is the question a dependency resolution asks about the
// .wasm of a Plugin entity authored in a catalog directory: what the build's own
// cascade resolves it to, over the same sources, or the refusal when no tier holds
// it. The pull that writes a lockfile and the build that holds one to its drift
// check both ask it, so the pin one records and the pin the other expects come
// from one computation.
//
// It carries no recorded digests, deliberately and on both sides. The pull is
// producing the record, so there is none to consult; and a build reconstructing
// the record compares the digest this answers against the one the record holds, so
// a pin supplied here would turn that comparison into a second check of the same
// bytes and report a changed module as an integrity failure rather than as a
// lockfile that no longer matches.
func CatalogPluginArtifacts(workspaceRoot string, wsCfg workspaceconfig.Config, colocatedDirs map[string]string) closure.LocalPluginArtifact {
	return PluginResolver(pluginpath.NoPins(), workspaceRoot, wsCfg, colocatedDirs).ResolveCatalogArtifact
}

// buildEntityPluginLookup fronts the build-time artifact cascade with the
// Plugin entity translation: a pipeline step references a Plugin entity
// URI, and the front derives the entity's .wasm artifact reference (its bare
// identity, via resolver.EntityToArtifact) before the cascade resolves the
// bytes. The catalog's Plugin accessor supplies the entity; the cascade keys its
// pins by the artifact reference, so the front leaves the pin model unchanged.
// A reference that matches no Plugin entity halts with
// ErrPluginEntityNotFound. This is the same entity-front the linter's
// workspace-config path binds, so the entity → artifact linkage is single-sourced.
func buildEntityPluginLookup(inputs Inputs, catalogLookup resolver.CatalogLookup) pluginpath.Lookup {
	cascade := buildPluginResolver(inputs, catalogLookup.PluginColocatedDirs).Resolve
	return pluginpath.EntityLookup(func(uri string) (string, *pluginpath.PluginContractClaim, *pluginpath.PluginRuntimeLimits, *[]pluginpath.PluginCommandMirror, bool) {
		if catalogLookup.Plugin == nil {
			return "", nil, nil, nil, false
		}
		entity, ok := catalogLookup.Plugin(uri)
		if !ok {
			return "", nil, nil, nil, false
		}
		return entity.ArtifactReference(), resolver.PluginContractClaim(entity), resolver.PluginRuntimeLimits(entity), resolver.PluginCommandsMirror(entity), true
	}, cascade, pluginpath.SurfaceEntityOnly)
}

// pluginCompileRevealDelay is the wall-clock a provider-guest warm must exceed
// for the eager pass to treat it as a genuine compilation rather than a cache
// hit. A guest served from the populated compile cache loads in a few
// milliseconds; compiling one from source takes seconds, so a warm still running
// past this delay is compiling. The margin is wide enough that a cache hit never
// crosses it and a real compile always does, so the signal cleanly separates the
// two without inspecting the cache's internal keys.
const pluginCompileRevealDelay = 750 * time.Millisecond

// warmProviderGuest compiles one provider guest, reporting through onCompiling
// when the warm runs long enough to be a genuine compile rather than a cache
// hit. A warm still running after pluginCompileRevealDelay is compiling: the
// timer fires onCompiling from its own goroutine while the warm proceeds, so a
// progress surface can reveal the compilation step mid-compile. A warm that
// finishes within the delay (a cache hit) cancels the timer and reports nothing.
// A nil onCompiling skips the timing and warms directly.
func warmProviderGuest(ctx context.Context, w guestWarmer, onCompiling func()) error {
	if onCompiling == nil {
		return w.Warm(ctx)
	}
	timer := time.AfterFunc(pluginCompileRevealDelay, onCompiling)
	defer timer.Stop()
	return w.Warm(ctx)
}

// collectProviderGuestWarmers resolves the distinct provider guests the render
// will load, in the same way the render loop does: it walks the graph's nodes,
// resolves each node's recipe to its provider reference, and routes that provider
// through the same hybrid engine factory the render uses. A provider that routes
// to a Wasm guest yields a guestWarmer; an engine that implements no warm
// capability yields none. Guests are deduplicated by the engine identity
// the factory memoizes, so two bindings that resolve to one guest warm once.
// Node order is stable so the pass is deterministic. A factory error (a version
// pin no plugin satisfies) is returned — the same error the render loop would
// raise for that provider, surfaced before the render loop instead.
func collectProviderGuestWarmers(graph *types.IRGraph, recipeLookup renderer.RecipeLookup, engineFactory renderer.EngineFactory) ([]guestWarmer, error) {
	if recipeLookup.Recipe == nil {
		return nil, nil
	}
	seenRef := map[string]struct{}{}
	var refs []string
	for _, nodeID := range graph.Spec.Nodes.Keys() {
		node, ok := graph.Spec.Nodes.Get(nodeID)
		if !ok {
			continue
		}
		r, found := recipeLookup.Recipe(nodeID, node.Component)
		if !found || r == nil || r.Spec.Capability == "" {
			continue
		}
		if _, dup := seenRef[r.Spec.Capability]; dup {
			continue
		}
		seenRef[r.Spec.Capability] = struct{}{}
		refs = append(refs, r.Spec.Capability)
	}
	sort.Strings(refs)

	var warmers []guestWarmer
	seenGuest := map[guestWarmer]struct{}{}
	for _, ref := range refs {
		engine, err := engineFactory(ref)
		if err != nil {
			return nil, err
		}
		w, ok := engine.(guestWarmer)
		if !ok {
			continue
		}
		if _, dup := seenGuest[w]; dup {
			continue
		}
		seenGuest[w] = struct{}{}
		warmers = append(warmers, w)
	}
	return warmers, nil
}

// RunPipeline executes the 5-step compiler pipeline against caller-supplied
// inputs and returns an [Outcome] carrying the rendered artifact map plus the IR
// graph. The function performs no filesystem reads or writes beyond the
// operations the supplied lookup callbacks perform; the caller is responsible for
// any filesystem-side effects (writing artifacts to dist/, writing the
// trace map to .trace/).
//
// It shares Steps 1-4 with [RunTier4Pipeline] through the unexported
// runStepsOneToFour helper, then runs Step 4b + Step 5. First-error-fatal
// semantic is preserved for terminal phases: analyser errors land as a joined
// error return so existing irgraph + CLI build callers see the flattened error
// shape they already consume.
//
// Mid-run advisories — the binding-lint findings, the static-analysis advisory
// stream, the governance sockets' warn and info verdicts, and the renderer's
// warnings — are displayed on os.Stderr inline by this function AND ride out on
// the Outcome, so a caller that reads no terminal receives every one of them as a
// structured entry rather than having to redirect a process stream to see them.
// The entries arrive in the order the display printed them, because each is
// recorded beside its own display.
//
// # A run that stops still reports what it found
//
// The returned error and the returned Outcome are not alternatives. A halt any
// phase raises comes back with the advisories the phases ahead of it had already
// raised, so a caller that renders findings has them on the path where a reader
// needs them most. Which of the two the caller is holding is not left to a
// convention: [Outcome.Completed] answers with the payload only for a run that
// finished, and [Outcome.Advisories] answers on either, so there is no spelling
// that reads a stopped run as a finished one.
//
// A trace-file close failure is displayed and is deliberately not an advisory:
// see Result.Advisories for why.
//
// The parameter map merged from Release.spec.parameters flows through to
// the renderer (for orchestrator-time path-template evaluation). RunPipeline
// runs inside resolver.ResolveAndUse's scope (via runStepsOneToFour →
// BuildGraph), which owns that scope: the render completes inside the
// callback, so the map is live for every render phase and reachable from no
// caller once RunPipeline returns.
func RunPipeline(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
	subAggregateLoader topology.SubAggregateLoader,
	opts Options,
) (Outcome, error) {
	// The entry point owns the log, so every phase below records into one of them
	// and a caller never has to supply it. Assigned unconditionally rather than
	// honoured-if-set: a caller-supplied log would let two runs share an
	// accumulator, and the second run's Result would then carry the first run's
	// advisories.
	inputs.AdvisoryLog = &AdvisoryLog{}
	var result *Result
	// Set where Step 5 begins, so a run that stops reports whether it stopped in
	// its render or ahead of it — see Outcome.ReachedRender.
	reachedRender := false
	err := runStepsOneToFour(ctx, inputs, catalogLookup, recipeLookup.Recipe, subAggregateLoader, func(stepsOut *stepsOneToFourOutput) error {
		// Step 4: convert analyzer errors to the joined-error shape that
		// pre-extraction RunPipeline returned. Preserves the first-error-fatal
		// semantic existing callers (irgraph, CLI build) consume.
		if err := joinedAnalysisHalt(stepsOut.Analysis.Errors); err != nil {
			return err
		}

		resolveResult := stepsOut.ResolveResult
		graph := stepsOut.IRGraph
		featureOrder := stepsOut.FeatureOrder

		if err := ctx.Err(); err != nil {
			return err
		}

		// Component / Feature assertions, evaluated in the shared Steps 1-4 core.
		if err := wrapAssertionErr(stepsOut.AssertionErr); err != nil {
			return err
		}

		// Resolution-completeness — every component/feature instance must resolve
		// to exactly one recipe before render. Skipped when the caller does not
		// supply a verifier (IR-inspection paths that do not render).
		// The domain is the operator's, because that is where the remedy the
		// condition states is written: every arm of it asks for a mapping under
		// the Manifest's own mapping table, and the Manifest is the operator's
		// document. One arm — a type nothing implements — additionally needs a
		// Recipe authored before any mapping can name it, but the reader still
		// acts on the Manifest first, and a category names one document.
		//
		// The gate has two producers and one switch. The rules declared over the
		// flattened composition answered for every component instance, including
		// the ones a sub-aggregate contributed — which is the whole set only that
		// stage holds — and their findings arrived here rather than halting at the
		// stage that raised them. The verifier answers for the attachment axes no
		// node-keyed rule covers. Both are the same gate the caller asked for, so
		// both are read under the one declaration, and the declared rules are read
		// first because their findings are positioned per instance.
		if opts.emitsSomethingToApply() {
			if err := emissionRefusal(stepsOut.EmissionViolations); err != nil {
				return cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{}, "recipe resolution")
			}
			if err := opts.RecipeResolutionVerifier(inputs.Manifest, graph); err != nil {
				return cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{}, "recipe resolution")
			}
		}

		// Render-skip cutoff: an IR-inspection caller that needs only the graph
		// stops here, before any render machinery (trace writer, plugin
		// compilation cache, provider engines) is constructed — so no rendering
		// plugin loads. The graph is already final after Step 4b; render never
		// mutates it, so this returns the same graph a full run would.
		//
		// The advisories the phases that DID run raised travel out even though no
		// render happened: a render-skipping caller still resolved and analysed the
		// document, so the findings from those phases exist and are exactly as
		// useful to it. Only the render's own advisories are absent, because the
		// render did not run.
		if opts.SkipRender {
			result = &Result{IRGraph: graph, Advisories: inputs.AdvisoryLog.Entries()}
			return nil
		}

		// Step 5: Render. Trace writer selection: in-memory collection when
		// IncludeTrace is true (caller surfaces or writes the map); no-op
		// otherwise. The trace close runs as this continuation unwinds
		// (deferred here) and the resolve scope closes after it, so render
		// reads live parameters throughout.
		reachedRender = true
		// The capture mode is constructed once and handed to both the writer that
		// stamps the header and the renderer that empties the withheld-key set, so
		// the announcement and the withholding cannot disagree.
		traceMode := trace.Mode{Unredacted: opts.TraceUnredacted}
		var memWriter trace.MemoryWriter
		var traceWriter trace.Writer
		if opts.IncludeTrace {
			memWriter = trace.NewMemory(traceMode)
			traceWriter = memWriter
		} else {
			traceWriter = trace.New("", traceMode)
		}
		// A close failure is reported on the diagnostic stream and reaches the
		// advisory channel deliberately NOT AT ALL. It says that a side effect the
		// caller asked for did not complete — the compiler's own housekeeping — and
		// nothing whatever about the author's Release, so it does not belong in a
		// list a client renders as problems with the reader's workspace, where it
		// would be both unactionable and misleading. Do not sweep it onto the channel
		// for symmetry with the advisories around it.
		defer func() {
			if cerr := traceWriter.Close(); cerr != nil {
				fmt.Fprintf(os.Stderr, "  trace: close: %s\n", cerr)
			}
		}()

		rendererInput := renderer.RendererInput{
			Graph: graph,
			// The presentation of the finalised graph, forwarded so the render
			// can present what every plugin reports about itself as that
			// presentation plus its own facts. Forwarding it is what keeps one
			// fact to one producer: rebuilding it inside the render would give
			// every earlier fact a second answer, free to disagree with this one.
			GraphView:         stepsOut.GraphView,
			FeatureOrder:      featureOrder,
			ResolveResult:     resolveResult,
			CatalogLookup:     catalogLookup,
			MappingLibraries:  inputs.MappingLibraries,
			WorkloadLibraries: inputs.WorkloadLibraries,
			ArtifactLibraries: inputs.ArtifactLibraries,
			// The pins the load recorded, forwarded for the reason the library sets
			// above are: the load is where an entity and the directory it was read
			// from are both in hand, so a render that asked the filesystem again
			// would be a second reading of one fact.
			RecordedPins: inputs.RecordedPins,
			Trace:        traceWriter,
			TraceMode:    traceMode,
			YAMLIndent:   inputs.WorkspaceConfig.Build.YAMLIndent,
			// Bind the entity-fronted plugin-resolution path so a Recipe /
			// FeatureRecipe pipeline step's plugin URI — a Plugin entity URI —
			// resolves to its verified .wasm bytes: the entity-front translates the
			// entity URI to its spec.plugin artifact reference, then the shared
			// cascade (embed → colocated → local → cache) resolves the
			// artifact. The build configures the cascade with the lockfile digest
			// pins keyed by the artifact reference, so an artifact no tier holds is a
			// loud halt and a drifted artifact halts on the pin. The
			// per-step plugin loads through the describe() gate at execution. A
			// caller-supplied override (e.g. a fixture pointing a plugin URI at a
			// built artifact) wins when present.
			PluginPathLookup: inputs.PluginPathLookup,
			// Per-node progress hook (nil = no-op). Forwarded verbatim; the
			// renderer invokes it once per node as the loop completes. It writes
			// only to a diagnostic sink, so rendered output is unaffected.
			NodeProgress: opts.NodeProgress,
		}
		// The enumeration of the finalised graph, forwarded for the reason the
		// presentation beside it is: the rules over what the render's plugins
		// report are invoked once per node and once per attachment, and those are
		// the axes the rules already invoked at the graph stage walked. One
		// enumeration reaches both, so neither can be asked about coordinates the
		// other was not.
		//
		// It is assigned only when one was produced, so an absent enumeration
		// reads as absent to the render. Assigned unconditionally it would arrive
		// as a present value that answers nothing, which is the state a rule
		// cannot tell from a satisfied one.
		if stepsOut.GraphCoordinates != nil {
			rendererInput.GraphCoordinates = stepsOut.GraphCoordinates
		}
		if rendererInput.PluginPathLookup == nil {
			rendererInput.PluginPathLookup = buildEntityPluginLookup(inputs, catalogLookup)
		}
		// Build-time source-artifact resolution: when the caller supplies a
		// lockfile and workspace root, the orchestrator resolves each remote
		// source to its pinned cache entry. Left unset, the renderer forwards
		// sources verbatim (sourceless / local-path paths).
		if inputs.Lockfile != nil && inputs.WorkspaceRoot != "" {
			rendererInput.Lockfile = inputs.Lockfile
			rendererInput.WorkspaceRoot = inputs.WorkspaceRoot
			rendererInput.CacheDir = inputs.WorkspaceConfig.ResolvedArtifactCacheDir()
		}

		// Route provider rendering through the hybrid dispatch: a provider whose
		// catalog Provider binds to a render_node-advertising Wasm plugin renders
		// through the wasmengine adapter; a provider that resolves to no such plugin
		// is a loud halt. The loader is backed by the process-wide shared
		// compilation cache, so a guest compiles once and every node, every later
		// build, and every concurrent served request reuses the machine code. The
		// routing is uniform across every caller of RunPipeline, so build, serve,
		// and the test harness all route through it with no per-caller change.
		sharedLoader := wasm.NewCachingLoaderFromContext(ctx, wasm.ExternalInputsFromContext(ctx))
		hybrid, providerVersion, closeEngines := enginefactory.NewHybrid(catalogLookup, rendererInput.PluginPathLookup, sharedLoader)
		// Release each provider engine's reused plugin instance at build end. A
		// guest that declares reusable_instance is loaded once per build and its
		// single instance lives on the per-build engine; this closes it after the
		// render (and eager compile) completes. A guest with no reuse claim holds
		// no instance, so the close is a no-op.
		defer func() { _ = closeEngines(ctx) }()
		rendererInput.ProviderVersion = providerVersion

		// Eager provider-guest compilation. When the caller opts in (a progress
		// surface that attributes the compile), force-compile the distinct provider
		// guests the render needs into the shared cache before the render loop, so a
		// cold guest compilation is a discrete phase the surface can report rather
		// than a stall inside the first node's render. It warms the same cache the
		// render loads hit, so it moves compilation earlier without changing output;
		// a warm cache makes each warm a near-instant cache hit. A compile failure
		// here is the same failure the render load would raise, surfaced earlier.
		if opts.PluginCompileBegin != nil {
			warmers, werr := collectProviderGuestWarmers(graph, recipeLookup, hybrid)
			if werr != nil {
				return fmt.Errorf("plugin compilation: %w", werr)
			}
			opts.PluginCompileBegin(len(warmers))
			for i, w := range warmers {
				if err := warmProviderGuest(ctx, w, opts.PluginCompileRunning); err != nil {
					return fmt.Errorf("plugin compilation: %w", err)
				}
				if opts.PluginCompileGuestDone != nil {
					opts.PluginCompileGuestDone(i + 1)
				}
			}
		}

		renderResult, rerr := renderer.RenderAll(ctx, rendererInput, recipeLookup, hybrid)
		if rerr != nil {
			return fmt.Errorf("rendering: %w", rerr)
		}

		emitRenderWarnings(os.Stderr, renderResult, opts.SuppressUnsupportedFieldWarnings)

		// The render's own advisories join the channel here, immediately after their
		// display and before the render-verdict socket's, so the channel's order is
		// the display's order across the render boundary too. They are recorded
		// verbatim: they are already this channel's element type, so there is no
		// conversion and nothing to lose — a render advisory keeps the node and
		// provider its location names.
		for _, d := range mergeRenderDiagnostics(renderResult) {
			inputs.AdvisoryLog.record(d)
		}

		// Render-verdict governance role. Config-driven off the render-verdict
		// attachments in WorkspaceConfig.Plugins.Governance — a no-op when
		// unconfigured, and it runs those attachments over the rendered artifacts
		// (path-sorted). A halt verdict returns a build-halting error carrying
		// ErrPolicyViolation; warn / info verdicts are non-halting diagnostics. A
		// verdict role never mutates the rendered artifacts.
		if err := runRenderPolicySocket(ctx, inputs, catalogLookup, renderResult.Files); err != nil {
			return err
		}

		// Every advisory the run raised is read off the log rather than re-collected
		// from the phases, which is what makes the returned order the displayed
		// order: the log was appended to beside each display, and a second collection
		// here would be free to disagree with those displays.
		result = &Result{
			Artifacts:  renderResult.FileBytes(),
			IRGraph:    graph,
			Advisories: inputs.AdvisoryLog.Entries(),
		}
		if memWriter != nil {
			result.Trace = memWriter.Entries()
		}
		return nil
	})
	if err != nil {
		// The run stopped, and the findings it had already raised are handed back
		// with it. The log lives on this function's own inputs rather than inside
		// the continuation, so it is readable here whichever phase refused — which
		// is what lets one site cover every halt instead of each halt remembering.
		//
		// Only the advisories travel. Artifacts and the graph are reachable from no
		// accessor on a run that did not finish, so carrying them would be data
		// nothing can read; a consumer that later needs the graph a stopped run
		// reached gets an accessor added for it, with the reader that wanted it.
		return Outcome{result: &Result{Advisories: inputs.AdvisoryLog.Entries()}, reachedRender: reachedRender}, err
	}
	return Outcome{result: result, finished: true, reachedRender: reachedRender}, nil
}
