// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// questionRecordingPuller is a registry holding nothing that records every
// question put to it — an entity fetch by kind, and a located pull — so a test can
// assert that a resolution asked a registry nothing at all. It answers every
// question with not-found, which is what lets a resolution that does ask carry on
// to the tiers below and still be caught by the record.
type questionRecordingPuller struct {
	mu    sync.Mutex
	asked []string
}

func (p *questionRecordingPuller) Pull(_ context.Context, reference, tag string) (*oci.PullResult, error) {
	p.record("pull " + reference + ":" + tag)
	return nil, oci.ErrFetchNotFound()
}

func (p *questionRecordingPuller) PullEntity(_ context.Context, identity, kind, floorVersion string) (*oci.PullResult, string, error) {
	p.record(kind + " " + identity + ":" + floorVersion)
	return nil, "", oci.ErrFetchNotFound()
}

func (p *questionRecordingPuller) record(question string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, question)
}

func (p *questionRecordingPuller) questions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
}

const childComponentDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/child
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: "test/base"
  schema: inherited
`

const baseComponentDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/base
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      size: { type: string, default: "1Gi" }
`

// TestTheMappingCheckAsksNoRegistryForAnAncestor holds the Manifest's
// mapping-table check to what is on this machine. The view is built with a
// puller — the shape a pull opens it in — and the mapping names a Recipe
// written against a base the mapped type extends, so the check walks the mapped
// type's extends chain. That walk reads the view's local copies and never a
// registry: the check runs before a pull reaches out, so a base only a registry
// could supply leaves the chain unwalkable rather than fetched.
func TestTheMappingCheckAsksNoRegistryForAnAncestor(t *testing.T) {
	cases := []struct {
		name           string
		baseHeldLocal  bool
		wantConsistent bool
	}{
		// Positive control: the chain is walkable locally, so the mapping onto the
		// base's Recipe is consistent — the walk was taken, and took nothing remote.
		{name: "base held in the catalog directory", baseHeldLocal: true, wantConsistent: true},
		{name: "base only a registry could serve", baseHeldLocal: false, wantConsistent: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			dir := filepath.Join(ws, "catalog")
			writeCatalogEntity(t, dir, "component/test/child/1.0.0/component.yaml", childComponentDoc)
			if tc.baseHeldLocal {
				writeCatalogEntity(t, dir, "component/test/base/1.0.0/component.yaml", baseComponentDoc)
			}
			writeCatalogEntity(t, dir, "recipe/probe/base-recipe/1.0.0/recipe.yaml", recipeYAML("base-recipe", "test/base"))

			puller := &questionRecordingPuller{}
			view := NewLazyLookup(ws, ".aggrega/cache", []string{dir}, puller)
			mapping := &types.MappingTier{}
			mapping.Type.Set("test/child:1.0.0", "probe/base-recipe:1.0.0")
			manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: mapping}}}

			err := view.AssertManifestImplementsConsistency(manifest, "manifest.yaml")
			if tc.wantConsistent && err != nil {
				t.Fatalf("a mapping onto a locally held base's Recipe must be consistent: %v", err)
			}
			if !tc.wantConsistent && err == nil {
				t.Fatal("a mapping onto a base no local copy holds was judged consistent; the check found the base somewhere other than this machine")
			}
			if asked := puller.questions(); len(asked) != 0 {
				t.Errorf("the mapping-table check asked a registry %v; it reads only what is on this machine", asked)
			}
		})
	}
}

// TestTheClosureProbeAsksNoRegistryForALocalEntity holds the closure lookup's
// Component and Feature probe to the view's local copies. A reference a catalog
// directory holds as another kind — a Recipe here, asked for with the kind hint
// a Manifest mapping value carries — is put to the probe first, as a Component
// and then as a Feature. Were the probe to read a registry, a pull would ask it
// about a local Recipe under two kinds it is not before the lookup found it
// where it is, and a registry serving either kind under that identity would be
// taken in place of the local Recipe.
func TestTheClosureProbeAsksNoRegistryForALocalEntity(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "catalog")
	writeCatalogEntity(t, dir, "recipe/probe/svc/1.0.0/recipe.yaml", recipeYAML("svc", "test/svc"))

	puller := &questionRecordingPuller{}
	view := NewLazyLookup(ws, ".aggrega/cache", []string{dir}, puller)
	lookup := view.ClosureLookup(puller, ws, ".aggrega/cache", nil, nil, nil)

	res, err := lookup.ResolveCatalogEntity(context.Background(), "probe/svc:1.0.0", "recipe")
	if err != nil {
		t.Fatalf("resolve the local Recipe: %v", err)
	}
	if res.NotFound || res.ResolutionPath != closure.ResolutionPathCatalog {
		t.Fatalf("the local Recipe resolved as %+v; want it found at the catalog tier", res)
	}
	if asked := puller.questions(); len(asked) != 0 {
		t.Errorf("resolving a Recipe a catalog directory holds asked a registry %v; nothing a local tier holds is asked of a registry", asked)
	}
}

