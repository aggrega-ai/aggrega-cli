// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's end-to-end proofs for the embedded expression-language
// governance guest (governance/cel) attached at each of the four governance
// roles and driven through the real pull -> build pipeline. The guest resolves
// through the embed tier by its bare identity and a workspace-catalog Plugin
// entity supplies the entity the closure pins.
//
// The four roles are proven by their observable effect on a real build rather
// than by inspecting guest output: a halting verdict stops the build, a warning
// verdict does not, a label decision reaches the rendered artifact through the
// compose recipe's node-label variable and reaches the graph through a Feature
// selector, and an annotation decision expands into a Feature attachment that
// reaches the same artifact's feature variable. The two confinement guardrails
// are proven by driving the guest into them.
//
// Both authored policy forms — one expression per file, and a rules document —
// are driven at every role, because they share one intake and one evaluator.
//
// The compose component/recipe fixtures, the single-component workspace, the
// annotation-binding workspace and the snapshot helpers are shared with the
// other embedded-guest proofs in this package.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

const celGovernanceURI = "governance/cel"

// writeCelPluginEntity writes the embedded expression-language guest's Plugin
// entity into the fixture catalog. Its .wasm resolves through the embed tier by
// the bare governance/cel coordinate; the entity is what the closure walk pins.
func writeCelPluginEntity(t *testing.T, catalogDir string) {
	t.Helper()
	writeFixtureEntity(t, catalogDir, "plugin/governance/cel/1.0.0/plugin.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: cel
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1alpha1
  exports:
    - name: verdict_ir
    - name: verdict_render
    - name: mutate
    - name: transform_annotations
`)
}

// celWorkspace lays the shared single-component workspace and adds this guest's
// Plugin entity, so every proof below starts from one component named app
// carrying component.aggrega.ai/type=server.
func celWorkspace(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root, catalogDir = singleComponentWorkspace(t)
	writeCelPluginEntity(t, catalogDir)
	return root, catalogDir
}

// celAnnotationWorkspace lays the annotation-binding workspace — a Manifest
// registering an annotation binding, so an injected annotation under that key
// expands into a Feature attachment — and adds this guest's Plugin entity to the
// same fixture catalog.
//
// The workspace itself is the one the other governance proof in this package
// lays, shared rather than re-laid so both proofs drive the same registered
// binding. The other guest's Plugin entity comes with it and is inert here:
// nothing attaches it.
func celAnnotationWorkspace(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root, catalogDir = regoAnnotationWorkspace(t)
	writeCelPluginEntity(t, catalogDir)
	return root, catalogDir
}

// celAttachment builds the workspace-config plugins block attaching this guest
// at one role with one policy file and optional settings.
func celAttachment(role, sourceFile, settings string) string {
	block := `workspace: {}
offline: true
plugins:
  governance:
    - uri: ` + celGovernanceURI + `:1.0.0
      role: ` + role + `
      with:
        sources:
          - ` + sourceFile + `
`
	if settings != "" {
		block += settings
	}
	return block
}

// TestCelEmbedded_IRVerdictSeverityRouting proves the guest attached over the
// build graph reports each of the three severities and that the build outcome
// follows: a halting decision stops the build with ErrPolicyViolation, while a
// warning and a recorded observation both leave a build that completes.
//
// The warn and info arms are the load-bearing half: "halt works" says nothing
// about whether the other two also stop the build, which would make every
// advisory policy a blocker.
func TestCelEmbedded_IRVerdictSeverityRouting(t *testing.T) {
	nonInteractive(t)

	t.Run("halt stops the build", func(t *testing.T) {
		root, catalogDir := celWorkspace(t)
		writeFixture(t, filepath.Join(root, "policy.cel"),
			`{"deny": size(input.graph.spec.nodes) > 0 ? ["the graph declares a node"] : []}`)
		err := pullThen(t, root, filepath.Join(root, "release.yaml"),
			[]string{catalogDir, standardCatalogDir(t)},
			celAttachment("ir-verdict", "policy.cel", ""), "out")
		if err == nil {
			t.Fatal("a halting decision must stop the build")
		}
		if !errors.Is(err, analyzer.ErrPolicyViolation()) {
			t.Fatalf("want ErrPolicyViolation, got: %v", err)
		}
		if !strings.Contains(err.Error(), "the graph declares a node") {
			t.Fatalf("the halt must carry the policy's own message, got: %v", err)
		}
	})

	for _, arm := range []struct {
		name string
		key  string
	}{
		{"warn does not stop the build", "warn"},
		{"info does not stop the build", "audit"},
	} {
		t.Run(arm.name, func(t *testing.T) {
			root, catalogDir := celWorkspace(t)
			writeFixture(t, filepath.Join(root, "policy.cel"),
				`{"`+arm.key+`": size(input.graph.spec.nodes) > 0 ? ["the graph declares a node"] : []}`)
			outDir := "out-" + arm.key
			if err := pullThen(t, root, filepath.Join(root, "release.yaml"),
				[]string{catalogDir, standardCatalogDir(t)},
				celAttachment("ir-verdict", "policy.cel", ""), outDir); err != nil {
				t.Fatalf("a %s decision must not stop the build: %v", arm.key, err)
			}
			// The build produced output, so the decision really was reached
			// without halting rather than the policy having matched nothing.
			if found, tree := distTreeContains(t, filepath.Join(root, outDir), "NODE_LABELS"); !found {
				t.Fatalf("the build produced no rendered output:\n%s", tree)
			}
		})
	}
}

// TestCelEmbedded_BothAuthoredFormsReachAVerdict proves the two authored forms
// both fire decisions through the real dispatch, that they work side by side in
// one attachment, and that the file's name is what decides which form it is read
// as — in both directions.
//
// The wrong-name arm is the load-bearing one: a rules document saved under an
// expression's name must be refused rather than sniffed and read anyway, because
// nothing about the text answers the question.
func TestCelEmbedded_BothAuthoredFormsReachAVerdict(t *testing.T) {
	nonInteractive(t)

	const document = `- key: deny
  expression: size(input.graph.spec.nodes) > 0
  message: a rules document reached its decision
`

	t.Run("an expression reaches its decision", func(t *testing.T) {
		root, catalogDir := celWorkspace(t)
		writeFixture(t, filepath.Join(root, "policy.cel"),
			`{"deny": size(input.graph.spec.nodes) > 0 ? ["an expression reached its decision"] : []}`)
		err := pullThen(t, root, filepath.Join(root, "release.yaml"),
			[]string{catalogDir, standardCatalogDir(t)},
			celAttachment("ir-verdict", "policy.cel", ""), "out")
		if err == nil || !strings.Contains(err.Error(), "an expression reached its decision") {
			t.Fatalf("want the expression's own message, got: %v", err)
		}
	})

	t.Run("a rules document reaches its decision", func(t *testing.T) {
		root, catalogDir := celWorkspace(t)
		writeFixture(t, filepath.Join(root, "policy.yaml"), document)
		err := pullThen(t, root, filepath.Join(root, "release.yaml"),
			[]string{catalogDir, standardCatalogDir(t)},
			celAttachment("ir-verdict", "policy.yaml", ""), "out")
		if err == nil || !strings.Contains(err.Error(), "a rules document reached its decision") {
			t.Fatalf("want the document's own message, got: %v", err)
		}
	})

	t.Run("the two forms evaluate side by side", func(t *testing.T) {
		root, catalogDir := celWorkspace(t)
		writeFixture(t, filepath.Join(root, "policy.cel"),
			`{"deny": size(input.graph.spec.nodes) > 0 ? ["from the expression"] : []}`)
		writeFixture(t, filepath.Join(root, "policy.yaml"), `- key: deny
  expression: size(input.graph.spec.nodes) > 0
  message: from the rules document
`)
		config := `workspace: {}
offline: true
plugins:
  governance:
    - uri: ` + celGovernanceURI + `:1.0.0
      role: ir-verdict
      with:
        sources:
          - policy.cel
          - policy.yaml
`
		err := pullThen(t, root, filepath.Join(root, "release.yaml"),
			[]string{catalogDir, standardCatalogDir(t)}, config, "out-mixed")
		if err == nil {
			t.Fatal("both files must reach their decisions")
		}
		for _, want := range []string{"from the expression", "from the rules document"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("one attachment must evaluate both forms; missing %q in: %v", want, err)
			}
		}
	})

	t.Run("a rules document under an expression's name is refused by name", func(t *testing.T) {
		root, catalogDir := celWorkspace(t)
		writeFixture(t, filepath.Join(root, "policy.cel"), document)
		err := pullThen(t, root, filepath.Join(root, "release.yaml"),
			[]string{catalogDir, standardCatalogDir(t)},
			celAttachment("ir-verdict", "policy.cel", ""), "out-wrong-name")
		if err == nil {
			t.Fatal("a document read as an expression must be refused, not evaluated")
		}
		if errors.Is(err, analyzer.ErrPolicyViolation()) {
			t.Fatalf("an unreadable policy is a build fault, not a policy verdict: %v", err)
		}
		if !strings.Contains(err.Error(), "policy.cel") {
			t.Fatalf("the refusal must name the file, got: %v", err)
		}
	})
}

// TestCelEmbedded_RenderVerdictHalts proves the guest attached over the rendered
// output reads the rendered files and halts the build on a decision about them.
// The negative control is the same policy asserting on content the artifact does
// not carry, which must leave the build standing — so the positive assertion
// cannot pass on an unconditional halt.
func TestCelEmbedded_RenderVerdictHalts(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	// Negative control: the policy looks for an image the recipe never renders.
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"deny": input.artifacts
                    .filter(a, a.content.contains("image-that-is-never-rendered"))
                    .map(a, "found the absent image")}`)
	if err := pullThen(t, root, releasePath, catDirs,
		celAttachment("render-verdict", "policy.cel", ""), "out-neg"); err != nil {
		t.Fatalf("negative control: a policy that matches nothing must not stop the build: %v", err)
	}

	// Positive: the same shape of policy, matching the image the recipe does
	// render, so the halt is attributable to the artifact's real content.
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"deny": input.artifacts
                    .filter(a, a.content.contains("busybox"))
                    .map(a, "a busybox image was rendered at " + a.path)}`)
	err := pullThen(t, root, releasePath, catDirs,
		celAttachment("render-verdict", "policy.cel", ""), "out-pos")
	if err == nil {
		t.Fatal("positive: a halting decision over the rendered output must stop the build")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("want ErrPolicyViolation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "a busybox image was rendered at") {
		t.Fatalf("the halt must carry the policy's own message naming the artifact, got: %v", err)
	}
}

// TestCelEmbedded_RenderVerdictReadsStructuredDocuments proves the guest hands a
// policy the rendered file's structured documents, not only its raw text, so a
// policy reasons over the rendered object rather than matching characters.
func TestCelEmbedded_RenderVerdictReadsStructuredDocuments(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"deny": input.artifacts
                    .filter(a, a.documents.exists(d, has(d.services)))
                    .map(a, "a service was rendered to " + a.path)}`)
	err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		celAttachment("render-verdict", "policy.cel", ""), "out")
	if err == nil {
		t.Fatal("a decision over the rendered documents must stop the build")
	}
	if !strings.Contains(err.Error(), "a service was rendered to") {
		t.Fatalf("the policy must have read the structured document, got: %v", err)
	}
}

