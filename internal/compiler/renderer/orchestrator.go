// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RenderResult is the output of the full Step 5 rendering pipeline.
//
// Files maps each boundary-relative path to its rendered content paired with the
// producer-declared render format (e.g. "hcl", "json"). Format is additive
// metadata carried ON the file record, not a parallel map: it is empty when the
// producing provider declared none, in which case a downstream consumer falls
// back to extension-based selection. Format never affects the emitted bytes, so
// carrying it alongside the content keeps the two from drifting apart.
type RenderResult struct {
	Files map[string]AssembledFile // boundary-relative path → content + producer-declared format
	// Warnings carries the render's advisory stream that no class code governs:
	// the notices a rule declared its own finding advisory, the authored keys
	// nothing consumed, the guest advisories a provider raised while rendering,
	// and the pipeline notices raised while a node's steps ran.
	//
	// Each entry is the structured diagnostic the raising site held rather than a
	// line of text it composed, which is what lets an entry reach a consumer that
	// reads no terminal — the node it concerns, the element it is about, and the
	// severity it carries are fields rather than substrings of a sentence. A site
	// that holds a class code puts it here; a site that holds none carries none,
	// so an absent code is a fact about the raising site and not a gap.
	//
	// It stays a separate field from Diagnostics because a display treats the two
	// differently: this stream prints unconditionally and that one is suppressible
	// by class code. Every surface renders an entry of either through
	// [RenderDiagnosticText], so one advisory has one wording wherever it is read.
	Warnings []Diagnostic
	// Contributions attributes each group of assembled output to the boundaries
	// and the provider that produced it, in assembly-path then provider order.
	//
	// Files is keyed by output path alone, so it cannot say which boundary or
	// which provider wrote a given file; those two coordinates would otherwise
	// end with the assembly that knew them. This is where they survive.
	//
	// It carries coordinates and never a second copy of the content: every
	// entry's OutputPaths are keys of Files, the entries' path sets are pairwise
	// disjoint, and together they cover Files exactly.
	//
	// The coordinates and the output paths become true at different points, and
	// the record is completed rather than built twice. Which boundaries met which
	// provider at which directory is settled once every node has been sorted into
	// its group, which is before any group assembles and is where a rule about the
	// grouping has to be able to read it; the paths a group wrote do not exist
	// until it has assembled, so they are filled into the same records afterwards.
	// The property above describes the value this returns, which is the completed
	// one.
	Contributions []AssemblyContribution
	// Diagnostics carries the CLASS-CODED advisories raised during render: the
	// unsupported-field notices a lower-fidelity target emits for an authored
	// field it cannot honor, and the sensitive-by-value notices attachment
	// delivery raises for a declared-sensitive datum travelling as a literal.
	// Each entry keeps its stable code and names the node and provider via its
	// Location. Out-of-band: never part of the emitted bytes.
	//
	// It is a separate field from Warnings because a display surface treats the
	// two differently — every entry here belongs to a class such a surface may
	// suppress by code, while an entry there is printed unconditionally. Both
	// streams carry the same shape, so the split is about what a display DOES
	// with an entry rather than about what an entry is.
	Diagnostics []Diagnostic

	// DescribedView presents, as named facts, what every plugin this render
	// reaches has reported about itself, together with the provider each site's
	// documents render through.
	//
	// It is published here because the retention it reads is the render's own
	// working state and is gone once the render returns, so a reader asking what
	// a plugin declared has this and nothing else. Reading a fact from it may
	// still load and describe a plugin no step reached, through the same
	// retention the render shares — nothing is described until an accessor asks.
	//
	// nil when the render was handed no presentation of the finalised graph to
	// extend. Out-of-band: it carries no content that reaches an emitted file,
	// and the emitted bytes are the same whether it is populated or not.
	DescribedView constraint.DescribedView

	// HydratedView presents, as named facts, the state each node reached before
	// any of its artifact bytes existed: the locals its Recipe declares, the
	// values it resolved for its provides keys, the reference set of its inline
	// values mapping, whether a source and a workload survived into the render,
	// and the text standing at each of its artifacts before evaluation.
	//
	// It is published here because the state it presents is a loop-local dropped
	// when each node's turn ends, so a reader asking what a node held before its
	// bytes existed has this and nothing else. It embeds the presentation of what
	// every plugin reports about itself, so every fact complete earlier is
	// readable through it.
	//
	// nil when the render could present no earlier stage to extend. Out-of-band:
	// it carries no content that reaches an emitted file, and the emitted bytes
	// are the same whether it is populated or not.
	HydratedView constraint.HydratedView

	// EmittedView presents, as named facts, what each node's own render emitted
	// before anything was grouped for assembly: which of the node's own blocks
	// produced the file standing at each output name.
	//
	// It is published here because the emission it presents is a loop-local
	// dropped when each node's turn ends, and because what survives the render is
	// the assembled output, where an assembly is free to have renamed, nested or
	// merged the node's own names away. So a reader asking which blocks of one
	// Recipe met on one name has this and nothing else. It embeds the presentation
	// of the state each node reached before its artifact bytes existed, so every
	// fact complete earlier is readable through it.
	//
	// nil when the render could present no earlier stage to extend. Out-of-band:
	// it carries no content that reaches an emitted file, and the emitted bytes
	// are the same whether it is populated or not.
	EmittedView constraint.EmittedView

	// DispatchedView presents, as named facts, how this render sorted its nodes
	// into the groups they were assembled in: the providers that met at each
	// evaluated directory, and the boundaries that fed each group.
	//
	// It is published here because the grouping it reads is the render's own
	// working state and is gone once the render returns, so a reader asking which
	// providers landed in one directory has this and nothing else. It embeds the
	// presentation of the state each node reached before its artifact bytes
	// existed, so every fact complete earlier is readable through it.
	//
	// nil when the render could present no earlier stage to extend. Out-of-band:
	// it carries no content that reaches an emitted file, and the emitted bytes
	// are the same whether it is populated or not.
	DispatchedView constraint.DispatchedView

	// RenderedView presents, as named facts, what this render produced: the output
	// paths each provider wrote within each boundary, the bytes standing at one of
	// them, the artifact names each node projected, and the bytes each of those
	// artifacts evaluated to.
	//
	// It is published here for the reason the presentations above are, reaching two
	// retentions rather than one. The paths and their bytes are read from the
	// attribution beside each assembled group, which the assembly that knew the
	// boundary and the provider is the last thing to hold; the artifact facts are
	// read from a per-node retention taken inside the artifacts walk, whose own
	// state is dropped when each node's turn ends. Neither is reachable once the
	// render returns except through this.
	//
	// It embeds the presentation of the state each node reached before its artifact
	// bytes existed, so every fact complete earlier is readable through it.
	//
	// nil when the render could present no earlier stage to extend. Out-of-band:
	// it carries no content that reaches an emitted file, and the emitted bytes are
	// the same whether it is populated or not.
	RenderedView constraint.RenderedView

	// hydrated is the render's retained set of per-node mid-render projections.
	//
	// It is published here because the state each projection is taken from is a
	// loop-local: it is dropped when its node's turn ends, and the render's own
	// working input is gone once the render returns. A reader asking what a node
	// held before its artifact bytes existed has this and nothing else.
	//
	// Out-of-band: it carries no content that reaches an emitted file, and the
	// emitted bytes are the same whether it is populated or not.
	hydrated *hydratedProjections

	// emitted is the render's retained set of per-node emission projections.
	//
	// It is published here because the emitted set each projection is taken from
	// is a loop-local: it is split by the intermediate flag as its node joins a
	// dispatch group and dropped when that node's turn ends. A reader asking which
	// of a node's blocks emitted under one name has this and nothing else.
	//
	// Out-of-band: it carries no content that reaches an emitted file, and the
	// emitted bytes are the same whether it is populated or not.
	emitted *emittedProjections

	// artifacts is the render's retained set of per-node artifact projections.
	//
	// It is published here because the artifacts walk's own state is a loop-local
	// and only the provider's returned files survive it: a reader asking which
	// artifacts a node projected, or what one of them evaluated to, has this and
	// nothing else.
	//
	// Out-of-band: it carries no content that reaches an emitted file, and the
	// emitted bytes are the same whether it is populated or not.
	artifacts *artifactProjections

	// elections is the render's retained record of what each dispatch group's
	// elected engine claimed about combining the group's output, and whether it
	// could carry that assembly out.
	//
	// It is published here because the candidate engines a group collected are the
	// render's own working state and are dropped when the render returns, while the
	// presentation of the grouping outlives it. A reader asking what a group's
	// elected engine claimed has this and nothing else.
	//
	// Out-of-band: it carries no content that reaches an emitted file, and the
	// emitted bytes are the same whether it is populated or not.
	elections *assemblyElections

	// derivedNames is the render's retained record of the names each dispatch
	// group's contributors declared they derived inside their own output.
	//
	// It is published here for the reason the election is: the accumulator each
	// group fills as its contributors render is the render's own working state and
	// is dropped when the render returns, while the presentation of the grouping
	// outlives it. A reader asking what a group's contributors declared has this
	// and nothing else.
	//
	// Out-of-band: it carries no content that reaches an emitted file, and the
	// emitted bytes are the same whether it is populated or not.
	derivedNames *derivedNameSets
}

// AssemblyContribution attributes one group of assembled output to the
// boundaries and the provider that produced it.
//
// Boundaries whose paths evaluate alike under one provider assemble as one
// group, and that collapse is what makes a collision between them visible — so
// the group stays keyed by the evaluated path together with the provider, as it
// always has been, and this record is what keeps every contributor named once
// the collapse has happened. Where a boundary's path is its own, Boundaries
// holds exactly that one boundary and the attribution is the identity it looks
// like.
type AssemblyContribution struct {
	// AssemblyPath is the evaluated final path this group assembled at.
	AssemblyPath string
	// Provider is the provider that rendered the group.
	Provider string
	// Boundaries is every boundary that fed the group, deduplicated, in the
	// order the group first heard from each. It holds more than one entry
	// exactly when several boundaries' paths evaluated alike under Provider.
	Boundaries []string
	// OutputPaths are the output paths this group wrote, in path order. They are
	// keys of RenderResult.Files, which holds the bytes.
	OutputPaths []string
	// PathProducers names, per output path, every contributor that placed a file
	// there, in producer order. Its key set is OutputPaths.
	//
	// It is carried beside the paths rather than folded into them because the
	// file map cannot hold it: that map has one record per path, so two
	// contributors meeting on one path leave one record and no trace of the
	// second. This is where the second survives, and it is what lets a rule about
	// assembled output name the contributors that met rather than only report
	// that something did.
	PathProducers map[string][]string
}

// EmittedPaths returns the output paths one assembly group wrote, in path order.
//
// The group is named by its evaluated assembly path together with the provider
// that rendered it, which is the pair that identifies exactly one group — so the
// first record matching both is the whole answer and there is nothing to union.
//
// It is keyed by the group rather than by a boundary the group was fed by, and
// the difference is not presentational. Boundaries whose own path declarations
// evaluate alike under one provider collapse into a single group, and the paths
// belong to what that group assembled: a boundary of a collapsed group holds no
// path of its own, so answering per boundary returns the same set once per
// boundary and a caller walking that keying walks one group's output several
// times.
//
// The order is stated rather than inherited: the order files are placed into a
// group is the order the contributing maps happen to iterate in, which is not a
// property of the input, so path order is what makes this answer a function of
// the build alone.
func (r *RenderResult) EmittedPaths(assemblyPath, provider string) []string {
	for _, c := range r.Contributions {
		if c.AssemblyPath == assemblyPath && c.Provider == provider {
			out := slices.Clone(c.OutputPaths)
			sort.Strings(out)
			return out
		}
	}
	return nil
}

// AssembledFileProducers returns the contributors that placed a file at one
// output path of one assembly group, in producer order.
//
// It answers for the group, on the reading the content lookup beside it follows:
// a path is looked up only among the paths that group wrote, so a caller asking
// about a group that wrote nothing there is answered with nothing rather than
// with another group's contributors.
//
// The group keying is what makes one collision one answer. Which contributors
// met at a path is a property of the group's own assembled output, so a set
// keyed per boundary answers every boundary of a collapsed group with the same
// contributors — including boundaries none of those contributors sat in, which
// such a keying cannot tell apart.
func (r *RenderResult) AssembledFileProducers(assemblyPath, provider, path string) []string {
	for _, c := range r.Contributions {
		if c.AssemblyPath != assemblyPath || c.Provider != provider {
			continue
		}
		return c.PathProducers[path]
	}
	return nil
}

// assemblyProviders returns the providers that assembled at one evaluated
// assembly path, deduplicated, in the order the grouping recorded its groups.
//
// It is unexported because both of its readers are in this package — the
// presentation of what the grouping holds, and the enumeration a rule about the
// grouping is invoked over — and one walk is what keeps the coordinate a rule
// runs over and the value it reads from being derived two ways. The exported
// readers above answer a question a caller outside this package asks; this one
// does not, so exporting it would publish a surface nothing consumes.
//
// The order is the attribution's own, which is assembly-path order and then
// provider order, so the answer is a function of the build alone.
func (r *RenderResult) assemblyProviders(assemblyPath string) []string {
	var out []string
	for _, c := range r.Contributions {
		if c.AssemblyPath != assemblyPath || slices.Contains(out, c.Provider) {
			continue
		}
		out = append(out, c.Provider)
	}
	return out
}

// assemblyBoundaries returns the boundaries whose components fed one assembly
// group, in the order that group first heard from each.
//
// The path and the provider together identify exactly one group, so the first
// record matching both is the whole answer and there is nothing to union. A pair
// no group was recorded for answers with nothing.
func (r *RenderResult) assemblyBoundaries(assemblyPath, provider string) []string {
	for _, c := range r.Contributions {
		if c.AssemblyPath == assemblyPath && c.Provider == provider {
			return c.Boundaries
		}
	}
	return nil
}

// AssembledFileContent returns the bytes standing at one output path of one
// assembly group, and reports whether that group wrote that path.
//
// It answers for the group rather than for the path alone: a path is looked up
// only among the paths the named group actually wrote, so a caller asking about
// a group that wrote nothing there is told so rather than handed another group's
// file.
//
// The bytes are the ones the result holds, not a copy of them.
func (r *RenderResult) AssembledFileContent(assemblyPath, provider, path string) ([]byte, bool) {
	for _, c := range r.Contributions {
		if c.AssemblyPath != assemblyPath || c.Provider != provider {
			continue
		}
		if !slices.Contains(c.OutputPaths, path) {
			return nil, false
		}
		af, ok := r.Files[path]
		return af.Content, ok
	}
	return nil, false
}

// FileBytes projects Files into a path→content map, dropping the per-file format
// metadata. It is the seam for consumers that write or route the rendered bytes
// alone (the CLI's dist/ writer, the daemon's artifact result) and never inspect
// format. Keys and byte slices are the same the records carry.
func (r *RenderResult) FileBytes() map[string][]byte {
	out := make(map[string][]byte, len(r.Files))
	for p, af := range r.Files {
		out[p] = af.Content
	}
	return out
}

// dispatchKey identifies a per-(evaluated_path, provider) dispatch group.
// Two boundaries with the same evaluated path and provider produce ONE
// dispatch group receiving contributors from both; mixed-provider boundaries
// produce one dispatch group per provider at the boundary's evaluated path.
//
// Replaces the previous boundary-keyed dispatch model. Per-(path, provider)
// dispatch grouping enables operator-controlled cross-boundary aggregation
// via shared `directory:` declarations and naturally accommodates mixed-
// provider boundaries.
type dispatchKey struct {
	Path     string // evaluated final_path (post-template-evaluation)
	Provider string // resolved per-component recipe.Spec.Capability
}

// pendingDispatch holds per-(path, provider) state across the per-node loop
// and the per-(path, provider) BoundaryAssembler assembly pass.
//
// formats mirrors files exactly — keyed [nodeID][path] with the SAME keys —
// carrying each file's producer-declared format alongside its bytes. An entry
// exists only when the rendering provider declared a format for that file. The
// two maps are populated at the same site and must be keyed in lockstep; the
// flat-output flush reads formats by the same key it reads files.
//
// intermediates carries Intermediate=true EmittedFiles separately from files
// so the orchestrator never writes them at the dispatch path directly; they
// flow only to the BoundaryAssembler at finalization. placed is its exact
// complement — the Intermediate=false EmittedFiles, in the order they were
// emitted — retained beside files because files is keyed by path and so loses
// both the emission order and the distinction between what per-node Render
// produced and what was merged in afterwards. An assembler is handed the union
// of the two as its contributor list, so a nesting assembler (whose per-node
// output is all Intermediate=false) sees its contributors at all. nodeAnnotations,
// nodeComponent and nodeSyncWave are the per-node metadata the assembler needs
// to assemble its input (annotations for per-boundary network derivation,
// component for per-node info, sync wave so an assembler can order the
// contributors it merges relative to one another). engineByNode lets the
// assembly pass map node-IDs to their engine instances for any
// provider-specific lookups.
//
// nodeSyncWave is keyed like nodeAnnotations and populated at the same site
// from the same node render context. A node whose boundary declares no wave
// carries the zero value, which is that field's documented default, so an
// absent entry and a declared zero are the same state by definition rather
// than by convention.
//
// nodeValues is keyed the same way and populated at the same site, from the
// values that node's own render resolved — but only for a node rendered from a
// source, because that is the only case where the compiler supplies inputs the
// node's own files do not already carry. A node whose render produced its files
// itself resolved the values into them, so it holds no entry here and an
// assembler reads no inputs for it. An assembler whose output is the only
// position its target evaluates carries each contributor's values into the
// assembled artifact; one whose contributors already carry their own inputs
// reads none of it.
//
// nodeProducer records, per the same key, the authoring-level coordinates of
// whatever placed that key's files: the kind of contributor it is and the name or
// names an author wrote for it. It is STATED at the site that routes a
// contributor's output, never recovered from the key, and that is the property
// rather than a convenience. The key is a routing identity, and a boundary-scope
// contributor's is a marker joining two authored coordinates with characters either
// coordinate may itself contain — so cutting it apart cannot return what went in,
// and a reader that cut it dropped one coordinate silently. Two contributors
// differing only in the dropped coordinate then arrive under one label, and every
// surface that compares contributors by label reads one where there are two.
//
// nodeNameBasis records, per the same key again, the identity that contributor
// derives its own names from inside an assembled artifact, or nothing where the key
// itself is that identity. It travels to the assembler beside the key for the
// reason the render carries the same pair: the name a contributor declared while
// rendering and the name the assembled artifact addresses that content by have to
// come from one identity, and the key cannot be it for a contributor whose key is a
// marker no target's alphabet accepts.
//
// contributingBoundaries records every boundary path whose components fed
// into this dispatch group — used for diagnostic messages on collision /
// chart-name-mismatch errors and for chart-name cascade lookups against
// the resolver-side BoundaryAnnotations map.
// derivedKeys accumulates the names every contributor to this group declares it
// derived, across all of them, so the injectivity check runs once per group with
// every declaration in view rather than per contributor as each arrives.
type pendingDispatch struct {
	files                  map[string]map[string][]byte
	formats                map[string]map[string]string
	derivedKeys            derivedKeySet
	intermediates          map[string][]EmittedFile
	placed                 map[string][]EmittedFile
	nodeAnnotations        map[string]map[string]string
	nodeComponent          map[string]string
	nodeSyncWave           map[string]int
	nodeValues             map[string]map[string]any
	nodeProducer           map[string]derivedKeyProducer
	nodeNameBasis          map[string]string
	subCharts              []SubChartInfo
	assembler              BoundaryAssembler
	assemblerCandidates    []assemblerCandidate
	engineByNode           map[string]Provider
	contributingBoundaries []string
}

// assemblerCandidate is one node's contribution to its dispatch group's
// boundary-assembler election: the engine that rendered the node and the
// resolved provider version behind it. The group's assembler is the engine of
// the highest-version candidate (see selectBoundaryAssembler); candidates are
// appended in node-processing order, so iterating them in order keeps the
// earliest node's engine on a version tie.
type assemblerCandidate struct {
	engine  Provider
	version string
}

// boundaryFRNodePrefix marks a synthetic per-node key that carries a
// boundary-scope FeatureRecipe's contribution rather than a component's. It is
// declared once so the key's construction and every reader of it agree.
const boundaryFRNodePrefix = "<boundary-fr>:"

// assembledFileEntry is one producer's placement of one file into a dispatch
// group: the group-relative path the file lands at, the file itself, and the
// authoring-level name of whatever placed it.
type assembledFileEntry struct {
	path     string
	file     AssembledFile
	producer string
}

