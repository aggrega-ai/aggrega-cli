// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive a Terraform module fetched from a git repository at a release
// tag through RenderAll and the embedded terraform provider guest: the repository
// archive is seeded in the cache as a pull leaves it, pinned at the commit the tag
// resolved to, and a second component consumes one of the module's outputs across
// a uses edge. This is the shape of a module a registry publishes, sourced from the
// repository the registry itself serves it from.

const (
	// tfGitURL is the repository the module is fetched from.
	tfGitURL = "https://example.com/modules/terraform-example-db.git"
	// tfGitRef is the release tag the Recipe authors, and tfGitCommit the commit a
	// pull pinned it at.
	tfGitRef    = "v1.2.3"
	tfGitCommit = "89abcdef0123456789abcdef0123456789abcdef"
)

// tfGitModuleArchive is the repository as archived at the pinned commit: the
// module at the repository root, a child module the root calls by a local path,
// and an example directory calling a registry module, which the engine never
// loads when the module is called. Every file is vendored, the example included,
// because a fetched source is the archive it was fetched as.
func tfGitModuleArchive() map[string]string {
	return map[string]string{
		"main.tf": `module "instance" {
  source     = "./modules/instance"
  identifier = var.identifier
}
`,
		"variables.tf": `variable "identifier" {
  type = string
}
`,
		"outputs.tf": `output "db_instance_address" {
  value = module.instance.address
}
`,
		"modules/instance/main.tf": `variable "identifier" {
  type = string
}

output "address" {
  value = "${var.identifier}.db.example.internal"
}
`,
		"examples/complete/main.tf": `module "vpc" {
  source  = "example-org/vpc/aws"
  version = "~> 6.0"
}

module "db" {
  source     = "../../"
  identifier = "example"
}
`,
	}
}

// tfGitConsumerModule is the consumer's module, kept beside its Recipe: one input
// carrying the producer's address and one output returning it.
func tfGitConsumerModule() map[string]string {
	return map[string]string{
		"main.tf": `variable "db_host" {
  type = string
}

output "seen_db_host" {
  value = var.db_host
}
`,
	}
}

// renderTerraformGitSource renders the producer and its consumer and returns what
// the build assembled. alter flips one byte of the seeded archive after its digest
// is recorded in the lockfile row and the cache metadata, which is what a cache
// whose bytes no longer match the pull looks like to a build.
func renderTerraformGitSource(t *testing.T, alter bool) (map[string]renderer.AssembledFile, error) {
	t.Helper()
	root := t.TempDir()
	blob := buildIntegrationTGZ(t, tfGitModuleArchive())
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "git-repository", tfGitURL, tfGitCommit, blob, digest)
	if alter {
		slot, err := sourcecache.SlotPath(root, ".aggrega/cache", "git-repository", tfGitURL, "", "", tfGitCommit, digest)
		if err != nil {
			t.Fatalf("slot: %v", err)
		}
		altered := bytes.Clone(blob)
		altered[len(altered)-1] ^= 0xFF
		if err := os.WriteFile(filepath.Join(slot, "blob.tgz"), altered, 0o644); err != nil {
			t.Fatalf("alter the seeded archive: %v", err)
		}
	}
	consumerDir := writeTerraformModule(t, tfGitConsumerModule())

	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "resource")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/storage/db", Labels: labels, ResolvedVersion: "1.0.0", BoundaryPath: "local/default"})
	nodes.Set("api", types.IRNode{Component: "core/compute/report-api", Labels: labels, ResolvedVersion: "1.0.0", BoundaryPath: "local/default"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("db", json.RawMessage(`{"identifier":"reports-db"}`))
	valuesMapping.Set("api", json.RawMessage(`{}`))

	// The consumer's edge to the producer carries the producer's published host
	// into the consumer's own inject pipe, which is what its values mapping reads.
	env := types.OrderedMap[string]{}
	env.Set("DB_HOST", "host")
	inject := types.OrderedMap[types.OrderedMap[string]]{}
	inject.Set("env", env)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test-app"},
			Spec: types.IRSpec{
				Nodes: nodes,
				Edges: []types.IREdge{{
					Source:            "api",
					Target:            "db",
					Socket:            "database",
					Alias:             "db",
					SocketDeclaration: types.SocketDeclarationAdHoc,
					Inject:            inject,
				}},
				ValuesMapping: valuesMapping,
				InfraMapping:  types.OrderedMap[json.RawMessage]{},
				Provenance:    types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		Lockfile: &lockfile.Lockfile{
			Version:     1,
			Release:     "./r.yaml",
			GeneratedAt: "2026-05-22T10:00:00Z",
			SourceArtifacts: []lockfile.SourceArtifact{{
				Kind:          "git-repository",
				URL:           tfGitURL,
				PinnedVersion: tfGitCommit,
				Digest:        digest,
				Mediatype:     "application/gzip",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "recipe", URI: "db-module:1.0.0", Field: "spec.source", RequestedRef: tfGitRef},
				},
			}},
		},
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	}

	producerProvides := types.OrderedMap[string]{}
	producerProvides.Set("host", "${module.{{ .context.instance_id }}.db_instance_address}")
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			switch componentType {
			case "core/storage/db":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "db-module", Version: "1.0.0"},
					Spec: types.RecipeSpec{
						Capability:    "terraform",
						Implements:    "core/storage/db",
						Source:        map[string]any{"kind": "git-repository", "url": tfGitURL, "ref": tfGitRef},
						ValuesMapping: types.ValuesMapping{Inline: "identifier: \"{{ .values.identifier }}\"\n"},
						Provides:      producerProvides,
					},
				}, true
			case "core/compute/report-api":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "report-api", Version: "1.0.0"},
					Spec: types.RecipeSpec{
						Capability:    "terraform",
						Implements:    "core/compute/report-api",
						Source:        consumerDir,
						ValuesMapping: types.ValuesMapping{Inline: "db_host: \"{{ .context.inject.env.DB_HOST }}\"\n"},
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		return nil, err
	}
	return result.Files, nil
}

