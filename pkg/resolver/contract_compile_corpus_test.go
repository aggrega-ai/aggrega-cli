// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Negative-authoring corpus for the schema-contract compile refusal, driven
// through the real compilation spine rather than through the guard entry alone.
//
// The guard-level tests beside this file prove the refusal is composed and
// attributed correctly. They cannot prove a BUILD refuses, because a verdict is
// a property of the pipeline and the pipeline is what the silence was measured
// on: before this refusal existed, a workspace carrying an unreadable socket
// contract exited zero and wrote its artifacts. So the claim "the build halts"
// is asserted where a build happens.
//
// It is an external test package so that it may import the build harness, which
// itself imports the package under test. That is the only reason for the split —
// the subject is this package's condition and the fixture belongs beside it.
package resolver_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The base workspace builds clean, which the positive control below proves. Each
// row replaces the Component document and nothing else, so a row's refusal is
// attributable to the contract it broke.
const (
	contractCorpusAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: contract-corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
`
	contractCorpusManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: contract-corpus-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`
	contractCorpusRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: contract-corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: contract-corpus-app
  manifest: contract-corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api: {}
`
	contractCorpusRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: x:1
        ports:
          p: 8080
`
	contractCorpusPlugin = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: compose
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - compose
  exports:
    - name: render_node
    - name: assemble_boundary
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`
)

// contractCorpusComponent renders the Component document with the supplied
// socket-contract bodies and entity contract spliced in, each omitted when its
// argument is empty.
//
// One template rather than a document per row: the rows differ only in which
// contract carries the fault, and a row authoring its own whole document could
// differ in something else without the difference being visible in the diff.
func contractCorpusComponent(valueSchema, infraSchema, entitySchema string) string {
	var b strings.Builder
	b.WriteString(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    peer:
      selector: "component.aggrega.ai/type=server"
`)
	if valueSchema != "" {
		b.WriteString("      valueSchema:\n        " + valueSchema + "\n")
	}
	if infraSchema != "" {
		b.WriteString("      infraSchema:\n        " + infraSchema + "\n")
	}
	b.WriteString("  schema:\n    values:\n      port: { type: integer }\n")
	if entitySchema != "" {
		b.WriteString("      " + entitySchema + "\n")
	}
	return b.String()
}

// contractCorpusWorkspace writes a workspace carrying the supplied Component and
// returns the input that builds it.
func contractCorpusWorkspace(t *testing.T, component string) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("aggregate.yaml", contractCorpusAggregate)
	write("manifest.yaml", contractCorpusManifest)
	write("release.yaml", contractCorpusRelease)
	write("catalog/components/server.yaml", component)
	write("catalog/recipes/server.yaml", contractCorpusRecipe)
	write("catalog/plugins/compose-renderer.yaml", contractCorpusPlugin)
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{filepath.Join(dir, "catalog")},
	}
}

// TestContractCorpus_BaseBuildsClean is the positive control, and it carries a
// second claim beyond "the base is clean": the socket it declares constrains
// NEITHER pillar.
//
// Declaring no per-edge contract is ordinary authoring — per-edge conformance is
// opted into — so this control is also the assertion that the refusal below does
// not fire on the absence it must stay silent about. Without it, every row could
// be refusing because a socket exists rather than because its contract is
// unreadable.
//
// INVARIANT: a socket that declares no per-edge contract builds.
// CONSEQUENCE: were this to fail, opt-in per-edge conformance would have become
// mandatory, refusing every catalog entity that leaves a socket unconstrained.
func TestContractCorpus_BaseBuildsClean(t *testing.T) {
	t.Parallel()

	in := contractCorpusWorkspace(t, contractCorpusComponent("", "", ""))
	if _, err := pipelinetest.BuildE(t, in); err != nil {
		t.Fatalf("the base workspace must build clean (positive control); got: %v\n"+
			"INVARIANT: a socket declaring no per-edge contract is accepted.\n"+
			"CONSEQUENCE: every row below is refusing for a reason that is not its fault.", err)
	}
}