// assembledFileSet accumulates every file a dispatch group's producers place,
// then resolves them into the group's path→file map and the attribution beside
// it.
//
// Placing a file into a group is one operation with one implementation: the
// boundary assembler's merged documents and each node's own rendered files —
// nested under their instance or written flat — all place through put. No
// producer holds the resulting map, and the map does not exist until resolve
// builds it, so a producer added later cannot place a file whose attribution is
// missing from what resolve returns.
//
// The attribution is returned rather than consumed here, and that is the whole
// of why this value keeps every placement instead of folding them as they
// arrive. The path→file map holds one record per key, so a second placement at
// one path is folded away by the map's own construction and is unreadable from
// the map afterwards; the per-path producer list is what survives that fold, and
// the rule declared over a render's assembled output reads it once every group
// has assembled.
//
// The scope is one dispatch group's assembled output — several independent
// producers meeting on one path. A single Recipe naming one output filename
// twice inside its own artifacts map is a different defect on a different key
// space, caught earlier and separately where that map is projected.
type assembledFileSet struct {
	entries []assembledFileEntry
}

func (s *assembledFileSet) put(path string, file AssembledFile, producer string) {
	s.entries = append(s.entries, assembledFileEntry{path: path, file: file, producer: producer})
}

// resolve returns the group's path→file map together with the contributors that
// placed a file at each of those paths, in producer order.
//
// The two are built in one walk because they are two readings of one set of
// placements: the map is what the group writes, and the attribution is who wrote
// it. Deriving the second from the first is impossible — the map has already
// folded a repeat away — and deriving it in a second walk over the same
// placements would be a second answer to one question.
//
// The record kept for a path several producers placed at is the first in
// producer order rather than the first placed. The maps the producers iterate
// are plain Go maps whose iteration order is randomised per run, so keeping the
// first placed would leave the retained bytes differing between runs of
// identical input. Such a group is refused before its output is written, but
// everything between here and the refusal reads the retained value, and a value
// that is not a function of the input has no business being read at all.
//
// Producer order is likewise what makes the attribution a function of the input:
// the order placements arrive in is the contributing maps' iteration order, and
// a rule reporting the producers that met would otherwise name them differently
// on different runs of one input.
func (s *assembledFileSet) resolve() (map[string]AssembledFile, map[string][]string) {
	byPath := make(map[string][]assembledFileEntry, len(s.entries))
	for _, e := range s.entries {
		byPath[e.path] = append(byPath[e.path], e)
	}
	files := make(map[string]AssembledFile, len(byPath))
	producers := make(map[string][]string, len(byPath))
	for path, placements := range byPath {
		ordered := make([]assembledFileEntry, len(placements))
		copy(ordered, placements)
		sort.SliceStable(ordered, func(i, j int) bool {
			if ordered[i].producer != ordered[j].producer {
				return ordered[i].producer < ordered[j].producer
			}
			return ordered[i].file.Format < ordered[j].file.Format
		})
		files[path] = ordered[0].file
		named := make([]string, 0, len(ordered))
		for _, e := range ordered {
			if !slices.Contains(named, e.producer) {
				named = append(named, e.producer)
			}
		}
		producers[path] = named
	}
	return files, producers
}

// nodeProducerLabel names a per-node contributor in authoring terms, so a
// finding about what it wrote points at something the author can find in their
// own input.
//
// 🔴 It carries EVERY coordinate the contributor's label holds, and dropping one is
// not a matter of how much detail a message wants. The attribution this label
// becomes is compared for equality by whatever reads it — a path several
// contributors wrote at reports the distinct producers that met there — so two
// contributors differing only in a coordinate this label omitted arrive as one
// producer, and the surface that exists to report them reports nothing, which is
// exactly what a build with no collision reports. The same reasoning is recorded at
// length on [derivedKeyProducer], whose coordinates these are.
//
// The kind is the word an author reads, and it is what says where the name is
// written — a component's under the components the aggregate declares, a
// boundary-scope feature's under that boundary's features. The further coordinate
// is spelled through the same caption the rule over derived names uses, so one
// contributor is called the same thing by both surfaces.
//
// The values are not quoted here. The label is retained as a contributor's
// attribution and reaches a reader through a diagnostic surface that quotes each
// value it is handed, so quoting at both ends would show the name inside two
// layers of quotation.
func nodeProducerLabel(producer derivedKeyProducer) string {
	label := producer.kind + " " + producer.name
	if producer.scope == "" {
		return label
	}
	return label + " " + derivedNameScopeCaption(producer.kind) + " " + producer.scope
}

// nodeOutputSegment is the directory a nesting assembler's dispatch group places
// one contributor's files under, composed from the coordinates that identify that
// contributor in the author's own input.
//
// The per-node key is never it: a boundary-scope contributor's key is a marker the
// compiler built for its own dispatch, and the characters that marker opens and
// joins with — angle brackets, a colon, an at sign — are refused in a path on some
// platforms and have to be escaped in a URL. It reads the coordinates from the same
// recorded label a diagnostic does, so the directory a reader opens and the name a
// finding calls that contributor by cannot come to disagree.
//
// # Which coordinates, and why that differs by kind rather than uniformly
//
// A component's own render, and a feature attached at a BOUNDARY, are identified
// by their name alone at this position. For the boundary-scope feature the reason
// is positional: the further coordinate is the boundary it is attached at, and
// that boundary IS the dispatch group whose position this segment sits inside, so
// repeating it would name it twice.
//
// A feature attached to a COMPONENT is identified by both, and the reason is that
// the same positional argument runs the other way. Its further coordinate is the
// instance name it was attached under, which the enclosing directory does NOT
// state — the group's position names the boundary, not the host. Dropping it would
// hand the attachment the segment its HOST already has, so host and attachment
// would compose one directory and each would hold the other's files, with the
// distinguishing coordinate discarded at exactly the point that decides them
// apart.
//
// # Every arity goes through one composition, which is what keeps the arities apart
//
// The segment is composed rather than taken raw even for the single-coordinate
// case, and that is load-bearing rather than uniformity for its own sake. The
// composition records how many coordinates it was given, so a component whose own
// name is "a--b" and an attachment "b" on host "a" reach different segments.
// Taking the single-coordinate case raw would let those two meet at one directory
// — and nothing would report it, because two contributors sharing a directory
// with different file names collide on no path, so the attribution that reports
// two producers meeting at one path would have nothing to compare. For a name the
// alphabet already admits the composition returns that name unchanged, so an
// ordinary contributor reaches the directory it always reached.
func nodeOutputSegment(producer derivedKeyProducer) string {
	if producer.kind == producerKindComponent && producer.scope != "" {
		return contributorDirSegment(producer.name, producer.scope)
	}
	return contributorDirSegment(producer.name)
}

// nodeRelFor is the position a nesting dispatch group writes the files keyed
// under one of its per-node keys: the group's declared enclosing segment
// followed by that key's own output segment, with a trailing separator.
//
// It is the single home of that composition, and it is the whole of a
// contributor's position: every contributor a nesting group holds has a key of
// its own, so a key's position is the group's segment and that key's own segment
// and nothing further. Both the group's own placement and the position reported
// to the group's assembler read it, so a contributor's files are written where
// the assembler was told they are rather than at a position one side composed and
// the other re-derived.
func (pd *pendingDispatch) nodeRelFor(nestPrefix, nodeID string) string {
	return nestPrefix + nodeOutputSegment(pd.producerOf(nodeID)) + "/"
}

// contributorKeys is every per-node key this dispatch group holds output for.
//
// The three maps are read rather than any one of them, because no single map
// holds the whole set and which one a contributor lands in is decided by how its
// output was routed. A contributor handed to the group's assembler is in
// intermediates alone; one the group places is in placed and in files. Reading
// fewer maps drops exactly the contributor whose routing the reader did not have
// in mind.
//
// EVERY contributor a nesting group holds now has a key of its own, and that is
// what makes three maps the whole set. A contributor whose files were keyed
// inside another contributor's map would appear in none of them and would need a
// record of its own to be found at all — and a contributor reachable only through
// another's key has no position the group can name, so it is precisely the one an
// assembled artifact cannot address.
func (pd *pendingDispatch) contributorKeys() map[string]struct{} {
	keys := make(map[string]struct{}, len(pd.files))
	for id := range pd.files {
		keys[id] = struct{}{}
	}
	for id := range pd.intermediates {
		keys[id] = struct{}{}
	}
	for id := range pd.placed {
		keys[id] = struct{}{}
	}
	return keys
}

// producerOf is the recorded label for one of a dispatch group's per-node keys.
//
// A key reaching any of the group's own flush paths was registered when its files
// were routed, so the recorded label is present for every key those paths walk. The
// zero label is returned for anything else rather than a name reconstructed from
// the key, because reconstructing one is the lossy cut this whole registration
// exists to remove — a label that named the wrong contributor would be worse than
// one that names none.
func (pd *pendingDispatch) producerOf(nodeID string) derivedKeyProducer {
	return pd.nodeProducer[nodeID]
}

func joinWithAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// RecipeLookup resolves a component/feature instance to its Recipe and
// FeatureRecipes. Both closures take the instance NAME plus the catalog TYPE so
// the Manifest name tier (a per-instance recipe override) can take precedence
// over the type mapping; an empty instance name resolves by type alone.
type RecipeLookup struct {
	Recipe        func(instanceName, componentType string) (*types.Recipe, bool)
	FeatureRecipe func(instanceName, featureType string) (*types.FeatureRecipe, bool)

	// RecipeDocument reports the file an already-resolved Recipe was declared in,
	// and whether one is recorded. It takes the entity rather than an identity so
	// the answer cannot describe a different Recipe from the one the render is
	// holding, and so the per-kind identity formula stays in the catalog beside the
	// walk that recorded the path.
	//
	// The document is what a relative source is resolved against once its template
	// has produced a value, and what a diagnostic raised inside the render names as
	// its location. A Recipe resolved from a registry is reported at its document in
	// the layout of its cache slot — where the load anchored its content paths — so
	// a relative result resolves inside what was published, as it does beside a
	// Recipe in a catalog directory.
	//
	// It may be nil. A nil field reports no document, so a caller that does not wire
	// it renders a templated relative source as the template produced it and
	// positions such a diagnostic at no file — the same answer as for a Recipe that
	// genuinely has none.
	RecipeDocument func(r *types.Recipe) (string, bool)

	// FeatureRecipeDocument is the FeatureRecipe counterpart of RecipeDocument, and
	// it answers the same two questions for the same two readers: what a relative
	// source is resolved against once the attachment's source template has produced
	// a value, and what a diagnostic raised inside that attachment's render names as
	// its location.
	//
	// It is a second field rather than one closure over both kinds because the
	// identity formula is per-kind and lives in the catalog beside the walk that
	// recorded the path — a single closure would have to take an identity, and an
	// identity composed at this call site is a second statement of that formula.
	//
	// It may be nil, with the same reading as a nil RecipeDocument.
	FeatureRecipeDocument func(fr *types.FeatureRecipe) (string, bool)
}

// recipeDocument reports the file a resolved Recipe was declared in, tolerating
// an unwired lookup and a nil Recipe alike. It exists so the render's call site
// reads one expression rather than repeating the two guards.
func (l RecipeLookup) recipeDocument(r *types.Recipe) string {
	if l.RecipeDocument == nil || r == nil {
		return ""
	}
	path, _ := l.RecipeDocument(r)
	return path
}

// featureRecipeDocument is the FeatureRecipe counterpart of recipeDocument, and
// exists for the same reason: both attachment scopes read the answer, and each
// repeating the two guards would be two expressions of one tolerance.
func (l RecipeLookup) featureRecipeDocument(fr *types.FeatureRecipe) string {
	if l.FeatureRecipeDocument == nil || fr == nil {
		return ""
	}
	path, _ := l.FeatureRecipeDocument(fr)
	return path
}