// TestCelEmbedded_LabelTransformApplies proves a label decision reaches the
// rendered artifact: the guest attached to shape labels reports one label, the
// build applies it to the node's label set, and the compose recipe renders it.
//
// The label key deliberately carries a "/", which is the ordinary shape of an
// Aggrega label key and the case a hand-written change instruction would get
// wrong.
func TestCelEmbedded_LabelTransformApplies(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celWorkspace(t)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"labels": input.graph.nodes
                      .filter(n, input.graph.nodes[n].labels["component.aggrega.ai/type"] == "server")
                      .map(n, {"node": n, "key": "compliance.example.com/tier", "value": "gold"})}`)
	rw := resolveWorkspaceWithConfig(t, root, celAttachment("label-transform", "policy.cel", ""))
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir, standardCatalogDir(t)}, ws: rw, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// A label-shaping attachment shapes output bytes, so pull pins it in the
	// lockfile. Asserting the pin here is what makes this role's reproducibility
	// tier observable rather than assumed.
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.Mutators) != 1 || lf.Mutators[0].Plugin != celGovernanceURI {
		t.Fatalf("lockfile mutators = %+v; want one attachment pinned at %q", lf.Mutators, celGovernanceURI)
	}

	out := filepath.Join(root, "out")
	if err := dispatchBuildWithDiag(rw, releasePath, []string{catalogDir, standardCatalogDir(t)}, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build: %v", err)
	}
	if found, tree := distTreeContains(t, out, "compliance.example.com/tier=gold"); !found {
		t.Fatalf("the decided label is absent from the rendered output:\n%s", tree)
	}
	// The pre-existing label the policy matched on is still there: a label
	// decision adds rather than replacing the node's label set.
	if found, tree := distTreeContains(t, out, "component.aggrega.ai/type=server"); !found {
		t.Fatalf("the pre-existing label was lost:\n%s", tree)
	}
}

// TestCelEmbedded_LabelTransformRemovesALabel proves a decision can drop a label
// the node carries, which is the other half of the change surface the build
// authorizes.
func TestCelEmbedded_LabelTransformRemovesALabel(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"labels": input.graph.nodes
                      .filter(n, "component.aggrega.ai/type" in input.graph.nodes[n].labels)
                      .map(n, {"node": n, "key": "component.aggrega.ai/type", "op": "remove"})}`)
	if err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		celAttachment("label-transform", "policy.cel", ""), "out"); err != nil {
		t.Fatalf("build: %v", err)
	}
	out := filepath.Join(root, "out")
	if found, tree := distTreeContains(t, out, "component.aggrega.ai/type=server"); found {
		t.Fatalf("the dropped label is still in the rendered output:\n%s", tree)
	}
	// The node still rendered, so the label really was dropped rather than the
	// whole artifact having gone missing.
	if found, tree := distTreeContains(t, out, "NODE_LABELS"); !found {
		t.Fatalf("the build produced no rendered output:\n%s", tree)
	}
}

