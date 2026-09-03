// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// contributionNode is one component instance in an attribution corpus case: the
// boundary it is declared in, the provider that renders it, and the output
// directory that boundary declares. Two nodes whose boundaries declare one
// directory under one provider are what makes their evaluated paths converge, so
// the directory is per node here rather than fixed by the case.
type contributionNode struct {
	id           string
	boundary     string
	provider     string
	directory    string
	subDirectory string
}

// contributionCase is one render of an attribution corpus: a set of component
// instances and whether their provider assembles its group's fragments.
type contributionCase struct {
	name      string
	nodes     []contributionNode
	assembles bool
}

// contributionEngine is a synthetic provider engine that emits one file named
// after the node it rendered, so every node's contribution is identifiable in
// the rendered output by inspection. It implements neither BoundaryAssembler nor
// BoundaryAssemblyReporter, so a group of these finalizes flat — per-node files
// land directly under the group's path.
type contributionEngine struct {
	capability string
}

func (e *contributionEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return []renderer.RenderedFile{{
		Path:    nodeCtx.NodeID + ".yaml",
		Content: []byte("instance: " + nodeCtx.NodeID + "\n"),
	}}, nil
}

func (e *contributionEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// contributionAssemblerEngine adds boundary assembly to contributionEngine,
// nesting each contributor under its own instance so two boundaries merging into
// one group produce no path collision.
type contributionAssemblerEngine struct {
	contributionEngine
}

func (e *contributionAssemblerEngine) BoundaryAssembly() string { return "nest" }

// NestSegment names the directory this provider's contributors sit under. A
// nesting provider names its own, because the host invents none: any name the
// host could supply would be some target's word in the provider-agnostic core.
func (e *contributionAssemblerEngine) NestSegment() string { return "charts" }

func (e *contributionAssemblerEngine) AssembleBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string]renderer.AssembledFile, error) {
	// The merged document names every boundary that fed the group, in the order
	// the group recorded them, so the assembled bytes themselves carry evidence
	// of a many-to-one merge.
	var b strings.Builder
	b.WriteString("assembly: " + e.capability + "\n")
	for _, bp := range info.ContributingBoundaries {
		b.WriteString("- " + bp + "\n")
	}
	return map[string]renderer.AssembledFile{
		"assembly.yaml": {Content: []byte(b.String()), Format: "yaml"},
	}, nil
}

// runContributionCase drives the full render pipeline over one corpus case.
func runContributionCase(t *testing.T, c contributionCase) *renderer.RenderResult {
	t.Helper()

	nodes := types.OrderedMap[types.IRNode]{}
	infra := map[string]*resolver.BoundaryInfra{}
	providerOf := map[string]string{}
	for _, n := range c.nodes {
		nodes.Set(n.id, types.IRNode{Component: "core/api", BoundaryPath: n.boundary})
		infra[n.id] = &resolver.BoundaryInfra{
			BoundaryPath: n.boundary,
			Directory:    n.directory,
			SubDirectory: n.subDirectory,
		}
		providerOf[n.id] = n.provider
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "attribution-app"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{},
			BoundaryInfra: infra,
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(instanceName, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "attribution-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: providerOf[instanceName], Source: "./chart"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	factory := func(providerRef string) (renderer.Provider, error) {
		if c.assembles {
			return &contributionAssemblerEngine{contributionEngine{capability: providerRef}}, nil
		}
		return &contributionEngine{capability: providerRef}, nil
	}

	res, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("case %q: RenderAll: %v", c.name, err)
	}
	return res
}