// RenderAll executes the full Step 5 pipeline:
//  1. Pass 1: Evaluate every node's locals, then its provides (nodes in any
//     order, each node self-contained)
//  2. For each node (sorted):
//     a. Pass 2: Pre-hydrate inject from provides
//     b. Build template context
//     c. Phase A: Execute injectMapping (Onion Model order)
//     d. Phase B: Execute valuesMapping → values dictionary
//     e. Helm render → K8s manifests
//  3. Collect all files
func RenderAll(ctx context.Context, input RendererInput, recipeLookup RecipeLookup, engineFactory EngineFactory) (*RenderResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Install this render's step-plugin retention before anything runs a
	// pipeline. The input travels by value, so every copy downstream carries the
	// same set and a plugin named at several sites is described once. It starts
	// empty and stays empty until a step asks for a descriptor, so installing it
	// crosses to no guest.
	input.stepDescriptors = newStepDescriptorMemo()

	graph := input.Graph
	result := &RenderResult{
		Files: make(map[string]AssembledFile),
	}

	// Resolve recipes per node
	nodeRecipes := make(map[string]*types.Recipe)
	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		if recipeLookup.Recipe != nil {
			if r, found := recipeLookup.Recipe(nodeID, node.Component); found {
				nodeRecipes[nodeID] = r
			}
		}
	}

	// Ask each node's own provider what name it will give the resources it renders
	// for that node, before anything is evaluated.
	//
	// It is here because the binding above is what selects a node's provider, so
	// this is the earliest point the question can be asked at all — and it has to
	// be asked before the provides pass below, because a producer's provides
	// templates are what publish an address built from the answer. One pass, one
	// computation site, and every later reader reads the same answer.
	renderedNames, err := CollectRenderedNames(ctx, graph, nodeRecipes, engineFactory)
	if err != nil {
		return nil, err
	}

	// Present what every plugin this render reaches reports about itself, with
	// the facts complete before any of them described itself answered by the
	// presentation the graph finalisation produced. Built here because this is
	// the first point holding every product it names: the shared plugin
	// retention installed above, the Recipe bound to each node, the names those
	// providers reported just above, and the engine factory the provider facts
	// read a self-report through.
	//
	// It describes nothing on its own. The retention is lazy, so a plugin is
	// loaded when a reader first asks about it and never because this
	// presentation exists.
	result.DescribedView = newDescribedView(
		ctx, graph, input.GraphView, nodeRecipes, recipeLookup, releaseOf(input.ResolveResult),
		input.stepDescriptors, input.PluginPathLookup, engineFactory, renderedNames,
		input.ArtifactLibraries, input.MappingLibraries, input.WorkloadLibraries,
	)

	// The rules declared over that presentation are invoked here, against the
	// coordinates the finalised graph carries extended by the ones this stage
	// adds. It is the first point holding both, and it is before the node walk
	// on purpose: every rule here decides whether a pipeline step may run at
	// all, so asking after the walk would report on a step that had already run.
	//
	// The enumeration is checked for absence here rather than inside: an absent
	// one held behind the axis interface is a present value that answers nothing,
	// and a rule invoked zero times reports exactly what a rule that ran and
	// approved reports.
	if describedAxes := newDescribedStageCoordinates(
		graph, input.GraphCoordinates, nodeRecipes, recipeLookup, input.ResolveResult,
	); describedAxes != nil {
		if err := runDescribedStageRules(
			ctx, result.DescribedView, describedAxes, input.PluginPathLookup != nil,
		); err != nil {
			return nil, err
		}

		// The rule over whether an attachment binding a FeatureRecipe that
		// generates once for a boundary has a boundary to generate for, invoked
		// against the same presentation and the same enumeration. It carries no
		// plugin-resolution guard, so it also reaches a render wired with no
		// plugin resolution, where the reading above is skipped entirely.
		//
		// It is asked ahead of the readings below because it is the coarsest of
		// them: those ask what a template may say, this asks whether the document
		// carrying those templates has a site to be evaluated at at all. It is
		// also ahead of the boundary-scope provides pre-pass and the node walk,
		// which are where such an attachment would otherwise be reached — and
		// reported there as a boundary projection the render could not find,
		// naming the consequence rather than the authoring mistake.
		if err := runBoundaryScopeAttachmentRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rule over what a Recipe wrote for each of its provides keys, invoked
		// against the same presentation and the same enumeration. It is a separate
		// call rather than a member of the reading above because it carries no
		// plugin-resolution guard: nothing about a provides template depends on a
		// plugin, so a render that resolved none still owes this refusal.
		//
		// It is asked here, ahead of the pass below, for the reason the rule reads
		// authored text at all: the value a refused template would produce is never
		// produced, so the refusal has to be reported before evaluation rather than
		// from it.
		if err := runAuthoredProvidesRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rule over a provides entry that reads the name its target gives the
		// resources it renders for the node, asked against the same presentation and
		// the same enumeration and immediately after the reading above.
		//
		// It is here rather than at evaluation because the name is a plain string
		// on the surface: a node whose provider reports none would render the
		// reference away and publish an address naming a resource nothing emitted,
		// at exit zero. Whether such a name exists is settled by the Recipe
		// binding above, so the refusal is over a fact rather than a guess.
		if err := runRenderedNameRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rules over an attachment template that reads its callers' render facts
		// — while its FeatureRecipe does not declare it, or in a pipeline step, which
		// never sees the callers — asked against the same presentation and the same
		// enumeration. They are here, ahead of the node walk, because the declaration
		// one of them reads is what decides where in the walk such an attachment
		// renders — so a refusal is reported against the field the author wrote
		// before anything renders, rather than after.
		if err := runCallerRenderFactsRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// Their twins over an attachment template that reads the render facts of the
		// components its host is wired to, asked against the same presentation and
		// the same enumeration, immediately after, for the same reason: the
		// declaration one of them reads decides where in the walk the attachment
		// renders, so the refusal reaches the author before anything renders.
		if err := runTargetRenderFactsRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rules over the text a Recipe wrote for each of its locals, asked here
		// for the same reason and against the same presentation. They are asked
		// AFTER the provides reading and BEFORE the pass below, and both halves of
		// that placement are deliberate: the provides reading is about a template
		// reaching outside its data at all, which is the coarser fault, while these
		// are about which sibling inside the block a template may read — and the
		// pass below is where the block would otherwise resolve a reference these
		// refuse into an empty string.
		if err := runAuthoredLocalsRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rule over the source entry a node's own Recipe writes, asked against
		// the same view and the same enumeration.
		//
		// It is asked here, ahead of the node walk, because the walk reads that
		// entry as its first act and reads it as the author wrote it — so a build
		// meeting the ambiguity partway through would have rendered every earlier
		// node first. It is asked ahead of the import readings below for the same
		// reason they are ordered among themselves: a node's source is decided
		// before its values mapping and its workload are, so an author meeting
		// several faults meets them in the order the render decides them.
		if err := runRecipeSourceRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rules over the library imports a node's own artifacts block writes,
		// asked against the same presentation and the same enumeration.
		//
		// They are asked here, ahead of the node walk, because that is where such
		// an import is resolved: a reference naming a library nothing declares
		// would otherwise reach the artifacts walk and halt from it, after every
		// earlier node had rendered and reported one entry at a time. Every fact
		// they read is complete once the Recipe is bound — the reference the author
		// wrote, the library set the load resolved, and the files the bundle holds
		// — so nothing is gained by waiting and a whole render is spent by it.
		if err := runArtifactsEntryImportRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rules over the values-mapping library import a node's own Recipe
		// writes, asked against the same presentation and the same enumeration.
		//
		// They are asked here, ahead of the node walk, because that is where such
		// an import is resolved: a reference naming an entry no library declares
		// would otherwise be met partway through the walk, after every earlier node
		// had rendered. Both facts they read — the reference the author wrote and
		// the library set the load resolved — are complete once the Recipe is
		// bound, so nothing is gained by waiting and a whole render is spent by it.
		if err := runValuesMappingImportRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rules over the workload library import a node's own Recipe writes,
		// asked against the same presentation and the same enumeration.
		//
		// They are asked here, ahead of the node walk, for the reason the values
		// mapping beside them is: a reference naming an entry no library declares
		// would otherwise be met partway through the walk, after every earlier node
		// had rendered — and later within a node than the values mapping, since a
		// workload is resolved after it. Both facts they read — the reference the
		// author wrote and the library set the load resolved — are complete once the
		// Recipe is bound.
		if err := runWorkloadImportRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rules over the inline template fields a boundary-declared
		// attachment's FeatureRecipe writes, asked against the same presentation
		// and the same enumeration.
		//
		// They are asked here, ahead of the boundary-scope provides pre-pass and
		// the node walk that follow, because that is where those fields are
		// evaluated: a reference the render context does not define would
		// otherwise reach the pre-pass and resolve through the missing-key policy
		// into an empty value in emitted output. Asking before evaluation is the
		// whole reason a rule reads authored text.
		if err := runAuthoredTemplateFieldRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rules over the path templates each boundary of the Release
		// writes, asked against the same presentation and the same enumeration.
		//
		// They are asked here, ahead of the boundary directory walk and the node
		// placement that follow, because that is where those templates are
		// evaluated: a reference the surface does not admit would otherwise reach
		// the walk and halt from it, reported against the scope's name with no
		// position inside the document and only for the first template reached.
		// Asking before evaluation is the whole reason a rule reads authored text.
		if err := runAuthoredPathTemplateRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The rules over a Recipe or FeatureRecipe that declares omit entries whose
		// provider does not declare it honours them, asked against the same
		// presentation and the same enumeration.
		//
		// They are asked here, ahead of every render dispatch, because what they read
		// is settled once the documents are bound and each provider has described
		// itself — and a provider that leaves nothing out would otherwise render the
		// very documents the author asked to leave out. They come after the other
		// refusals above, so a build carrying one of those faults reports it first.
		if err := runOmitsProviderRule(ctx, result.DescribedView, describedAxes); err != nil {
			return nil, err
		}

		// The advisory over the two providers a connection's ends render through,
		// invoked against the same presentation and the same enumeration. It is
		// asked here because both providers and both capability claims are settled
		// once every plugin has described itself, and nothing the render does
		// afterwards changes either — so a connection no provider can wire natively
		// is reported before the output produced under that fallback exists.
		//
		// It joins the render's warning stream rather than its error, which is the
		// rule's own declaration: the connection carries the scalar value the
		// injection contract pipes, so the deployment works and the build finishes.
		linkProviderWarnings, err := runCrossBoundaryLinkProviderRule(
			ctx, result.DescribedView, describedAxes,
		)
		if err != nil {
			return nil, err
		}
		result.Warnings = append(result.Warnings, linkProviderWarnings...)
	}

	// What each Component type and each Feature type declares sensitive, read once
	// per type for this render through one memoised reader: the schema compile
	// behind each answer is uncached, and every node or attachment of one type reads
	// the same answer. The memo's lifetime is this render, so a catalog change
	// between builds is never carried over.
	//
	// It is read here, ahead of the first template this render evaluates, because a
	// template's failure is withheld by these declarations: a locals or provides
	// template, an assertion and every later surface report a failure without a
	// value an author declared sensitive, so the declarations must be in hand before
	// the first of them runs.
	//
	// The Feature reader is installed on the travelling input rather than kept as a
	// local, because the same answer is read at sites a long way apart: the
	// withholding sets assembled below, and every attachment's own render dispatch,
	// reached from deep inside the per-node walk. One reader answers all of them, so
	// they cannot come to disagree about what a Feature declares, and one memo serves
	// them all.
	readers := newDeclarationReaders(input.CatalogLookup)
	input.declaredFeatureLeaves = readers.feature
	// Each node's own declarations — its Component's, those of the Features attached
	// to it, and which of its Recipe's locals and source parts are computed from a
	// declared leaf — read from the bound Recipe as the catalog holds it, BEFORE
	// anything replaces a local copy's source with its resolved value: the reading is
	// over authored template text, and resolved text carries no reference to read.
	declarations := readRefusalDeclarations(graph, nodeRecipes, readers)

	// Pass 1: per node, resolve locals then provides. The locals travel with the
	// provides so every downstream reader — Step 5b assertions and Phase B.0 —
	// consumes one evaluation rather than repeating it.
	allProvides, allLocals, err := EvaluateAllProvides(graph, nodeRecipes, renderedNames, declarations)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "locals and provides evaluation")
	}

	// Install this render's retention of per-node mid-render state, holding the
	// resolved provides by reference: they are evaluated once above for the whole
	// render and are not written to afterwards, so a per-node copy of them would
	// carry nothing the one bag does not. It is installed here rather than at the
	// top because that bag is its first content, and it is placed on both the
	// travelling input and the result — the input reaches the recording sites, the
	// result outlives the node walk.
	input.hydrated = newHydratedProjections(allProvides)
	result.hydrated = input.hydrated

	// Install this render's retention of what each node's artifacts walk projects.
	// It is installed beside the retention above and travels the same way — onto
	// the travelling input, which reaches the recording site inside the walk, and
	// onto the result, which outlives the node walk. It starts empty because its
	// first content is produced by the walk rather than before it.
	input.artifacts = newArtifactProjections()
	result.artifacts = input.artifacts

	// Install this render's retention of what each node's own render emits. It is
	// installed beside the retentions above and travels the same way — onto the
	// travelling input, which reaches the recording site inside the node walk, and
	// onto the result, which outlives that walk. It starts empty because its first
	// content is produced by the walk rather than before it.
	input.emitted = newEmittedProjections()
	result.emitted = input.emitted

	// Present the state each node reaches before its artifact bytes exist, with
	// the facts complete earlier answered by the presentation built above. Built
	// here because this is the first point holding every product it names: the
	// retention installed on the line before, and the earlier presentation it
	// extends. It holds the retention by pointer, so it answers from whatever the
	// node walk has recorded by the time a reader asks — the walk fills the
	// retention this presentation reads, rather than this presentation waiting on
	// the walk.
	result.HydratedView = newHydratedView(result.DescribedView, input.hydrated)

	// Present what each node's own render emits, with the facts complete earlier
	// answered by the presentation built above. Built here rather than after the
	// node walk for the reason that one is: it holds the retention by pointer, so
	// it answers from whatever the walk has recorded by the time a reader asks.
	result.EmittedView = newEmittedView(result.HydratedView, input.emitted)

	// The output contract each node's component type declares, retained per node
	// rather than recomputed later: it is what the consumer-context projection
	// narrows each target's provides bag to, so a consumer reads the Component's
	// published contract instead of whatever its Recipe happens to emit. Retaining
	// it here is what lets the projection stay catalog-free — it receives the
	// finished set as data. Every `continue` below leaves a node OUT of the set,
	// which the projection reads as "do not narrow"; that is the intended reading,
	// not an oversight (see NodeDeclaredProvides).
	//
	// The producer-fulfilment reading over this retention — the bound Recipe must
	// publish every key the contract declares — is the declared rule invoked
	// below. It is not decided here: what this loop produces is the contract, and
	// the two absences it passes over are properties of that axis rather than
	// verdicts, which is why they read the same way to the projection and to the
	// rule.
	declaredProvides := NodeDeclaredProvides{}
	if input.CatalogLookup.Component != nil {
		for _, nodeID := range nodeIDs {
			recipe := nodeRecipes[nodeID]
			if recipe == nil {
				continue
			}
			node, _ := graph.Spec.Nodes.Get(nodeID)
			comp, found := input.CatalogLookup.Component(node.Component)
			if !found {
				continue
			}
			declaredProvides[nodeID] = resolver.ComponentProvidesKeys(comp, input.CatalogLookup.SchemaLibraries)
		}
	}

	// Producer-fulfilment (Component↔Recipe), asked of the presentation of what
	// the render resolved for each provides key. The binding is a render-time fact
	// (Manifest-driven), so the reading runs here where the resolved bag exists —
	// a shortfall halts loudly rather than delivering nothing under the missing key
	// at inject/render time and leaving the consumer's reference silently unset.
	if err := runProvidesContractRule(ctx.Err, input.GraphView != nil, result.HydratedView, declaredProvidesContract{
		nodeOrder: nodeIDs,
		declared:  declaredProvides,
		recipes:   nodeRecipes,
	}); err != nil {
		return nil, err
	}

	// Step 5b: Recipe / FeatureRecipe assertions.
	if err := EvaluateRecipeAndFeatureRecipeAssertions(graph, nodeRecipes, recipeLookup, allProvides, allLocals, declarations); err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "Recipe/FeatureRecipe assertions")
	}

	dispatch := map[dispatchKey]*pendingDispatch{}
	orderedDispatch := []dispatchKey{}

	// Per-node dispatch identity and engine, recorded during the per-node loop so
	// the post-assembly cross-boundary link pass can classify each uses edge by
	// its endpoints' dispatch groups and reach each endpoint's provider engine.
	// A uses edge whose endpoints resolve to different dispatch keys crosses a
	// boundary; the pass reads these maps to route the crossing and to project the
	// consumed end's resolved annotations into the per-edge payload.
	nodeDispatch := map[string]dispatchKey{}
	nodeEngine := map[string]Provider{}
	nodeAnnotations := map[string]map[string]string{}

	// Deferred-PATCH accumulator, keyed by the dispatch group of the assembler
	// node. A pipeline against an assembler node cannot PATCH per-node — the
	// merged documents are not discrete until assembly. This covers both scopes:
	// a component-scope FeatureRecipe whose host's provider is an assembler (its
	// /host pipeline) and a Recipe (or component-scope FeatureRecipe) whose own
	// provider is an assembler (its /own pipeline). Each such pipeline is recorded
	// here and replayed in the post-assembly phase against the assembled
	// documents. The records carry the A1 ordering keys (node author order,
	// FeatureOrder index) so the replay layers deterministically.
	deferredByDispatch := map[dispatchKey][]DeferredPatch{}

	// Pre-compute the per-boundary evaluated `directory:` map for the
	// .parent_boundary.directory cascade. Path-template eval at any
	// boundary depth can read its IMMEDIATE parent's evaluated directory
	// from this map; root-level boundaries have no parent and surface
	// missingkey=error if their templates reference .parent_boundary.*.
	var boundaryDirEvals map[string]string
	var perBoundaryAnnotations map[string]types.OrderedMap[string]
	var releaseParameters map[string]any
	if input.ResolveResult != nil {
		releaseParameters = input.ResolveResult.Parameters
		_, perBoundaryAnnotations = resolver.WireBoundaryInfra(types.NormalizeRelease(input.ResolveResult.Release))
		var err error
		boundaryDirEvals, err = evaluateAllBoundaryDirectories(input.ResolveResult.Release, releaseParameters, perBoundaryAnnotations)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{}, "boundary directory pre-eval")
		}
	}

	// Boundary-scope FR cross-node accumulator (dedup, provides, files,
	// validator cache). One instance lives per RenderAll invocation;
	// phaseB6 threads it into the executionScope: boundary dispatch path.
	// See boundary_fr.go.
	boundaryFRState := NewBoundaryFRState()

	// Provides pre-pass: evaluate every boundary-scope FeatureRecipe's
	// provides before the per-node loop so a per-component consumer's uses
	// edge can resolve them during Phase A inject rehydration. The per-node
	// Phase B.6 dispatch (which renders the FR's files) re-evaluates the same
	// provides idempotently. See PrepopulateBoundaryScopeProvides.
	if input.ResolveResult != nil {
		if err := PrepopulateBoundaryScopeProvides(ctx, graph, input.ResolveResult.Release, input.ResolveResult.BoundaryAnnotations, recipeLookup, allProvides, engineFactory, boundaryFRState, readers.feature); err != nil {
			return nil, err
		}
	}

	// Trace writer (no-op when --trace is off; async filesystem writer
	// when on). Per-pass capture surfaces happen at four representative
	// sites in this revision: Phase A injectMapping output, Phase B.4
	// Workload synthesis output, Phase B.6 boundary-scope FR dispatch,
	// and per-emission provenance-label routing. Other sub-passes wire
	// in a follow-up.
	traceWriter := input.Trace
	if traceWriter == nil {
		traceWriter = trace.New("", input.TraceMode)
	}

	// Declared-sensitive attachment slots per Component type, memoised the same
	// way and for the same reason as the two declaration readers above. The read
	// goes through the one home for that declaration, so this reader and the
	// per-attachment fact the IR graph carries cannot disagree about what a slot
	// declares.
	sensitiveSlotsByType := map[string]map[string]bool{}
	sensitiveSlots := func(componentType string) map[string]bool {
		if s, cached := sensitiveSlotsByType[componentType]; cached {
			return s
		}
		slots, _ := ir.ComponentAttachmentSlots(input.CatalogLookup, componentType)
		marked := ir.SensitiveSlots(slots)
		sensitiveSlotsByType[componentType] = marked
		return marked
	}

	// The build's own description of each node, indexed once for the walk below.
	// A rule invoked per node needs the node as the enumeration describes it —
	// its authored name and the subsystem path it was reached through are what a
	// finding's position is assembled from — and the enumeration answers with a
	// slice, so indexing it here is what keeps the walk from scanning it per node.
	// An absent enumeration leaves the map empty, and a rule handed a zero
	// instance is not invoked; that is checked where the rule is asked.
	nodeInstances := map[string]constraint.NodeInstance{}
	if input.GraphCoordinates != nil {
		for _, instance := range input.GraphCoordinates.Nodes() {
			nodeInstances[instance.ID] = instance
		}
	}

	// What each node's renders reported, as the node's own turn of the walk left
	// them, keyed by node. An attachment held for the pass after the walk reads its
	// callers' records from here.
	nodeRenderFacts := map[string]renderFactsRecord{}
	// The nodes carrying an attachment held for the pass after the walk, in walk
	// order, each with what its own turn captured for that attachment's render.
	var heldHosts []heldAttachmentHost

	// placeAttachmentOutput places what one run of a node's attachment render
	// produced into the node's dispatch group. It runs once in the node's own turn
	// and, for a node carrying an attachment held for the pass after the walk, once
	// more there — so an attachment rendered in that pass is placed by exactly the
	// routing an attachment rendered in its host's turn is.
	placeAttachmentOutput := func(
		key dispatchKey, nodeID string, nodeOrder int, state *PhaseBState, nodeCtx NodeRenderContext, out attachmentOutput,
	) error {
		// Record this contributor's derived-name declarations against the group its
		// output merges into, under the same authoring-level producer name a path
		// collision reports, so both collision reports point at something the author
		// can find in their own input. The check itself runs once per group, after
		// every contributor has rendered.
		//
		// Read AFTER the attachments whose output this places have rendered, not
		// before them, and the position is the whole correctness of it: this moves
		// the node's accumulator, and an attachment's render declares into that same
		// accumulator — its guest's declarations through this node's sink, and the
		// component identity the compiler derives for it directly. Reading the
		// accumulator earlier moves it before any of that arrives, which drops every
		// attachment's declaration silently: the group check then reports nothing,
		// and reads as coverage. It is the same snapshot-before-attachments hazard the
		// host-document write-back below is keyed by path to survive.
		//
		// The move empties the accumulator, which is what lets this run twice for one
		// node: an attachment held for the pass after the walk declares into the
		// emptied accumulator, so the second placement carries its declarations and
		// nothing the first already moved.
		//
		// Every producer within the node arrives under its own label rather than all
		// of them under the node's: the node's own render, and each attachment it
		// carries under the key the author wrote it beneath. Two attachments
		// reaching one name are then two declarers, which is what they are.
		state.DerivedKeys.into(&dispatch[key].derivedKeys)
		// Stash deferred host PATCHes under the host component's dispatch group.
		// The host node IS this node (a component-scope FR patches the component
		// it is attached to), so the merged documents the PATCH targets are the
		// ones this dispatch group's assembler produces. Stamp the host-node
		// author-order key for the A1 replay ordering.
		for i := range out.deferred {
			out.deferred[i].HostNodeOrder = nodeOrder
			deferredByDispatch[key] = append(deferredByDispatch[key], out.deferred[i])
		}
		for path, content := range out.files {
			dispatch[key].files[nodeID][path] = content
		}
		// Host-document write-back conduit: a component-scope FR pipeline's
		// /host PATCH (a rewritten host doc) or /host EMIT (a new host doc)
		// arrives keyed by the host doc's original emitted path. The host docs
		// were snapshotted into dispatch[key].files[nodeID] before phaseB6 ran,
		// so this merge — keyed by path — is the only live write-back path;
		// mutating state.EmittedFiles would be inert. A PATCH overwrites the
		// snapshotted host doc; an EMIT adds a new entry.
		for path, content := range out.hostPatched {
			dispatch[key].files[nodeID][path] = content
		}
		// Route each attachment into this node's dispatch group as a contributor of
		// its own, so the group's assembler is told about it alongside the
		// components. Every attachment the sub-pass reports is routed, whichever way
		// its output travelled, because the contributor list is a property of the
		// group: an attachment absent from it is one no assembled artifact can name,
		// even where its files sit in the group's own output.
		//
		// Output handed over joins the intermediates map rather than the placed one,
		// which is the same split the host component's own emission takes: the
		// intermediates map is the half an assembler folds, and the placed half is
		// also the half the flush writes at the group's own position — so content
		// placed there would be delivered twice, once inside the assembled document
		// and once beside it. An attachment that wrote its own files hands over
		// nothing and adds no entry to either map; what it adds is where it wrote
		// them, which is the only record of a position the group holds under the
		// host's key rather than the attachment's.
		//
		// The coordinates are STATED here from the values the render carried out,
		// never recovered from the key. The key is a routing marker joining two
		// authored coordinates with characters either coordinate may itself contain,
		// so cutting it apart cannot return what went in. The annotations and the wave
		// are the host node's own: an attachment renders into the host's boundary and
		// its output lands in the host's group, so the scope it is assembled under is
		// the host's — and these are the same values its render was handed, which is
		// what keeps one contributor from being described two ways on the two
		// crossings.
		for _, contrib := range out.contributions {
			// Two attachments reaching one dispatch identity halt rather than merge,
			// the same guardrail the boundary-scope routing carries and for the same
			// reason: the identity joins the host to the instance with a character
			// neither part is constrained to exclude, so an attachment named `a@b` on
			// host `c` and one named `a` on host `b@c` spell one identity — and the
			// map would then take the second silently, folding two contributors'
			// fragments into one with nothing in the output recording that two were
			// declared.
			if _, alreadyHere := dispatch[key].nodeProducer[contrib.nodeID]; alreadyHere {
				inner := fmt.Errorf("%w: dispatch path %q provider %q already holds output from %s, which reaches the same dispatch identity as %s; two contributors to one assembly group must be distinguishable",
					ErrInstanceIDCollision(), key.Path, key.Provider,
					nodeProducerLabel(dispatch[key].producerOf(contrib.nodeID)), nodeProducerLabel(contrib.producer))
				return cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{}, inner.Error())
			}
			dispatch[key].nodeProducer[contrib.nodeID] = contrib.producer
			dispatch[key].nodeNameBasis[contrib.nodeID] = contrib.nameBasis
			dispatch[key].nodeAnnotations[contrib.nodeID] = nodeCtx.Annotations
			dispatch[key].nodeSyncWave[contrib.nodeID] = nodeCtx.SyncWave
			if len(contrib.fragments) > 0 {
				dispatch[key].intermediates[contrib.nodeID] = append(dispatch[key].intermediates[contrib.nodeID], contrib.fragments...)
			}
			if len(contrib.files) > 0 {
				// Keyed under the ATTACHMENT's own key rather than the host's, which
				// is what puts its files at a position of their own — the group
				// composes that position from this contributor's coordinates, so the
				// attachment lands beside the host it is attached to rather than
				// somewhere below it.
				//
				// Into BOTH maps, exactly as a component's own render is: the path map
				// is what the group's flush writes, and the placed list is what the
				// group's assembler is handed as this contributor's rendered output.
				// A contributor present in only the first is written to disk and
				// invisible to the assembler — so an assembler deriving anything from
				// what a contributor rendered, its declared name among it, would
				// derive it from an empty set and fall back to the routing key.
				nodeFileMap := make(map[string][]byte, len(contrib.files))
				for _, f := range contrib.files {
					nodeFileMap[f.Name] = f.Body
				}
				dispatch[key].files[contrib.nodeID] = nodeFileMap
				dispatch[key].placed[contrib.nodeID] = append(dispatch[key].placed[contrib.nodeID], contrib.files...)
				// And named among the group's children, so an assembled artifact
				// that addresses its children by name addresses this one. Placing
				// the files is only half of being discovered: a target that reads a
				// declared list of children reads that list, so files at a position
				// no entry names are files nothing loads. This is the entry.
				//
				// It carries NO source and NO version, and neither is an omission.
				// Both are properties of what this contributor RENDERED, and what it
				// rendered is already in the group's hands — an assembler that names
				// children from a manifest reads the manifest this contributor
				// placed. A source restated here would be resolved against a
				// different moment than the render that placed the files.
				//
				// It carries the values this attachment's own valuesMapping resolved
				// to, which is what the address the entry establishes exists to
				// deliver. They are needed here and nowhere else: an attachment on a
				// group that places its contributors is one whose files were vendored
				// in verbatim from what its author shipped, so nothing wrote the
				// resolved values into them and the assembled artifact is the only
				// surface left that can supply them. Without them the placed content
				// renders whatever defaults its author shipped and reports success,
				// which is the same reading-nothing outcome as files at a position no
				// entry names — one level further in.
				dispatch[key].subCharts = append(dispatch[key].subCharts, SubChartInfo{
					InstanceID: contrib.nodeID,
					Values:     contrib.values,
				})
			}
		}
		return nil
	}

	// completeNodeRender ends one node's render once its last attachment has
	// rendered: the node's per-pass display capture, the drain of what its renders
	// raised into the build's streams, and its emission records. For a node carrying
	// an attachment held for the pass after the walk that is in that pass, so what
	// the held attachment raised is drained with the rest of its host's.
	completeNodeRender := func(
		state *PhaseBState, tmplCtx *types.TemplateContext, nodeCtx NodeRenderContext, nodeID, boundaryPath string,
	) {
		// Trace point: Phase B.6 — FeatureRecipe Resource Generation. The
		// snapshot captures the post-B.6 TemplateContext for this node;
		// boundary-scope FR contributions deduplicated by Phase B.6 surface
		// in boundaryFRState.Files (post-loop, not per-node here).
		state.capture(trace.PassPhaseB6, "", tmplCtx)

		// Drain Phase B's per-node warnings into the build-level result.
		// Drain position is the end of the per-node Phase B sequence
		// (after phaseB6 — the LAST sub-pass) so any sub-pass appending
		// to state.Warnings reaches result.Warnings, not just B.3's
		// helm-chart provenance-skip notice today.
		result.Warnings = append(result.Warnings, state.Warnings...)
		result.Diagnostics = append(result.Diagnostics, state.Diagnostics...)

		// Trace point: per-node emission log (slot 11). Captures the
		// post-Phase-B node summary — the warnings accumulated during the
		// per-node sub-pass run plus the final emitted-files inventory.
		emittedNames := make([]string, 0, len(state.EmittedFiles))
		for _, f := range state.EmittedFiles {
			emittedNames = append(emittedNames, f.Name)
		}
		emissionLog := map[string]any{
			"node_id":       nodeID,
			"boundary_path": boundaryPath,
			"warnings":      state.Warnings,
			"emitted_files": emittedNames,
		}
		_ = traceWriter.Write(trace.PassEmission, nodeID, boundaryPath, "", emissionLog)

		// Trace point: per-emission provenance routing summary. Captures
		// the per-node label and annotation set applied to emitted files
		// at the post-render handoff. Surfaces the canonical aggrega.ai/*
		// labels and the argocd.argoproj.io/sync-wave annotation each
		// node's emission carries.
		labelsSummary := map[string]any{
			"node_id":             nodeID,
			"boundary_path":       boundaryPath,
			"provenance_labels":   buildProvenanceLabels(state, nodeCtx),
			"provenance_annots":   buildProvenanceAnnotations(state, nodeCtx),
			"emitted_files_count": len(state.EmittedFiles),
		}
		_ = traceWriter.Write(trace.PassLabels, nodeID, boundaryPath, "", labelsSummary)
	}

	// Per-node rendering. nodeIDs is sorted (topology author order for the
	// deterministic walk); nodeOrder is the host node's position in that order,
	// the outer A1 replay-ordering key for deferred host PATCHes.
	for nodeOrder, nodeID := range nodeIDs {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		recipe := nodeRecipes[nodeID]
		// This node's own declarations, read above for the whole render.
		nodeDeclared := declarations.node(nodeID)

		// Pass 2a: Self-inject resolution (component-level inject)
		inject, injectRaw := BuildInjectPipe(node.SelfInject)
		if node.SelfInject != nil {
			siCtx := SelfInjectContext{
				Values: tmplCtxValues(graph, nodeID),
				Infra:  tmplCtxInfra(graph, nodeID),
				Context: SelfInjectContextBlock{
					InstanceID: encodeNodeIdentity(nodeID),
					Labels:     node.Labels,
					Annotations: &types.AnnotationBuckets{
						Catalog:   node.CatalogAnnotations,
						Release:   node.ReleaseAnnotations,
						Aggregate: node.AggregateAnnotations,
					},
				},
			}
			if err := ExecutePass2a(&inject, node.SelfInject, nodeID, allProvides[nodeID], siCtx, nodeDeclared.pillarKeys()); err != nil {
				return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
			}
		}

		// Pass 2b: Rehydrate inject from uses-edge provides
		RehydrateInjectFromProvides(&inject, graph.Spec.Edges, nodeID, allProvides, graph, recipeLookup, boundaryFRState)

		// Attachment-level inject (feature.inject / secret.inject): resolve each
		// authored delivery key's provides-key reference against the attachment's
		// own resolved provides and deliver into .context.inject, before Phase A
		// merges each FeatureRecipe's injectMapping overlay over it.
		//
		// Delivery is also where the sensitive-by-value advisories are collected:
		// the delivery site is the only place that knows a marked key actually
		// received a literal. They are held unattributed until the node's target
		// is resolved below, then located and handed to the same out-of-band
		// stream the render diagnostics use.
		var sensitiveByValue []Diagnostic
		injectDeliveries, err := ResolveAttachmentInject(&inject, graph, nodeID, renderedNames[nodeID], recipeLookup,
			func(attachment, mechanism, deliveryKey string) {
				sensitiveByValue = append(sensitiveByValue, SensitiveByValueDiagnostic(attachment, mechanism, deliveryKey))
			}, readers.feature)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q attachment inject", nodeID))
		}

		// Retain what every surface asked this node's pipe for, then ask the rule
		// declared over that record whether any attachment took a key another
		// source had already delivered under. The recording is unconditional and
		// the reading is immediately after it: this is the point the pipe is
		// complete and nothing has read it yet, so a duplicate is refused before
		// any output is rendered against an ambiguous key.
		input.hydrated.recordNodeInjectDeliveries(nodeID, injectDeliveries)
		if err := runNodeInjectDeliveryRule(
			ctx.Err, result.HydratedView, input.GraphCoordinates, nodeInstances[nodeID],
		); err != nil {
			return nil, err
		}

		// Resolve every attachment's bound-FeatureRecipe provides ONCE (a single
		// bag for the node) and expose it to the consumer as
		// .features.<name>.provides — or, for an attachment one of the node's
		// connections carries, as .context.uses.<alias>.features.<slot>.provides.
		// The same bag is retained on state below and reused at Phase B.6 as the
		// attachment FeatureRecipe's own .provides, so the consumer's read and the
		// generated resource derive from one evaluation (no drift). Resolution also
		// halts a bound FeatureRecipe that does not fulfil its Feature's provides
		// contract. It reads nothing the template context carries, so it runs
		// first and the context is assembled with the bag already in hand.
		attachments, _ := graph.Spec.FeatureValuesMapping.Get(nodeID)
		attachmentProvides, err := ResolveAttachmentProvides(attachments, nodeID, renderedNames[nodeID], recipeLookup, input.CatalogLookup, readers.feature)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q attachment provides", nodeID))
		}

		// Build template context
		tmplCtx := AssembleNodeContext(nodeID, graph, allProvides, declaredProvides, inject, injectRaw, renderedNames, attachmentProvides)
		if input.ResolveResult != nil && input.ResolveResult.Release != nil {
			AssembleNodeContextWithRelease(tmplCtx, input.ResolveResult.Release)
		}

		// The node's redaction set, assembled once here where the catalog, the
		// node's attachments and the bound Recipe's authored template text are all
		// in scope, and passed down to every display capture rather than re-derived
		// at each one: the trace package sees a set of keys, never a catalog.
		//
		// The derived reading is static analysis over authored text, so it needs no
		// resolved value and has no ordering relationship to the evaluation — which
		// is why it belongs beside the declarations rather than inside the locals
		// pass, where the catalog is not in scope.
		//
		// This set is consumed by the per-pass capture and by nothing else — every
		// read of it below, and every read of the copy on the Phase B state, is an
		// argument to a capture projection. That is what bounds the capture mode's
		// reach to the capture: it can empty this set without any other surface
		// that withholds a declared-sensitive datum being able to observe the
		// difference.
		// A uses edge carries its TARGET's resolved provides into this node's
		// display — published under the edge, and delivered onto this node's own
		// delivery keys where the edge's inject mapping routes it — so both are
		// governed by the target's declarations and not this node's. Reading them
		// costs one more call to the same memoised declaration reader, against the
		// target Component the edge names. The attachments written on a connection
		// ride the same edge into the display and are governed by their Features, so
		// they are read through the one Feature reader every attachment's
		// declarations come from.
		declared := nodeDeclared.component
		crossNode := usesWithheld(tmplCtx.Context.Uses, func(componentType string) map[string]bool {
			return readers.component(componentType).Provides
		}, readers.feature)
		// Read from the bound Recipe as the catalog held it, before anything replaced
		// the local copy's source with the resolved value: the reading is over
		// authored template text, and resolved text carries no reference to read.
		derived := nodeDeclared.derived
		// ONE assembly site for this node, reached twice. The FeatureRecipe route
		// cannot be read until Phase A has rendered the mappings — a mapping's
		// delivery key is itself a template — so the set is assembled once for the
		// captures that run before Phase A, where those keys are not on the pipe yet,
		// and once after with that route folded in. Both go through the same call, so
		// neither can carry a route the other does not.
		// The node's own component-level self-inject is the one route whose material
		// and governing declaration belong to the same node, so it is read against
		// this node's declarations rather than a target's or an attachment's.
		selfInjectKeys := selfInjectWithheld(node.SelfInject, declared.Provides, declared.Values, declared.Infra)
		// The boundary consumer-env channel's values are computed inside a provider
		// guest and carried here uninterpreted, so the host can see which keys
		// arrived and not what any of them was computed from. That is the untraced
		// condition itself rather than an unwired route, so the keys feed the
		// fail-safe argument rather than a reading that could not exist.
		untracedKeys := boundaryEnvUntraced(boundaryFRState.ConsumerEnvOverrides[nodeID])
		nodeKeys := func(injectMapping redact.DeliveryKeys) redact.Keys {
			return redact.NodeKeys(
				declared.Values, declared.Infra,
				attachments,
				crossNode,
				injectMapping,
				selfInjectKeys,
				untracedKeys,
				derived)
		}
		// Two sets come out of the one assembly, and they are one set: the set a
		// failure's text is withheld by, and that same set passed through the trace
		// mode for the capture, whose escape hatch empties it for one invocation.
		nodeSets := func(injectMapping redact.DeliveryKeys) (capture, failure redact.Keys) {
			return nodeDeclared.withheldSets(nodeKeys(injectMapping), input.TraceMode)
		}
		redactKeys, withheldKeys := nodeSets(nil)

		// Phase B sub-pass pipeline per chapter 16 §5. State carries cross-
		// sub-pass accumulators (resolved values, parsed workload, emitted
		// files) and the document this node's Recipe was declared in, which is
		// both what a relative source produced by a template is resolved against
		// and what a diagnostic raised inside the sub-passes names as its
		// location. An entity carrying no document on disk reports none, and the
		// sub-passes treat that as the absence it is.
		//
		// It is constructed ahead of this node's FIRST capture, not at Phase B, so
		// that every capture for this node routes through the one projection call
		// the state owns. A capture taking its own route would be free to show the
		// emitted workload document where the display promised a re-render of it.
		state := NewPhaseBState(recipe, nodeID, node.BoundaryPath, recipeLookup.recipeDocument(recipe))
		// The pins the load recorded for that same document, resolved here so the
		// sub-passes below read one answer rather than each keying into the lookup
		// for itself. A node whose Recipe was declared in no document on disk
		// resolves to the zero pins, which record nothing.
		state.RecordedPins = input.RecordedPins.For(state.RecipePath)
		state.Trace = traceWriter
		// The retentions travel onto this node's own state so the artifacts walk can
		// record each entry's template text where that text is resolved, and each
		// entry's evaluated bytes where the entry becomes part of what the render
		// projects. An attachment's render builds its own state and receives
		// neither.
		state.hydrated = input.hydrated
		state.artifacts = input.artifacts
		// The presentation over the first of those retentions travels with it, so
		// the rule declared over the mid-render facts can be asked about an entry
		// inside the same walk that records it. It is set here rather than derived
		// at the asking site because it is built once for the whole render, above,
		// and holds the retention by pointer — so one presentation answers every
		// node from what the walk has recorded by then.
		//
		// It is set beside the retention deliberately: the walk that records the
		// facts is exactly the walk that may read them, and separating the two
		// would let a state record facts nothing can then be asked about, which
		// reads as a rule that ran and approved.
		state.hydratedFacts = result.HydratedView
		// The emitted retention and its presentation travel the same way, and for a
		// narrower reason: a node's own emission is recorded by this orchestrator
		// directly, so what these reach is the owner an attachment's render is
		// given, which is built from this state and must carry both stages' products.
		state.emitted = input.emitted
		state.emittedFacts = result.EmittedView
		// Whether the CALLER supplied the presentation this chain extends, so a
		// halting rule over an attachment's emission can tell a render asked for
		// without one from a chain that broke in this package. Every build path
		// supplies it; a caller assembling a render's input itself may not.
		state.callerPresentationSupplied = input.GraphView != nil
		// The state's copy is the authoritative set for this node: a Phase B
		// sub-pass records the workload display decision onto it, and a capture
		// reading a copy taken earlier would show a document the decision said to
		// substitute or withhold.
		state.Redact = redactKeys
		// The set a failure's text is withheld by, from the same call as the capture's
		// and re-assembled with it after Phase A.
		state.Withheld = withheldKeys

		// Trace points: per-node pre-loop captures.
		state.capture(trace.PassPillarsInitial, "", tmplCtx)
		state.capture(trace.PassProvides, "", tmplCtx)
		state.capture(trace.PassInject, "", tmplCtx)

		state.MappingLibraries = input.MappingLibraries
		state.WorkloadLibraries = input.WorkloadLibraries
		state.ArtifactLibraries = input.ArtifactLibraries
		state.AttachmentProvides = attachmentProvides
		// The connections made to this node, for the Features attached to it and for
		// nothing else: it goes onto the state and never onto tmplCtx, the context
		// this node's own Recipe renders against. Every node's provides were
		// evaluated above, before this walk began, so each connecting node's bag is
		// complete here whatever order the nodes render in.
		state.InboundEdges = AssembleInboundEdges(nodeID, graph, allProvides, declaredProvides)

		// Phase B.0 — publishes the Pass-1 locals and evaluates
		// source-as-template; populates ctx.Locals + ctx.Source. May replace
		// state.Recipe with a local copy whose Spec.Source carries the resolved
		// value.
		if err := phaseB0(tmplCtx, state, allLocals[nodeID]); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
		}
		recipe = state.Recipe // pick up the local copy with resolved source

		// Phase A: injectMapping from FeatureRecipes in Onion Model order.
		// Sits between B.0 and B.1 — chapter 16 sub-pass numbering scopes
		// to Phase B; Phase A is a separate phase and stays at the
		// orchestrator level.
		// One ref set in one order. Every attachment participates in Phase A —
		// an inject-only FeatureRecipe (no source/workload) rewrites the
		// component's inject pipe whatever the attachment produces — and the
		// order is the cascade order the IR recorded, which Phase B.6 walks too.
		// The Phase A deep-merge sets each key a mapping's output carries over
		// whatever stood under it. Where that was the component's own block or
		// one of its connections, the mapping's value replaces that one entry —
		// interception. Where another attachment delivered the key, by its own
		// block or its mapping, the build halts below, naming both attachments,
		// so the merge never decides between two attachments' deliveries.
		featureRefs := resolveFeatureRecipes(nodeID, input.FeatureOrder, graph, recipeLookup)
		attachmentLeaves := func(instanceName string) resolver.SensitiveLeaves {
			entry, ok := attachments.Get(instanceName)
			if !ok {
				return resolver.SensitiveLeaves{}
			}
			return readers.feature(entry.Type)
		}
		mappedDelivery, err := ExecutePhaseA(tmplCtx, featureRefs, hostWithheld{host: state.Withheld, attachment: attachmentLeaves})
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
		}
		// Retain what each attachment's mapping said about the mechanisms this
		// component declared, and what it delivered — the deliveries join the
		// node's record after every block's, in the order the mappings ran — then
		// ask the two rules declared over those records. The recording is
		// unconditional and the readings are immediately after it: this is the
		// point every mapping has rendered and nothing below has read the
		// rewritten pipe yet, so a declaration reaching nothing, and a key two
		// attachments delivered, are refused before any output is rendered
		// against the pipe. The mechanism reading is asked first, so an input it
		// refuses is still refused by it.
		for _, delivery := range mappedDelivery {
			input.hydrated.recordAttachmentInjectMappingMechanisms(
				nodeID, delivery.InstanceName, delivery.Mechanisms)
			injectDeliveries = append(injectDeliveries, delivery.Deliveries...)
		}
		input.hydrated.recordNodeInjectDeliveries(nodeID, injectDeliveries)
		if err := runInjectMappingMechanismRule(
			ctx.Err, result.HydratedView, input.GraphCoordinates, nodeInstances[nodeID],
		); err != nil {
			return nil, err
		}
		if err := runNodeInjectMappingDeliveryRule(
			ctx.Err, result.HydratedView, input.GraphCoordinates, nodeInstances[nodeID],
		); err != nil {
			return nil, err
		}
		// The FeatureRecipe route is readable only now. A slot declaring no delivery
		// of its own hands delivery to the FeatureRecipe, whose injectMapping names
		// its delivery key with a template — so the key exists only in the rendered
		// overlay, and the marking has to be folded in after the render rather than
		// beside the node's declarations. Every capture from Phase A onward reads the
		// state's copy, which is what this replaces.
		mappedKeys := injectMappingWithheld(mappedDelivery, attachmentLeaves)
		state.Redact, state.Withheld = nodeSets(mappedKeys)
		// Boundary->component env channel. The overrides a boundary-scope FR
		// emitted for this consumer (computed in the pre-loop) are a third route
		// by which an attachment delivers: they join the node's record after every
		// mapping's, attributed to the attachment that produced them, and the rule
		// declared over that record is asked before they are applied — so an
		// override writing a key another attachment delivered halts, naming both,
		// while one writing a key the component's own block or a connection
		// delivered rewrites that one entry. Gated on map membership, so
		// non-named consumers are untouched; a targeted overwrite of exactly the
		// named keys.
		producers := boundaryFRState.ConsumerEnvOverrideProducers[nodeID]
		injectDeliveries = append(injectDeliveries, consumerEnvOverrideDeliveries(producers)...)
		input.hydrated.recordNodeInjectDeliveries(nodeID, injectDeliveries)
		if err := runBoundaryEnvOverrideDeliveryRule(
			ctx.Err, result.HydratedView, releaseFileOf(input.ResolveResult), producers,
		); err != nil {
			return nil, err
		}
		applyConsumerEnvOverrides(&tmplCtx.Context.Inject, boundaryFRState.ConsumerEnvOverrides[nodeID])
		// Trace point: Phase A — feature-scoped per-consumer inject pipe
		// after FR injectMapping rewrites.
		state.capture(trace.PassPhaseA, "", tmplCtx)

		// Phase B.1 — valuesMapping → state.ResolvedValues.
		if err := phaseB1(tmplCtx, state); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
		}
		values := state.ResolvedValues

		// Ghost Infrastructure: authored values, infra and per-connection uses
		// keys no template the Recipe declares ever reads, so an author notices a
		// knob nothing consumes. See unreadValueWarnings for what the reading
		// covers.
		//
		// The AUTHORED document is what this reads, which is why it goes back to
		// the lookup rather than using the local copy above. The reading asks
		// which keys the Recipe's templates reference, and a source template has
		// by this point been replaced by the value it produced — so the local copy
		// no longer carries the reference, and a reading given it would report a
		// key the source coordinate consumes as read by nothing.
		result.Warnings = append(result.Warnings,
			unreadValueWarnings(nodeID, nodeRecipes[nodeID], tmplCtx.Values, tmplCtx.Infra, tmplCtx.Context.Uses)...)

		hasWorkload := recipeWorkloadDeclared(recipe)

		// Resolve engine for this node's provider via factory. The factory
		// receives the full binding (version pin included) so it can resolve the
		// pinned provider-rendering plugin; every other consumer below keys on the
		// bare engine kind (dispatch grouping, path segment, codec, and the
		// provider a declared rule matches a pipeline step's plugin against).
		providerRef := ""
		providerName := ""
		if recipe != nil {
			providerRef = recipe.Spec.Capability
			providerName = ProviderEngineKind(recipe.Spec.Capability)
		}
		// The node's target is known now, so the by-value advisories collected at
		// attachment delivery can be located and joined to the per-node diagnostic
		// accumulator. Collection order is the delivery order, which is itself
		// deterministic, so the stream is byte-stable across runs.
		for i := range sensitiveByValue {
			sensitiveByValue[i].Location = &DiagnosticLocation{URI: nodeID, Kind: providerName}
		}
		state.Diagnostics = append(state.Diagnostics, sensitiveByValue...)
		// Resolved provider-rendering plugin version behind this binding, shared
		// with the engine the factory returns for it. Empty when no provider-
		// rendering plugin version resolves behind the binding. Stamped on the
		// aggrega.ai/provider-version label and used to pick the dispatch group's
		// boundary assembler when the group mixes provider versions.
		providerVersion := ""
		if input.ProviderVersion != nil {
			providerVersion = input.ProviderVersion(providerRef)
		}
		engine, err := engineFactory(providerRef)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q: selecting its rendering plugin", nodeID))
		}

		// Cross-boundary link placeholder stamp. For a consumer whose provider
		// links across boundaries, overwrite each cross-boundary edge's injected
		// value with a deterministic placeholder now — before the artifacts render
		// consumes the inject pipe below — so the placeholder is what renders into
		// the consuming output. The post-assembly link pass replaces it there with
		// the provider-native cross-reference. Inert (no mutation) for every
		// non-linking provider and every edge that does not cross a boundary.
		stampRecipeName := ""
		if recipe != nil {
			stampRecipeName = recipe.Metadata.Name
		}
		if err := stampCrossBoundaryLinkSentinels(tmplCtx, nodeID, providerName, stampRecipeName, engine, node, graph, nodeRecipes, input, boundaryDirEvals, perBoundaryAnnotations); err != nil {
			return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("cross-boundary link stamp node %q", nodeID))
		}

		// Per-node engine render — the source-based render. Output files
		// are tagged EmitOriginSource and accumulated on the state for
		// B.5's collision detector. A node with no source: emits no
		// engine.Render output: no compute is invented for it (Invariant 4).
		// The Workload synthesizer at Phase B.4 is the source of truth for a
		// workload-only Recipe, and coexists with a source-based render when
		// a Recipe declares both source and workload.
		nodeCtx := buildNodeRenderContext(nodeID, node, graph, input)
		nodeCtx.ProviderName = providerName
		nodeCtx.ProviderVersion = providerVersion
		// The declarations a provider guest's per-node marking is recovered from.
		// The leaf half reads the SAME memoised declaration reader the node's
		// withholding set reads above, so the two answers cannot describe
		// different declarations; what differs downstream is the direction, and
		// deliberately so. Withholding treats an untraceable key as sensitive,
		// because printing a secret is worse than hiding a readable value. Marking
		// admits only a key whose identity is recovered exactly, because asserting
		// sensitivity of a value an author left public makes the target's own
		// tooling hide it from an operator who needed to see it. The slot half is
		// the slot DECLARATION rather than the per-attachment roll-up the graph
		// carries: the roll-up is true whenever the attached entity declares any
		// sensitive leaf anywhere, which says nothing about the datum one rendered
		// key reads.
		//
		// The slots a Workload entry may name are assembled once, here, and read
		// twice: by the Workload's fold and checks below, and by the marking's
		// socket half, so an entry folded onto a connection's slot is joined to the
		// declaration of the very slot the fold resolved it to.
		workloadSlots := NodeWorkloadSlots(input.CatalogLookup, node.Component, tmplCtx.Context.Uses)
		nodeCtx.DeclaredSensitivity = DeclaredSensitivity{
			ValueLeaves:             declared.Values,
			ValuesAreDeclaredLeaves: state.ValuesAreDeclaredLeaves,
			Slots:                   sensitiveSlots(node.Component),
			SocketSlots:             SensitiveSocketSlots(workloadSlots),
		}
		// Whether the values mapping read a declared value, as the values pass
		// recorded it. It travels beside the declarations rather than inside them
		// because it is read from a template, which a declaration set never is.
		nodeCtx.SensitiveValues = state.SensitiveValues
		// The omit entries the values pass resolved, which only this node's source
		// render reads.
		nodeCtx.Omits = state.Omits
		// Route the non-fatal advisories a provider guest raises while rendering
		// this node into the per-node warnings accumulator, which the orchestrator
		// drains into result.Warnings at the end of the per-node sequence. Set once
		// here so every downstream crossing that carries this node context —
		// engine.Render, artifacts emission, workload synthesis, and the FeatureRecipe
		// sub-renders that copy it — surfaces its guest warnings through one channel.
		// Attached before the recipe block below so the advisory a promoted output
		// label raises for a key the component does not declare rides it too.
		nodeCtx.WarningSink = func(d Diagnostic) { state.Warnings = append(state.Warnings, d) }
		if recipe != nil {
			nodeCtx.RecipeName = recipe.Metadata.Name
			nodeCtx.RecipeVersion = recipe.Metadata.Version
			nodeCtx.OutputLabels = resolveOutputLabels(recipe, nodeID, node, nodeCtx.WarningSink)
		}
		// Unsupported-field advisories route here, kept apart from the stream above
		// so the class stays suppressible by its stable code.
		nodeCtx.UnsupportedFieldSink = func(d Diagnostic) { state.Diagnostics = append(state.Diagnostics, d) }
		// How this node's own output is routed, resolved once here and read at each
		// of the four places that need it: the Intermediate marking every emitted
		// file below carries, the derived-name declaration's disposition, the
		// own-scope pipeline's deferral, and the host-document surface a
		// component-scope attachment's pipeline is offered. It is read before any
		// crossing runs because the declaration sink attached below closes over it,
		// and a sink attached after a crossing would lose that crossing's names.
		// The full statement of what the marking means is at the emission below.
		renderIsIntermediate := RenderIsIntermediate(engine)
		// The names the node's provider guest declares it derived accumulate here,
		// merged across every crossing that carries this node context, and move into
		// the node's dispatch group below once the group key exists. The sink is
		// attached before any crossing runs, for the same reason the warning sink is:
		// a declaration made by a crossing that ran before the sink existed would be
		// lost, and losing a declaration is losing exactly the evidence the
		// duplicate check reads.
		//
		// This sink is the NODE's own, so what arrives over it is recorded under the
		// node's own label. An attachment's render carries a copy of this context and
		// rewires the sink to its own label before it runs, because a name an
		// attachment's guest derived is the attachment's rather than the node's and
		// has to be comparable against a sibling attachment's.
		//
		// Where the node's own output lands travels with the label, read from the
		// same claim that stamps every file this node emits below: an intermediate
		// render is handed to the group's assembler, and a non-intermediate one is
		// written as discrete files at the node's own position. Reading it here
		// rather than at the routing is what keeps the two from being separate
		// decisions — the value that routes the files is the value that labels the
		// declaration.
		nodeProducer := derivedKeyProducerFor(producerKindComponent, nodeID, "")
		nodeContributor := derivedKeyContributorFor(nodeProducer, !renderIsIntermediate)
		nodeCtx.DerivedKeySink = func(declared types.OrderedMap[[]string]) {
			state.DerivedKeys.declare(nodeContributor, declared)
		}
		// The facts the node's provider guest reports about what this render
		// emitted accumulate on the node's state, in the order they arrive, so the
		// attachment pass below is handed them with the rest of the state. Attached
		// before any crossing runs, for the reason the two sinks above are. An
		// attachment's render rewires its copy to a record of its own.
		nodeCtx.RenderFactSink = state.recordRenderFacts
		// The source as its author wrote it is the catalog's Recipe, which nothing
		// replaced: the copy in hand carries the value the template produced.
		var authoredSource any
		if authored := nodeRecipes[nodeID]; authored != nil {
			authoredSource = authored.Spec.Source
		}
		if err := resolveSourceForNode(input, recipe, state.RecipePath, &nodeCtx,
			sourceOrigin{authored: authoredSource, withheld: state.Withheld}); err != nil {
			return nil, wrapSourceResolutionError(err, nodeID,
				fmt.Sprintf("node %q source resolution", nodeID))
		}
		hasSource := sourceValueDeclared(nodeCtx.Source)
		hasArtifacts := recipe != nil && recipe.Spec.Artifacts.Len() > 0

		// Central input-kind gate (the describe()-fed seat), sited here because
		// this is the last point before any Phase B dispatch — so a kind the
		// resolved provider cannot honour is refused before anything is emitted
		// and before any guest is handed input it cannot render. What the gate
		// checks is stated on the gate itself rather than restated here.
		if err := gateInputKind(nodeID, providerName, engine, hasSource, hasWorkload, hasArtifacts); err != nil {
			return nil, err
		}

		// The node's declared input kinds are settled now, and the resolved
		// provider and the Release's authored annotations were already in hand —
		// so this is the one point that holds every fact the advisory needs. It
		// reports the platform annotations this node carries that its target's own
		// support claim covers for none of those kinds: authored directives that
		// reach no consumer, which without a word are indistinguishable from a
		// typo. A degradation, never a refusal: the annotations pass through
		// untouched and no rendered byte moves.
		//
		// It is handed the same declared set the gate above consults, not the one
		// kind the precedence names this node's mode after. A node's declared kinds
		// each render and their outputs are added together, so a target acting on
		// an annotation through any one of them acts on it — and a set of one is
		// the ordinary case, which reads exactly as it always has.
		warnUnhonorableAnnotations(nodeID, providerRef, providerName,
			declaredNodeInputKinds(hasSource, hasWorkload, hasArtifacts),
			node.ReleaseAnnotations, input.CatalogLookup.ProviderPlugin,
			nodeCtx.UnsupportedFieldSink)

		// The same point holds every fact the sensitivity advisory needs, which is
		// why it is raised beside the one above rather than at the dispatch: the
		// node's declarations were assembled onto the context above, the declared
		// input kinds are settled here, and the resolved provider is in hand. It
		// reports a `values` leaf whose declared sensitivity this node's render
		// cannot carry to its target while the author still holds a lever over it
		// — a security-relevant declaration spent on nothing, which without a word
		// is indistinguishable from one that worked. A degradation, never a
		// refusal: no rendered byte moves and the declaration passes through
		// untouched.
		//
		// It is handed the node context's own declaration set rather than a second
		// reading, so the advisory and the marking on the wire cannot describe
		// different declarations.
		warnSensitivityNotConveyed(nodeID, providerName, nodeCtx.RecipeName,
			nodeCtx.DeclaredSensitivity, hasSource, nodeCtx.UnsupportedFieldSink)

		// Advertised-only refusal of an authored identity the node's target will
		// not accept. This node's identity is the one its author wrote, so it is
		// refused rather than shortened: it is the deployed name of the workload,
		// and the person able to change it is the person who wrote it. The refusal
		// therefore names the Aggregate field they wrote, and carries the target's
		// own reason for refusing, which only the guest knows. A provider whose
		// guest answers no such question accepts every name, exactly as before.
		if err := SettleAuthoredComponentIdentity(ctx, engine, nodeID,
			fmt.Sprintf("spec.components.%s", nodeID)); err != nil {
			return nil, err
		}

		// Advertised-only per-node input validation. When the resolved provider's
		// guest declares the optional input-validation export, run it once here —
		// after the capability gate, before any Phase B render pass — so a provider
		// that can detect a malformed input halts loudly (Invariant 4) before
		// emission rather than producing broken output. Error-severity diagnostics
		// halt; warnings surface to the build result.
		if validator, ok := engine.(NodeInputValidator); ok {
			diags, verr := validator.ValidateNodeInput(ctx, values, nodeCtx)
			if verr != nil {
				return nil, cerrors.Wrap(verr, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q input validation", nodeID))
			}
			if err := routeNodeInputDiagnostics(nodeID, providerName, diags, nodeCtx.WarningSink); err != nil {
				return nil, err
			}
		}

		// engine.Render (the source-based render) runs only for sourced mode;
		// synthesized renders in Phase B.4, artifacts-only emits in Phase B.3.
		runEngineRender := hasSource
		var files []RenderedFile
		if runEngineRender {
			var err error
			files, err = engine.Render(ctx, values, nodeCtx)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
			}
		}
		// Tag with EmitOriginSource and accumulate (deterministic across
		// runs of identical input by iterating the engine.Render output
		// by sorted filename; state.EmittedFiles order stays stable).
		//
		// Intermediate marking. When the engine implements BoundaryAssembler
		// AND consumes per-node Render output as fragment input (today:
		// compose), Render's output is intermediate to the per-(path,
		// provider) BoundaryAssembler at the contributor's evaluated path.
		// Engines that implement BoundaryAssembler but place per-node
		// content at engine-controlled positions (today: helm-chart,
		// signalled by the engine's artifacts capability — its sub-charts land
		// under the per-node sub-directory its provider declares) keep
		// Render output Intermediate=false so the orchestrator nests their
		// files under the per-node prefix rather than routing them to
		// AssembleBoundary's IntermediatesByNode. Engines that are not
		// BoundaryAssemblers also keep Intermediate=false. The claim itself is
		// resolved once above, before the sink that reads it is attached.
		//
		// Accumulate by sorted path so state.EmittedFiles order is stable across
		// runs of identical input regardless of the guest's emission order. Each
		// file's producer-declared Format rides onto the EmittedFile so it stays
		// bound to its file downstream.
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		for _, f := range files {
			state.EmittedFiles = append(state.EmittedFiles, EmittedFile{
				Name:         f.Path,
				Origin:       EmitOriginSource,
				Body:         f.Content,
				Intermediate: renderIsIntermediate,
				Format:       f.Format,
			})
		}

		// Phase B.2 — workload eval + ValidateWorkload + the connection fold +
		// ValidateWorkloadAttachments + referenceability. No-op for Recipes that
		// don't declare workload:. The workload slot cross-check validates
		// containers.<n>.attachments against the bound Component's featuresSchema
		// slot allowlist and, for an entry naming a connection, against the slots
		// that connection's socket declares.
		if err := phaseB2(tmplCtx, state, workloadSlots); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
		}

		// Reduce this node's mid-render state to the projection that outlives it.
		// The position is the latest point at which every recorded value still
		// stands as its own definition names it: source resolution has replaced
		// the Recipe with the copy carrying the resolved source, the workload has
		// been parsed, and no artifact template has been evaluated. The state is
		// read and not retained.
		input.hydrated.recordNode(state)

		// Then ask the value declared over this node's own Workload whether one of
		// its containers names an environment variable twice. It reads the recording
		// above, so it sits immediately after it, and it sits before the artifacts
		// pass and workload synthesis because both read the parsed Workload: a pair
		// refused here never reaches a provider.
		if err := runWorkloadEnvironmentRule(ctx.Err, state); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
		}

		// Phase B.3 — artifacts evaluation. Each artifacts: entry (inline
		// content or $import:-loaded) renders through Go templates against
		// the full TemplateContext, gets provenance labels merged into K8s metadata
		// when present, and accumulate on state.EmittedFiles — which is what the
		// filename-collision rule reads once this node's emission is complete.
		if err := phaseB3(ctx, tmplCtx, state, engine, nodeCtx); err != nil {
			// An artifacts-evaluation failure is normally a catalog fault: the
			// entry, its import, or its template is what went wrong. A provider
			// that rendered non-deterministically is the exception — the fault is
			// in the provider itself, not in the artifacts entry that happened to
			// route through it — so it carries the same translation category here
			// that it carries on the source-render and workload-synthesis
			// crossings. The condition fixes the category; the crossing it
			// surfaced through does not.
			category := cerrors.CatalogError
			if errors.Is(err, ErrRenderNondeterministic()) {
				category = cerrors.TranslationError
			}
			return nil, cerrors.Wrap(err, category, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
		}

		// Phase B.4 — workload synthesizer dispatch. No-op for a node with no
		// parsed workload (state.ParsedWorkload is nil); dispatches to the
		// engine's WorkloadSynthesizer otherwise. Whether this provider may be
		// handed a workload at all was settled by gateInputKind above, from the
		// provider's own declared contract; the dispatch asserts the interface
		// but adds no capability check of its own.
		if err := phaseB4(ctx, tmplCtx, state, engine, nodeCtx); err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
		}
		// Slot 10 is wired per-emitted-file inside that stage itself, not at
		// the orchestrator level: the granularity is per emitted file, not per
		// node.

		// Two output names one node's own blocks both produced are refused by the
		// value declared over this node's emission, invoked once below at the point
		// that emission is recorded. It is asked there rather than here because the
		// set is not yet whole at this line: an own-scope Recipe pipeline can still
		// append what a step emits.

		// Group per (evaluated_path, provider). Per-node file map is built
		// from state.EmittedFiles (carries every input-kind contribution).
		// Intermediate=true entries are split off into intermediates so
		// the orchestrator never writes them at the dispatch path directly;
		// they flow only to the BoundaryAssembler at finalization.
		//
		// Path-template evaluation runs per-component at orchestrator-time
		// using BoundaryInfra.{Directory, SubDirectory} (raw template
		// strings populated by the resolver) plus the live parameter map
		// from ResolveResult.Parameters. The evaluated final_path becomes
		// the dispatch Path; the per-(path, provider) tuple is the dispatch
		// key. Two boundaries with same evaluated path + same provider
		// merge into one dispatch group.
		bp := node.BoundaryPath
		recipeName := ""
		if recipe != nil {
			recipeName = recipe.Metadata.Name
		}
		var bi *resolver.BoundaryInfra
		var parameters map[string]any
		if input.ResolveResult != nil {
			if rbi, ok := input.ResolveResult.BoundaryInfra[nodeID]; ok && rbi != nil {
				bi = rbi
			}
			parameters = input.ResolveResult.Parameters
		}
		finalPath, err := computeFinalPath(nodeID, bp, providerName, recipeName, bi, parameters, boundaryDirEvals, perBoundaryAnnotations)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("path eval node %q", nodeID))
		}

		// Recipe pipeline (own scope). Runs per-node AFTER the primary render
		// (state.EmittedFiles carries source/workload/artifacts — the node's
		// own discrete documents) and BEFORE the dispatch snapshot below reads
		// them. FeatureRecipe files are not yet in state.EmittedFiles (phaseB6
		// returns them separately and merges them into the dispatch map), so
		// the own set here is exactly the node's own output. The pipeline
		// applies /own PATCHes and appends /own EMITs as EmitOriginPipeline
		// files; re-running the filename-collision detector catches a pipeline
		// EMIT clashing with a rendered filename.
		//
		// Own-on-assembler: when this node's own provider is an assembler
		// (renderIsIntermediate — its per-node output is pre-assembly fragments,
		// not discrete documents), a /own PATCH cannot run per-node. The pipeline
		// is deferred whole to the post-assembly phase (recorded below, after the
		// dispatch key exists) and replayed against the merged documents, exactly
		// as an assembler host's /host pipeline defers. EMIT is additive and could
		// run per-node, but the whole pipeline defers as one record so a mixed
		// PATCH+EMIT pipeline replays in its declared step order on the merged
		// document.
		var recipeOwnDeferred *DeferredPatch
		if recipe != nil && len(recipe.Spec.Pipeline) > 0 {
			renderCtx := buildPipelineRenderCtx(node, bi, parameters)
			if renderIsIntermediate {
				recipeOwnDeferred = &DeferredPatch{
					Steps:             recipe.Spec.Pipeline,
					WithCtx:           tmplCtx,
					Withheld:          state.Withheld,
					RenderCtx:         renderCtx,
					OwnProvider:       providerName,
					NodeID:            nodeID,
					FeatureOrderIndex: -1, // a Recipe own pipeline precedes every FeatureRecipe in the cascade
				}
			} else {
				// own provider is this node's recipe provider; a Recipe step has no
				// host scope (host nil), so hostProvider is empty.
				pres, perr := runNodePipeline(ctx, recipe.Spec.Pipeline, state.EmittedFiles, nil, false, providerName, "", renderCtx, tmplCtx, state.Withheld, input.YAMLIndent, input.PluginPathLookup, engineFactory, nodeID)
				if perr != nil {
					return nil, perr
				}
				state.EmittedFiles = pres.Own
				result.Warnings = append(result.Warnings, pres.Warnings...)
				// A step EMIT clashing with a rendered name is refused below, with
				// every other way two of this node's blocks can meet on one name:
				// the set is whole from this line and the value declared over it is
				// asked once, at the point the emission is recorded.
			}
		}

		// Reduce this node's completed emission to the projection that outlives it.
		// The position is the last point at which the set is the node's OWN output
		// and the first at which it is whole: every render pass that can contribute
		// a file has run, and an own-scope pipeline has appended whatever it
		// emitted. One statement later the node joins its dispatch group, where the
		// set is split by the intermediate flag and merged with what other nodes and
		// what attached FeatureRecipes emitted — so a projection taken after that
		// would no longer be about this node alone.
		//
		// A pipeline deferred whole to post-assembly has contributed nothing by this
		// point, which the projection records as the truthful answer rather than as a
		// gap: such a pipeline emits into the merged documents a group assembled,
		// under names no single node chose.
		//
		// The state is read and not retained.
		input.emitted.recordNode(state)

		// Then ask the value declared over this node's own emission whether two of
		// its blocks met on one output name. The recording above and this reading
		// are adjacent because the rule reads that retention: recording anywhere
		// else would leave the rule asked about a set that is not this node's.
		//
		// This is the one place the question is asked for this owner. The two
		// hand-written detections it replaces ran earlier in this walk — one after
		// workload synthesis, one after the own-scope pipeline — and the inputs that
		// halt are unchanged, because the pipeline rebuilds every file it was given
		// and appends what a step emitted: it cannot drop a file or change one's
		// name or origin, so a collision present before it is present here.
		// The node is taken from the build's own enumeration where it describes
		// this node, and falls back to the bare identity where it does not. The
		// fallback is what keeps the rule's REACH independent of the enumeration:
		// a caller supplying no coordinates would otherwise leave this node
		// unchecked, and this rule replaced a detection that ran on every node.
		// What the enumeration contributes is the locator, not the invocation.
		emittingNode, described := nodeInstances[nodeID]
		if !described {
			emittingNode = constraint.NodeInstance{ID: nodeID}
		}
		if err := runNodeEmissionCollisionRule(
			ctx.Err, input.GraphView != nil, result.EmittedView,
			input.emitted.coordinates(), emittingNode,
		); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError,
				cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("rendering node %q", nodeID))
		}

		key := dispatchKey{Path: finalPath, Provider: providerName}
		nodeDispatch[nodeID] = key
		nodeEngine[nodeID] = engine
		nodeAnnotations[nodeID] = nodeCtx.Annotations
		// Stash a deferred own-scope Recipe pipeline under this node's dispatch
		// group now that the key exists. The merged documents the /own PATCH
		// targets are the ones this dispatch group's assembler produces, the same
		// group an attached FeatureRecipe's deferred /host pipeline lands in.
		if recipeOwnDeferred != nil {
			recipeOwnDeferred.HostNodeOrder = nodeOrder
			deferredByDispatch[key] = append(deferredByDispatch[key], *recipeOwnDeferred)
		}
		if _, ok := dispatch[key]; !ok {
			dispatch[key] = &pendingDispatch{
				files:           map[string]map[string][]byte{},
				formats:         map[string]map[string]string{},
				intermediates:   map[string][]EmittedFile{},
				placed:          map[string][]EmittedFile{},
				nodeAnnotations: map[string]map[string]string{},
				nodeComponent:   map[string]string{},
				nodeSyncWave:    map[string]int{},
				nodeValues:      map[string]map[string]any{},
				nodeProducer:    map[string]derivedKeyProducer{},
				nodeNameBasis:   map[string]string{},
				engineByNode:    map[string]Provider{},
			}
			orderedDispatch = append(orderedDispatch, key)
		}
		// INSTANCE_ID_COLLISION check: when two contributors land in the
		// same dispatch group with the same node-ID, the assembler input
		// would be ambiguous. Defensive guardrail — topology flattening's
		// prefix discipline should make collisions impossible — but the
		// check surfaces structural violations immediately rather than
		// letting them propagate to broken assembled output.
		if _, alreadyHere := dispatch[key].files[nodeID]; alreadyHere {
			inner := fmt.Errorf("%w: dispatch path %q provider %q already contains node %q (contributing boundaries: %v); component instance IDs must be unique within an assembly group",
				ErrInstanceIDCollision(), key.Path, key.Provider, nodeID, dispatch[key].contributingBoundaries)
			return nil, cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{}, inner.Error())
		}
		// Track contributing boundaries (deduplicated). Used for diagnostic
		// messages on collision/chart-name-mismatch errors and for chart-name
		// cascade lookups against ResolveResult.BoundaryAnnotations.
		alreadyContributing := false
		for _, existing := range dispatch[key].contributingBoundaries {
			if existing == bp {
				alreadyContributing = true
				break
			}
		}
		if !alreadyContributing {
			dispatch[key].contributingBoundaries = append(dispatch[key].contributingBoundaries, bp)
		}
		nodeFileMap := make(map[string][]byte, len(state.EmittedFiles))
		nodeFormatMap := map[string]string{}
		for _, f := range state.EmittedFiles {
			if f.Intermediate {
				dispatch[key].intermediates[nodeID] = append(dispatch[key].intermediates[nodeID], f)
				continue
			}
			dispatch[key].placed[nodeID] = append(dispatch[key].placed[nodeID], f)
			nodeFileMap[f.Name] = f.Body
			// Keep the producer-declared format bound to its file, keyed identically
			// to nodeFileMap so the flat-output flush reads them in lockstep.
			if f.Format != "" {
				nodeFormatMap[f.Name] = f.Format
			}
		}
		dispatch[key].files[nodeID] = nodeFileMap
		dispatch[key].formats[nodeID] = nodeFormatMap
		// The label this node's own declarations were recorded under is the label its
		// files are attributed by, read from the value composed above rather than
		// composed a second time here. A component's own render needs no name basis:
		// its key is the name it deploys under, so a target derives names from the key
		// directly and an entry stating that again would be a second copy of it.
		dispatch[key].nodeProducer[nodeID] = nodeProducer
		dispatch[key].nodeAnnotations[nodeID] = nodeCtx.Annotations
		dispatch[key].nodeComponent[nodeID] = node.Component
		dispatch[key].nodeSyncWave[nodeID] = nodeCtx.SyncWave
		// A node's values travel to its group's assembler only when the compiler is
		// the one supplying that node's inputs — the sourced case, where the files
		// were vendored in verbatim and so could not have the values written into
		// them. On every other render path the values were resolved into the files
		// the render itself produced, so there is nothing left for an assembler to
		// supply: handing them over would offer inputs the node's own files never
		// declare, which a target is free to reject. Same predicate the source-based
		// render runs on above, because it is the same distinction.
		if hasSource {
			dispatch[key].nodeValues[nodeID] = values
		}
		dispatch[key].engineByNode[nodeID] = engine
		dispatch[key].subCharts = append(dispatch[key].subCharts, SubChartInfo{
			InstanceID: nodeID,
			Version:    node.ResolvedVersion,
			Values:     values,
			Source:     nodeCtx.Source,
		})
		// Record this node as a boundary-assembler candidate for its dispatch
		// group. The group's assembler is elected after the per-node loop as the
		// engine of the highest resolved provider version (selectBoundaryAssembler)
		// — not the first engine seen — so a mixed-version group assembles through
		// its newest provider. Candidates append in node-processing order, which
		// the election's earliest-wins tie-break relies on.
		dispatch[key].assemblerCandidates = append(dispatch[key].assemblerCandidates, assemblerCandidate{
			engine:  engine,
			version: providerVersion,
		})

		// Phase B.6 — FeatureRecipe Resource Generation for every attachment on
		// the node. Returns paths prefixed with the attachment's own position —
		// the directory the group's provider names for an attached Feature's files,
		// then the host and the instance — merged into the per-node files map
		// alongside source/workload outputs.
		var releaseForFR *types.Release
		var boundaryAnnotationsForFR map[string]types.OrderedMap[string]
		if input.ResolveResult != nil {
			releaseForFR = input.ResolveResult.Release
			boundaryAnnotationsForFR = input.ResolveResult.BoundaryAnnotations
		}
		// Host docs a component-scope FR pipeline may /host PATCH or EMIT: the
		// host component's own discrete documents (state.EmittedFiles).
		// hostIsAssembler mirrors the per-node renderIsIntermediate predicate —
		// an assembler that does not place its own artifacts renders
		// pre-assembly fragments, so a /host PATCH against it halts (post-
		// assembly territory). providerName is the host component's recipe
		// provider, threaded in so a host-scope pipeline step's
		// targets is enforced against the provider rendering the
		// /host documents. renderCtx is the render-time context every pipeline
		// step receives.
		pipelineRenderCtx := buildPipelineRenderCtx(node, bi, parameters)
		// Whether this node's dispatch group gives each contributor a position of
		// its own, which is what decides where this node's attachments write. It is
		// asked of the engine that rendered THIS node: a component-scope attachment
		// is placed by its host's dispatch group, so the shape is that group's
		// provider's answer and not the attachment's. An attachment's files are
		// placed while this node renders, so the answer has to be in hand here
		// rather than at the group's own assembly, which runs a whole pass later.
		hostNestsContributors := boundaryNestingOf(engine).PerNode
		b6, held, err := phaseB6(ctx, tmplCtx, state, featureRefs, engineFactory, allProvides, recipeLookup, nodeCtx, boundaryFRState, graph, releaseForFR, boundaryAnnotationsForFR, input, state.EmittedFiles, renderIsIntermediate, providerName, hostNestsContributors, pipelineRenderCtx)
		if err != nil {
			return nil, err
		}
		// What this node's renders reported, as this turn leaves them: its own render
		// and every attachment rendered in this turn. Recorded now, before any held
		// attachment renders onto this node's state, so an attachment held for the
		// pass after the walk is handed what its callers' and its targets' turns
		// produced and nothing another held attachment reported.
		nodeRenderFacts[nodeID] = state.renderFactsAsItStands()
		if err := placeAttachmentOutput(key, nodeID, nodeOrder, state, nodeCtx, b6); err != nil {
			return nil, err
		}
		// A node carrying an attachment held for the pass after the walk is completed
		// there, once that attachment has rendered; every other node is completed now.
		if held != nil {
			heldHosts = append(heldHosts, heldAttachmentHost{
				nodeID:       nodeID,
				boundaryPath: node.BoundaryPath,
				nodeOrder:    nodeOrder,
				key:          key,
				held:         held,
			})
		} else {
			completeNodeRender(state, tmplCtx, nodeCtx, nodeID, node.BoundaryPath)
		}

		// Per-node progress hook (nil = no-op). Fires once as this node's render
		// completes, with the 1-based completed count and the total node count.
		// A diagnostic observation only; it must not touch the file map, so
		// rendered output is invariant with respect to whether it is set.
		if input.NodeProgress != nil {
			input.NodeProgress(nodeOrder+1, len(nodeIDs), nodeID)
		}
	}

	// The declared pass. An attachment whose FeatureRecipe declares it reads the
	// render facts of its callers or of its targets renders here, after every node
	// has rendered, so each caller's and each target's own render and its in-turn
	// attachments have reported what they emitted by the time it is handed them —
	// a target later in the walk than its consumer included. Hosts are taken in
	// walk order and each host's held attachments in the order they hold in its
	// sequence, so the order is a function of the input.
	//
	// It runs before anything reads what it produces: every reading of the completed
	// groups — the attribution, the elections, the derived names, the rules over the
	// grouping and every assembly — is below. The guards the walk runs as it adds a
	// contributor to a group run here too, through the same routing, so two
	// contributors reaching one identity halt whichever of them arrives second.
	//
	// Each held attachment renders from what its host's turn captured, kept rather
	// than rebuilt, and its output is placed by the same routing and the host then
	// completed as any node is. What it is handed of its callers and its targets is
	// read from the records their own turns left, so no held attachment reads
	// another's output.
	//
	// One thing differs from an attachment rendered in its host's turn, and it is
	// an order rather than a content: a held attachment joins its group after every
	// node of the walk has, so in the list of children a group names it comes after
	// them. Everything else a group hands its assembler is keyed by contributor.
	for _, h := range heldHosts {
		pass := h.held.pass
		callers := gatherCallerRenderFacts(pass.state.InboundEdges, nodeRenderFacts)
		targets := gatherTargetRenderFacts(pass.tmplCtx.Context.Uses, nodeRenderFacts)
		out, err := h.held.render(ctx, callers, targets)
		if err != nil {
			return nil, err
		}
		if err := placeAttachmentOutput(h.key, h.nodeID, h.nodeOrder, pass.state, pass.nodeCtx, out); err != nil {
			return nil, err
		}
		completeNodeRender(pass.state, pass.tmplCtx, pass.nodeCtx, h.nodeID, h.boundaryPath)
	}

	// Boundary-scope FR output routing. After the per-node loop, any
	// boundary-scope FR that ran (deduped to one execution per
	// (boundary_path, fr_instance)) has produced files captured in
	// boundaryFRState.Files. Classify each by Intermediate flag:
	//
	//   - Intermediate=true: route to the per-(path, provider) dispatch
	//     group at the FR's evaluated path, using the FR's spec.capability
	//     for the {{ .capability }} substitution. The compose-bridge case
	//     drives this — the bridge service fragment lands alongside per-
	//     component compose contributors at the same dispatch path so the
	//     compose engine's AssembleBoundary merges them.
	//   - Intermediate=false: the instance name alone is the coordinate, and
	//     where it sits follows from whether the group's assembler gives each
	//     contributor a position of its own. Where it does, the attachment
	//     occupies that position, at
	//     <boundary_directory>/<provider>/<nest-segment>/<fr_instance>/; where
	//     it does not, the instance is encoded into a filename prefix and the
	//     files land directly in the boundary's own directory, at
	//     <boundary_directory>/<provider>/<fr_instance>--<file>.
	//
	// Synthetic node-IDs ("<boundary-fr>:<instance>@<boundary-path>")
	// distinguish boundary-scope contributions from per-component
	// contributions in the dispatch group's per-node maps. Both authored
	// coordinates are carried, because either alone is shared by contributors
	// an author can tell apart.
	for key, files := range boundaryFRState.Files {
		// Look up the FR by walking the release; the FR's provider is
		// what determines the dispatch path's provider component.
		feat, found := lookupBoundaryFeatureAttachment(graph.Spec.BoundaryFeatures, key.BoundaryPath, key.FRInstance)
		if !found {
			continue
		}
		// Resolve the FR for this boundary-feature instance (name tier wins,
		// else by type) to get its provider.
		fr, ok := recipeLookup.FeatureRecipe(key.FRInstance, feat.Type)
		if !ok {
			continue
		}
		// Bare engine kind for the {{ .capability }} path segment and the dispatch
		// grouping; the FR's files were already rendered through its version-pinned
		// engine upstream, so no engine resolution happens here.
		frProvider := ProviderEngineKind(fr.Spec.Capability)
		// Compute the dispatch path for this boundary-scope FR. The FR is
		// attached at the boundary level (no per-component BoundaryInfra
		// entry), so look up the boundary's authored directory/subDirectory
		// directly from the Release tree and synthesize a BoundaryInfra
		// for computeFinalPath. Operator-authored templates on the boundary
		// thus drive the FR's dispatch path the same way they drive
		// per-component dispatch paths.
		var parameters map[string]any
		if input.ResolveResult != nil {
			parameters = input.ResolveResult.Parameters
		}
		var bi *resolver.BoundaryInfra
		if input.ResolveResult != nil && input.ResolveResult.Release != nil {
			parts := strings.Split(key.BoundaryPath, "/")
			if b, ok := findBoundaryAtPath(input.ResolveResult.Release.Spec.Boundaries, parts); ok {
				bi = &resolver.BoundaryInfra{
					BoundaryPath: key.BoundaryPath,
					Directory:    b.Directory,
					SubDirectory: b.SubDirectory,
					SyncWave:     b.SyncWave,
				}
			}
		}
		// Use the FR's provider for the {{ .capability }} substitution
		// instead of any per-component provider.
		//
		// The identity handed to the path evaluation is the instance-only marker, and
		// it stays that way deliberately. It reaches the author-facing `.component_id`
		// path-template variable, so the value here is one an operator's own
		// `subDirectory:` template may read — and it is what decides which dispatch
		// group this contribution joins. Two boundaries whose declared directories
		// evaluate alike are MEANT to feed one group; sending them to two by
		// disambiguating this value would not separate two contributors, it would
		// separate two groups and take the whole question off the table.
		finalPath, err := computeFinalPath(boundaryFRNodePrefix+key.FRInstance, key.BoundaryPath, frProvider, fr.Metadata.Name, bi, parameters, boundaryDirEvals, perBoundaryAnnotations)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{}, fmt.Sprintf("boundary-scope FR %q path eval", key.FRInstance))
		}
		dKey := dispatchKey{Path: finalPath, Provider: frProvider}
		// Both coordinates the author wrote, through the one home that spells this
		// contributor's dispatch identity — the same call the render that produced
		// these files made for the same tuple, so the identity the group holds this
		// contribution under is the identity the render declared its names under.
		//
		// The instance name alone identifies one contributor per name across every
		// boundary feeding this group, so two boundaries' output occupies one entry
		// there and one of them is lost.
		syntheticNodeID := boundaryFRNodeID(key)
		// The authoring-level coordinates and the settled composed identity, stated
		// once here and read by every surface that names this contributor or derives
		// a name for its content. The producer is the same value the derived-name
		// declarations below are recorded under, so the label a path collision reports
		// this contributor by and the label a name collision reports it by are one
		// value rather than two that agree.
		frProducer := derivedKeyProducerFor(producerKindFeature, key.FRInstance, key.BoundaryPath)
		if _, ok := dispatch[dKey]; !ok {
			dispatch[dKey] = &pendingDispatch{
				files:           map[string]map[string][]byte{},
				formats:         map[string]map[string]string{},
				intermediates:   map[string][]EmittedFile{},
				placed:          map[string][]EmittedFile{},
				nodeAnnotations: map[string]map[string]string{},
				nodeComponent:   map[string]string{},
				nodeSyncWave:    map[string]int{},
				nodeValues:      map[string]map[string]any{},
				nodeProducer:    map[string]derivedKeyProducer{},
				nodeNameBasis:   map[string]string{},
				engineByNode:    map[string]Provider{},
			}
			orderedDispatch = append(orderedDispatch, dKey)
		}
		// Two tuples reaching one dispatch identity halt rather than merge, the same
		// guardrail the per-component routing above carries and for a sharper reason
		// than defensiveness. The identity joins the instance name to the boundary path
		// with a character neither part is constrained to exclude, so an attachment
		// named `a@b` at boundary `c` and one named `a` at boundary `b@c` spell one
		// identity — and either map would then take the second silently, one by
		// replacing a whole boundary's rendered output and the other by folding two
		// boundaries' fragments into one contributor, with nothing in the output
		// recording that two were declared.
		//
		// It is asked of the label registry rather than of either output map, because
		// that is the one entry every contributor makes whatever it emitted: a
		// contributor whose whole output is pre-assembly fragments writes no file map,
		// and one that emitted nothing at all writes neither, so a guard on an output
		// map would let exactly those through. The contributors are named by their own
		// labels, which carry both coordinates, so the message names the two things an
		// author would have to edit.
		if _, alreadyHere := dispatch[dKey].nodeProducer[syntheticNodeID]; alreadyHere {
			inner := fmt.Errorf("%w: dispatch path %q provider %q already holds output from %s, which reaches the same dispatch identity as %s; two contributors to one assembly group must be distinguishable",
				ErrInstanceIDCollision(), dKey.Path, dKey.Provider,
				nodeProducerLabel(dispatch[dKey].producerOf(syntheticNodeID)), nodeProducerLabel(frProducer))
			return nil, cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{}, inner.Error())
		}
		dispatch[dKey].nodeProducer[syntheticNodeID] = frProducer
		dispatch[dKey].nodeNameBasis[syntheticNodeID] = boundaryFRState.ComponentIDs[key]
		// Whether the provider whose group holds this attachment's files gives each
		// of its contributors a position of its own. Where it does, this attachment
		// IS one of those contributors and its files sit at its own position with
		// nothing further inside it; where it does not, they sit beside the
		// boundary's own files with the identity in the filename.
		//
		// The engine is resolved for the group's own provider, which is this
		// attachment's: a boundary-scope attachment forms its dispatch group under the
		// capability its own FeatureRecipe names. Resolving it cannot fail for a
		// provider this render already rendered through, so a failure here is an
		// injection fault rather than an authoring one and is reported as it arrives.
		frEngineForPlacement, ferr := engineFactory(frProvider)
		if ferr != nil {
			return nil, cerrors.Wrap(ferr, cerrors.TranslationError, cerrors.SourceLocation{}, fmt.Sprintf("boundary-scope attachment %q: selecting the plugin its output is placed by", key.FRInstance))
		}
		frGroupNests := boundaryNestingOf(frEngineForPlacement).PerNode
		intermediates := []EmittedFile{}
		nodeFiles := map[string][]byte{}
		// This feature's own placements, in emission order, for a group that gives
		// each contributor a position of its own — the half such a group's assembler
		// reads as what this contributor rendered.
		var placedOwn []EmittedFile
		// The prefix this feature's own files are keyed under inside its own file
		// map, composed once here and used to key them below.
		//
		// EMPTY where the group places each contributor at a position of its own,
		// and that is the whole of this scope's placement fix. The position this
		// feature's key already resolves to is named for its instance, so keying a
		// prefix carrying that same instance inside it would state the identity
		// twice — and the resulting directory sits a level below the one the
		// group's target discovers its contributors at, so the files are never read.
		// One identity, once, at the position the group already gives it.
		featureOutputPrefix := ""
		if !frGroupNests {
			featureOutputPrefix = attachmentOutputPrefix(key.FRInstance)
		}
		for _, f := range files {
			if f.Intermediate {
				intermediates = append(intermediates, f)
				continue
			}
			// Intermediate=false: emit under the prefix the attachment directory
			// resolved above and this attachment's instance name compose to. The
			// instance alone identifies it because a boundary-scope attachment sits
			// inside the boundary already, so naming a host would repeat what the
			// location says — and such a feature has no host component to name.
			//
			// The prefix is composed through the same home the component-scope
			// sub-pass composes its own through, so the two scopes differ only in
			// the coordinates they hand it and never in the layout they lay out.
			// Where the group's provider declared no directory, that home encodes
			// the instance into a filename prefix rather than a directory segment,
			// which lands this feature's files directly beside the boundary's own.
			// These join the group's placed map ONLY where the group places each
			// contributor at a position of its own, and the two reasons they stay out
			// otherwise are both properties of the other shapes. That map carries the
			// emission order and the input-kind origin of each file, which the FLAT
			// flush reads to decide which files move under a contributor's own
			// directory — and a boundary-scope attachment under a flat group is
			// already pre-keyed at its own position, so a second placement decision
			// there would relocate output nothing forced. And it is half the
			// contributor list an assembler is handed alongside the fragments, so
			// under a MERGING group joining it would hand the assembler this
			// contribution twice. A nesting group runs neither path: its flush reads
			// the per-key path map, and it folds nothing.
			//
			// What joining it buys under a nesting group is the half an assembler
			// reads as this contributor's rendered output. An assembler that derives
			// anything from what a contributor rendered — the name its placed
			// manifest declares, so a declared child can be bound to it — derives it
			// from an empty set otherwise, and falls back to the routing key, which is
			// a marker no target's alphabet accepts.
			//
			// Its Intermediate=true fragments still reach a fragment-merging assembler,
			// which is the case where a boundary-scope feature's content is genuinely
			// merged rather than placed.
			nodeFiles[featureOutputPrefix+f.Name] = f.Body
			if frGroupNests {
				placedOwn = append(placedOwn, f)
			}
		}
		if len(intermediates) > 0 {
			dispatch[dKey].intermediates[syntheticNodeID] = append(dispatch[dKey].intermediates[syntheticNodeID], intermediates...)
		}
		if len(placedOwn) > 0 {
			dispatch[dKey].placed[syntheticNodeID] = append(dispatch[dKey].placed[syntheticNodeID], placedOwn...)
			// Named among the group's children, for the same reason the
			// component-scope routing does it: files at a position no entry names
			// are files nothing loads. No source and no version — see that routing
			// for why each is absent rather than forgotten. The values it carries
			// are this feature's own, on the same terms: the files it placed were
			// vendored in verbatim from what its author shipped, so the assembled
			// artifact is the only surface that can supply them, and read from the
			// tuple's own record rather than re-evaluated here.
			dispatch[dKey].subCharts = append(dispatch[dKey].subCharts, SubChartInfo{
				InstanceID: syntheticNodeID,
				Values:     boundaryFRState.Values[key],
			})
		}
		if len(nodeFiles) > 0 {
			dispatch[dKey].files[syntheticNodeID] = nodeFiles
		}
		// This boundary-scope feature's derived-name declarations join the same
		// per-group check its files do, under the same authoring-level producer name
		// a path collision reports it by — the one composed above and recorded against
		// this contribution's own key, so the two surfaces read one value rather than
		// two that agree. Recorded unconditionally rather than alongside the file
		// routing above: a contributor whose output is entirely pre-assembly fragments
		// still derived the names it declared, and those names still meet the other
		// contributors' names in the merged output.
		//
		// The label carries the boundary path as well as the instance name, and both
		// are read from the accumulator's own key rather than recovered from the
		// synthetic node identity above. That identity joins the two parts with
		// characters either part may itself contain, so cutting it apart again
		// cannot return what went in.
		//
		// Where this contributor's output landed travels with the label, read from
		// the routing performed immediately above rather than from any claim about
		// it: a boundary-scope feature whose fragments reached the group's
		// assembler has its names inside the assembled document, and one whose
		// output was placed at its own coordinate does not. A feature that emitted
		// nothing reached no document either, which is the same answer and the
		// same reason.
		frContributor := derivedKeyContributorFor(frProducer, len(intermediates) == 0)
		dispatch[dKey].derivedKeys.putAll(boundaryFRState.DerivedKeys[key], boundaryFRState.DerivedKeyRepeats[key], frContributor)
		// Record the boundary as a contributor for the dispatch group's
		// diagnostic context.
		alreadyContributing := false
		for _, existing := range dispatch[dKey].contributingBoundaries {
			if existing == key.BoundaryPath {
				alreadyContributing = true
				break
			}
		}
		if !alreadyContributing {
			dispatch[dKey].contributingBoundaries = append(dispatch[dKey].contributingBoundaries, key.BoundaryPath)
		}
	}

	// Finalize per-(path, provider) output. Sort dispatch keys for
	// deterministic output ordering: by Path, then by Provider.
	sort.Slice(orderedDispatch, func(i, j int) bool {
		if orderedDispatch[i].Path != orderedDispatch[j].Path {
			return orderedDispatch[i].Path < orderedDispatch[j].Path
		}
		return orderedDispatch[i].Provider < orderedDispatch[j].Provider
	})

	// Record each group's attribution now that every contributor has been sorted
	// into one: which boundaries met which provider at which evaluated directory.
	// The output paths each group writes are filled into these same records once
	// the group has assembled, because they do not exist before that.
	//
	// It is recorded here rather than at the flush because this is the first point
	// it is complete and the last point at which acting on it still saves work:
	// the rules declared over the grouping read it, they run below, and every one
	// of them is about where a boundary's own directory declaration sent its
	// output — a question whose answer no amount of further assembly changes.
	result.Contributions = make([]AssemblyContribution, 0, len(orderedDispatch))
	for _, key := range orderedDispatch {
		result.Contributions = append(result.Contributions, AssemblyContribution{
			AssemblyPath: key.Path,
			Provider:     key.Provider,
			Boundaries:   slices.Clone(dispatch[key].contributingBoundaries),
		})
	}

	// Record what each group's elected engine claimed about combining the group's
	// output, whether it could, and what it names the result from. The candidates a
	// group collected are complete once the node walk has ended and the election is
	// a function of those alone, so nothing further in the render can change what a
	// group would elect — which is why the election is readable here, the rules
	// about it run here, and the engine is bound to the group below.
	//
	// The resolved boundary annotations come from the resolution the render was
	// handed rather than from anything the render computes, so the value recorded
	// per boundary is the one every other reader of that cascade sees.
	result.elections = newAssemblyElections(
		dispatch, orderedDispatch, resolvedBoundaryAnnotations(input.ResolveResult),
	)

	// Record the names each group's contributors declared they derived inside their
	// own output. Every contributor has rendered by now, so each group's
	// accumulator already holds every declaration it will ever hold, and the
	// accumulators themselves are loop-locals dropped when the render returns.
	//
	// It is recorded before any assembly rather than after, because a duplicate is
	// settled entirely by the declarations in hand: no assembler output is needed
	// to see one, and the rule declared over the grouping runs below — so the build
	// stops before an assembler merges output whose names were already known to
	// collide.
	result.derivedNames = newDerivedNameSets(dispatch, orderedDispatch)

	// Present how the grouping came out, with the facts complete earlier answered
	// by the presentation built before the node walk.
	result.DispatchedView = newDispatchedView(
		result.EmittedView, assemblyGroups{result: result},
	)

	// The rules declared over the grouping are invoked here, against the directory
	// and provider axes the attribution just recorded.
	//
	// The enumeration is checked for absence here rather than inside: an absent one
	// held behind the axis interface is a present value that answers nothing, and a
	// rule invoked zero times reports exactly what a rule that ran and approved
	// reports.
	// One enumeration over the grouping serves both stages keyed by it: the rules
	// declared over how the nodes were grouped, here, and the rules declared over
	// what those groups assembled, further down. Its two axes are the grouping's
	// own and are complete at this point, so building it once is what keeps the
	// subjects the two stages are invoked over from being walked two ways.
	groupAxes := newAssemblyGroupCoordinates(result, releaseFileOf(input.ResolveResult))
	if groupAxes != nil {
		if err := runDispatchedStageRules(ctx, result.DispatchedView, groupAxes); err != nil {
			return nil, err
		}
	}

	// Bind each dispatch group to its boundary assembler now that every
	// contributing node has been grouped: the engine of the highest resolved
	// provider version in the group, ties broken by node-processing order. It never
	// falls back to a lower-version engine that happens to assemble — a group whose
	// elected engine cannot assemble is left unbound, and the rule declared over the
	// grouping has already halted the build if that group's provider claimed one.
	for _, key := range orderedDispatch {
		selectBoundaryAssembler(dispatch[key])
	}

	// Retain each dispatch group's assembled output — provider identity, disk-
	// write prefix, and group-relative files — across the loop rather than
	// flushing and discarding it per group. The post-assembly cross-boundary
	// link pass runs after every group assembles and needs a producer group's and
	// a consumer group's assembled artifacts at once; the patched files are what
	// finally land in result.Files.
	assembledByDispatch := map[dispatchKey]*assembledGroup{}

	for _, key := range orderedDispatch {
		pd := dispatch[key]
		// Disk-write prefix is the evaluated final_path verbatim. Default
		// boundaries/<bp>/<provider>/ shape produced by computeFinalPath
		// when boundaries omit directory: / subDirectory: preserves
		// today's per-boundary, per-provider segregation.
		dispatchPrefix := ""
		if key.Path != "" {
			dispatchPrefix = key.Path + "/"
		}
		// Group-relative files (keyed without the dispatch prefix) so the link
		// pass addresses an assembled artifact by the name the provider
		// emitted it. Each record pairs the assembled bytes with the producer-
		// declared format (empty when the producer declared none — the consumer
		// then falls back to extension-based selection). The prefix is reattached
		// at the final flush.
		var groupFiles assembledFileSet

		// If engine is a BoundaryAssembler (helm-chart, compose) AND the dispatch path is non-empty:
		// nest per-node under the sub-directory the provider declares and emit assembled files at
		// the dispatch path.
		if pd.assembler != nil && key.Path != "" {
			edges := make([]IREdgeInfo, 0, len(graph.Spec.Edges))
			for _, e := range graph.Spec.Edges {
				edges = append(edges, IREdgeInfo{Source: e.Source, Target: e.Target})
			}
			// Cascaded assembly-name lookup: a provider whose assembler declares
			// an assembly-name annotation cascades the first resolved value from
			// its contributing boundaries into the assembler as
			// BoundaryInfo.ChartName. Empty when no cascade value resolves or the
			// provider declares no such annotation (assembler falls back to
			// path-derived naming).
			chartName := ""
			if annotationKey, _ := assemblyNameAnnotationOf(groupAssemblerEngine(pd)); annotationKey != "" && input.ResolveResult != nil {
				for _, bp := range pd.contributingBoundaries {
					if ann, ok := input.ResolveResult.BoundaryAnnotations[bp]; ok {
						if v, ok := ann.Get(annotationKey); ok && v != "" {
							chartName = v
							break
						}
					}
				}
			}
			// Where this group's contributors sit is read from the elected provider's
			// own self-description rather than from the reply below, and the reading is
			// the same one the render already made for the attachment position: both are
			// static properties of the provider, so both are answerable before any node
			// renders. A reply cannot answer the second at all — an attachment's files
			// are placed while its host renders, which is hundreds of lines and one
			// whole pass before an assembler is called.
			//
			// It is resolved before the assembler is called rather than after, because
			// the assembler is told where each of its contributors sits and cannot be
			// told from a value derived after it has replied.
			nesting := boundaryNestingOf(groupAssemblerEngine(pd))
			nestPrefix := ""
			if nesting.PerNode {
				prefix, serr := nesting.resolvedPrefix(key.Provider)
				if serr != nil {
					// The refusal's own text becomes the frame's message so the
					// provider and the value reach a reader of one frame as well
					// as a reader of the whole chain.
					return nil, cerrors.Wrap(serr, cerrors.TranslationError, cerrors.SourceLocation{File: key.Path}, serr.Error())
				}
				nestPrefix = prefix
			}
			// Each contributor's own directory, composed once from the values that
			// keyed its files and read by both the placement below and the assembler
			// above. Filled only where the provider places contributors under
			// positions of their own: a folding provider has no per-contributor
			// position for this to name.
			//
			// The contributor set is every key the group holds output for, read from
			// the one home of that question rather than from whichever file map the
			// reader had in mind — a contributor handed over as fragments places no
			// files at all and still has a position.
			var nodeOutputDirectory map[string]string
			if nesting.PerNode {
				nodeOutputDirectory = make(map[string]string, len(pd.files))
				for nodeID := range pd.contributorKeys() {
					nodeOutputDirectory[nodeID] = strings.TrimSuffix(pd.nodeRelFor(nestPrefix, nodeID), "/")
				}
			}
			info := BoundaryInfo{
				BoundaryPath:           key.Path,
				AggregateName:          graph.Metadata.AggregateName,
				YAMLIndent:             input.YAMLIndent,
				SubCharts:              pd.subCharts,
				IntermediatesByNode:    pd.intermediates,
				PlacedByNode:           pd.placed,
				NodeAnnotations:        pd.nodeAnnotations,
				NodeComponent:          pd.nodeComponent,
				NodeSyncWave:           pd.nodeSyncWave,
				NodeValues:             pd.nodeValues,
				NodeNameBasis:          pd.nodeNameBasis,
				NodeOutputDirectory:    nodeOutputDirectory,
				ContributingBoundaries: pd.contributingBoundaries,
				Edges:                  edges,
				ChartName:              chartName,
				WarningSink:            func(d Diagnostic) { result.Warnings = append(result.Warnings, d) },
			}
			boundaryFiles, err := pd.assembler.AssembleBoundary(ctx, info)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: key.Path}, fmt.Sprintf("dispatch %q assemble", key.Path))
			}
			// Post-assembly PATCH replay (scope-symmetric). A pipeline against
			// this assembler node deferred per-node because the merged documents
			// were not discrete yet: a component-scope FeatureRecipe's /host
			// pipeline, or a Recipe (or component-scope FeatureRecipe) whose own
			// provider is this assembler (its /own pipeline). Now that the
			// assembler has produced the merged documents, replay each deferred
			// pipeline against them — own-scope routed to the own root, host-scope
			// to the host root, own-before-host— in the deterministic A1
			// cascade, rewriting boundaryFiles in place before they are written.
			if recs := deferredByDispatch[key]; len(recs) > 0 {
				warnings, perr := replayDeferredPatches(ctx, boundaryFiles, key.Provider, recs, engineFactory, input)
				if perr != nil {
					return nil, perr
				}
				result.Warnings = append(result.Warnings, warnings...)
			}
			// The assembler already pairs each merged file's bytes with its
			// producer-declared format; the record travels intact so the render-
			// policy parser seam prefers the producer's format over the path
			// extension (empty format falls through to extension selection).
			assemblyProducer := "the " + key.Provider + " assembly"
			for p, af := range boundaryFiles {
				groupFiles.put(p, af, assemblyProducer)
			}
			// Node files go under the per-node sub-directory the provider declared
			// (or flat where it declares no nesting). The segment is the provider's
			// own vocabulary: the orchestrator joins it with the contributor's
			// authoring-level name without reading it, so no target's directory
			// vocabulary is fixed here. A provider that declares none gets no
			// enclosing directory — there is no host default for this position.
			// The name rather than the per-node key, because a boundary-scope
			// feature's key is a dispatch marker whose characters a path refuses.
			// The segment is resolved once per dispatch group rather than once per
			// node, so an out-of-bound segment is refused even for a group that
			// placed no per-node files at all.
			// These per-node fragments carry no producer format at this nesting
			// position, so their records leave Format empty.
			//
			// The position comes from the one home of that composition, which the
			// assembler was already told each contributor's position from — so the
			// directory named inside the assembled artifact and the directory the
			// files are written to are one value rather than two.
			for nodeID, nodeFiles := range pd.files {
				nodeRel := ""
				if nesting.PerNode {
					nodeRel = pd.nodeRelFor(nestPrefix, nodeID)
				}
				producer := nodeProducerLabel(pd.producerOf(nodeID))
				for p, c := range nodeFiles {
					groupFiles.put(nodeRel+p, AssembledFile{Content: c}, producer)
				}
			}
		} else {
			// Flat: per-node files directly under the dispatch path. Each file's
			// producer-declared format (from the per-node source, empty when the
			// producer declared none) rides on the record so the render-policy
			// parser seam can prefer it over the path extension.
			//
			// A file a node's artifacts block placed is written under that
			// contributor's own directory rather than at the dispatch path itself.
			// Such a file is named by the key its author wrote, and that key carries
			// nothing of the component the Recipe was bound to — so two components
			// in one boundary bound to one Recipe write one name, and neither
			// author has a surface on which to separate them. The directory is the
			// contributor's authoring-level name, read through the same
			// classification a finding about that contributor reads, so the
			// directory and the attribution cannot come to disagree.
			//
			// Only a directory is added: the filename stays the author-declared key,
			// which is what the whole artifacts naming rests on — an author reads
			// back the name they wrote rather than one derived from something else.
			//
			// The other input kinds stay where they are. A rendered or synthesized
			// file is named by the provider from the node it belongs to, so it
			// already carries its contributor, and moving it would relocate output
			// nothing forced.
			//
			// Which files those are is read from what the node PLACED, the record
			// carrying each file's input-kind origin. Output a node acquired after
			// that record was taken — an attachment's contribution, or a
			// host-supplied patch — appears in none of it and stays flat, which is
			// what leaves a boundary-scope attachment's own layout untouched.
			for nodeID, nodeFiles := range pd.files {
				nodeFormats := pd.formats[nodeID]
				producer := nodeProducerLabel(pd.producerOf(nodeID))
				artifactsDir := nodeOutputSegment(pd.producerOf(nodeID)) + "/"
				fromArtifacts := make(map[string]struct{}, len(pd.placed[nodeID]))
				for _, f := range pd.placed[nodeID] {
					if f.Origin == EmitOriginArtifactsInline || f.Origin == EmitOriginArtifactsImport {
						fromArtifacts[f.Name] = struct{}{}
					}
				}
				for p, c := range nodeFiles {
					path := p
					if _, isArtifact := fromArtifacts[p]; isArtifact {
						path = artifactsDir + p
					}
					groupFiles.put(path, AssembledFile{Content: c, Format: nodeFormats[p]}, producer)
				}
			}
		}

		files, producers := groupFiles.resolve()
		assembledByDispatch[key] = &assembledGroup{
			provider:  key.Provider,
			prefix:    dispatchPrefix,
			files:     files,
			producers: producers,
		}
	}

	// Post-assembly cross-boundary link pass. Now that every dispatch group has
	// assembled, wire the cross-boundary uses edges — those whose endpoints land
	// in different dispatch groups — through the providers' link exports, editing
	// the retained assembled artifacts in place. Inert on inputs whose providers
	// advertise no linking (no file change, no warning).
	releaseID := ""
	if input.ResolveResult != nil && input.ResolveResult.Release != nil {
		releaseID = input.ResolveResult.Release.Metadata.Name
	}
	linkWarnings, err := wireCrossBoundaryLinks(ctx, graph, allProvides, nodeDispatch, nodeEngine, nodeAnnotations, assembledByDispatch, engineFactory, input.YAMLIndent, releaseID)
	if err != nil {
		return nil, err
	}
	result.Warnings = append(result.Warnings, linkWarnings...)

	// Flush the retained (and possibly link-patched) assembled output into the
	// result, reattaching each group's disk-write prefix. Groups iterate in
	// orderedDispatch order for a deterministic walk.
	//
	// The same walk completes each group's attribution, so the paths a group wrote
	// and the contributors that wrote each of them join the boundaries and the
	// provider already recorded for it, and the whole coordinate set outlives the
	// assembly that knew it. The file map alone is keyed by output path, which is
	// the right key for a build's output and the wrong one for asking who produced
	// it.
	//
	// The per-path attribution is rekeyed onto the same full paths as it is
	// carried over, so one key set serves both and a reader holding a path from
	// one can ask the other about it. A path the link pass introduced carries no
	// contributor, which is the truthful answer: it was written by that pass
	// rather than placed by any producer.
	//
	// The records are indexed by position rather than searched for: they were
	// appended in this same order, over this same key set, so the i-th record is
	// the i-th group by construction and no lookup can pair them wrongly.
	for i, key := range orderedDispatch {
		grp := assembledByDispatch[key]
		outputPaths := make([]string, 0, len(grp.files))
		pathProducers := make(map[string][]string, len(grp.files))
		for p, af := range grp.files {
			full := grp.prefix + p
			result.Files[full] = af
			outputPaths = append(outputPaths, full)
			if producers, placed := grp.producers[p]; placed {
				pathProducers[full] = producers
			}
		}
		sort.Strings(outputPaths)
		result.Contributions[i].OutputPaths = outputPaths
		result.Contributions[i].PathProducers = pathProducers
	}

	// Present what this render produced, with the facts complete earlier answered
	// by the presentation built before the node walk. Built here rather than beside
	// that one because this is the first point holding every product it names: the
	// output paths the loop above has just filled in, and the artifact retention the
	// walk has filled. Building it earlier would hand a reader a presentation whose
	// path facts answer from an attribution not yet completed.
	result.RenderedView = newRenderedView(
		result.DispatchedView, assembledOutput{result: result}, input.artifacts,
	)

	// The rules declared over what this render assembled are invoked here, against
	// the same group axes the stage before this ran over — the paths and the
	// contributors they ask about are read as facts of the group rather than
	// enumerated as axes. It is the
	// first point holding both the presentation and the enumeration, and it is after
	// the cross-boundary link pass on purpose: a resolved link replaces its own
	// placeholder, so a reading taken before resolution would object to every marker
	// that pass was about to remove.
	//
	// The enumeration is checked for absence here rather than inside: an absent one
	// held behind the axis interface is a present value that answers nothing, and a
	// rule invoked zero times reports exactly what a rule that ran and approved
	// reports.
	if groupAxes != nil {
		if err := runRenderedStageRules(
			ctx, input.GraphView != nil, result.RenderedView, groupAxes,
		); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// resolveFeatureRecipes builds the node's attachment refs in cascade order —
// the one order vector the IR recorded — resolving each attachment's
// FeatureRecipe by its Type. It is the single source both Phase A and Phase B.6
// draw their sequence from, so the two phases cannot disagree on order. An
// attachment whose FeatureRecipe is not found is skipped.
func resolveFeatureRecipes(
	nodeID string,
	featureOrder map[string][]string,
	graph *types.IRGraph,
	recipeLookup RecipeLookup,
) []FeatureRecipeRef {
	order, ok := featureOrder[nodeID]
	if !ok || recipeLookup.FeatureRecipe == nil {
		return nil
	}

	fvm, ok := graph.Spec.FeatureValuesMapping.Get(nodeID)
	if !ok {
		return nil
	}

	var refs []FeatureRecipeRef
	for _, instanceName := range order {
		entry, ok := fvm.Get(instanceName)
		if !ok {
			continue
		}
		fr, found := recipeLookup.FeatureRecipe(instanceName, entry.Type)
		if !found {
			continue
		}
		refs = append(refs, FeatureRecipeRef{
			InstanceName:  instanceName,
			FeatureRecipe: fr,
		})
	}
	return refs
}

// gateInputKind is the central input-kind capability gate. It confirms the
// resolved provider's engine supports EVERY input kind the node declares — once
// per node, BEFORE any Phase B dispatch (engine.Render, B.3 artifacts, B.4
// synthesis), so a kind the provider cannot honour halts loudly before emission.
//
// Every declared kind, not one, and that is the whole of what it checks. A node
// may declare a source, a workload and artifacts together; the kinds compose by
// addition and their outputs are added together, so each one is separately
// dispatched and each one separately needs a provider that can honour it. A gate
// consulting only the kind a node's mode is NAMED after would be satisfied by a
// node whose other declared kind reaches a guest that cannot render it — and the
// guest would then refuse in its own words, as an internal mis-routing rather
// than the authoring fault it is.
//
// Nothing here refuses a combination for BEING a combination. The declared kinds
// are consulted independently and a node is refused only for a kind its provider
// does not claim, so every combination a provider can honour passes. artifacts is
// consulted against ArtifactsStandalone only when it is the node's sole declared
// kind: alongside a source or a workload it rides through as composed artifacts,
// which that capability does not govern. declaredNodeInputKinds is where that
// bound and the source → workload → artifacts report order both live.
//
// The first unhonourable kind in that order is the one reported. A node with two
// of them names one fault at a time, which keeps the diagnostic about a single
// line of the document rather than a list.
//
// Both the component render path (RenderAll) and the FeatureRecipe render
// sites call this, so a Recipe and a FeatureRecipe bound to the same provider
// gate identically — there is one gate, not a parallel FR track.
//
// This is also the only place an unsupported combination becomes an authoring
// diagnostic. Engines carry no mode-rejection logic of their own, which is what
// keeps the message an author sees identical whichever provider refused and
// stops a fourth and fifth provider from each inventing their own wording. The
// consequence for whoever adds a provider: declare the input kinds and write no
// rejection switch. The consequence for whoever debugs one: an engine that is
// handed a kind its own report did not declare has been mis-routed — that is a
// defect on this path, not something the author of the Recipe can fix.
//
// declaredNodeInputKinds is also what the annotation-support advisory reads, so
// the two per-kind readings of a node agree on which kinds it renders through;
// each kind's scope and user-facing name come from classifyNodeInputKind, so an
// input kind's name is stated once rather than once per reader.
//
// A node declaring no input kind at all is passed without the provider's contract
// being read, and the early return is load-bearing rather than a micro-optimisation:
// reading the contract calls the guest's describe(), so consulting it here would
// load a guest for a node that renders nothing — a FeatureRecipe may legitimately
// declare no input kind, being pure-wiring or inject-only.
func gateInputKind(nodeID, providerName string, engine Provider, hasSource, hasWorkload, hasArtifacts bool) error {
	declared := declaredNodeInputKinds(hasSource, hasWorkload, hasArtifacts)
	if len(declared) == 0 {
		return nil
	}
	caps := engine.Contract()
	for _, kind := range declared {
		if kind.claimedBy(caps) {
			continue
		}
		return unsupportedInputKindError(nodeID, providerName, kind.Display, caps)
	}
	return nil
}

// routeNodeInputDiagnostics splits a provider's per-node input-validation
// diagnostics by severity: warn- and info-severity entries go to the node's
// warning sink (nil-safe; the orchestrator wires it to result.Warnings), and
// halt-severity entries halt the build with the ErrProviderNodeInputInvalid
// Loud Guardrail, naming the node, the provider, and the guest's messages.
// Warnings never affect emitted bytes. The component render path and the
// FeatureRecipe render path share this routing, so a provider's input check
// reads identically on both surfaces.
//
// An advisory keeps the node and provider as its location rather than as a
// prefix on its text, so a consumer that reads no terminal can attribute the
// finding without parsing it back out of a sentence. A guest that set its own
// location keeps it: it knows where inside the input the fault sits, which this
// crossing does not.
func routeNodeInputDiagnostics(nodeID, providerName string, diags []Diagnostic, sink func(Diagnostic)) error {
	errs, advisories := SplitDiagnostics(diags)
	for _, a := range advisories {
		if sink != nil {
			if a.Location == nil {
				a.Location = &DiagnosticLocation{URI: nodeID, Kind: providerName}
			}
			sink(a)
		}
	}
	if len(errs) > 0 {
		inner := fmt.Errorf("%w: node %q, provider %q: %s",
			ErrProviderNodeInputInvalid(), nodeID, providerName, strings.Join(errs, "; "))
		return cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, inner.Error())
	}
	return nil
}