// celSelectorWorkspace lays a workspace whose Feature selector requires a label
// the component does not carry, so the build only stands when a policy decides
// that label. Returns root and the fixture catalog dir.
func celSelectorWorkspace(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root = t.TempDir()
	catalogDir = filepath.Join(root, "catalog")
	writeComposeComponentRecipe(t, catalogDir, "test/svc", "svc", map[string]string{
		"component.aggrega.ai/type": "server",
	})
	writeCelPluginEntity(t, catalogDir)

	writeFixtureEntity(t, catalogDir, "feature/core/label-gate/1.0.0/feature.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/label-gate
  version: "1.0.0"
spec:
  appliesTo: "role.aggrega.ai/kind=cache"
  schema:
    values:
      enabled: { type: boolean, default: true }
`)
	writeFixtureEntity(t, catalogDir, "feature-recipe/compose/label-gate/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: label-gate-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: core/label-gate
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
      features:
        gate:
          type: core/label-gate
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: compose/svc:1.0.0
    features:
      type:
        core/label-gate: label-gate-compose:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app: {}
`)
	return root, catalogDir
}

// TestCelEmbedded_LabelTransformDrivesFeatureAttachment is the load-bearing
// proof that a label decision reaches the graph rather than only the rendered
// text: a Feature whose selector requires a label the component does not carry
// attaches only because the policy decided that label.
//
// The negative control is the same workspace with no attachment, where the
// selector is unsatisfied and the build halts — so the positive assertion cannot
// be vacuous.
func TestCelEmbedded_LabelTransformDrivesFeatureAttachment(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celSelectorWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	// Negative control: no attachment, so the selector is unsatisfied.
	errNeg := pullThen(t, root, releasePath, catDirs, "workspace: {}\noffline: true\n", "out-neg")
	if errNeg == nil {
		t.Fatal("negative control: the feature's selector should be unsatisfied and halt the build")
	}
	if !errors.Is(errNeg, analyzer.ErrSelectorViolation()) {
		t.Fatalf("negative control: want ErrSelectorViolation, got: %v", errNeg)
	}

	// Positive: the policy decides the label the selector requires.
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"labels": input.graph.nodes
                      .filter(n, input.graph.nodes[n].labels["component.aggrega.ai/type"] == "server")
                      .map(n, {"node": n, "key": "role.aggrega.ai/kind", "value": "cache"})}`)
	if err := pullThen(t, root, releasePath, catDirs,
		celAttachment("label-transform", "policy.cel", ""), "out-pos"); err != nil {
		t.Fatalf("positive: the decided label should satisfy the selector: %v", err)
	}
	if found, tree := distTreeContains(t, filepath.Join(root, "out-pos"), "core/label-gate"); !found {
		t.Fatalf("positive: the feature attachment did not survive into the rendered output:\n%s", tree)
	}
}

// TestCelEmbedded_LabelTransformConfinementIsEnforced drives the guest into the
// label confinement from both directions a policy can reach it, and requires the
// build to refuse loudly each time.
//
// The confinement matters because a plugin attached to shape labels is the
// invited exception to the rule that nothing invents infrastructure: the
// invitation is exactly as wide as the role. A policy naming a node the build
// does not have, or dropping a label the node does not carry, is reaching past
// the label set it was invited to shape.
func TestCelEmbedded_LabelTransformConfinementIsEnforced(t *testing.T) {
	nonInteractive(t)

	for _, arm := range []struct {
		name   string
		policy string
	}{
		{
			name:   "a decision naming a node the build does not have",
			policy: `{"labels": [{"node": "no-such-node", "key": "tier", "value": "gold"}]}`,
		},
		{
			name: "a decision dropping a label the node does not carry",
			policy: `{"labels": input.graph.nodes
                                  .map(n, {"node": n, "key": "never.example.com/set", "op": "remove"})}`,
		},
	} {
		t.Run(arm.name, func(t *testing.T) {
			root, catalogDir := celWorkspace(t)
			writeFixture(t, filepath.Join(root, "policy.cel"), arm.policy)
			err := pullThen(t, root, filepath.Join(root, "release.yaml"),
				[]string{catalogDir, standardCatalogDir(t)},
				celAttachment("label-transform", "policy.cel", ""), "out")
			if err == nil {
				t.Fatal("a change outside the label set must be refused, not applied")
			}
			if !errors.Is(err, topology.ErrMutatorPatchRejected()) {
				t.Fatalf("want ErrMutatorPatchRejected, got: %v", err)
			}
		})
	}
}

// TestCelEmbedded_LabelTransformCannotReachBeyondLabels proves the other half of
// the confinement — that a policy has no way to name a surface other than a
// node's labels — by having it try. Whatever a decision names becomes part of a
// label's own name, so the attempt lands as an oddly-named label on the node the
// policy named rather than as a change to the node's component or version.
func TestCelEmbedded_LabelTransformCannotReachBeyondLabels(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celWorkspace(t)
	// The decision names a key shaped like a path out of the label set. It
	// cannot escape: the name is carried as one label name.
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"labels": input.graph.nodes
                      .map(n, {"node": n, "key": "../resolved_version", "value": "9.9.9"})}`)
	if err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		celAttachment("label-transform", "policy.cel", ""), "out"); err != nil {
		t.Fatalf("build: %v", err)
	}
	out := filepath.Join(root, "out")
	if found, tree := distTreeContains(t, out, "../resolved_version=9.9.9"); !found {
		t.Fatalf("the attempt should land as a label of that name:\n%s", tree)
	}
	if found, tree := distTreeContains(t, out, "9.9.9\n"); found {
		t.Fatalf("the version was reached, which the confinement must prevent:\n%s", tree)
	}
}