// contributionCorpus is the attribution corpus. It is deliberately not
// one-to-one throughout: three of its cases put two boundaries on one evaluated
// path, which is the case the attribution exists for and which a corpus of
// per-boundary outputs would never reach.
func contributionCorpus() []contributionCase {
	return []contributionCase{
		{
			name: "one boundary, one provider, flat",
			nodes: []contributionNode{
				{id: "api", boundary: "billing", provider: "flatprov", directory: "dist/billing"},
			},
		},
		{
			name: "two boundaries, distinct paths, flat (one-to-one)",
			nodes: []contributionNode{
				{id: "api", boundary: "billing", provider: "flatprov", directory: "dist/billing"},
				{id: "web", boundary: "payments", provider: "flatprov", directory: "dist/payments"},
			},
		},
		{
			name: "two boundaries, one evaluated path, flat (many-to-one)",
			nodes: []contributionNode{
				{id: "api", boundary: "billing", provider: "flatprov", directory: "dist/shared"},
				{id: "web", boundary: "payments", provider: "flatprov", directory: "dist/shared"},
			},
		},
		{
			name:      "two boundaries, one evaluated path, assembled (many-to-one)",
			assembles: true,
			nodes: []contributionNode{
				{id: "api", boundary: "billing", provider: "asmprov", directory: "dist/shared"},
				{id: "web", boundary: "payments", provider: "asmprov", directory: "dist/shared"},
			},
		},
		{
			name:      "three boundaries, one evaluated path, assembled (many-to-one)",
			assembles: true,
			nodes: []contributionNode{
				{id: "api", boundary: "billing", provider: "asmprov", directory: "dist/shared"},
				{id: "web", boundary: "payments", provider: "asmprov", directory: "dist/shared"},
				{id: "worker", boundary: "ledger", provider: "asmprov", directory: "dist/shared"},
			},
		},
		{
			name: "one boundary, two providers, distinct paths",
			nodes: []contributionNode{
				{id: "api", boundary: "billing", provider: "flatprov", directory: "dist/billing"},
				{id: "web", boundary: "billing", provider: "otherprov", directory: "dist/billing"},
			},
		},
		{
			name: "two boundaries, two providers, one directory",
			nodes: []contributionNode{
				{id: "api", boundary: "billing", provider: "flatprov", directory: "dist/shared"},
				{id: "web", boundary: "payments", provider: "otherprov", directory: "dist/shared"},
			},
		},
	}
}

// sortedFileKeys is the corpus's own path projection, kept independent of the
// attribution index so a comparison between the two is a real cross-check.
func sortedFileKeys(files map[string]renderer.AssembledFile) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestContributionCorpusExercisesManyToOne is the corpus's own anti-needle. The
// attribution proofs below are worthless over a corpus where every boundary maps
// to its own path, so the property that makes the corpus adequate is asserted
// here rather than assumed: several cases must place more than one boundary on
// one evaluated path under one provider, and every case must render something.
func TestContributionCorpusExercisesManyToOne(t *testing.T) {
	t.Parallel()

	manyToOne := 0
	for _, c := range contributionCorpus() {
		// Count the (directory, provider) pairs claimed by more than one boundary.
		boundariesPerPair := map[string]map[string]bool{}
		for _, n := range c.nodes {
			pair := n.directory + "\x00" + n.provider
			if boundariesPerPair[pair] == nil {
				boundariesPerPair[pair] = map[string]bool{}
			}
			boundariesPerPair[pair][n.boundary] = true
		}
		shared := false
		for _, bs := range boundariesPerPair {
			if len(bs) > 1 {
				shared = true
			}
		}
		if shared {
			manyToOne++
		}

		res := runContributionCase(t, c)
		if len(res.Files) == 0 {
			t.Errorf("case %q rendered no files; an empty case proves nothing", c.name)
		}
	}
	if manyToOne < 3 {
		t.Errorf("corpus exercises the many-to-one case %d times; the attribution proofs need it and a "+
			"one-to-one corpus would pass with the defect intact", manyToOne)
	}
}