// TestContractCorpus_ABrokenSocketContractRefusesTheBuild is the claim itself: a
// real build over a real workspace refuses a per-edge contract it cannot compile.
//
// The two rows are the two pillars a socket declares separately, and each is a
// distinct authored field with its own reading site — so a row covering one says
// nothing about the other.
//
// The locator is read off the structured per-violation slice rather than out of the
// rendered group text. A group renders as a header and a count, so a substring
// assertion over it would pass on a refusal that named the wrong field entirely;
// the slice is also the documented way a caller consumes these, so asserting there
// asserts what a consumer sees.
//
// INVARIANT: a build refuses a declared per-edge contract that does not compile, and
// the refusal names the authored field it was declared in.
// CONSEQUENCE: were a row to stop refusing, the per-edge configuration that contract
// covers would go unvalidated with a green build reporting success — the silence this
// condition exists to end.
func TestContractCorpus_ABrokenSocketContractRefusesTheBuild(t *testing.T) {
	t.Parallel()

	// A keyword the compact form does not carry, beside a well-formed constraint.
	// The sibling is what would otherwise have been enforced, so a row that does
	// not refuse is one where a working constraint went silently unread.
	const broken = "timeout: { type: integer, minimun: 1 }"

	cases := []struct {
		name string
		// component is the Component document the row authors.
		component string
		// wantField is the authored field a violation must name.
		wantField string
	}{
		{
			name:      "socket developer contract",
			component: contractCorpusComponent(broken, "", ""),
			wantField: ".spec.usesSchema.peer.valueSchema",
		},
		{
			name:      "socket operator contract",
			component: contractCorpusComponent("", broken, ""),
			wantField: ".spec.usesSchema.peer.infraSchema",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := pipelinetest.BuildE(t, contractCorpusWorkspace(t, tc.component))
			if err == nil {
				t.Fatalf("the build accepted a contract that does not compile.\n" +
					"INVARIANT: a declared contract that cannot be compiled refuses the build.\n" +
					"CONSEQUENCE: the payload it covers is unvalidated and the build says so " +
					"nowhere — a check retired in silence.")
			}
			if !errors.Is(err, resolver.ErrSchemaContractCompileFailure()) {
				t.Fatalf("the build refused with %v; want the compile-failure condition.\n"+
					"INVARIANT: the refusal carries the identity of the condition it met.\n"+
					"CONSEQUENCE: a caller matching on the condition stops seeing this refusal, "+
					"and the row passes on an unrelated fault.", err)
			}

			var group *resolver.MultiResolverError
			if !errors.As(err, &group) {
				t.Fatalf("the refusal carries no per-violation slice: %v\n"+
					"INVARIANT: a resolution refusal carries its findings structurally.\n"+
					"CONSEQUENCE: a consumer reading locators from the group has nothing to "+
					"read, and the field assertion below cannot run.", err)
			}
			found := false
			for _, v := range group.Violations {
				if !errors.Is(v, resolver.ErrSchemaContractCompileFailure()) {
					continue
				}
				if v.Field == tc.wantField {
					found = true
				}
				if !strings.Contains(v.Message, "timeout") {
					t.Errorf("the violation names no authored key: %s\n"+
						"INVARIANT: the diagnostic carries the key inside the contract that "+
						"failed.\n"+
						"CONSEQUENCE: an author is told a contract is unreadable and not which "+
						"fragment of it to correct.", v.Message)
				}
			}
			if !found {
				t.Errorf("no violation names %q; the slice holds %v.\n"+
					"INVARIANT: the refusal locates the contract at the authored field it was "+
					"declared in.\n"+
					"CONSEQUENCE: an author is sent to the wrong one of the three fields a "+
					"contract can be declared in.", tc.wantField, group.Violations)
			}
		})
	}
}