// TestCelEmbedded_AnnotationTransformExpandsIntoAFeature proves an annotation
// decision reaches the build: the guest reports one annotation under a key the
// Manifest has registered, and the annotation expands into a Feature attachment
// that reaches the rendered artifact.
//
// The negative control is the same workspace with no attachment, where no
// annotation exists and no feature attaches — so the positive assertion is not
// reading a feature that was always there.
func TestCelEmbedded_AnnotationTransformExpandsIntoAFeature(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celAnnotationWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	// Negative control: no attachment, so nothing injects the annotation.
	if err := pullThen(t, root, releasePath, catDirs, "workspace: {}\noffline: true\n", "out-neg"); err != nil {
		t.Fatalf("negative control build: %v", err)
	}
	if found, _ := distTreeContains(t, filepath.Join(root, "out-neg"), "core/annotation-gate"); found {
		t.Fatal("negative control: the feature is present with no attachment, so the positive assertion would be vacuous")
	}

	// Positive: the policy decides the annotation, under a registered key it
	// reads from its own input rather than from a hardcoded list.
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"annotations": input.targets
                          .filter(t, "gate.example.com/tier" in input.binding_keys)
                          .map(t, {
                            "boundary_path": t.boundary_path,
                            "component_name": t.component_name,
                            "annotation_key": "gate.example.com/tier",
                            "annotation_value": "gold",
                          })}`)
	if err := pullThen(t, root, releasePath, catDirs,
		celAttachment("annotation-transform", "policy.cel", ""), "out-pos"); err != nil {
		t.Fatalf("positive build: %v", err)
	}
	if found, tree := distTreeContains(t, filepath.Join(root, "out-pos"), "core/annotation-gate"); !found {
		t.Fatalf("positive: the injected annotation did not expand into a feature attachment:\n%s", tree)
	}
}

// TestCelEmbedded_AnnotationTransformConfinementIsEnforced drives the guest into
// the annotation confinement: a decision naming a component the deployment does
// not have must be refused loudly rather than applied or dropped.
func TestCelEmbedded_AnnotationTransformConfinementIsEnforced(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celAnnotationWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.cel"),
		`{"annotations": [{
                            "boundary_path": "main",
                            "component_name": "no-such-component",
                            "annotation_key": "gate.example.com/tier",
                            "annotation_value": "gold",
                          }]}`)
	err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		celAttachment("annotation-transform", "policy.cel", ""), "out")
	if err == nil {
		t.Fatal("an annotation naming a component the deployment does not have must be refused")
	}
	if !errors.Is(err, pipeline.ErrAnnotationInjectionRejected()) {
		t.Fatalf("want ErrAnnotationInjectionRejected, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no-such-component") {
		t.Fatalf("the refusal must name the coordinate, got: %v", err)
	}
}

// TestCelEmbedded_BothAuthoredFormsShapeTheBuild proves the rules-document form
// reaches the two roles that shape a build, not only the verdict roles. What a
// rule reports follows the decision key it reaches, so a rule under a
// build-shaping key names a decision rather than a message.
//
// This is the half of the two-form contract that the intake alone does not buy:
// sharing an intake gets a document parsed and evaluated, while reaching a label
// change or an annotation injection needs the rule to be able to name one.
func TestCelEmbedded_BothAuthoredFormsShapeTheBuild(t *testing.T) {
	nonInteractive(t)

	t.Run("a rules document decides a label", func(t *testing.T) {
		root, catalogDir := celWorkspace(t)
		writeFixture(t, filepath.Join(root, "policy.yaml"), `- key: labels
  expression: size(input.graph.nodes) > 0
  message:
    node: app
    key: compliance.example.com/tier
    value: gold