// corpusOutput is the corpus's emitted output, path to content, captured before
// the attribution index existed. Attribution is additive — it records who
// produced what and changes nothing about what is produced — so this table is
// what holds that claim to account: any edit that moves a byte of rendered
// output fails here, naming the path and both texts.
//
// The content is pinned rather than digested so a failure reads as a diff rather
// than as two opaque hexadecimal strings.
func corpusOutput() map[string]map[string]string {
	return map[string]map[string]string{
		"one boundary, one provider, flat": {
			"dist/billing/flatprov/api.yaml": "instance: api\n",
		},
		"two boundaries, distinct paths, flat (one-to-one)": {
			"dist/billing/flatprov/api.yaml":  "instance: api\n",
			"dist/payments/flatprov/web.yaml": "instance: web\n",
		},
		"two boundaries, one evaluated path, flat (many-to-one)": {
			"dist/shared/flatprov/api.yaml": "instance: api\n",
			"dist/shared/flatprov/web.yaml": "instance: web\n",
		},
		"two boundaries, one evaluated path, assembled (many-to-one)": {
			"dist/shared/asmprov/assembly.yaml":       "assembly: asmprov\n- billing\n- payments\n",
			"dist/shared/asmprov/charts/api/api.yaml": "instance: api\n",
			"dist/shared/asmprov/charts/web/web.yaml": "instance: web\n",
		},
		"three boundaries, one evaluated path, assembled (many-to-one)": {
			"dist/shared/asmprov/assembly.yaml":             "assembly: asmprov\n- billing\n- payments\n- ledger\n",
			"dist/shared/asmprov/charts/api/api.yaml":       "instance: api\n",
			"dist/shared/asmprov/charts/web/web.yaml":       "instance: web\n",
			"dist/shared/asmprov/charts/worker/worker.yaml": "instance: worker\n",
		},
		"one boundary, two providers, distinct paths": {
			"dist/billing/flatprov/api.yaml":  "instance: api\n",
			"dist/billing/otherprov/web.yaml": "instance: web\n",
		},
		"two boundaries, two providers, one directory": {
			"dist/shared/flatprov/api.yaml":  "instance: api\n",
			"dist/shared/otherprov/web.yaml": "instance: web\n",
		},
	}
}

// TestContributionCorpusOutputUnchanged holds the emitted bytes to the capture
// taken before the attribution index existed.
func TestContributionCorpusOutputUnchanged(t *testing.T) {
	t.Parallel()

	pinned := corpusOutput()
	compared := 0
	for _, c := range contributionCorpus() {
		want, ok := pinned[c.name]
		if !ok {
			t.Errorf("case %q has no pinned output; a case with nothing to compare against proves nothing", c.name)
			continue
		}
		res := runContributionCase(t, c)
		if got, wantN := len(res.Files), len(want); got != wantN {
			t.Errorf("case %q emitted %d files, pinned capture has %d\n got: %v\nwant: %v",
				c.name, got, wantN, sortedFileKeys(res.Files), sortedStringKeys(want))
		}
		for p, wantBody := range want {
			af, ok := res.Files[p]
			if !ok {
				t.Errorf("case %q no longer emits %q", c.name, p)
				continue
			}
			if string(af.Content) != wantBody {
				t.Errorf("case %q moved bytes at %q\n got: %q\nwant: %q", c.name, p, string(af.Content), wantBody)
			}
			compared++
		}
	}
	if compared == 0 {
		t.Fatal("compared no files; a byte-identity claim over an empty comparison is vacuous")
	}
	if compared != 16 {
		t.Errorf("compared %d files; the capture covers 16, so the comparison has silently narrowed", compared)
	}
}

// TestAssemblyContributionPartitionsEmittedFiles proves the index describes the
// emitted output exactly: every path it names is emitted, no two groups claim
// one path, and between them they claim every emitted path.
//
// The reference side is taken straight off the file map rather than through the
// index or its accessors, so the two sides are independent and the comparison is
// a real cross-check rather than the index agreeing with itself.
func TestAssemblyContributionPartitionsEmittedFiles(t *testing.T) {
	t.Parallel()

	for _, c := range contributionCorpus() {
		res := runContributionCase(t, c)

		claimedBy := map[string]string{}
		var claimed []string
		for _, ct := range res.Contributions {
			for _, p := range ct.OutputPaths {
				if prev, dup := claimedBy[p]; dup {
					t.Errorf("case %q: %q is claimed by both %q and %q; a path belongs to one group",
						c.name, p, prev, ct.AssemblyPath+"|"+ct.Provider)
				}
				claimedBy[p] = ct.AssemblyPath + "|" + ct.Provider
				claimed = append(claimed, p)
			}
			if !sort.StringsAreSorted(ct.OutputPaths) {
				t.Errorf("case %q: group %q output paths are not in path order: %v",
					c.name, ct.AssemblyPath, ct.OutputPaths)
			}
		}
		sort.Strings(claimed)

		emitted := sortedFileKeys(res.Files)
		if !slices.Equal(claimed, emitted) {
			t.Errorf("case %q: the index does not partition the emitted files\nclaimed: %v\nemitted: %v",
				c.name, claimed, emitted)
		}
		if len(emitted) == 0 {
			t.Errorf("case %q emitted nothing; a partition of two empty sets proves nothing", c.name)
		}
	}
}