// unsupportedInputKindError is the single uniform diagnostic the central
// input-kind gate emits when a provider's engine does not support the node's
// mode. It names the provider, the unsupported mode, the kinds the provider
// does support, and the remedy — framed as a provider capability, not a
// "compute mandatory" semantic opinion (Invariant 1).
//
// The diagnostic opens with the sentence ErrUnsupportedInputKind registers and
// appends what this occurrence supplies after it, which is what puts the
// condition's short code in the head a reader matches on. Opening with the
// registered sentence is load-bearing rather than stylistic: the chain walk that
// renders a halt prints any frame whose text the output does not already carry,
// so a message that did not lead with the sentence would print it again as a
// redundant continuation line beneath the diagnostic.
//
// The occurrence's own clause stays a single call to UnsupportedInputKindMessage
// so that the provider-capability wording has one home across both surfaces that
// report it — this gate at render, and the early detector that reads a Recipe on
// its own. The clause repeating in miniature what the sentence states is the
// price of that: the early detector's boundary prints no head, so the clause is
// the only statement of the fault a reader gets there and cannot be reduced to
// occurrence values alone.
func unsupportedInputKindError(nodeID, providerName, mode string, caps ProviderContract) error {
	return cerrors.Wrap(ErrUnsupportedInputKind(), cerrors.CatalogError,
		cerrors.SourceLocation{File: nodeID},
		cerrors.ComposeMessage(ErrUnsupportedInputKind(),
			[]string{fmt.Sprintf("node %q", nodeID)},
			UnsupportedInputKindMessage(providerName, mode, caps)))
}

