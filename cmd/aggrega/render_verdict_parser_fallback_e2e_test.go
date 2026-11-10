// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// End-to-end coverage of the parser a build reads its Terraform output through for
// a policy attached at the render-verdict role, where the workspace's catalog holds
// no parser: the build parses the file through the HCL parser the binary carries,
// and the policy reads the parsed tree. The embedded Rego engine is attached the
// way the other embedded-guest proofs in this package attach it.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// parserlessTerraformWorkspace authors a Terraform workspace whose catalog holds
// one Component, its Recipe — writing one resource block in a .tf file — the
// binary's own Terraform provider entity and the Rego engine's Plugin entity, and
// no parser, with policy as the Rego source beside the workspace configuration.
// It returns the Release path and the catalog directory.
func parserlessTerraformWorkspace(t *testing.T, root, policy string) (releasePath, catalogDir string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "policy.rego"), policy)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n"+
		"metadata:\n  name: aggregate\n  version: \"1.0.0\"\nspec:\n  components:\n    gateway:\n      type: gateway\n")
	writeFixture(t, filepath.Join(root, "release.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Release\n"+
		"metadata:\n  name: parserless\n  environment: testing\n  version: \"1.0.0\"\nspec:\n"+
		"  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n"+
		"  boundaries:\n    main:\n      directory: ./dist/main\n      components:\n        gateway: {}\n")
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\n"+
		"metadata:\n  name: manifest\n  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n"+
		"        gateway: gateway\n")
	writeFixture(t, filepath.Join(catalogDir, "components", "gateway.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Component\nmetadata:\n  name: gateway\n  version: \"1.0.0\"\n  labels:\n"+
		"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
	writeFixture(t, filepath.Join(catalogDir, "recipes", "gateway", "recipe.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Recipe\nmetadata:\n  name: gateway\n  version: \"1.0.0\"\nspec:\n  capability: terraform\n"+
		"  implements: gateway\n  artifacts:\n    main.tf: |\n"+
		"      resource \"terraform_data\" \"gateway\" {\n        input = \"gateway\"\n      }\n")
	seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/terraform")
	writeRegoPluginEntity(t, catalogDir)
	return filepath.Join(root, "release.yaml"), catalogDir
}

// TestABuildWhoseCatalogHoldsNoParserReadsTerraformThroughTheBinarysParser pulls
// and builds a workspace whose catalog holds no parser, with the Rego engine at
// the render-verdict role over its Terraform output, twice: once with a policy
// refusing the resource block it reads from the file's parsed documents, and once
// with a policy that matches nothing.
//
// The first build halts with the policy's own refusal naming the block, which only
// a policy handed the file's parsed tree can give; the second completes. Neither
// halts for want of a parser.
//
// 🔴 PLANTED DEFECT: in parserSeam (internal/compiler/pipeline/wasm_sockets.go),
// bind the resolver to the catalog's lookup alone, and both builds halt with no
// parser plugin advertising "parse:hcl".
func TestABuildWhoseCatalogHoldsNoParserReadsTerraformThroughTheBinarysParser(t *testing.T) {
	nonInteractive(t)
	const refusing = `package proof

import rego.v1

deny contains msg if {
	some artifact in input.artifacts
	some doc in artifact.documents
	some name, _ in doc.resource.terraform_data
	msg := sprintf("the parsed tree declares terraform_data %v", [name])
}
`
	const matchingNothing = `package proof

import rego.v1

deny contains msg if {
	some artifact in input.artifacts
	some doc in artifact.documents
	some name, _ in doc.resource.never_rendered
	msg := sprintf("found %v", [name])
}
`
	for _, row := range []struct {
		name    string
		policy  string
		wantMsg string
	}{
		{name: "a policy refusing the parsed resource block", policy: refusing,
			wantMsg: "the parsed tree declares terraform_data gateway"},
		{name: "a policy matching nothing", policy: matchingNothing},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			releasePath, catalogDir := parserlessTerraformWorkspace(t, root, row.policy)
			err := pullThen(t, root, releasePath, []string{catalogDir},
				regoAttachment("render-verdict", "policy.rego", ""), "dist")
			if errors.Is(err, catalog.ErrNoParserForFormat()) {
				t.Fatalf("the build halted for want of a parser, the binary's not standing behind the catalog: %v", err)
			}
			if row.wantMsg == "" {
				if err != nil {
					t.Fatalf("a policy matching nothing must leave the build standing: %v", err)
				}
				return
			}
			if !errors.Is(err, analyzer.ErrPolicyViolation()) {
				t.Fatalf("want the policy's refusal, got: %v", err)
			}
			if !strings.Contains(err.Error(), row.wantMsg) {
				t.Fatalf("the refusal must carry the policy's message read from the parsed tree, %q; got: %v", row.wantMsg, err)
			}
		})
	}
}