// contributionAttribution is the pinned per-case attribution: for each group,
// its assembly path, its provider, and every boundary that fed it.
func contributionAttribution() map[string][]string {
	return map[string][]string{
		"one boundary, one provider, flat": {
			"dist/billing/flatprov|flatprov|billing",
		},
		"two boundaries, distinct paths, flat (one-to-one)": {
			"dist/billing/flatprov|flatprov|billing",
			"dist/payments/flatprov|flatprov|payments",
		},
		"two boundaries, one evaluated path, flat (many-to-one)": {
			"dist/shared/flatprov|flatprov|billing,payments",
		},
		"two boundaries, one evaluated path, assembled (many-to-one)": {
			"dist/shared/asmprov|asmprov|billing,payments",
		},
		"three boundaries, one evaluated path, assembled (many-to-one)": {
			"dist/shared/asmprov|asmprov|billing,payments,ledger",
		},
		"one boundary, two providers, distinct paths": {
			"dist/billing/flatprov|flatprov|billing",
			"dist/billing/otherprov|otherprov|billing",
		},
		"two boundaries, two providers, one directory": {
			"dist/shared/flatprov|flatprov|billing",
			"dist/shared/otherprov|otherprov|payments",
		},
	}
}

// TestAssemblyContributionNamesEveryContributingBoundary is the attribution
// proof at the group level. Three of the pinned cases collapse several
// boundaries onto one path, and each names every one of them: dropping any
// contributor is exactly the defect that leaves a boundary unattributable.
func TestAssemblyContributionNamesEveryContributingBoundary(t *testing.T) {
	t.Parallel()

	pinned := contributionAttribution()
	sawCollapsed := 0
	for _, c := range contributionCorpus() {
		res := runContributionCase(t, c)
		var got []string
		for _, ct := range res.Contributions {
			got = append(got, ct.AssemblyPath+"|"+ct.Provider+"|"+strings.Join(ct.Boundaries, ","))
			if len(ct.Boundaries) > 1 {
				sawCollapsed++
			}
			if len(ct.Boundaries) == 0 {
				t.Errorf("case %q: group %q names no contributing boundary", c.name, ct.AssemblyPath)
			}
		}
		if !slices.Equal(got, pinned[c.name]) {
			t.Errorf("case %q attribution\n got: %v\nwant: %v", c.name, got, pinned[c.name])
		}
	}
	if sawCollapsed < 3 {
		t.Errorf("only %d groups carried more than one boundary; the many-to-one case is what this test "+
			"exists for, and a corpus that never reaches it would pass with the defect intact", sawCollapsed)
	}
}

