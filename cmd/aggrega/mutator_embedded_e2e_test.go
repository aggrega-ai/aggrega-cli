// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's end-to-end proofs for the two embedded mutator guests
// (governance/label-injector and governance/selector-tagger) driven through the real
// pull -> build pipeline. Each mutator is attached at the mutator socket by its
// bare embedded identity: its .wasm resolves through the embed tier,
// and a workspace-catalog Plugin entity supplies the entity the closure pins.
//
// The compose provider entity ships in the standard catalog (its .wasm is
// embedded); the fixture catalog supplies the component(s), recipe(s), the
// mutator Plugin entities, and — for the feature proof — a Feature and its
// FeatureRecipe. The compose recipe emits every node-label key=value pair and
// every attached feature type into a container variable, so a label a mutator
// injects — and a feature whose attachment the injected label validates —
// surface deterministically in the rendered artifact.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

const (
	labelInjectorURI  = "governance/label-injector"
	selectorTaggerURI = "governance/selector-tagger"
)

// writeComposeComponentRecipe writes a Component of type compType (carrying the
// pre-existing labels the mutators match on) plus a compose Recipe that
// implements it. The recipe emits each node-label key=value pair into the
// NODE_LABELS container variable and each attached feature type into FEATURES,
// so both a mutator-injected label and a validated feature attachment surface in
// the rendered compose artifact. recipeLeaf is the recipe's metadata.name and the
// leaf of its compose/<leaf> URI.
func writeComposeComponentRecipe(t *testing.T, catalogDir, compType, recipeLeaf string, labels map[string]string) {
	t.Helper()
	labelBlock := ""
	for k, v := range labels {
		labelBlock += "    " + k + ": \"" + v + "\"\n"
	}
	writeFixtureEntity(t, catalogDir, "component/"+compType+"/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: `+compType+`
  version: "1.0.0"
  labels:
`+labelBlock+`spec:
  schema:
    values: {}
`)
	writeFixtureEntity(t, catalogDir, "recipe/compose/"+recipeLeaf+"/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: `+recipeLeaf+`
  version: "1.0.0"
spec:
  capability: compose
  implements: `+compType+`
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
        variables:
          NODE_LABELS: "{{ range $k, $v := .context.labels }}{{ $k }}={{ $v }};{{ end }}"
          FEATURES: "{{ range $k, $v := .features }}{{ $v.type }};{{ end }}"
`)
}

// writeMutatorPluginEntities writes the two embedded mutators' Plugin entities
// into the fixture catalog. Their .wasm artifacts resolve through the embed tier
// by the bare coordinate (mutator/<name>); the entity is what the closure walk
// pins for the lockfile.
func writeMutatorPluginEntities(t *testing.T, catalogDir string) {
	t.Helper()
	for _, name := range []string{"label-injector", "selector-tagger"} {
		writeFixtureEntity(t, catalogDir, "plugin/governance/"+name+"/1.0.0/plugin.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: `+name+`
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1alpha1
  exports:
    - name: mutate
`)
	}
}

// singleComponentWorkspace lays a workspace with one component (app, type
// test/svc, carrying the label component.aggrega.ai/type=server) routed through a
// compose boundary, plus the mutator Plugin entities. The caller writes the
// .aggrega-config.yaml so it can vary the plugins block. Returns root and the
// fixture catalog dir.
func singleComponentWorkspace(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root = t.TempDir()
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
	catalogDir = filepath.Join(root, "catalog")
	writeComposeComponentRecipe(t, catalogDir, "test/svc", "svc", map[string]string{
		"component.aggrega.ai/type": "server",
	})
	writeMutatorPluginEntities(t, catalogDir)
	return root, catalogDir
}

// TestMutatorEmbedded_LabelInjectorApplies proves the label-injector guest,
// attached by its embedded identity, injects a label onto a node whose
// pre-existing labels match its rule — reaching the rendered artifact — and that
// the settings form (rules inline) and the sources form (rules in a rules.yaml
// definition file) produce byte-identical output.
func TestMutatorEmbedded_LabelInjectorApplies(t *testing.T) {
	nonInteractive(t)
	root, catalogDir := singleComponentWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	const injected = "inject.aggrega.ai/mark=hit"

	// Settings form: the rule set is authored inline under with.settings.
	rwSettings := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  governance:
    - uri: `+labelInjectorURI+`:1.0.0
      role: label-transform
      with:
        settings:
          rules:
            - match:
                component.aggrega.ai/type: server
              set:
                inject.aggrega.ai/mark: hit
`)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwSettings, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (settings form): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.Mutators) != 1 || lf.Mutators[0].Plugin != labelInjectorURI {
		t.Fatalf("lockfile mutators = %+v; want one attachment pinned at %q", lf.Mutators, labelInjectorURI)
	}

	outSettings := filepath.Join(root, "out-settings")
	if err := dispatchBuildWithDiag(rwSettings, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: outSettings}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build (settings form): %v", err)
	}
	if found, tree := distTreeContains(t, outSettings, injected); !found {
		t.Fatalf("settings form: injected label %q absent from rendered output:\n%s", injected, tree)
	}
	snapSettings := snapshotDir(t, outSettings)

	// Sources form: the same rule set authored in a rules.yaml definition file.
	writeFixture(t, filepath.Join(root, "rules.yaml"), `rules:
  - match:
      component.aggrega.ai/type: server
    set:
      inject.aggrega.ai/mark: hit
`)
	rwSources := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  governance:
    - uri: `+labelInjectorURI+`:1.0.0
      role: label-transform
      with:
        sources:
          - rules.yaml
