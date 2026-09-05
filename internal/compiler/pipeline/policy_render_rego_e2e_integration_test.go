// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The catalog-only example renders a real Docker Compose file (compose = YAML,
// a non-Kubernetes output shape) from standard-library catalog entities. Its
// two services — db (image mariadb:11.4) and web (image adminer:4) — both carry
// bare image references with no registry prefix, so a policy requiring an
// approved-registry prefix rejects the render, while a policy requiring only
// that a service declares an image accepts it.
const (
	// catalogOnlyDir is the standard-library compose example; hubCatalogPath is
	// the standard-library catalog it resolves against, exactly as the example's
	// documented build command (`aggrega build --catalog <hub>`) loads it.
	catalogOnlyDir = "../../../../aggrega-docs/language-reference/examples/catalog-only"
	hubCatalogPath = "../../../../aggrega-hub/aggrega-catalog"

	// regorusRenderRegistryPolicy denies any Compose service whose image lacks a
	// registry prefix (a '/'). Both rendered services carry bare images, so this
	// fires over the real compose render and halts the build.
	regorusRenderRegistryPolicy = `package aggrega.render

import rego.v1

deny contains msg if {
	some artifact in input.artifacts
	some document in artifact.documents
	some name, service in document.services
	not contains(service.image, "/")
	msg := sprintf("service %v image %v is not from an approved registry", [name, service.image])
}
`

	// regorusRenderImagePresentPolicy denies only a Compose service that declares
	// no image. Both rendered services declare one, so the deny set stays empty
	// and the build proceeds — the compliant path over the real compose render.
	regorusRenderImagePresentPolicy = `package aggrega.render

import rego.v1

deny contains msg if {
	some artifact in input.artifacts
	some document in artifact.documents
	some name, service in document.services
	not service.image
	msg := sprintf("service %v is missing an image", [name])
}
`
)

// loadComposeExampleForSockets loads the catalog-only compose example against
// the standard-library catalog, returning the inputs and lookups RunPipeline
// consumes. It mirrors the fullstack socket loader but points at the compose
// example so the render-stage policy runs over a real docker-compose.yaml
// (a non-Kubernetes, YAML output shape) rather than Kubernetes manifests.
//
// The loaded view is marked for embedded core-provider resolution, which is what
// makes it faithful to the build command it stands in for. A real build resolves
// through the lazy backing, which resolves core providers from the binary's own
// embedded entity set; the eager whole-catalog view this loader uses does not do
// that on its own. Without the mark, a core provider's entity comes from the
// published catalog while its guest bytes come from this binary — two different
// builds — so a contract the guest reports and the published entity does not yet
// declare reads as a disagreement that no real build can produce. The mark pairs
// each embedded core provider's entity with the guest bytes it was generated
// beside, which is the pairing a build actually loads. A third-party provider the
// binary does not embed is untouched and still resolves from the catalog, so the
// example keeps exercising the real standard-library entities it names.
func loadComposeExampleForSockets(t *testing.T) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	rel, err := resolver.LoadRelease(filepath.Join(catalogOnlyDir, "release.yaml"))
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(filepath.Join(catalogOnlyDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggPath := filepath.Join(catalogOnlyDir, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", hubCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	cat.UseEmbeddedProviderResolution()
	inputs := pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggBytes,
		AggregatePath:     aggPath,
		WorkspaceConfig:   workspaceconfig.Default(),
		CompilerVersion:   fixtureCompilerVersion,
	}
	return inputs, cat.CatalogLookup(), cat.RecipeLookupWithManifest(manifest), cat.SubAggregateLoader()
}

// regorusRenderPolicyInputs clones the compose-example inputs and attaches the
// real render-stage embedded-Rego adapter at the policy socket, writing the
// given Rego to a temp file and naming it as the attachment's source. The guest
// is built on demand from its own source under its pinned toolchain; the
// artifact is not committed.
func regorusRenderPolicyInputs(t *testing.T, rego string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadComposeExampleForSockets(t)
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "policy.rego")
	if err := os.WriteFile(srcPath, []byte(rego), 0o644); err != nil {
		t.Fatalf("write rego: %v", err)
	}
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-regorus-render",
		Role: plugintype.RoleRenderVerdict,
		With: &workspaceconfig.PluginRefWith{Sources: []string{srcPath}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-regorus-render": "policy-regorus-render-rust.wasm",
	})
	return inputs, buildLookup, recipeLookup, subAgg
}

// runRegorusRenderPolicy builds the guest, attaches it, and runs the pipeline.
// It builds the guest first so a toolchain-absent skip lands cleanly before the
// pipeline runs.
func runRegorusRenderPolicy(t *testing.T, rego string) (*pipeline.Result, error) {
	t.Helper()
	wasmtest.EnsureFixtureBuilt(t, "policy-regorus-render-rust.wasm")
	inputs, buildLookup, recipeLookup, subAgg := regorusRenderPolicyInputs(t, rego)
	return pipeline.RunPipeline(context.Background(), inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
}

// TestRegorusRenderPolicyDenyHaltsE2E proves the adapter evaluates operator Rego
// over a real, non-Kubernetes compose render and halts the build when the policy
// rejects: a policy requiring an approved-registry image prefix rejects both
// rendered services (bare mariadb/adminer images), which halts loudly carrying
// ErrPolicyViolation and a message naming a real rendered service and its image
// — proving the adapter parsed the actual compose output, not an empty payload.
func TestRegorusRenderPolicyDenyHaltsE2E(t *testing.T) {
	_, err := runRegorusRenderPolicy(t, regorusRenderRegistryPolicy)
	if err == nil {
		t.Fatalf("violating compose render: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("violating halt not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "is not from an approved registry") {
		t.Errorf("violating halt does not carry the policy deny message:\n%v", msg)
	}
	// The message must prove the policy evaluated the real parsed compose output:
	// the deny interpolates a real service name and its rendered image.
	if !strings.Contains(msg, "mariadb") && !strings.Contains(msg, "adminer") {
		t.Errorf("violating halt does not name a real rendered service image (mariadb/adminer):\n%v", msg)
	}
}

// TestRegorusRenderPolicyCompliantProceedsE2E proves a compliant policy — one
// whose deny guard never holds over the real compose render — passes: the build
// completes and produces artifacts, and a second build of identical input is
// byte-identical (the policy emits no verdict and does not perturb output).
func TestRegorusRenderPolicyCompliantProceedsE2E(t *testing.T) {
	res, err := runRegorusRenderPolicy(t, regorusRenderImagePresentPolicy)
	if err != nil {
		t.Fatalf("compliant compose render: unexpected halt: %v", err)
	}
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("compliant compose render produced no artifacts")
	}
	// Prove the render is really the compose output the policy reasoned over.
	var sawCompose bool
	for path := range res.Artifacts {
		if strings.HasSuffix(path, "compose/docker-compose.yaml") {
			sawCompose = true
		}
	}
	if !sawCompose {
		t.Fatalf("compliant build did not render a docker-compose.yaml; artifacts: %v", artifactPaths(res.Artifacts))
	}

	res2, err := runRegorusRenderPolicy(t, regorusRenderImagePresentPolicy)
	if err != nil {
		t.Fatalf("compliant compose render (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// artifactPaths returns the artifact paths for a diagnostic message.
func artifactPaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	return paths
}