// TestContractCorpus_TheEntityContractIsRefusedEarlier pins which gate fires for the
// third field, because it is not this one.
//
// A Component's own contract is compiled per pillar as the catalog loads, so the
// fault is refused there — before resolution, with the source file and line the
// loader holds. The resolver's refusal covers the same field and is a backstop for a
// path reaching resolution without that validation, so it is unreachable from a
// build. Pinning that is what stops a later reader concluding the resolver refusal
// is what catches this case, and stops anyone "fixing" a duplicate that does not
// exist.
//
// Two identities carry the claim, and they point in opposite directions. The
// resolution condition must NOT appear, which is what this test is about. The
// catalog-load finding MUST appear, and it is reachable because the aggregate the
// loader returns carries its findings on the chain: the group names the pass and
// counts what it found, while the condition each finding met stays matchable
// beneath it. So the offending keyword is read off the structured compile failure
// rather than looked for in a string.
//
// One claim has no identity to match. Which of the three fields a contract may be
// declared in was at fault is carried in the finding's message text alone — no
// condition distinguishes the pillars — so that claim is asserted on the flattened
// cause chain, as one contiguous phrase. Contiguity is the point rather than a
// convenience: several independent substring probes are satisfiable by several
// unrelated frames, and one phrase is not. It is also why the flattened chain is
// read instead of the error's own text, which renders a single frame and so carries
// the group's header without the finding beneath it.
//
// INVARIANT: the entity's own contract is refused at catalog load, not at resolution.
// CONSEQUENCE: were the resolver condition to appear here, one fault would be
// reported by two gates, and the earlier one's file-and-line provenance would be
// buried under a finding located by authored path alone.
func TestContractCorpus_TheEntityContractIsRefusedEarlier(t *testing.T) {
	t.Parallel()

	// The keyword the entity contract below misspells, named once so the fixture
	// and the assertion that reads it back cannot drift apart: were they two
	// literals, correcting the spelling in one would leave the other asserting a
	// keyword no fixture plants, and the row would fail for a reason that is not
	// its subject.
	const contractCorpusPlantedKeyword = "minimun"

	_, err := pipelinetest.BuildE(t, contractCorpusWorkspace(t,
		contractCorpusComponent("", "", "timeout: { type: integer, "+contractCorpusPlantedKeyword+": 1 }")))
	if err == nil {
		t.Fatalf("the build accepted an entity contract that does not compile.\n" +
			"INVARIANT: the fault is refused somewhere.\n" +
			"CONSEQUENCE: the placement claim below is asserted over a passing build.")
	}
	if errors.Is(err, resolver.ErrSchemaContractCompileFailure()) {
		t.Errorf("the resolution condition refused an entity contract: %v\n"+
			"INVARIANT: catalog load refuses this field first, so resolution never sees it.\n"+
			"CONSEQUENCE: one fault is reported by two gates, and the earlier one's source "+
			"file and line are buried.", err)
	}
	if !errors.Is(err, compactform.ErrUnknownKeyword()) {
		t.Errorf("the refusal does not carry the unknown-keyword condition: %v\n"+
			"INVARIANT: the earlier gate's finding stays matchable for the condition it met.\n"+
			"CONSEQUENCE: this test would pass on any refusal at all, including one from an "+
			"unrelated fault in the fixture.", cerrors.CauseChain(err))
	}

	var compileErr *compactform.CompileError
	switch {
	case !errors.As(err, &compileErr):
		t.Errorf("the refusal carries no structured compile failure: %v\n"+
			"INVARIANT: the finding beneath the group locates the fault inside the contract.\n"+
			"CONSEQUENCE: the keyword assertion cannot run, and an author is told a contract "+
			"is unreadable and not which fragment of it to correct.", cerrors.CauseChain(err))
	case compileErr.Keyword != contractCorpusPlantedKeyword:
		t.Errorf("the compile failure names keyword %q; want %q\n"+
			"INVARIANT: the finding names the keyword this fixture planted.\n"+
			"CONSEQUENCE: the row passes on a compile failure the fixture did not author.",
			compileErr.Keyword, contractCorpusPlantedKeyword)
	}

	const wantPillar = "spec.schema.values body failed to compile"
	if chain := cerrors.CauseChain(err); !strings.Contains(chain, wantPillar) {
		t.Errorf("the refusal does not carry %q: %v\n"+
			"INVARIANT: the earlier gate names the pillar the unreadable contract was "+
			"declared in.\n"+
			"CONSEQUENCE: an author is sent to the wrong one of the three fields a contract "+
			"can be declared in.", wantPillar, chain)
	}
}