`)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwSources, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (sources form): %v", err)
	}
	outSources := filepath.Join(root, "out-sources")
	if err := dispatchBuildWithDiag(rwSources, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: outSources}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build (sources form): %v", err)
	}
	if found, tree := distTreeContains(t, outSources, injected); !found {
		t.Fatalf("sources form: injected label %q absent from rendered output:\n%s", injected, tree)
	}
	snapSources := snapshotDir(t, outSources)

	if !snapshotEqual(snapSettings, snapSources) {
		t.Fatal("settings form and sources form produced divergent output; a rule set authored either way must inject the same label")
	}
}

// TestMutatorEmbedded_SelectorTaggerApplies proves the selector-tagger guest,
// driven by a component-to-tag mapping file plus a settings default, sets the tag
// label to the mapped value on a mapped component and to the default on an
// unmapped one — both reaching the rendered artifact.
func TestMutatorEmbedded_SelectorTaggerApplies(t *testing.T) {
	nonInteractive(t)
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	writeComposeComponentRecipe(t, catalogDir, "test/web", "web", map[string]string{
		"component.aggrega.ai/type": "server",
	})
	writeComposeComponentRecipe(t, catalogDir, "test/db", "db", map[string]string{
		"component.aggrega.ai/type": "resource",
	})
	writeMutatorPluginEntities(t, catalogDir)

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    web:
      type: test/web
    db:
      type: test/db
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
        test/web:1.0.0: compose/web:1.0.0
        test/db:1.0.0: compose/db:1.0.0
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
        web: {}
        db: {}
`)
	// Map test/web -> frontend; test/db is absent so it takes the default.
	writeFixture(t, filepath.Join(root, "mapping.yaml"), "test/web: frontend\n")

	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	rw := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  governance:
    - uri: `+selectorTaggerURI+`:1.0.0
      role: label-transform
      with:
        sources:
          - mapping.yaml
        settings:
          default: untagged