`)
		if err := pullThen(t, root, filepath.Join(root, "release.yaml"),
			[]string{catalogDir, standardCatalogDir(t)},
			celAttachment("label-transform", "policy.yaml", ""), "out"); err != nil {
			t.Fatalf("build: %v", err)
		}
		if found, tree := distTreeContains(t, filepath.Join(root, "out"), "compliance.example.com/tier=gold"); !found {
			t.Fatalf("the decided label is absent from the rendered output:\n%s", tree)
		}
	})

	t.Run("a rules document decides an annotation", func(t *testing.T) {
		root, catalogDir := celAnnotationWorkspace(t)
		writeFixture(t, filepath.Join(root, "policy.yaml"), `- key: annotations
  expression: '"gate.example.com/tier" in input.binding_keys'
  message:
    boundary_path: main
    component_name: app
    annotation_key: gate.example.com/tier
    annotation_value: gold
`)
		if err := pullThen(t, root, filepath.Join(root, "release.yaml"),
			[]string{catalogDir, standardCatalogDir(t)},
			celAttachment("annotation-transform", "policy.yaml", ""), "out"); err != nil {
			t.Fatalf("build: %v", err)
		}
		if found, tree := distTreeContains(t, filepath.Join(root, "out"), "core/annotation-gate"); !found {
			t.Fatalf("the injected annotation did not expand into a feature attachment:\n%s", tree)
		}
	})
}

// TestCelEmbedded_AMalformedDecisionHaltsRatherThanChangingTheBuild proves a
// decision the guest cannot read stops the build with a fault naming the file and
// what a decision must name — never a silently dropped change.
//
// A dropped change is the dangerous failure here: the build would complete,
// render output, and simply not carry the label or annotation the operator
// attached a policy to add.
func TestCelEmbedded_AMalformedDecisionHaltsRatherThanChangingTheBuild(t *testing.T) {
	nonInteractive(t)

	for _, arm := range []struct {
		name   string
		role   string
		policy string
		names  string
	}{
		{
			name:   "a label decision that is a message rather than a decision",
			role:   "label-transform",
			policy: `{"labels": ["gold"]}`,
			names:  `"node"`,
		},
		{
			name:   "a label decision naming a field the form does not define",
			role:   "label-transform",
			policy: `{"labels": [{"node": "app", "key": "tier", "values": "gold"}]}`,
			names:  `"values"`,
		},
		{
			name:   "an annotation decision missing a coordinate",
			role:   "annotation-transform",
			policy: `{"annotations": [{"component_name": "app", "annotation_key": "k", "annotation_value": "v"}]}`,
			names:  `"boundary_path"`,
		},
	} {
		t.Run(arm.name, func(t *testing.T) {
			root, catalogDir := celAnnotationWorkspace(t)
			writeFixture(t, filepath.Join(root, "policy.cel"), arm.policy)
			err := pullThen(t, root, filepath.Join(root, "release.yaml"),
				[]string{catalogDir, standardCatalogDir(t)},
				celAttachment(arm.role, "policy.cel", ""), "out")
			if err == nil {
				t.Fatal("a decision the guest cannot read must stop the build, not be dropped")
			}
			if !strings.Contains(err.Error(), "policy.cel") {
				t.Fatalf("the fault must name the file, got: %v", err)
			}
			if !strings.Contains(err.Error(), arm.names) {
				t.Fatalf("the fault must say what a decision names; missing %s in: %v", arm.names, err)
			}
		})
	}
}

// TestCelEmbedded_AnUnreadablePolicyHaltsRatherThanAllowing proves a policy that
// cannot be read stops the build as a fault rather than reaching no decision.
// "This policy could not be evaluated" must never read as "this policy allows the
// build".
func TestCelEmbedded_AnUnreadablePolicyHaltsRatherThanAllowing(t *testing.T) {
	nonInteractive(t)

	for _, arm := range []struct {
		name    string
		file    string
		content string
		names   string
	}{
		{
			name:    "an expression that cannot be read",
			file:    "policy.cel",
			content: `{"deny": size(input.graph.spec.nodes) >`,
			names:   "policy.cel",
		},
		{
			name:    "a rules document that cannot be read",
			file:    "policy.yaml",
			content: "- key: deny\n  expression: \"size(input) >\"\n  message: broken\n",
			names:   "policy.yaml",
		},
		{
			name:    "an expression that produces no decisions",
			file:    "policy.cel",
			content: `size(input.graph.spec.nodes) <= 1`,
			names:   "policy.cel",
		},
	} {
		t.Run(arm.name, func(t *testing.T) {
			root, catalogDir := celWorkspace(t)
			writeFixture(t, filepath.Join(root, arm.file), arm.content)
			err := pullThen(t, root, filepath.Join(root, "release.yaml"),
				[]string{catalogDir, standardCatalogDir(t)},
				celAttachment("ir-verdict", arm.file, ""), "out")
			if err == nil {
				t.Fatal("a policy that cannot be read must stop the build")
			}
			if errors.Is(err, analyzer.ErrPolicyViolation()) {
				t.Fatalf("an unevaluable policy is a build fault, not a policy verdict: %v", err)
			}
			if !strings.Contains(err.Error(), arm.names) {
				t.Fatalf("the fault must name the file, got: %v", err)
			}
		})
	}
}

// TestCelEmbedded_TranslationOverrideMapsACustomKey proves an operator can map
// their own decision key onto a severity, and that declaring a mapping replaces
// the default rather than extending it.
//
// The replace half is what makes the proof more than "the setting is read": the
// same policy also names the conventional key, which must not fire once a mapping
// naming only the custom key is declared.
func TestCelEmbedded_TranslationOverrideMapsACustomKey(t *testing.T) {
	nonInteractive(t)

	const policy = `{
      "blocked": size(input.graph.spec.nodes) > 0 ? ["the custom decision key fired"] : [],
      "deny": size(input.graph.spec.nodes) > 0 ? ["the conventional key fired"] : [],
    }`

	// Without a mapping, the conventional key halts and the custom one is not
	// read at all.
	root, catalogDir := celWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.cel"), policy)
	errDefault := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		celAttachment("ir-verdict", "policy.cel", ""), "out-default")
	if errDefault == nil {
		t.Fatal("the conventional key should halt with no mapping declared")
	}
	if !strings.Contains(errDefault.Error(), "the conventional key fired") {
		t.Fatalf("want the conventional message, got: %v", errDefault)
	}
	if strings.Contains(errDefault.Error(), "the custom decision key fired") {
		t.Fatalf("the custom key should not be read with no mapping declared: %v", errDefault)
	}

	// With a mapping naming only the custom key, the custom key halts and the
	// conventional key is no longer read.
	root2, catalogDir2 := celWorkspace(t)
	writeFixture(t, filepath.Join(root2, "policy.cel"), policy)
	errMapped := pullThen(t, root2, filepath.Join(root2, "release.yaml"),
		[]string{catalogDir2, standardCatalogDir(t)},
		celAttachment("ir-verdict", "policy.cel", `        settings:
          translation:
            - key: blocked
              severity: halt
`), "out-mapped")
	if errMapped == nil {
		t.Fatal("the mapped custom key should halt")
	}
	if !strings.Contains(errMapped.Error(), "the custom decision key fired") {
		t.Fatalf("want the custom message, got: %v", errMapped)
	}
	if strings.Contains(errMapped.Error(), "the conventional key fired") {
		t.Fatalf("a declared mapping replaces the default rather than extending it: %v", errMapped)
	}
}

// TestCelEmbedded_MalformedTranslationSettingsHaltBeforeDispatch proves a mapping
// the guest's declared settings schema does not admit halts the build against
// that schema, before the policy runs — so a typo in an attachment's settings is
// caught as a misconfiguration rather than surfacing as a policy verdict or being
// silently ignored.
func TestCelEmbedded_MalformedTranslationSettingsHaltBeforeDispatch(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celWorkspace(t)
	// A policy that would reach no decision at all, so a halt can only have come
	// from the settings check rather than from the policy.
	writeFixture(t, filepath.Join(root, "policy.cel"), "{}")
	err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		celAttachment("ir-verdict", "policy.cel", `        settings:
          translation:
            - key: blocked
              severity: reject
`), "out")
	if err == nil {
		t.Fatal("a severity outside the declared set must halt the build")
	}
	if !errors.Is(err, pipeline.ErrPolicySettingsInvalid()) {
		t.Fatalf("want ErrPolicySettingsInvalid, got: %v", err)
	}
}

// TestCelEmbedded_DeterministicOutput proves two builds of the same pinned input
// are byte-identical with the guest attached at both build-shaping roles at once,
// which is where a non-deterministic decision order would show.
//
// Both policies reach their decisions by walking a map, which is the idiom whose
// walk order the evaluator does not fix — so this is the arrangement that would
// diverge without the total order each role establishes over its decisions.
func TestCelEmbedded_DeterministicOutput(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := celAnnotationWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	writeFixture(t, filepath.Join(root, "labels.cel"),
		`{"labels": input.graph.nodes.map(n, {"node": n, "key": "z.example.com/last", "value": "set"})
                    + input.graph.nodes.map(n, {"node": n, "key": "a.example.com/first", "value": "set"})
                    + input.graph.nodes.map(n, {"node": n, "key": "m.example.com/middle", "value": "set"})}`)
	writeFixture(t, filepath.Join(root, "annotations.cel"),
		`{"annotations": input.targets.map(t, {
                          "boundary_path": t.boundary_path,
                          "component_name": t.component_name,
                          "annotation_key": "gate.example.com/tier",
                          "annotation_value": "gold",
                        })}`)
	config := `workspace: {}
offline: true
plugins:
  governance:
    - uri: ` + celGovernanceURI + `:1.0.0
      role: label-transform
      with:
        sources:
          - labels.cel
    - uri: ` + celGovernanceURI + `:1.0.0
      role: annotation-transform
      with:
        sources:
          - annotations.cel
`
	rw := resolveWorkspaceWithConfig(t, root, config)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rw, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	outA := filepath.Join(root, "outA")
	if err := dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: outA}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build A: %v", err)
	}
	if found, tree := distTreeContains(t, outA, "a.example.com/first=set"); !found {
		t.Fatalf("build A applied no label; the comparison would be vacuous:\n%s", tree)
	}
	if found, tree := distTreeContains(t, outA, "core/annotation-gate"); !found {
		t.Fatalf("build A attached no feature; the comparison would be vacuous:\n%s", tree)
	}
	outB := filepath.Join(root, "outB")
	if err := dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: outB}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build B: %v", err)
	}
	if !snapshotEqual(snapshotDir(t, outA), snapshotDir(t, outB)) {
		t.Fatal("two builds of the same pinned input produced divergent output")
	}
}