// UnsupportedInputKindMessage is the provider-capability clause shared by the
// render-time input-kind gate and the early lint detector, so an unsupported
// (mode, provider) reads identically whether it surfaces at lint or at render.
// It names the provider, the unsupported mode, the kinds the provider does
// support, and the remedy — framed as a provider capability, not a "compute
// mandatory" semantic opinion (Invariant 1). Callers prefix it with the
// offending entity: the gate with the flattened node id, the lint detector with
// the Recipe/FeatureRecipe identity.
func UnsupportedInputKindMessage(providerName, mode string, caps ProviderContract) string {
	return fmt.Sprintf(
		"provider %q does not support %s recipes (this provider supports: %s); "+
			"declare one of the supported input kinds, or target a provider that supports %s",
		providerName, mode, supportedKindsList(caps), mode)
}

// supportedKindsList renders a provider's supported input kinds for the
// diagnostic, in source → synthesized → artifacts-only order. Returns "none"
// when the provider supports no input kind (not expected for a rendering
// provider).
func supportedKindsList(caps ProviderContract) string {
	var kinds []string
	if caps.Sourced {
		kinds = append(kinds, "sourced")
	}
	if caps.Synthesized {
		kinds = append(kinds, "synthesized")
	}
	if caps.ArtifactsStandalone {
		kinds = append(kinds, "artifacts-only")
	}
	if len(kinds) == 0 {
		return "none"
	}
	return strings.Join(kinds, ", ")
}