`)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rw, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	out := filepath.Join(root, "out")
	if err := dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build: %v", err)
	}
	if found, tree := distTreeContains(t, out, "tag=frontend"); !found {
		t.Fatalf("mapped component: tag=frontend absent from rendered output:\n%s", tree)
	}
	if found, tree := distTreeContains(t, out, "tag=untagged"); !found {
		t.Fatalf("unmapped component: default tag=untagged absent from rendered output:\n%s", tree)
	}
}

// TestMutatorEmbedded_LabelDrivesFeatureAttachment is the load-bearing IR proof:
// a Feature whose appliesTo selector requires a label the component does not
// initially carry is attached to that component. Without the mutator the label is
// absent and the appliesTo constraint halts the build loudly
// (ErrSelectorViolation) — the negative control, so the positive assertion cannot
// be vacuous. With the label-injector attached, the injected label satisfies the
// selector, the attachment survives into the IR, and the feature type reaches the
// rendered artifact. This proves label -> IR -> selector-match -> feature
// attachment through a real build.
//
// The product mechanism is a loud guardrail, not a silent filter: appliesTo is a
// validated constraint on an attached Feature, so "the Feature does not attach"
// manifests as a build halt rather than silent omission.
func TestMutatorEmbedded_LabelDrivesFeatureAttachment(t *testing.T) {
	nonInteractive(t)
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	writeComposeComponentRecipe(t, catalogDir, "test/svc", "svc", map[string]string{
		"component.aggrega.ai/type": "server",
	})
	writeMutatorPluginEntities(t, catalogDir)

	// A Feature that applies only to nodes labelled role.aggrega.ai/kind=cache —
	// a label test/svc does not carry until the mutator injects it.
	writeFixtureEntity(t, catalogDir, "feature/core/mutator-gate/1.0.0/feature.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/mutator-gate
  version: "1.0.0"
spec:
  appliesTo: "role.aggrega.ai/kind=cache"
  schema:
    values:
      enabled: { type: boolean, default: true }
`)
	// A FeatureRecipe implementing it; renders no resources — the feature's
	// presence is observed through the consuming node's .features context.
	writeFixtureEntity(t, catalogDir, "feature-recipe/compose/mutator-gate/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: mutator-gate-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: core/mutator-gate
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
          type: core/mutator-gate
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
        core/mutator-gate: mutator-gate-compose:1.0.0
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

	// Negative control: NO mutator attached. The appliesTo constraint is
	// unsatisfied (the node lacks role.aggrega.ai/kind=cache) and the build halts
	// with ErrSelectorViolation.
	rwNoMutator := resolveWorkspaceWithConfig(t, root, "workspace: {}\noffline: true\n")
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwNoMutator, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (no mutator): %v", err)
	}
	outNeg := filepath.Join(root, "out-neg")
	errNeg := dispatchBuildWithDiag(rwNoMutator, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: outNeg}, os.Stderr, os.Stderr)
	if errNeg == nil {
		t.Fatal("negative control: build with no mutator succeeded; the feature's appliesTo should be unsatisfied and halt the build")
	}
	if !errors.Is(errNeg, analyzer.ErrSelectorViolation) {
		t.Fatalf("negative control: want ErrSelectorViolation (the injected label is load-bearing), got: %v", errNeg)
	}
	t.Logf("negative control PASS: no mutator -> appliesTo unsatisfied -> build halts with ErrSelectorViolation")

	// Positive: attach the label-injector, which sets role.aggrega.ai/kind=cache
	// on the node matched by its pre-existing component.aggrega.ai/type=server
	// label. The appliesTo constraint is now satisfied; the feature attaches and
	// its type reaches the rendered artifact.
	rwMutator := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  governance:
    - uri: `+labelInjectorURI+`:1.0.0
      role: label-transform
      with:
        settings:
          rules:
            - match:
                component.aggrega.ai/type: server
              set:
                role.aggrega.ai/kind: cache
`)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwMutator, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (mutator attached): %v", err)
	}
	outPos := filepath.Join(root, "out-pos")
	if err := dispatchBuildWithDiag(rwMutator, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: outPos}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("positive: build with the label-injector attached must succeed (injected label satisfies appliesTo): %v", err)
	}
	if found, tree := distTreeContains(t, outPos, "core/mutator-gate"); !found {
		t.Fatalf("positive: feature type core/mutator-gate absent from rendered output; the attachment did not survive:\n%s", tree)
	}
	t.Logf("positive PASS: label-injector injects role.aggrega.ai/kind=cache -> appliesTo satisfied -> feature core/mutator-gate reaches rendered output")
}

// TestMutatorEmbedded_SettingsConfigInvariantAfterPull proves that after a pull
// pins a label-injector attachment carrying inline settings, changing
// with.settings in workspace config and rebuilding WITHOUT a re-pull produces
// byte-identical output — the build reads the pinned settings from the lockfile.
func TestMutatorEmbedded_SettingsConfigInvariantAfterPull(t *testing.T) {
	nonInteractive(t)
	root, catalogDir := singleComponentWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	rwPull := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  governance:
    - uri: `+labelInjectorURI+`:1.0.0
      role: label-transform
      with:
        settings:
          rules:
            - match:
                component.aggrega.ai/type: server
              set:
                inject.aggrega.ai/mark: pinned
`)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwPull, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	out1 := filepath.Join(root, "out1")
	if err := dispatchBuildWithDiag(rwPull, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out1}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build 1: %v", err)
	}
	if found, tree := distTreeContains(t, out1, "inject.aggrega.ai/mark=pinned"); !found {
		t.Fatalf("build 1: pinned settings label absent; the assertion would be vacuous:\n%s", tree)
	}
	snap1 := snapshotDir(t, out1)

	// Change the settings (a different injected value) with NO re-pull. Were the
	// runtime settings read live, the output would move; because the build reads
	// the pinned settings, the output is byte-identical.
	rwChanged := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  governance:
    - uri: `+labelInjectorURI+`:1.0.0
      role: label-transform
      with:
        settings:
          rules:
            - match:
                component.aggrega.ai/type: server
              set:
                inject.aggrega.ai/mark: changed
