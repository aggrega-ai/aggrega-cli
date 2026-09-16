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

// An author may name a `uses` socket the catalog did not declare. The compiler
// accepts it and records on each IR edge whether its socket was catalog-declared,
// which is what lets an operator who wants no free-form connections say so as a
// policy rather than relying on the compiler to hold an opinion it does not have.
//
// These tests prove that is real rather than nominal: a REAL policy guest
// evaluating operator-supplied Rego reads the per-edge marker off the wire and
// rejects a build for carrying a free-form connection. A payload assertion could
// only show that the field exists; it could not show a policy can be written
// against it.
//
// The deny is paired with a pass over a graph whose sockets are all declared, and
// the deny message quotes the marker value the guest read. Without the pair, a
// policy that rejected everything would satisfy the deny assertion; without the
// quoted value, a rule that fired for any other reason would too.
const (
	// socketDeclarationDenyPolicy is the "no free-form connections" rule an
	// operator would actually write.
	socketDeclarationDenyPolicy = `package aggrega.policy

import rego.v1

deny contains msg if {
	some e in input.graph.spec.edges
	e.socket_declaration == "ad-hoc"
	msg := sprintf("free-form connection: %v -> %v on socket %v is %v", [e.source, e.target, e.socket, e.socket_declaration])
}
`

	// socketDeclarationCensusPolicy denies on the CONTRASTING value, so a graph
	// whose sockets are all catalog-declared halts and the halt message carries
	// the value the guest read for each edge. This is what distinguishes "the
	// guest read the marker" from "the rule happened to fire".
	socketDeclarationCensusPolicy = `package aggrega.policy

import rego.v1

deny contains msg if {
	some e in input.graph.spec.edges
	e.socket_declaration == "declared"
	msg := sprintf("declared connection: %v on socket %v is %v", [e.source, e.socket, e.socket_declaration])
}
`
)

// loadAdHocSocketForSockets loads the sub-aggregate fixture whose flattened
// component authors one catalog-declared socket and one the catalog does not
// declare, so a single graph carries both marker values. The flattened
// sub-aggregate surface is deliberate: it is where the socket guards previously
// had a coverage gap, so a marker hole would surface here first.
func loadAdHocSocketForSockets(t *testing.T) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	const (
		dir           = "../testdata/uses-edge-subagg/"
		releasePath   = dir + "release-badsocket.yaml"
		manifestPath  = dir + "manifest.yaml"
		aggregatePath = dir + "aggregate-badsocket.yaml"
	)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", "../testdata/uses-edge-config/catalog/base", dir+"catalog/base")
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	return pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggBytes,
		AggregatePath:     aggregatePath,
		WorkspaceConfig:   fixtureWorkspaceConfig(),
		CompilerVersion:   fixtureCompilerVersion,
	}, cat.CatalogLookup(), cat.RecipeLookupWithManifest(manifest), cat.SubAggregateLoader()
}

// TestPolicyRejectsFreeFormConnectionE2E is the load-bearing proof: a real policy
// guest reads the per-edge declaration marker and rejects a build for carrying a
// connection on a socket the catalog did not declare.
func TestPolicyRejectsFreeFormConnectionE2E(t *testing.T) {
	_, err := runRegorusPolicyOn(t, loadAdHocSocketForSockets, socketDeclarationDenyPolicy)
	if err == nil {
		t.Fatalf("no-free-form-connections policy: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("halt not errors.Is ErrPolicyViolation: %v", err)
	}
	// The message is built from values the guest read off the wire, so its
	// presence is what proves the marker arrived rather than that a rule fired.
	for _, want := range []string{"free-form connection", "mod.bad", "nonexistent", "ad-hoc"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("deny message does not carry %q (so the marker may not have been read):\n%v", want, err)
		}
	}
	// The declared edge on the SAME component must not be reported: the rule is
	// keyed on the marker, so a message naming the declared socket would mean the
	// marker did not discriminate.
	if strings.Contains(err.Error(), "on socket databases") {
		t.Errorf("deny message also reports the catalog-declared socket, so the marker did not discriminate:\n%v", err)
	}
}

// TestPolicyAllowsDeclaredOnlyGraphE2E is the paired pass: the same
// no-free-form-connections rule over a graph whose sockets are all
// catalog-declared lets the build proceed. Without it, a guest that denied
// unconditionally would satisfy the deny test above.
func TestPolicyAllowsDeclaredOnlyGraphE2E(t *testing.T) {
	res, err := runRegorusPolicyOn(t, loadFullstackForSockets, socketDeclarationDenyPolicy)
	if err != nil {
		t.Fatalf("no-free-form-connections policy over an all-declared graph: unexpected halt: %v", err)
	}
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("build produced no artifacts")
	}
}

// TestPolicyReadsDeclaredMarkerE2E denies on the contrasting value over that same
// all-declared graph, so the pass above is attributable to the marker's value and
// not to the rule being unreachable for some unrelated reason.
func TestPolicyReadsDeclaredMarkerE2E(t *testing.T) {
	_, err := runRegorusPolicyOn(t, loadFullstackForSockets, socketDeclarationCensusPolicy)
	if err == nil {
		t.Fatalf("deny-on-declared policy: expected a halt, got nil (so the marker was not readable)")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("halt not errors.Is ErrPolicyViolation: %v", err)
	}
	for _, want := range []string{"declared connection", "is declared"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("deny message does not carry %q:\n%v", want, err)
		}
	}
}

// runRegorusPolicyOn attaches the real reference policy guest at the governance
// socket with the given Rego over whichever fixture the loader supplies, and runs
// the pipeline. It exists because the sibling regorus tests are hardwired to one
// fixture, and the marker proof needs two graphs that differ in exactly the
// marker's value.
func runRegorusPolicyOn(
	t *testing.T,
	load func(*testing.T) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader),
	rego string,
) (*pipeline.Result, error) {
	t.Helper()
	wasmtest.EnsureFixtureBuilt(t, "policy-regorus-rust.wasm")
	inputs, buildLookup, recipeLookup, subAgg := load(t)
	srcPath := filepath.Join(t.TempDir(), "policy.rego")
	if err := os.WriteFile(srcPath, []byte(rego), 0o644); err != nil {
		t.Fatalf("write rego: %v", err)
	}
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-regorus",
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{Sources: []string{srcPath}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-regorus": "policy-regorus-rust.wasm",
	})
	return runPipelineCompleted(context.Background(), inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
}