func buildNodeRenderContext(
	nodeID string,
	node types.IRNode,
	graph *types.IRGraph,
	input RendererInput,
) NodeRenderContext {
	attachments, _ := graph.Spec.FeatureValuesMapping.Get(nodeID)
	nrc := NodeRenderContext{
		NodeID:       nodeID,
		Annotations:  flattenOrderedStringMap(node.ReleaseAnnotations),
		BoundaryPath: node.BoundaryPath,
		SyncWave:     node.SyncWave,
		Attachments:  attachments,
		YAMLIndent:   input.YAMLIndent,
		Provenance: ProvenanceContext{
			CompilerVersion:  graph.Spec.Provenance.CompilerVersion,
			AggregateVersion: graph.Spec.Provenance.AggregateVersion,
		},
	}
	if graph.Metadata.AggregateName != "" {
		nrc.Provenance.AggregateName = graph.Metadata.AggregateName
	}
	if input.ResolveResult != nil && input.ResolveResult.Release != nil {
		nrc.Provenance.ReleaseName = input.ResolveResult.Release.Metadata.Name
		nrc.Provenance.Environment = input.ResolveResult.Release.Metadata.Environment
	}
	return nrc
}

// selectBoundaryAssembler binds a dispatch group to the engine that assembles it.
// The assembler is the engine of the highest resolved provider version among the
// group's nodes (providerVersionGreater); a version-less node, one carrying no
// resolved plugin version, ranks below any versioned node. Ties break on
// node-processing order: candidates append in that order and the scan replaces the
// leader only on a strict version win, so the earliest node keeps the assembler on
// a tie.
//
// It binds only an engine that can carry an assembly out. A group whose elected
// engine cannot leaves the assembler unset and finalizes flat, as does a group
// whose elected engine makes no assembly claim and a group with no candidates at
// all (e.g. only boundary-scope FeatureRecipe contributions).
//
// The mismatch between what a group's elected provider claims and what the plugin
// serving it offers is not decided here: it is a declared rule over the grouping,
// read from the two facts the election records and raised before any group is
// bound, so a build that would emit unassembled fragments has already halted by
// the time this runs.
func selectBoundaryAssembler(pd *pendingDispatch) {
	if a, ok := groupAssemblerEngine(pd).(BoundaryAssembler); ok {
		pd.assembler = a
	}
}