// TestContractCorpus_TheBuildAndValidationSurfacesAgree asserts one fixture
// against both entry points that run the semantic checks.
//
// The two are separate entries — one compiles and renders, the other stops after
// the semantic stages and serves the validation surfaces — and a condition
// reachable from only one of them would refuse a build while an author's own
// pre-flight check reported the workspace fine. That is a worse outcome than the
// silence this condition removes, because the author would be told twice that
// nothing is wrong.
//
// INVARIANT: the build entry and the validation entry reach the same verdict on the
// same workspace.
// CONSEQUENCE: were this to fail, a workspace would pass validation and fail the
// build on a condition validation is positioned to catch.
func TestContractCorpus_TheBuildAndValidationSurfacesAgree(t *testing.T) {
	t.Parallel()

	in := contractCorpusWorkspace(t,
		contractCorpusComponent("timeout: { type: integer, minimun: 1 }", "", ""))

	_, buildErr := pipelinetest.BuildE(t, in)
	if !errors.Is(buildErr, resolver.ErrSchemaContractCompileFailure()) {
		t.Fatalf("the build entry refused with %v; want the compile-failure condition.\n"+
			"INVARIANT: the fixture reaches the refusal on the build entry.\n"+
			"CONSEQUENCE: the agreement below is asserted over a fixture that does not "+
			"exercise it.", buildErr)
	}

	_, validateErr := pipelinetest.Tier4E(t, in)
	if !errors.Is(validateErr, resolver.ErrSchemaContractCompileFailure()) {
		t.Errorf("the validation entry answered %v; want the same condition the build "+
			"refused with.\n"+
			"INVARIANT: both entries reach the same verdict on one workspace.\n"+
			"CONSEQUENCE: an author's pre-flight check reports a workspace fine and the "+
			"build then refuses it.", validateErr)
	}
}

// contractCorpusUnusableBody is a contract body that COMPILES cleanly and then
// builds no validator: `items` carries a bare scalar where a schema belongs.
//
// It is deliberately not a compile fault. A body the compile rejects is already
// refused by the pass beside this one, so a fixture that failed to compile would
// exercise that refusal instead and this corpus would pass without ever reaching
// its subject.
//
// It is ONE authored entry, on one line. The templates splice a body into three
// fields at two different indents, so a body carrying its own line breaks would
// be valid YAML in one field and malformed in another — and a malformed document
// is skipped as it is read, which sends the build to a missing-component fault
// that has nothing to do with what the row asserts.
const contractCorpusUnusableBody = "bad: { type: array, items: nonsense }"

// contractCorpusSchemalessComponent is a Component declaring no contract at all
// — no spec.schema and no per-edge socket schema.
//
// It is the negative control for the refusal below: a subject declaring nothing
// has nothing to check and must build. Without it, the refusal could be firing
// on the presence of a socket or of a schema field rather than on a contract
// nothing can be built from, and every positive row would still pass.
const contractCorpusSchemalessComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    peer:
      selector: "component.aggrega.ai/type=server"
`

// contractCorpusAggregateWithValues is the corpus Aggregate with the component's
// authored values block replaced, so a row can supply a payload that violates a
// contract the row's Component declares. The rest of the workspace is untouched,
// which is what keeps a row's verdict attributable to the payload it wrote.
func contractCorpusAggregateWithValues(values string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: contract-corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
` + values
}