// TestRenderAll_TerraformGitRepositorySourceVendorsTheModuleAndWiresItsOutput is the
// path end to end: every file of the fetched archive is vendored byte-for-byte in
// the producer's own directory — the example directory included — the producer's
// module block carries its mapped input, and the consumer's module block carries
// the producer's published address as a reference to the producer's module call,
// which is the scope the engine resolves it in. Two renders are byte-identical.
func TestRenderAll_TerraformGitRepositorySourceVendorsTheModuleAndWiresItsOutput(t *testing.T) {
	t.Parallel()

	files, err := renderTerraformGitSource(t, false)
	if err != nil {
		t.Fatalf("RenderAll: %s", cerrors.CauseChain(err))
	}

	for rel, want := range tfGitModuleArchive() {
		got := findBySuffix(t, files, "/modules/db/"+rel)
		if !bytes.Equal(got, []byte(want)) {
			t.Errorf("archived file %q not vendored verbatim:\n got: %q\nwant: %q", rel, got, want)
		}
	}

	root := string(findBySuffix(t, files, "aggrega.modules.tf"))
	for _, want := range []string{
		"module \"api\" {\n  source = \"./modules/api\"\n\n  db_host = \"${module.db.db_instance_address}\"\n}\n",
		"module \"db\" {\n  source = \"./modules/db\"\n\n  identifier = \"reports-db\"\n}\n",
	} {
		if !strings.Contains(root, want) {
			t.Errorf("the root configuration does not carry the block:\n%s\ngot:\n%s", want, root)
		}
	}

	again, err := renderTerraformGitSource(t, false)
	if err != nil {
		t.Fatalf("RenderAll (second run): %s", cerrors.CauseChain(err))
	}
	if len(again) != len(files) {
		t.Fatalf("second run emitted %d files, first emitted %d", len(again), len(files))
	}
	for k, v := range files {
		if !bytes.Equal(again[k].Content, v.Content) {
			t.Errorf("non-deterministic output for %q across two runs", k)
		}
	}
}

// TestRenderAll_TerraformGitRepositorySourceAlteredArchiveHalts holds the build to
// the digest the pull recorded: an archive in the cache whose bytes no longer match
// it halts the render as a fetch failure naming the mismatch, rather than vendoring
// bytes nothing verified.
func TestRenderAll_TerraformGitRepositorySourceAlteredArchiveHalts(t *testing.T) {
	t.Parallel()

	_, err := renderTerraformGitSource(t, true)
	if err == nil {
		t.Fatal("RenderAll rendered from an archive whose bytes do not match the recorded digest")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("the halt is not the integrity failure: %s", cerrors.CauseChain(err))
	}
	var compilerErr *cerrors.CompilerError
	if !errors.As(err, &compilerErr) || compilerErr.Category() != cerrors.FetchError {
		t.Errorf("the halt is not a FETCH_ERROR compiler error: %T %v", err, err)
	}
	if chain := cerrors.CauseChain(err); !strings.Contains(chain, "mismatches lockfile digest") {
		t.Errorf("the halt does not name the digest mismatch:\n%s", chain)
	}
}