// groupAssemblerEngine returns the engine that assembles a dispatch group — the
// highest resolved-provider-version candidate, ties broken by node-processing
// order (candidates append in that order and the scan replaces the leader only on
// a strict version win). It is the single election every reader of a group's
// provider-level claim consults: selectBoundaryAssembler, which sets pd.assembler;
// the record the render takes of what each group elected, which is what the rules
// about the grouping read their facts from; and the cascade that names an
// assembled artifact. So a group reads its claim from the same engine that
// assembles it. Returns nil when the group has no candidates — e.g. only
// boundary-scope FeatureRecipe contributions — in which case the group finalizes
// flat.
func groupAssemblerEngine(pd *pendingDispatch) Provider {
	best := -1
	for i := range pd.assemblerCandidates {
		if best == -1 || providerVersionGreater(pd.assemblerCandidates[i].version, pd.assemblerCandidates[best].version) {
			best = i
		}
	}
	if best == -1 {
		return nil
	}
	return pd.assemblerCandidates[best].engine
}

func tmplCtxValues(graph *types.IRGraph, nodeID string) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	raw, ok := graph.Spec.ValuesMapping.Get(nodeID)
	if !ok {
		return out
	}
	_ = parse.IRPayload(raw, &out)
	return out
}

func tmplCtxInfra(graph *types.IRGraph, nodeID string) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	raw, ok := graph.Spec.InfraMapping.Get(nodeID)
	if !ok {
		return out
	}
	_ = parse.IRPayload(raw, &out)
	return out
}