// TestEmittedPathsAttributesPerGroup is the substitution proof. It asks each
// group in each case for its own paths and compares against the pinned answer, so
// a lookup that returned one group's file set for another — the failure a
// path-keyed map leaves open, because a group can only be guessed back from a
// path — fails here.
//
// It is keyed by the group rather than by a boundary, and the two are not the
// same question. Where several boundaries merged onto one path there is no
// per-boundary subset of the output to return: the assembler merged their
// contributions into one, so a per-boundary reading answers each of them with the
// group's whole output. That answer is not wrong so much as unusable — a rule
// walking it walks one group's output once per boundary — so the reading is the
// group's and the boundaries it was fed by are a fact of their own.
func TestEmittedPathsAttributesPerGroup(t *testing.T) {
	t.Parallel()

	want := map[string]map[string][]string{
		"one boundary, one provider, flat": {
			"dist/billing/flatprov|flatprov": {"dist/billing/flatprov/api.yaml"},
		},
		"two boundaries, distinct paths, flat (one-to-one)": {
			// Neither group may be handed the other's file.
			"dist/billing/flatprov|flatprov":  {"dist/billing/flatprov/api.yaml"},
			"dist/payments/flatprov|flatprov": {"dist/payments/flatprov/web.yaml"},
		},
		"two boundaries, one evaluated path, flat (many-to-one)": {
			// Two boundaries collapsed into one group, which answers once with the
			// merged set rather than once per boundary.
			"dist/shared/flatprov|flatprov": {
				"dist/shared/flatprov/api.yaml",
				"dist/shared/flatprov/web.yaml",
			},
		},
		"two boundaries, one evaluated path, assembled (many-to-one)": {
			"dist/shared/asmprov|asmprov": {
				"dist/shared/asmprov/assembly.yaml",
				"dist/shared/asmprov/charts/api/api.yaml",
				"dist/shared/asmprov/charts/web/web.yaml",
			},
		},
		"three boundaries, one evaluated path, assembled (many-to-one)": {
			"dist/shared/asmprov|asmprov": {
				"dist/shared/asmprov/assembly.yaml",
				"dist/shared/asmprov/charts/api/api.yaml",
				"dist/shared/asmprov/charts/web/web.yaml",
				"dist/shared/asmprov/charts/worker/worker.yaml",
			},
		},
		"one boundary, two providers, distinct paths": {
			// One boundary, two providers: two groups, and the provider half of the
			// coordinate separates them.
			"dist/billing/flatprov|flatprov":   {"dist/billing/flatprov/api.yaml"},
			"dist/billing/otherprov|otherprov": {"dist/billing/otherprov/web.yaml"},
		},
		"two boundaries, two providers, one directory": {
			"dist/shared/flatprov|flatprov":   {"dist/shared/flatprov/api.yaml"},
			"dist/shared/otherprov|otherprov": {"dist/shared/otherprov/web.yaml"},
			// The pairs that are no group: same directory, wrong provider.
			"dist/shared/flatprov|otherprov": nil,
			"dist/shared/otherprov|flatprov": nil,
		},
	}

	for _, c := range contributionCorpus() {
		res := runContributionCase(t, c)
		expected, ok := want[c.name]
		if !ok {
			t.Errorf("case %q has no pinned per-group answer", c.name)
			continue
		}
		for pair, wantPaths := range expected {
			assemblyPath, provider, _ := strings.Cut(pair, "|")
			got := res.EmittedPaths(assemblyPath, provider)
			if !slices.Equal(got, wantPaths) {
				t.Errorf("case %q: EmittedPaths(%q, %q)\n got: %v\nwant: %v",
					c.name, assemblyPath, provider, got, wantPaths)
			}
		}
		// A group the build never formed is answered with nothing rather than with
		// whichever group happened to be first.
		if got := res.EmittedPaths("no-such-path", "flatprov"); len(got) != 0 {
			t.Errorf("case %q: an unknown assembly path was handed %v", c.name, got)
		}
	}
}

// TestAssembledFileContentAnswersForTheGroupAndRefusesOtherwise proves the second
// half of the coordinate. Every emitted path answers with its own bytes for the
// group that wrote it, and refuses for every other group — even though that path
// is in the file map and a path-keyed lookup would have returned it.
func TestAssembledFileContentAnswersForTheGroupAndRefusesOtherwise(t *testing.T) {
	t.Parallel()

	answered, refused := 0, 0
	for _, c := range contributionCorpus() {
		res := runContributionCase(t, c)
		for _, ct := range res.Contributions {
			for _, p := range ct.OutputPaths {
				// Reference bytes come from the file map directly, never through
				// the accessor being tested.
				wantBody := res.Files[p].Content
				body, ok := res.AssembledFileContent(ct.AssemblyPath, ct.Provider, p)
				if !ok {
					t.Errorf("case %q: %q refused for the group that wrote it", c.name, p)
					continue
				}
				if string(body) != string(wantBody) {
					t.Errorf("case %q: %q for %q\n got: %q\nwant: %q",
						c.name, p, ct.AssemblyPath, string(body), string(wantBody))
				}
				answered++

				// Every other group in the case: none may read this path.
				for _, other := range res.Contributions {
					if other.AssemblyPath == ct.AssemblyPath && other.Provider == ct.Provider {
						continue
					}
					if _, ok := res.AssembledFileContent(other.AssemblyPath, other.Provider, p); ok {
						t.Errorf("case %q: %q answered for group %q|%q, which did not write it",
							c.name, p, other.AssemblyPath, other.Provider)
					}
					refused++
				}
				if _, ok := res.AssembledFileContent("no-such-path", ct.Provider, p); ok {
					t.Errorf("case %q: %q answered for a group the build never formed", c.name, p)
				}
				refused++
			}
		}
	}
	if answered == 0 || refused == 0 {
		t.Fatalf("the coordinate was answered %d times and refused %d; both directions must be exercised "+
			"or the test proves only that one of them compiles", answered, refused)
	}
}