`)
	out2 := filepath.Join(root, "out2")
	if err := dispatchBuildWithDiag(rwChanged, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out2}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build 2 (settings changed, no re-pull): %v", err)
	}
	snap2 := snapshotDir(t, out2)
	if !snapshotEqual(snap1, snap2) {
		t.Fatal("build output changed after a with.settings change with no re-pull; build must read the pinned settings from the lockfile, not live config")
	}
}

// TestMutatorEmbedded_SourceContentConfigInvariantAfterPull proves that after a
// pull pins a label-injector attachment whose rule set lives in a source file,
// changing the source FILE's content on disk and rebuilding WITHOUT a re-pull
// produces byte-identical output — the build reads the pinned source content from
// the lockfile, not live disk.
func TestMutatorEmbedded_SourceContentConfigInvariantAfterPull(t *testing.T) {
	nonInteractive(t)
	root, catalogDir := singleComponentWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	rulesPath := filepath.Join(root, "rules.yaml")
	writeFixture(t, rulesPath, `rules:
  - match:
      component.aggrega.ai/type: server
    set:
      inject.aggrega.ai/mark: pinned
`)
	rw := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  governance:
    - uri: `+labelInjectorURI+`:1.0.0
      role: label-transform
      with:
        sources:
          - rules.yaml
`)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rw, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	out1 := filepath.Join(root, "out1")
	if err := dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out1}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build 1: %v", err)
	}
	if found, tree := distTreeContains(t, out1, "inject.aggrega.ai/mark=pinned"); !found {
		t.Fatalf("build 1: pinned source label absent; the assertion would be vacuous:\n%s", tree)
	}
	snap1 := snapshotDir(t, out1)

	// Rewrite the source file's content on disk (a different injected value),
	// with NO re-pull. Were the source read live at build, the output would move;
	// because the build reads the pinned content, the output is byte-identical.
	writeFixture(t, rulesPath, `rules:
  - match:
      component.aggrega.ai/type: server
    set:
      inject.aggrega.ai/mark: changed
`)
	out2 := filepath.Join(root, "out2")
	if err := dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out2}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build 2 (source content changed, no re-pull): %v", err)
	}
	snap2 := snapshotDir(t, out2)
	if !snapshotEqual(snap1, snap2) {
		t.Fatal("build output changed after a source-file content change with no re-pull; build must read the pinned source content from the lockfile, not live disk")
	}
}

// TestMutatorEmbedded_DeterministicOutput proves a mutator-attached build is
// byte-identical across two hermetic invocations of the same pinned input.
func TestMutatorEmbedded_DeterministicOutput(t *testing.T) {
	nonInteractive(t)
	root, catalogDir := singleComponentWorkspace(t)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	releasePath := filepath.Join(root, "release.yaml")

	rw := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  governance:
    - uri: `+labelInjectorURI+`:1.0.0
      role: label-transform
      with:
        settings:
          rules:
            - match:
                component.aggrega.ai/type: server
              set:
                inject.aggrega.ai/mark: hit
`)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rw, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	outA := filepath.Join(root, "outA")
	if err := dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: outA}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build A: %v", err)
	}
	outB := filepath.Join(root, "outB")
	if err := dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: outB}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build B: %v", err)
	}
	if !snapshotEqual(snapshotDir(t, outA), snapshotDir(t, outB)) {
		t.Fatal("two hermetic builds of the same pinned mutator input produced divergent output")
	}
}
