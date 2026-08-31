// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// End-to-end proof that the central input-kind capability gate refuses EVERY
// input kind a node declares that its bound provider cannot honour, and that it
// refuses them itself rather than letting one reach a guest that then refuses it
// in its own words.
//
// The fixtures bind the terraform provider, which renders sourced input and
// standalone artifacts and does not synthesise workloads. That makes it the one
// target where a combination is separable: a Recipe declaring a source AND a
// workload has one kind the provider honours and one it does not, so a gate that
// consults only the kind precedence names is satisfied while the build still
// cannot succeed.
//
// A node declaring several input kinds is legal and its outputs are added
// together, so nothing here refuses a combination for being a combination — the
// last two tests are what hold that line, and each builds a combination the
// provider does honour.

// writeTerraformGateWorkspace lays down a buildable workspace whose single
// component renders through the terraform provider, with the Recipe's input-kind
// blocks supplied verbatim by recipeInputs — the whole point being that the cases
// below differ in those blocks and in nothing else.
//
// When frInputs is non-empty the component also carries an attachment, so the
// same corpus reaches the FeatureRecipe render path. The gate is one gate called
// from both paths, and passing one corpus builder through both is what keeps the
// two cases comparable.
func writeTerraformGateWorkspace(t *testing.T, root, recipeInputs, frInputs string) (releasePath, catalogRoot string) {
	t.Helper()

	attachment := ""
	featureMapping := ""
	if frInputs != "" {
		attachment = "      features:\n        obs:\n          type: core/metrics\n"
		featureMapping = "    features:\n      type:\n        core/metrics: metrics-fr:1.0.0\n"
	}

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: tf-gate
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
      values:
        port: 8080
`+attachment)

	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: tf-gate-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only:
          infra:
            replicas: 1
`)

	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: tf-gate-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`+featureMapping)

	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	seedEmbeddedPlugins(t, filepath.Join(catalogRoot, "base", "providers"), "provider/terraform")

	writeFixture(t, filepath.Join(catalogRoot, "recipes", "terraform", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: terraform
  implements: core/server
`+recipeInputs)

	if frInputs != "" {
		writeFixture(t, filepath.Join(catalogRoot, "base", "features", "metrics.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/metrics
  version: "1.0.0"
spec:
  schema: {}
`)
		writeFixture(t, filepath.Join(catalogRoot, "feature-recipes", "terraform", "metrics.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: metrics-fr
  version: "1.0.0"
spec:
  capability: terraform
  implements: core/metrics
`+frInputs)
	}

	return filepath.Join(root, "release.yaml"), catalogRoot
}

// writeTerraformModule writes a minimal vendorable Terraform module — a
// configuration body declaring no input variables, so the boundary root's module
// block carries no arguments and the corpus turns on the input kinds alone.
func writeTerraformModule(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, filepath.Join(dir, "main.tf"), `resource "null_resource" "server" {
  triggers = {
    always = "static"
  }
}
`)
}

// tfWorkloadBlock is the workload: block the mixed-kind fixtures declare. It is
// well-formed Workload content, so a build that halts halts on the provider's
// inability to synthesise it rather than on anything wrong with the block.
const tfWorkloadBlock = `  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`

// tfAttachmentWorkloadBlock is the workload: block the attachment fixture
// declares. It templates nothing, unlike the component's own block above: an
// attachment's render context carries the pillars an attachment has, and a
// workload reaching for a component's values resolves them to nothing and is
// refused by workload validation — an earlier gate than the one under test, which
// would leave the case proving only that.
const tfAttachmentWorkloadBlock = `  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      sidecar:
        image: test-sidecar:latest
        ports:
          web: 9090
`

// tfArtifactsBlock is the artifacts: block the fixtures declare — a standalone
// Terraform body with nothing templated, so it contributes an output without
// contributing a second reason for a build to fail.
const tfArtifactsBlock = `  artifacts:
    extra.tf: |
      resource "null_resource" "extra" {
        triggers = {
          always = "static"
        }
      }
`

// assertGateRefusal pins a halt to the input-kind gate's own registered identity
// and to the gate's own wording, and — the half that matters most — pins that the
// guest's internal refusal is absent.
//
// The wording is read off the condition through cerrors.MessageOf rather than
// spelled out here, so this asserts the sentence the product registers rather than
// a second copy of it that would keep passing after the registered one moved.
//
// The guest's phrase is asserted absent because a fixture written against it
// would pass on the defect: before the gate consulted every declared kind, this
// same corpus failed inside the guest, and an assertion on that text would pin the
// mis-routing rather than the refusal.
func assertGateRefusal(t *testing.T, err error, wantKind, wantSupports string) {
	t.Helper()
	if err == nil {
		t.Fatal("a node declaring an input kind its provider cannot honour must halt at the gate; it built instead")
	}
	t.Logf("emitted diagnostic:\n%v", err)
	for i, link := 0, err; link != nil; i, link = i+1, errors.Unwrap(link) {
		t.Logf("  chain[%d]: %v", i, link)
	}

	if !errors.Is(err, renderer.ErrUnsupportedInputKind()) {
		t.Fatalf("the halt must carry the input-kind gate's condition; got: %v", err)
	}
	sentence, ok := cerrors.MessageOf(renderer.ErrUnsupportedInputKind())
	if !ok || sentence == "" {
		t.Fatal("the gate's condition carries no registered sentence, so there is nothing to assert against")
	}
	if !chainContains(err, sentence) {
		t.Errorf("the halt does not state the condition's registered sentence %q; got: %v", sentence, err)
	}
	for _, want := range []string{`provider "terraform"`, "does not support " + wantKind + " recipes", "this provider supports: " + wantSupports} {
		if !chainContains(err, want) {
			t.Errorf("the halt does not carry %q; got: %v", want, err)
		}
	}
	if chainContains(err, "received a workload projection") {
		t.Errorf("the guest's internal refusal reached the operator, so the gate did not refuse first; got: %v", err)
	}
}