// contractCorpusWorkspaceWith writes the corpus workspace with both the
// Component and the Aggregate supplied, reusing every other document and the
// same layout contractCorpusWorkspace writes.
//
// It exists because the two negative controls need a payload the base Aggregate
// does not author, and a row that wrote its own whole workspace could differ in
// something the diff does not show.
func contractCorpusWorkspaceWith(t *testing.T, component, aggregate string) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("aggregate.yaml", aggregate)
	write("manifest.yaml", contractCorpusManifest)
	write("release.yaml", contractCorpusRelease)
	write("catalog/components/server.yaml", component)
	write("catalog/recipes/server.yaml", contractCorpusRecipe)
	write("catalog/plugins/compose-renderer.yaml", contractCorpusPlugin)
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{filepath.Join(dir, "catalog")},
	}
}

// TestContractCorpus_AnUnusableContractRefusesTheBuild is the claim: a real
// build over a real workspace refuses a declared contract that compiles and then
// builds no validator, and the refusal names the authored field it was declared
// in.
//
// The three rows are the three fields a contract can be declared in, each with
// its own reading site, so a row covering one says nothing about the others. All
// three are asserted against the CONDITION rather than against message text,
// because a consumer matches the condition and text is not an interface.
//
// INVARIANT: a build refuses a declared contract no validator can be built from,
// under the same condition a contract that does not compile is refused with.
// CONSEQUENCE: were a row to stop refusing, the configuration that contract
// covers would go unmeasured behind a green build — the silence this refusal
// exists to end, and the one a reader cannot distinguish from a passing check.
func TestContractCorpus_AnUnusableContractRefusesTheBuild(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// component is the Component document the row authors.
		component string
		// wantField is the authored field the diagnostic must name.
		wantField string
	}{
		{
			name:      "entity contract",
			component: contractCorpusComponent("", "", contractCorpusUnusableBody),
			wantField: "spec.schema.values",
		},
		{
			name:      "socket developer contract",
			component: contractCorpusComponent(contractCorpusUnusableBody, "", ""),
			wantField: "spec.usesSchema.peer.valueSchema",
		},
		{
			name:      "socket operator contract",
			component: contractCorpusComponent("", contractCorpusUnusableBody, ""),
			wantField: "spec.usesSchema.peer.infraSchema",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := pipelinetest.BuildE(t, contractCorpusWorkspace(t, tc.component))
			if err == nil {
				t.Fatalf("the build accepted a contract no validator can be built from.\n" +
					"INVARIANT: a declared contract nothing can be measured against refuses the build.\n" +
					"CONSEQUENCE: the payload it covers is unvalidated and the build says so " +
					"nowhere — a check retired in silence.")
			}
			if !errors.Is(err, resolver.ErrSchemaContractCompileFailure()) {
				t.Fatalf("the build refused with %v; want the compile-failure condition.\n"+
					"INVARIANT: the refusal carries the identity of the condition it met.\n"+
					"CONSEQUENCE: a caller matching on the condition stops seeing this refusal, "+
					"and the row passes on an unrelated fault.", cerrors.CauseChain(err))
			}
			if chain := cerrors.CauseChain(err); !strings.Contains(chain, tc.wantField) {
				t.Errorf("the refusal does not name %q: %v\n"+
					"INVARIANT: the refusal locates the contract at the authored field it was "+
					"declared in.\n"+
					"CONSEQUENCE: an author is sent to the wrong one of the three fields a "+
					"contract can be declared in.", tc.wantField, chain)
			}
		})
	}
}

