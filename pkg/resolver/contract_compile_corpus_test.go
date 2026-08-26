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
// 🔴 The assertion is on the message rather than on an identity, and the reason is a
// property of the earlier gate rather than a concession: catalog load aggregates its
// per-entity findings by formatting them into one message, so no per-finding identity
// survives for errors.Is to match. That is worth knowing independently of this
// condition, and it is why the identity assertion here is against the condition that
// must NOT appear — which is matchable, and is the claim this test is actually about.
//
// INVARIANT: the entity's own contract is refused at catalog load, not at resolution.
// CONSEQUENCE: were the resolver condition to appear here, one fault would be
// reported by two gates, and the earlier one's file-and-line provenance would be
// buried under a finding located by authored path alone.
func TestContractCorpus_TheEntityContractIsRefusedEarlier(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, contractCorpusWorkspace(t,
		contractCorpusComponent("", "", "timeout: { type: integer, minimun: 1 }")))
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
	for _, want := range []string{"spec.schema.values", "failed to compile", "minimun"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v\n"+
				"INVARIANT: the earlier gate names the pillar, the condition and the offending "+
				"keyword.\n"+
				"CONSEQUENCE: this test would pass on any refusal at all, including one from an "+
				"unrelated fault in the fixture.", want, err)
		}
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