// TestBuild_WorkloadPlusArtifactsOnTerraformHaltsAtGate is the control: the kind
// the precedence names IS the unsupported one, so the gate already refuses it.
// It is here to make the next test's failure attributable — the two corpora
// declare the same unsupported workload and differ only in which other kind
// accompanies it.
func TestBuild_WorkloadPlusArtifactsOnTerraformHaltsAtGate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeTerraformGateWorkspace(t, root, tfWorkloadBlock+tfArtifactsBlock, "")

	err := renderInMemory(t, root, releasePath, catalogRoot)
	assertGateRefusal(t, err, "synthesised", "sourced, artifacts-only")
}

// TestBuild_SourcePlusWorkloadOnTerraformHaltsAtGate is the headline. The source
// is a kind terraform honours and the workload is one it does not, so a gate
// consulting only the kind the precedence names is satisfied and the build
// proceeds — handing the workload to a guest that cannot synthesise it. The
// author's mistake is the same one the control corpus makes, and it has to reach
// the same refusal.
func TestBuild_SourcePlusWorkloadOnTerraformHaltsAtGate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	moduleDir := filepath.Join(root, "module")
	writeTerraformModule(t, moduleDir)
	releasePath, catalogRoot := writeTerraformGateWorkspace(t, root,
		"  source: "+moduleDir+"\n"+tfWorkloadBlock, "")

	err := renderInMemory(t, root, releasePath, catalogRoot)
	assertGateRefusal(t, err, "synthesised", "sourced, artifacts-only")
}

// TestBuild_AttachmentSourcePlusWorkloadOnTerraformHaltsAtGate carries the same
// fault on the attachment path. One gate serves both render paths, so an
// attachment declaring a kind its provider cannot honour has to be refused in the
// same words a component's Recipe is — the component's own Recipe here declares
// only artifacts, so the halt reported can only be the attachment's.
func TestBuild_AttachmentSourcePlusWorkloadOnTerraformHaltsAtGate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	moduleDir := filepath.Join(root, "module")
	writeTerraformModule(t, moduleDir)
	releasePath, catalogRoot := writeTerraformGateWorkspace(t, root,
		tfArtifactsBlock,
		"  source: "+moduleDir+"\n"+tfAttachmentWorkloadBlock)

	err := renderInMemory(t, root, releasePath, catalogRoot)
	assertGateRefusal(t, err, "synthesised", "sourced, artifacts-only")
}

// TestBuild_SourcePlusArtifactsOnTerraformStillBuilds holds the gate to the
// refusal it exists for. Composition by addition is the authoring model: a node
// may declare several input kinds and their outputs are added together. This
// corpus declares two kinds terraform DOES honour, so it has to keep building —
// a gate that refused it would have refused a combination for being a
// combination, which is the one thing widening the gate must not do.
func TestBuild_SourcePlusArtifactsOnTerraformStillBuilds(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	moduleDir := filepath.Join(root, "module")
	writeTerraformModule(t, moduleDir)
	releasePath, catalogRoot := writeTerraformGateWorkspace(t, root,
		"  source: "+moduleDir+"\n"+tfArtifactsBlock, "")

	if err := renderInMemory(t, root, releasePath, catalogRoot); err != nil {
		t.Fatalf("a node declaring two kinds the provider honours must still build: %v", err)
	}
}

// TestBuild_SourcedOnlyOnTerraformStillBuilds is the single-kind boundary of the
// same line: the gate consulting every declared kind must not have changed what a
// node declaring exactly one supported kind does.
func TestBuild_SourcedOnlyOnTerraformStillBuilds(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	moduleDir := filepath.Join(root, "module")
	writeTerraformModule(t, moduleDir)
	releasePath, catalogRoot := writeTerraformGateWorkspace(t, root, "  source: "+moduleDir+"\n", "")

	if err := renderInMemory(t, root, releasePath, catalogRoot); err != nil {
		t.Fatalf("a sourced-only node on terraform must still build: %v", err)
	}
}

// TestBuild_TerraformGateRefusalNamesTheDeclaredKindNotTheClassifiedOne pins the
// distinction the widened gate rests on, in the emitted text rather than in the
// code: the refusal names the kind the provider cannot honour, which for a mixed
// node is NOT the kind the precedence classifies it as. A gate that reported the
// classified kind would tell the author their source was unsupported, which it is
// not, and send them to change the wrong line.
func TestBuild_TerraformGateRefusalNamesTheDeclaredKindNotTheClassifiedOne(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	moduleDir := filepath.Join(root, "module")
	writeTerraformModule(t, moduleDir)
	releasePath, catalogRoot := writeTerraformGateWorkspace(t, root,
		"  source: "+moduleDir+"\n"+tfWorkloadBlock, "")

	err := renderInMemory(t, root, releasePath, catalogRoot)
	if err == nil {
		t.Fatal("the mixed-kind corpus must halt")
	}
	msg := err.Error()
	if strings.Contains(msg, "does not support sourced recipes") {
		t.Errorf("the refusal names the classified kind rather than the unhonourable one: %v", err)
	}
	if !chainContains(err, "does not support synthesised recipes") {
		t.Errorf("the refusal must name the synthesised kind the provider cannot honour; got: %v", err)
	}
}
