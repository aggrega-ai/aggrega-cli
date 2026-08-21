// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's end-to-end proofs for the embedded Rego governance guest
// (governance/rego) attached at each of the four governance roles and driven
// through the real pull -> build pipeline. The guest resolves through the embed
// tier by its bare identity and a workspace-catalog Plugin entity supplies the
// entity the closure pins.
//
// The four roles are proven by their observable effect on a real build rather
// than by inspecting guest output: a halting verdict stops the build, a warning
// verdict does not, a label decision reaches the rendered artifact through the
// compose recipe's node-label variable, and an annotation decision expands into
// a Feature attachment that reaches the same artifact's feature variable. The
// two confinement guardrails are proven by driving the guest into them.
//
// The compose component/recipe fixtures, the single-component workspace and the
// snapshot helpers are shared with the other embedded-guest proofs in this
// package.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

const regoGovernanceURI = "governance/rego"

// encodedPolicyArchive builds a base64-encoded gzipped tar carrying the named
// files, which is how a whole policy set is attached as one archive. The
// encoding is what the attached content is: a compressed archive is arbitrary
// bytes and an attached file's content travels as text, so the archive is
// encoded before it is attached.
//
// Members are written in sorted name order so the archive itself is
// byte-identical across runs of this test.
func encodedPolicyArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("write archive header for %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write archive member %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close compression: %v", err)
	}
	return base64.StdEncoding.EncodeToString(compressed.Bytes()) + "\n"
}

// writeRegoPluginEntity writes the embedded Rego guest's Plugin entity into the
// fixture catalog. Its .wasm resolves through the embed tier by the bare
// governance/rego coordinate; the entity is what the closure walk pins.
func writeRegoPluginEntity(t *testing.T, catalogDir string) {
	t.Helper()
	writeFixtureEntity(t, catalogDir, "plugin/governance/rego/1.0.0/plugin.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: rego
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1alpha1
  exports:
    - name: validate
    - name: mutate
    - name: transform_annotations
`)
}

// regoWorkspace lays the shared single-component workspace and adds the Rego
// guest's Plugin entity, so every proof below starts from one component named
// app carrying component.aggrega.ai/type=server.
func regoWorkspace(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root, catalogDir = singleComponentWorkspace(t)
	writeRegoPluginEntity(t, catalogDir)
	return root, catalogDir
}

// regoAttachment builds the workspace-config plugins block attaching the Rego
// guest at one role with one policy file and optional settings.
func regoAttachment(role, sourceFile, settings string) string {
	block := `workspace: {}
offline: true
plugins:
  governance:
    - uri: ` + regoGovernanceURI + `:1.0.0
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

// pullThen pulls the workspace and then builds into outDir, returning the build
// error so a proof can assert on a halt.
func pullThen(t *testing.T, root, releasePath string, catDirs []string, config, outDir string) error {
	t.Helper()
	rw := resolveWorkspaceWithConfig(t, root, config)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rw, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	return dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: filepath.Join(root, outDir)}, os.Stderr, os.Stderr)
}