// computeFinalPath evaluates the per-component path templates against the
// path-template variable allowlist and returns the dispatch path used for
// per-(path, provider) grouping.
//
// bi is the resolver-side per-component BoundaryInfra (the same struct
// already threaded through the orchestrator from input.ResolveResult.
// BoundaryInfra). Pass nil to use defaults across the board.
//
// Defaults:
//   - bi nil OR bi.Directory empty → "boundaries/<bp>" (today's per-boundary segregation).
//   - bi nil OR bi.SubDirectory empty → render `{{ .capability }}` against the per-component
//     provider name.
//
// Available template variables: .capability, .boundary_name, .parameters.<key>,
// .component_id (subDirectory: only), .manifest.recipe_name, .parent_boundary.*.
//
// boundaryDirEvals is the precomputed per-boundary evaluated `directory:`
// map produced by evaluateAllBoundaryDirectories; it sources
// .parent_boundary.directory for the IMMEDIATE parent boundary of bp.
// perBoundaryAnnotations sources .parent_boundary.annotations from the
// resolver's cascaded annotation map. Both default to no .parent_boundary.*
// when the parent path is empty (root-level boundary) or absent from the
// maps. .parent_boundary.parameters mirrors .parameters since parameters
// are Release-global.
func computeFinalPath(
	nodeID, bp, providerName, recipeName string,
	bi *resolver.BoundaryInfra,
	parameters map[string]any,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) (string, error) {
	data := PathTemplateContext{
		Capability:     providerName,
		BoundaryName:   bp,
		Parameters:     parameters,
		ComponentID:    nodeID,
		Manifest:       PathTemplateManifest{RecipeName: recipeName},
		ParentBoundary: parentBoundaryData(bp, parameters, boundaryDirEvals, perBoundaryAnnotations),
	}

	// Evaluate directory:
	var evalDir string
	if bi != nil && bi.Synthesized {
		// The compiler-synthesized `default` boundary emits flat to dist/ —
		// no "boundaries/<bp>" segregation segment. Keyed on the provenance
		// flag, NOT the name: a boundary a user explicitly named "default"
		// has Synthesized == false and still segregates to boundaries/default.
		evalDir = ""
	} else if bi == nil || bi.Directory == "" {
		// Default: today's per-boundary segregation. No template eval needed.
		evalDir = "boundaries/" + bp
	} else {
		var err error
		evalDir, err = EvalPathTemplate("directory:"+bp, "directory", bp, bi.Directory, data)
		if err != nil {
			return "", err
		}
	}

	// Evaluate subDirectory:
	subTmpl := ""
	if bi != nil {
		subTmpl = bi.SubDirectory
	}
	if subTmpl == "" {
		subTmpl = "{{ .capability }}"
	}
	evalSub, err := EvalPathTemplate("subDirectory:"+bp+":"+nodeID, "subDirectory", bp, subTmpl, data)
	if err != nil {
		return "", err
	}

	if evalSub == "" || evalSub == "." {
		return evalDir, nil
	}
	// The dispatch path is a slash-separated output name, not a host path: it
	// prefixes every emitted file's name and reaches an assembling provider as its
	// boundary path, so it is joined in slash arithmetic on every platform and
	// converted to a host path only where a file is written.
	return path.Join(evalDir, evalSub), nil
}

// parentBoundaryData returns the .parent_boundary.* data map for path-template
// evaluation at boundary path bp. The parent path is the prefix of bp before
// the last `/`; root-level boundaries return an empty map (no parent). Fields:
//
//   - directory: parent's evaluated directory string from boundaryDirEvals,
//     or the default "boundaries/<parentPath>" when the parent was not
//     evaluated explicitly (parent has no `directory:` declared and the
//     pre-compute walk skipped it).
//   - boundary_name: the parent's authored name (last segment of parent path).
//   - parameters: the same Release-global parameters map.
//   - annotations: parent's cascaded annotations from perBoundaryAnnotations.
//
// Returns an empty map for root-level boundaries; references like
// `.parent_boundary.directory` then resolve to nil and trigger missingkey=error
// per the path-template strictness policy.
func parentBoundaryData(
	bp string,
	parameters map[string]any,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) map[string]any {
	parentPath, parentName, ok := splitParentPath(bp)
	if !ok {
		return map[string]any{}
	}
	parentDir, hasEval := boundaryDirEvals[parentPath]
	if !hasEval {
		parentDir = "boundaries/" + parentPath
	}
	annotations := map[string]string{}
	if pa, ok := perBoundaryAnnotations[parentPath]; ok {
		for k, v := range pa.All() {
			annotations[k] = v
		}
	}
	return map[string]any{
		"directory":     parentDir,
		"boundary_name": parentName,
		"parameters":    parameters,
		"annotations":   annotations,
	}
}

// splitParentPath returns the parent boundary path and parent boundary name
// for a slash-separated boundary path bp. For "cluster/services-net":
// ("cluster", "cluster", true). For "a/b/c": ("a/b", "b", true). For a
// root-level boundary "cluster" (no slash): ("", "", false).
func splitParentPath(bp string) (parentPath, parentName string, ok bool) {
	idx := strings.LastIndex(bp, "/")
	if idx < 0 {
		return "", "", false
	}
	parentPath = bp[:idx]
	if pIdx := strings.LastIndex(parentPath, "/"); pIdx >= 0 {
		parentName = parentPath[pIdx+1:]
	} else {
		parentName = parentPath
	}
	return parentPath, parentName, true
}

// evaluateAllBoundaryDirectories walks the Release boundary tree top-down and
// returns a map keyed by boundary path of evaluated `directory:` strings.
// The eval at each boundary uses the parent's already-evaluated directory as
// `.parent_boundary.directory` (cascade). `.capability` is empty during the walk
// because a boundary-level `directory:` cannot meaningfully reference a
// per-component provider — per-provider segregation is the `subDirectory:`
// concern (default `{{ .capability }}`). Boundaries without an explicit
// `directory:` declaration are skipped (consumers fall back to the default
// "boundaries/<bp>" string in parentBoundaryData).
//
// perBoundaryAnnotations sources `.parent_boundary.annotations` for nested
// boundaries — read by the parent_boundary cascade walk before the parent's
// own eval finalises.
func evaluateAllBoundaryDirectories(
	release *types.Release,
	parameters map[string]any,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) (map[string]string, error) {
	out := map[string]string{}
	if release == nil {
		return out, nil
	}
	var walk func(name, parentPath string, b types.Boundary) error
	walk = func(name, parentPath string, b types.Boundary) error {
		currentPath := name
		if parentPath != "" {
			currentPath = parentPath + "/" + name
		}
		if b.Directory != "" {
			// Capability and Manifest.RecipeName are left zero: this walk runs once
			// per boundary, before any component is placed, so neither a provider
			// binding nor a bound recipe exists to name. They render as empty
			// segments, which is what a boundary-level directory template gets today.
			data := PathTemplateContext{
				BoundaryName:   currentPath,
				Parameters:     parameters,
				ParentBoundary: parentBoundaryData(currentPath, parameters, out, perBoundaryAnnotations),
			}
			eval, err := EvalPathTemplate("directory:"+currentPath, "directory", currentPath, b.Directory, data)
			if err != nil {
				return cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: currentPath}, fmt.Sprintf("boundary %q directory eval", currentPath))
			}
			out[currentPath] = eval
		}
		for childName, childBoundary := range b.Boundaries.All() {
			if err := walk(childName, currentPath, childBoundary); err != nil {
				return err
			}
		}
		return nil
	}
	for bName, boundary := range release.Spec.Boundaries.All() {
		if err := walk(bName, "", boundary); err != nil {
			return nil, err
		}
	}
	return out, nil
}