// TestContractCorpus_AnUnusableContractIsRefusedOnEverySurface drives one
// violating workspace through both entries that run the semantic checks.
//
// A refusal the build reports and a pre-flight check swallows is not one
// behaviour with two presentations — it is a divergence, and it is worse than
// the silence being removed, because the author would be told twice that nothing
// is wrong.
//
// INVARIANT: the build entry and the validation entry reach the same verdict on
// one workspace carrying an unusable contract.
// CONSEQUENCE: were this to fail, a workspace would pass validation and fail the
// build on a condition validation is positioned to catch.
func TestContractCorpus_AnUnusableContractIsRefusedOnEverySurface(t *testing.T) {
	t.Parallel()

	in := contractCorpusWorkspace(t,
		contractCorpusComponent("", "", contractCorpusUnusableBody))

	_, buildErr := pipelinetest.BuildE(t, in)
	if !errors.Is(buildErr, resolver.ErrSchemaContractCompileFailure()) {
		t.Fatalf("the build entry refused with %v; want the compile-failure condition.\n"+
			"INVARIANT: the fixture reaches the refusal on the build entry.\n"+
			"CONSEQUENCE: the agreement below is asserted over a fixture that does not "+
			"exercise it.", cerrors.CauseChain(buildErr))
	}

	_, validateErr := pipelinetest.Tier4E(t, in)
	if !errors.Is(validateErr, resolver.ErrSchemaContractCompileFailure()) {
		t.Errorf("the validation entry answered %v; want the same condition the build "+
			"refused with.\n"+
			"INVARIANT: both entries reach the same verdict on one workspace.\n"+
			"CONSEQUENCE: an author's pre-flight check reports a workspace fine and the "+
			"build then refuses it.", cerrors.CauseChain(validateErr))
	}
}

// TestContractCorpus_TheRefusalDoesNotOverFire is the pair of negative controls,
// and they fail in opposite directions.
//
// The first is a subject declaring NO contract: nothing to build a validator
// from, nothing to refuse, and it must build. The second is a subject declaring
// a contract that IS usable, handed a payload that violates it: the refusal must
// stay out of the way and the ordinary conformance verdict must arrive
// unchanged. Without the second, a refusal that swallowed every payload verdict
// would pass the first control and every positive row.
//
// INVARIANT: the refusal fires on a contract nothing can be built from, and on
// nothing else.
// CONSEQUENCE: were the first to fail, declaring no contract would have become
// an error and every entity that leaves configuration unconstrained would be
// refused. Were the second to fail, the payload verdict a usable contract earns
// would have been replaced by a verdict about the contract.
func TestContractCorpus_TheRefusalDoesNotOverFire(t *testing.T) {
	t.Parallel()

	t.Run("a subject declaring no contract still builds", func(t *testing.T) {
		t.Parallel()

		in := contractCorpusWorkspaceWith(t,
			contractCorpusSchemalessComponent,
			contractCorpusAggregateWithValues("        port: 8080\n"))
		if _, err := pipelinetest.BuildE(t, in); err != nil {
			t.Fatalf("a Component declaring no contract was refused: %v\n"+
				"INVARIANT: a subject declaring nothing has nothing to check and builds.\n"+
				"CONSEQUENCE: declaring no contract has become an error, refusing every "+
				"entity that leaves its configuration unconstrained.", cerrors.CauseChain(err))
		}
	})

	t.Run("a usable contract still measures its payload", func(t *testing.T) {
		t.Parallel()

		// The base Component declares `port: { type: integer }`; the payload
		// writes a string. The verdict must be the conformance one, about the
		// payload, and must NOT be the contract-compile one.
		in := contractCorpusWorkspaceWith(t,
			contractCorpusComponent("", "", ""),
			contractCorpusAggregateWithValues("        port: \"not-an-integer\"\n"))
		_, err := pipelinetest.BuildE(t, in)
		if err == nil {
			t.Fatalf("a payload violating a usable contract was accepted.\n" +
				"INVARIANT: a usable contract still measures the payload written against it.\n" +
				"CONSEQUENCE: the refusal has replaced conformance validation rather than " +
				"adding a case to it, and every payload now passes.")
		}
		if !errors.Is(err, resolver.ErrSchemaConformance()) {
			t.Errorf("the payload violation was refused with %v; want the conformance "+
				"condition.\n"+
				"INVARIANT: a payload fault is reported as a payload fault.\n"+
				"CONSEQUENCE: an author is told their contract is unreadable when what is "+
				"wrong is the value they wrote.", cerrors.CauseChain(err))
		}
		if errors.Is(err, resolver.ErrSchemaContractCompileFailure()) {
			t.Errorf("a usable contract was reported as unusable: %v\n"+
				"INVARIANT: the refusal does not fire on a contract a validator was built "+
				"from.\n"+
				"CONSEQUENCE: the refusal over-fires on correct authoring, and the halt "+
				"cannot be trusted to mean what it says.", cerrors.CauseChain(err))
		}
	})
}