// TestRegoEmbedded_IRVerdictSeverityRouting proves the guest attached over the
// build graph reports each of the three severities and that the build outcome
// follows: a halting decision stops the build with ErrPolicyViolation, while a
// warning and a recorded observation both leave a build that completes.
//
// The warn and info arms are the load-bearing half: "halt works" says nothing
// about whether the other two also stop the build, which would make every
// advisory policy a blocker.
func TestRegoEmbedded_IRVerdictSeverityRouting(t *testing.T) {
	nonInteractive(t)

	t.Run("halt stops the build", func(t *testing.T) {
		root, catalogDir := regoWorkspace(t)
		writeFixture(t, filepath.Join(root, "policy.rego"), `package proof

deny contains msg if {
	count(input.graph.spec.nodes) > 0
	msg := "the graph declares a node"
}
`)
		err := pullThen(t, root, filepath.Join(root, "release.yaml"),
			[]string{catalogDir, standardCatalogDir(t)},
			regoAttachment("ir-verdict", "policy.rego", ""), "out")
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
		name       string
		collection string
	}{
		{"warn does not stop the build", "warn"},
		{"info does not stop the build", "audit"},
	} {
		t.Run(arm.name, func(t *testing.T) {
			root, catalogDir := regoWorkspace(t)
			writeFixture(t, filepath.Join(root, "policy.rego"), `package proof

`+arm.collection+` contains msg if {
	count(input.graph.spec.nodes) > 0
	msg := "the graph declares a node"
}
`)
			outDir := "out-" + arm.collection
			if err := pullThen(t, root, filepath.Join(root, "release.yaml"),
				[]string{catalogDir, standardCatalogDir(t)},
				regoAttachment("ir-verdict", "policy.rego", ""), outDir); err != nil {
				t.Fatalf("a %s decision must not stop the build: %v", arm.collection, err)
			}
			// The build produced output, so the decision really was reached
			// without halting rather than the policy having matched nothing.
			if found, tree := distTreeContains(t, filepath.Join(root, outDir), "NODE_LABELS"); !found {
				t.Fatalf("the build produced no rendered output:\n%s", tree)
			}
		})
	}
}

// TestRegoEmbedded_RenderVerdictHalts proves the guest attached over the
// rendered output reads the rendered files and halts the build on a decision
// about them. The negative control is the same policy asserting on content the
// artifact does not carry, which must leave the build standing — so the positive
// assertion cannot pass on an unconditional halt.
func TestRegoEmbedded_RenderVerdictHalts(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	// Negative control: the policy looks for an image the recipe never renders.
	writeFixture(t, filepath.Join(root, "policy.rego"), `package proof

deny contains msg if {
	some artifact in input.artifacts
	contains(artifact.content, "image-that-is-never-rendered")
	msg := "found the absent image"
}
`)
	if err := pullThen(t, root, releasePath, catDirs,
		regoAttachment("render-verdict", "policy.rego", ""), "out-neg"); err != nil {
		t.Fatalf("negative control: a policy that matches nothing must not stop the build: %v", err)
	}

	// Positive: the same shape of policy, matching the image the recipe does
	// render, so the halt is attributable to the artifact's real content.
	writeFixture(t, filepath.Join(root, "policy.rego"), `package proof

deny contains msg if {
	some artifact in input.artifacts
	contains(artifact.content, "busybox")
	msg := sprintf("a busybox image was rendered at %v", [artifact.path])
}
`)
	err := pullThen(t, root, releasePath, catDirs,
		regoAttachment("render-verdict", "policy.rego", ""), "out-pos")
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

// TestRegoEmbedded_RenderVerdictReadsStructuredDocuments proves the guest hands
// a policy the rendered file's structured documents, not only its raw text, so a
// policy reasons over the rendered object rather than matching characters.
func TestRegoEmbedded_RenderVerdictReadsStructuredDocuments(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.rego"), `package proof

deny contains msg if {
	some artifact in input.artifacts
	some name, service in artifact.documents[_].services
	msg := sprintf("service %v declares image %v", [name, service.image])
}
`)
	err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		regoAttachment("render-verdict", "policy.rego", ""), "out")
	if err == nil {
		t.Fatal("a policy reading the rendered documents must reach its decision")
	}
	if !strings.Contains(err.Error(), "declares image busybox") {
		t.Fatalf("the policy must have read the structured document, got: %v", err)
	}
}

// TestRegoEmbedded_LabelTransformApplies proves a label decision reaches the
// rendered artifact: the guest attached to shape labels reports one label, the
// build applies it to the node's label set, and the compose recipe renders it.
//
// The label key deliberately carries a "/", which is the ordinary shape of an
// Aggrega label key and the case a hand-written change instruction would get
// wrong.
func TestRegoEmbedded_LabelTransformApplies(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoWorkspace(t)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, filepath.Join(root, "policy.rego"), `package tagging

labels contains {"node": id, "key": "compliance.example.com/tier", "value": "gold"} if {
	some id, node in input.graph.nodes
	node.labels["component.aggrega.ai/type"] == "server"
}
`)
	rw := resolveWorkspaceWithConfig(t, root, regoAttachment("label-transform", "policy.rego", ""))
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
	if len(lf.Mutators) != 1 || lf.Mutators[0].Plugin != regoGovernanceURI {
		t.Fatalf("lockfile mutators = %+v; want one attachment pinned at %q", lf.Mutators, regoGovernanceURI)
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

// TestRegoEmbedded_LabelTransformRemovesALabel proves a decision can drop a
// label the node carries, which is the other half of the change surface the
// build authorizes.
func TestRegoEmbedded_LabelTransformRemovesALabel(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.rego"), `package tagging

labels contains {"node": id, "key": "component.aggrega.ai/type", "op": "remove"} if {
	some id, node in input.graph.nodes
	node.labels["component.aggrega.ai/type"] == "server"
}
`)
	if err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		regoAttachment("label-transform", "policy.rego", ""), "out"); err != nil {
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

// TestRegoEmbedded_LabelTransformDrivesFeatureAttachment is the load-bearing
// proof that a label decision reaches the graph rather than only the rendered
// text: a Feature whose selector requires a label the component does not carry
// attaches only because the policy decided that label.
//
// The negative control is the same workspace with no attachment, where the
// selector is unsatisfied and the build halts — so the positive assertion cannot
// be vacuous.
func TestRegoEmbedded_LabelTransformDrivesFeatureAttachment(t *testing.T) {
	nonInteractive(t)

	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	writeComposeComponentRecipe(t, catalogDir, "test/svc", "svc", map[string]string{
		"component.aggrega.ai/type": "server",
	})
	writeRegoPluginEntity(t, catalogDir)

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
	writeFixture(t, filepath.Join(root, "policy.rego"), `package tagging

labels contains {"node": id, "key": "role.aggrega.ai/kind", "value": "cache"} if {
	some id, node in input.graph.nodes
	node.labels["component.aggrega.ai/type"] == "server"
}
`)
	if err := pullThen(t, root, releasePath, catDirs,
		regoAttachment("label-transform", "policy.rego", ""), "out-pos"); err != nil {
		t.Fatalf("positive: the decided label should satisfy the selector: %v", err)
	}
	if found, tree := distTreeContains(t, filepath.Join(root, "out-pos"), "core/label-gate"); !found {
		t.Fatalf("positive: the feature attachment did not survive into the rendered output:\n%s", tree)
	}
}

// TestRegoEmbedded_LabelTransformConfinementIsEnforced drives the guest into the
// label confinement from both directions a policy can reach it, and requires the
// build to refuse loudly each time.
//
// The confinement matters because a plugin attached to shape labels is the
// invited exception to the rule that nothing invents infrastructure: the
// invitation is exactly as wide as the role. A policy naming a node the build
// does not have, or dropping a label the node does not carry, is reaching past
// the label set it was invited to shape.
func TestRegoEmbedded_LabelTransformConfinementIsEnforced(t *testing.T) {
	nonInteractive(t)

	for _, arm := range []struct {
		name   string
		policy string
	}{
		{
			name: "a decision naming a node the build does not have",
			policy: `package tagging

labels contains {"node": "no-such-node", "key": "tier", "value": "gold"} if { true }
`,
		},
		{
			name: "a decision dropping a label the node does not carry",
			policy: `package tagging

labels contains {"node": id, "key": "never.example.com/set", "op": "remove"} if {
	some id, _ in input.graph.nodes
}
`,
		},
	} {
		t.Run(arm.name, func(t *testing.T) {
			root, catalogDir := regoWorkspace(t)
			writeFixture(t, filepath.Join(root, "policy.rego"), arm.policy)
			err := pullThen(t, root, filepath.Join(root, "release.yaml"),
				[]string{catalogDir, standardCatalogDir(t)},
				regoAttachment("label-transform", "policy.rego", ""), "out")
			if err == nil {
				t.Fatal("a change outside the label set must be refused, not applied")
			}
			if !errors.Is(err, topology.ErrMutatorPatchRejected()) {
				t.Fatalf("want ErrMutatorPatchRejected, got: %v", err)
			}
		})
	}
}

// TestRegoEmbedded_LabelTransformCannotReachBeyondLabels proves the other half
// of the confinement — that a policy has no way to name a surface other than a
// node's labels — by having it try. Whatever a decision names becomes part of a
// label's own name, so the attempt lands as an oddly-named label on the node the
// policy named rather than as a change to the node's component or version.
func TestRegoEmbedded_LabelTransformCannotReachBeyondLabels(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoWorkspace(t)
	// The decision names a key shaped like a path out of the label set. It
	// cannot escape: the name is carried as one label name.
	writeFixture(t, filepath.Join(root, "policy.rego"), `package tagging

labels contains {"node": id, "key": "../resolved_version", "value": "9.9.9"} if {
	some id, _ in input.graph.nodes
}
`)
	if err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		regoAttachment("label-transform", "policy.rego", ""), "out"); err != nil {
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

// regoAnnotationWorkspace lays a workspace whose Manifest registers an
// annotation binding, so an injected annotation under that key expands into a
// Feature attachment. Returns root and the fixture catalog dir.
func regoAnnotationWorkspace(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root = t.TempDir()
	catalogDir = filepath.Join(root, "catalog")
	writeComposeComponentRecipe(t, catalogDir, "test/svc", "svc", map[string]string{
		"component.aggrega.ai/type": "server",
	})
	writeRegoPluginEntity(t, catalogDir)

	writeFixtureEntity(t, catalogDir, "feature/core/annotation-gate/1.0.0/feature.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/annotation-gate
  version: "1.0.0"
spec:
  schema:
    infra:
      tier: { type: string }
`)
	writeFixtureEntity(t, catalogDir, "feature-recipe/compose/annotation-gate/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: annotation-gate-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: core/annotation-gate
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
        core/annotation-gate: annotation-gate-compose:1.0.0
      annotations:
        gate.example.com/tier:
          feature: core/annotation-gate
          maps:
            infra:
              - tier
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

// TestRegoEmbedded_AnnotationTransformExpandsIntoAFeature proves an annotation
// decision reaches the build: the guest reports one annotation under a key the
// Manifest has registered, and the annotation expands into a Feature attachment
// that reaches the rendered artifact.
//
// The negative control is the same workspace with no attachment, where no
// annotation exists and no feature attaches — so the positive assertion is not
// reading a feature that was always there.
func TestRegoEmbedded_AnnotationTransformExpandsIntoAFeature(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoAnnotationWorkspace(t)
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
	writeFixture(t, filepath.Join(root, "policy.rego"), `package tagging

annotations contains {
	"boundary_path": target.boundary_path,
	"component_name": target.component_name,
	"annotation_key": "gate.example.com/tier",
	"annotation_value": "gold",
} if {
	some target in input.targets
	"gate.example.com/tier" in input.binding_keys
}
`)
	if err := pullThen(t, root, releasePath, catDirs,
		regoAttachment("annotation-transform", "policy.rego", ""), "out-pos"); err != nil {
		t.Fatalf("positive build: %v", err)
	}
	if found, tree := distTreeContains(t, filepath.Join(root, "out-pos"), "core/annotation-gate"); !found {
		t.Fatalf("positive: the injected annotation did not expand into a feature attachment:\n%s", tree)
	}
}

// TestRegoEmbedded_AnnotationTransformConfinementIsEnforced drives the guest
// into the annotation confinement: a decision naming a component the deployment
// does not have must be refused loudly rather than applied or dropped.
func TestRegoEmbedded_AnnotationTransformConfinementIsEnforced(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoAnnotationWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.rego"), `package tagging

annotations contains {
	"boundary_path": "main",
	"component_name": "no-such-component",
	"annotation_key": "gate.example.com/tier",
	"annotation_value": "gold",
} if { true }
`)
	err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		regoAttachment("annotation-transform", "policy.rego", ""), "out")
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

// TestRegoEmbedded_TranslationOverrideMapsACustomKey proves an operator can map
// their own decision key onto a severity, and that declaring a mapping replaces
// the default rather than extending it.
//
// The replace half is what makes the proof more than "the setting is read": the
// same policy also collects a conventional deny, which must not fire once a
// mapping naming only the custom key is declared.
func TestRegoEmbedded_TranslationOverrideMapsACustomKey(t *testing.T) {
	nonInteractive(t)

	const policy = `package proof

blocked contains msg if {
	count(input.graph.spec.nodes) > 0
	msg := "the custom decision key fired"
}

deny contains msg if {
	count(input.graph.spec.nodes) > 0
	msg := "the conventional key fired"
}
`

	// Without a mapping, the conventional key halts and the custom one is not
	// read at all.
	root, catalogDir := regoWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.rego"), policy)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	errDefault := pullThen(t, root, filepath.Join(root, "release.yaml"), catDirs,
		regoAttachment("ir-verdict", "policy.rego", ""), "out-default")
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
	root2, catalogDir2 := regoWorkspace(t)
	writeFixture(t, filepath.Join(root2, "policy.rego"), policy)
	catDirs2 := []string{catalogDir2, standardCatalogDir(t)}
	errMapped := pullThen(t, root2, filepath.Join(root2, "release.yaml"), catDirs2,
		regoAttachment("ir-verdict", "policy.rego", `        settings:
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

// TestRegoEmbedded_MalformedTranslationSettingsHaltBeforeDispatch proves a
// mapping the guest's declared settings schema does not admit halts the build
// against that schema, before the policy runs — so a typo in an attachment's
// settings is caught as a misconfiguration rather than surfacing as a policy
// verdict or being silently ignored.
func TestRegoEmbedded_MalformedTranslationSettingsHaltBeforeDispatch(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoWorkspace(t)
	// A policy that would reach no decision at all, so a halt can only have come
	// from the settings check rather than from the policy.
	writeFixture(t, filepath.Join(root, "policy.rego"), "package proof\n")
	err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		regoAttachment("ir-verdict", "policy.rego", `        settings:
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

// TestRegoEmbedded_ArchiveEvaluatesAsTheSameFilesAttachedSeparately proves the
// two ways of attaching a policy set are equivalent: an archive carrying the
// policy and its data reaches the same decision as the same policy attached on
// its own with the same data supplied as settings, and a build under each is
// byte-identical.
func TestRegoEmbedded_ArchiveEvaluatesAsTheSameFilesAttachedSeparately(t *testing.T) {
	nonInteractive(t)

	// The policy reads its threshold from data, so the archive's own data file is
	// load-bearing: were it dropped, the rule would be undefined and reach no
	// decision.
	const policy = `package tagging

labels contains {"node": id, "key": "tier", "value": data.limits.tier} if {
	some id, _ in input.graph.nodes
}
`
	const dataDocument = `{"limits":{"tier":"gold"}}`

	// Attached separately: the policy file, with the data supplied as settings.
	rootA, catalogA := regoWorkspace(t)
	writeFixture(t, filepath.Join(rootA, "policy.rego"), policy)
	if err := pullThen(t, rootA, filepath.Join(rootA, "release.yaml"),
		[]string{catalogA, standardCatalogDir(t)},
		regoAttachment("label-transform", "policy.rego", `        settings:
          limits:
            tier: gold
`), "out"); err != nil {
		t.Fatalf("separate-files build: %v", err)
	}
	outA := filepath.Join(rootA, "out")
	if found, tree := distTreeContains(t, outA, "tier=gold"); !found {
		t.Fatalf("separate-files build did not apply the label; the comparison would be vacuous:\n%s", tree)
	}

	// Attached as one archive carrying both files.
	rootB, catalogB := regoWorkspace(t)
	writeFixture(t, filepath.Join(rootB, "bundle.tar.gz.base64"),
		encodedPolicyArchive(t, map[string]string{"policy.rego": policy, "data.json": dataDocument}))
	if err := pullThen(t, rootB, filepath.Join(rootB, "release.yaml"),
		[]string{catalogB, standardCatalogDir(t)},
		regoAttachment("label-transform", "bundle.tar.gz.base64", ""), "out"); err != nil {
		t.Fatalf("archive build: %v", err)
	}
	outB := filepath.Join(rootB, "out")
	if found, tree := distTreeContains(t, outB, "tier=gold"); !found {
		t.Fatalf("archive build did not apply the label:\n%s", tree)
	}

	if !snapshotEqual(snapshotDir(t, outA), snapshotDir(t, outB)) {
		t.Fatal("an archive and the same files attached separately produced divergent output")
	}
}

// TestRegoEmbedded_ADamagedArchiveHaltsTheBuild proves a policy set that cannot
// be opened is a loud build fault rather than an empty policy set, which would
// let every decision go unreached while the build succeeded.
func TestRegoEmbedded_ADamagedArchiveHaltsTheBuild(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoWorkspace(t)
	writeFixture(t, filepath.Join(root, "bundle.tar.gz.base64"), "this is not an archive ###\n")
	err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		regoAttachment("ir-verdict", "bundle.tar.gz.base64", ""), "out")
	if err == nil {
		t.Fatal("a policy set that cannot be opened must halt the build")
	}
	if errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("an unreadable policy set is a build fault, not a policy verdict: %v", err)
	}
	if !strings.Contains(err.Error(), "bundle.tar.gz.base64") {
		t.Fatalf("the fault must name the attached file, got: %v", err)
	}
}

// TestRegoEmbedded_UnimplementedBuiltinHaltsRatherThanAllowing proves a policy
// the engine cannot evaluate stops the build as a fault, distinct from a
// decision — "this policy could not be evaluated" must never read as "this
// policy allowed the build".
func TestRegoEmbedded_UnimplementedBuiltinHaltsRatherThanAllowing(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoWorkspace(t)
	writeFixture(t, filepath.Join(root, "policy.rego"), `package proof

deny contains msg if {
	net.lookup_ip_addr("example.com")
	msg := "resolved"
}
`)
	err := pullThen(t, root, filepath.Join(root, "release.yaml"),
		[]string{catalogDir, standardCatalogDir(t)},
		regoAttachment("ir-verdict", "policy.rego", ""), "out")
	if err == nil {
		t.Fatal("a policy that cannot be evaluated must stop the build")
	}
	if errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("an unevaluable policy is a build fault, not a policy verdict: %v", err)
	}
}

// TestRegoEmbedded_DeterministicOutput proves two builds of the same pinned
// input are byte-identical with the guest attached at both transform roles at
// once, which is where a non-deterministic decision order would show.
func TestRegoEmbedded_DeterministicOutput(t *testing.T) {
	nonInteractive(t)

	root, catalogDir := regoAnnotationWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	writeFixture(t, filepath.Join(root, "labels.rego"), `package tagging

labels contains {"node": id, "key": key, "value": "set"} if {
	some id, _ in input.graph.nodes
	some key in {"z.example.com/last", "a.example.com/first", "m.example.com/middle"}
}
`)
	writeFixture(t, filepath.Join(root, "annotations.rego"), `package tagging

annotations contains {
	"boundary_path": target.boundary_path,
	"component_name": target.component_name,
	"annotation_key": "gate.example.com/tier",
	"annotation_value": "gold",
} if {
	some target in input.targets
}
`)
	config := `workspace: {}
offline: true
plugins:
  governance:
    - uri: ` + regoGovernanceURI + `:1.0.0
      role: label-transform
      with:
        sources:
          - labels.rego
    - uri: ` + regoGovernanceURI + `:1.0.0
      role: annotation-transform
      with:
        sources:
          - annotations.rego
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