// TestTheRefusalRecordTellsAShadowFromARefusal holds the view's refusal record
// to the whole-catalog load's reading of the same files, which is what a failure
// annotation reading the record needs. A file an earlier catalog directory
// shadows is no refusal — its identity is held — while two files of one directory
// claiming one identity, a document no walk can index, and a Component refused on
// its read are each recorded with the refusal that load gives the file.
func TestTheRefusalRecordTellsAShadowFromARefusal(t *testing.T) {
	type layout struct {
		dirs  []string          // catalog directories, in precedence order, relative to the workspace
		files map[string]string // workspace-relative path -> document
	}
	cases := []struct {
		name   string
		layout layout
		read   string // a Component URI to read through the cascade first, or ""
		// subject is the workspace-relative file whose record the row asserts.
		subject    string
		wantRecord bool
		check      func(t *testing.T, reason error)
	}{
		{
			name: "a file an earlier catalog directory shadows is not a refusal",
			layout: layout{dirs: []string{"a", "b"}, files: map[string]string{
				"a/recipe.yaml": recipeYAML("svc", "test/svc"),
				"b/recipe.yaml": recipeYAML("svc", "test/svc"),
			}},
			subject:    "b/recipe.yaml",
			wantRecord: false,
		},
		{
			name: "two files of one directory are the load's duplicate refusal",
			layout: layout{dirs: []string{"a"}, files: map[string]string{
				"a/a.yaml": recipeYAML("svc", "test/svc"),
				"a/b.yaml": recipeYAML("svc", "test/svc"),
			}},
			subject:    "a/b.yaml",
			wantRecord: true,
			check: func(t *testing.T, reason error) {
				if !errors.Is(reason, ErrDuplicateEntityIdentity()) {
					t.Errorf("the within-directory duplicate is recorded as %v; want the duplicate-identity condition the whole-catalog load raises", reason)
				}
			},
		},
		{
			name: "a document no walk can index is recorded with the load's refusal",
			layout: layout{dirs: []string{"a"}, files: map[string]string{
				"a/broken.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: test/broken\n  version: \"1.0.0\"\nspec: not-a-mapping\n",
			}},
			subject:    "a/broken.yaml",
			wantRecord: true,
			check: func(t *testing.T, reason error) {
				if chain := cerrors.CauseChain(reason); !strings.Contains(chain, "unmarshal Component") {
					t.Errorf("the unindexable document is recorded as %q; want the refusal the whole-catalog load gives it", chain)
				}
			},
		},
		{
			// Negative control for the row above: a YAML file that is no catalog entity
			// at all is outside the load's refusals, however its header reads.
			name: "a file that is no catalog entity is not recorded",
			layout: layout{dirs: []string{"a"}, files: map[string]string{
				"a/values.yaml": "kind: Deployment\nspec: not-a-mapping\n",
			}},
			subject:    "a/values.yaml",
			wantRecord: false,
		},
		{
			name: "a Component refused on its read carries the load's frame",
			layout: layout{dirs: []string{"a"}, files: map[string]string{
				"a/refused.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: test/refused\n  version: \"1.0.0\"\nspec:\n  schema: {}\n",
			}},
			read:       "test/refused:1.0.0",
			subject:    "a/refused.yaml",
			wantRecord: true,
			check: func(t *testing.T, reason error) {
				if chain := cerrors.CauseChain(reason); !strings.Contains(chain, "unmarshal Component") {
					t.Errorf("the refused Component is recorded as %q; want the refusal the whole-catalog load gives it", chain)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			for rel, body := range tc.layout.files {
				path := filepath.Join(ws, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatalf("write %s: %v", rel, err)
				}
			}
			dirs := make([]string, 0, len(tc.layout.dirs))
			for _, d := range tc.layout.dirs {
				dirs = append(dirs, filepath.Join(ws, d))
			}
			view := NewLazyLookup(ws, ".aggrega/cache", dirs, nil)
			if tc.read != "" {
				if _, ok := view.Component(tc.read); ok {
					t.Fatalf("fixture no longer exercises the class: %s resolved", tc.read)
				}
			}
			reason, recorded := view.Skips()[filepath.Join(ws, tc.subject)]
			if recorded != tc.wantRecord {
				t.Fatalf("%s recorded = %v (reason %v); want %v", tc.subject, recorded, reason, tc.wantRecord)
			}
			if tc.check != nil {
				tc.check(t, reason)
			}
		})
	}

	// The shadow row's identity is held — by the earlier directory's file — which is
	// what makes it no refusal; a record saying otherwise would tell an author an
	// entity the catalog holds is missing.
	ws := t.TempDir()
	for _, d := range []string{"a", "b"} {
		writeCatalogEntity(t, filepath.Join(ws, d), "recipe.yaml", recipeYAML("svc", "test/svc"))
	}
	view := NewLazyLookup(ws, ".aggrega/cache", []string{filepath.Join(ws, "a"), filepath.Join(ws, "b")}, nil)
	if _, ok := view.RecipeByURI("probe/svc:1.0.0"); !ok {
		t.Error("the shadowed identity must still be held, by the earlier directory's file")
	}
}