// TestContractCorpus_APillarLocalDefinitionalBlockIsMeasured pins that a pillar
// carrying its own reusable-sub-schema block is validated rather than passed
// over.
//
// The shape is ordinary compact-form authoring: a definitional block beside the
// property declarations, referenced by a document-root pointer. Assembling the
// validator has to lift that block to the root of the document it assembles, or
// the pointer resolves against a root that does not carry it, no validator is
// built, and the WHOLE pillar goes unmeasured — including the plainly-declared
// siblings that have nothing to do with the block.
//
// Both halves are asserted because they fail apart. The sibling proves the
// pillar is measured at all; the referenced constraint proves the reference
// itself resolved rather than being skipped.
//
// INVARIANT: a pillar declaring a definitional block is measured, and the
// constraints reached through it are enforced.
// CONSEQUENCE: were this to fail, adding a reusable sub-schema to a pillar would
// silently switch that pillar's validation off behind a green build.
func TestContractCorpus_APillarLocalDefinitionalBlockIsMeasured(t *testing.T) {
	t.Parallel()

	const withLocalDefs = `$defs:
        Port: { type: integer, minimum: 1024 }
      altPort: { $ref: "#/$defs/Port" }`

	cases := []struct {
		name string
		// values is the authored payload, violating in exactly one way.
		values string
		// wantField is the locator the conformance violation must name.
		wantField string
	}{
		{
			name:      "a plainly-declared sibling is still measured",
			values:    "        port: \"not-an-integer\"\n",
			wantField: "values.port",
		},
		{
			name:      "the constraint reached through the reference is measured",
			values:    "        port: 8080\n        altPort: 80\n",
			wantField: "values.altPort",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := contractCorpusWorkspaceWith(t,
				contractCorpusComponent("", "", withLocalDefs),
				contractCorpusAggregateWithValues(tc.values))
			_, err := pipelinetest.BuildE(t, in)
			if err == nil {
				t.Fatalf("the build accepted a payload violating a pillar that declares a "+
					"definitional block.\n"+
					"INVARIANT: such a pillar is measured like any other.\n"+
					"CONSEQUENCE: declaring a reusable sub-schema switches the pillar's "+
					"validation off, and %q goes unmeasured behind a green build.", tc.wantField)
			}
			if !errors.Is(err, resolver.ErrSchemaConformance()) {
				t.Fatalf("the refusal carries %v; want the conformance condition.\n"+
					"INVARIANT: the payload fault is reported as a payload fault.\n"+
					"CONSEQUENCE: the row would pass on a refusal about the contract, which "+
					"is the opposite of what it asserts.", cerrors.CauseChain(err))
			}
			// The locator is read off the structured per-violation slice rather
			// than out of the group text, for the reason the socket rows above
			// record: a group renders as a header and a count, so a substring
			// assertion over it would pass on a violation naming another leaf
			// entirely.
			var group *resolver.MultiResolverError
			if !errors.As(err, &group) {
				t.Fatalf("the refusal carries no per-violation slice: %v\n"+
					"INVARIANT: a resolution refusal carries its findings structurally.\n"+
					"CONSEQUENCE: the locator assertion below cannot run.", err)
			}
			found := false
			for _, v := range group.Violations {
				if errors.Is(v, resolver.ErrSchemaConformance()) &&
					strings.Contains(v.Field, tc.wantField) {
					found = true
				}
			}
			if !found {
				t.Errorf("no conformance violation names %q; the slice holds %v.\n"+
					"INVARIANT: the finding locates the leaf that failed.\n"+
					"CONSEQUENCE: the row would pass on a violation of the other leaf, so "+
					"neither half of the claim would be covered.", tc.wantField, group.Violations)
			}
		})
	}
}