// TestEmittedPathsSeparateTwoGroupsOneBoundaryReached covers a boundary that
// reaches more than one assembly path under one provider — two of its components
// declaring different sub-directories. Each group answers with its own path and
// neither answers with the other's.
//
// That separation is the property the group keying adds and the boundary keying
// could not have: read per boundary, this boundary's answer was the union of both
// groups, so a rule invoked over it was asked about output written by a group it
// was not looking at.
//
// It stands outside the pinned corpus because that corpus is the frozen
// byte-identity capture; this case was added after it.
func TestEmittedPathsSeparateTwoGroupsOneBoundaryReached(t *testing.T) {
	t.Parallel()

	res := runContributionCase(t, contributionCase{
		name: "one boundary spanning two assembly paths",
		nodes: []contributionNode{
			{id: "api", boundary: "billing", provider: "flatprov", directory: "dist/billing", subDirectory: "zeta"},
			{id: "web", boundary: "billing", provider: "flatprov", directory: "dist/billing", subDirectory: "alpha"},
		},
	})

	if len(res.Contributions) != 2 {
		t.Fatalf("expected two groups for one boundary at two sub-directories, got %d: %+v",
			len(res.Contributions), res.Contributions)
	}
	perGroup := map[string][]string{
		"dist/billing/alpha": {"dist/billing/alpha/web.yaml"},
		"dist/billing/zeta":  {"dist/billing/zeta/api.yaml"},
	}
	for assemblyPath, want := range perGroup {
		if got := res.EmittedPaths(assemblyPath, "flatprov"); !slices.Equal(got, want) {
			t.Errorf("EmittedPaths(%q, \"flatprov\")\n got: %v\nwant: %v", assemblyPath, got, want)
		}
		for _, p := range want {
			if _, ok := res.AssembledFileContent(assemblyPath, "flatprov", p); !ok {
				t.Errorf("%q refused for the group that wrote it", p)
			}
		}
	}
	// Neither group answers with the other's file, which is what the union hid.
	if _, ok := res.AssembledFileContent("dist/billing/alpha", "flatprov", "dist/billing/zeta/api.yaml"); ok {
		t.Error("one group answered for a path the other wrote")
	}
}

// TestAssemblyContributionIsDeterministic pins the attribution to the input. The
// maps feeding a group iterate in randomised order, so an index that leaked that
// order would answer differently on different runs of identical input — the same
// property the collision diagnostic is held to, one layer out.
func TestAssemblyContributionIsDeterministic(t *testing.T) {
	t.Parallel()

	c := contributionCase{
		name:      "three boundaries, one evaluated path, assembled (many-to-one)",
		assembles: true,
		nodes: []contributionNode{
			{id: "api", boundary: "billing", provider: "asmprov", directory: "dist/shared"},
			{id: "web", boundary: "payments", provider: "asmprov", directory: "dist/shared"},
			{id: "worker", boundary: "ledger", provider: "asmprov", directory: "dist/shared"},
		},
	}

	var first string
	for i := 0; i < 40; i++ {
		res := runContributionCase(t, c)
		var b strings.Builder
		for _, ct := range res.Contributions {
			b.WriteString(ct.AssemblyPath + "|" + ct.Provider + "|" +
				strings.Join(ct.Boundaries, ",") + "|" + strings.Join(ct.OutputPaths, ",") + "\n")
		}
		for _, boundary := range []string{"billing", "payments", "ledger"} {
			b.WriteString(boundary + "=>" + strings.Join(res.EmittedPaths(boundary, "asmprov"), ",") + "\n")
		}
		if i == 0 {
			first = b.String()
			continue
		}
		if b.String() != first {
			t.Fatalf("attribution changed between runs of identical input\nrun 0:\n%s\nrun %d:\n%s", first, i, b.String())
		}
	}
	if !strings.Contains(first, "billing,payments,ledger") {
		t.Errorf("expected all three contributing boundaries named in contribution order, got:\n%s", first)
	}
}

func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
