// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// The subjects of the provenance table. Each is an entity a closure walk resolves
// by URI, and each has exactly one true answer to "where did this come from",
// whichever lookup construction is asked.
const (
	provenanceAuthoredPluginYAML = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: authored-step
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1beta1
  exports:
    - name: step
      operates: patch
      scope: own
`
	provenanceColocatedPluginYAML = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: colocated-step
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1beta1
  exports:
    - name: step
      operates: patch
      scope: own
`
	provenancePulledPluginYAML = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: pulled-step
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1beta1
  exports:
    - name: step
      operates: patch
      scope: own
`
	provenanceAuthoredFeatureRecipeYAML = `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: authored-secret
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/secret-value"
`
	provenanceAuthoredRecipeYAML = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: authored-db
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/datastore/mariadb"
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: demo:1
`
	provenanceUnpinnedRecipeYAML = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: unpinned-db
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/datastore/mariadb"
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: demo:1
`
	provenanceAuthoredAggregateYAML = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: example/authored-app
  version: "1.0.0"
spec: {}
`
	provenanceAuthoredSchemaLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: AuthoredSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`
	provenanceAuthoredMappingLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: AuthoredMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
`
	provenanceAuthoredWorkloadLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: WorkloadLibrary
metadata:
  name: AuthoredWorkloads
  version: 1.0.0
spec:
  workloads:
    SimpleService: 'kind: Deployment'
`
	provenanceAuthoredArtifactLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: ArtifactLibrary
metadata:
  name: AuthoredArtifacts
  version: 1.0.0
spec:
  artifacts:
    WidgetCRD:
      widget-config.yaml: "kind: ConfigMap"
`
	provenancePinnedArtifactLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: ArtifactLibrary
metadata:
  name: PinnedArtifacts
  version: 1.0.0
spec:
  artifacts:
    WidgetCRD:
      widget-config.yaml: "kind: ConfigMap"
`
)

// provenanceConstruction is one way of building the closure lookup a walk asks.
// addressesArtifactPins says whether the lookup carries the prior pins a cached .wasm
// is addressed by: its slot is keyed by the content digest only a pin names, so a
// lookup without them cannot read it.
type provenanceConstruction struct {
	name                  string
	addressesArtifactPins bool
	build                 func(t *testing.T) closure.Lookup
}

// provenanceSubject is one entity the table asks about. resolve asks the lookup the
// question a closure walk asks for that kind; wantDigest, when set, is the content
// digest a cache-served answer must carry; artifact marks the cached .wasm, the one
// subject some constructions cannot address.
type provenanceSubject struct {
	name       string
	want       string
	wantDigest string
	artifact   bool
	resolve    func(ctx context.Context, lookup closure.Lookup) (closure.Resolution, error)
}

// TestClosureLookupProvenanceAgreesAcrossConstructions is the table the pin
// decision rests on: every construction of the closure lookup reports the same
// resolution path for the same subject.
//
// The pull resolves through the eager catalog, which never holds a cached entity
// and so reaches one through its cache tier; the build resolves through the lazy
// view, which folds a lockfile-pinned cache slot into the same index its authored
// entities live in. A pulled entity therefore arrives at the two sides by
// different routes, and the pull-time rule that a catalog-authored plugin is not
// pinned keys on the path each route reports. Where the two disagree, a build's
// reconstruction drops what the pull pinned and refuses the lockfile the pull
// just wrote — so the agreement is asserted here, per subject and per
// construction, rather than left to the two routes happening to match.
//
// The subjects are, for every kind the lazy view's fold places — Plugin, Recipe,
// FeatureRecipe, Aggregate, and the schema, mapping, workload and artifacts
// libraries — one authored in a catalog directory and one folded from a pinned cache
// slot; a plugin authored with its artifact beside it; a core provider carried inside
// the binary; a cached entity no pin names; and a cached plugin .wasm.
//
// Every cell is asserted, and two groups of cells reach their answer by a route other
// than the one the subject is named for, by construction:
//
//   - A pinned-fold subject asked of the eager catalog, or of a lazy view carrying no
//     pins, is not folded — the eager catalog folds no pinned slot, and a view with no
//     pins has nothing to fold — so it is reached through the cache tier and reported
//     as cache-served by that tier. The fold's own answer is what the lazy
//     constructions carrying pins assert, and restoring a hard-coded catalog answer
//     for a folded entry fails every pinned-fold cell of those constructions while
//     leaving these unchanged.
//   - A cached plugin .wasm sits in a slot keyed by the content digest only its pin
//     names, so a construction whose lookup carries no prior pins cannot address it
//     and reports it not found. That is asserted as the cell's answer rather than
//     skipped.
func TestClosureLookupProvenanceAgreesAcrossConstructions(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	dir := filepath.Join(ws, "catalog")

	writeProvenanceFile(t, filepath.Join(dir, "plugin", "authored-step", "plugin.yaml"), []byte(provenanceAuthoredPluginYAML))
	writeProvenanceFile(t, filepath.Join(dir, "plugin", "colocated-step", "plugin.yaml"), []byte(provenanceColocatedPluginYAML))
	writeProvenanceFile(t, filepath.Join(dir, "plugin", "colocated-step", "colocated-step.wasm"), []byte("\x00asm\x01\x00\x00\x00"))
	writeProvenanceFile(t, filepath.Join(dir, "feature-recipe", "compose", "authored-secret", "recipe.yaml"), []byte(provenanceAuthoredFeatureRecipeYAML))
	writeProvenanceFile(t, filepath.Join(dir, "recipe", "compose", "authored-db", "recipe.yaml"), []byte(provenanceAuthoredRecipeYAML))
	writeProvenanceFile(t, filepath.Join(dir, "aggregate", "authored-app", "aggregate.yaml"), []byte(provenanceAuthoredAggregateYAML))
	writeProvenanceFile(t, filepath.Join(dir, "library", "authored-sizing", "library.yaml"), []byte(provenanceAuthoredSchemaLibYAML))
	writeProvenanceFile(t, filepath.Join(dir, "library", "authored-mappings", "library.yaml"), []byte(provenanceAuthoredMappingLibYAML))
	writeProvenanceFile(t, filepath.Join(dir, "library", "authored-workloads", "library.yaml"), []byte(provenanceAuthoredWorkloadLibYAML))
	writeProvenanceFile(t, filepath.Join(dir, "library", "authored-artifacts", "library.yaml"), []byte(provenanceAuthoredArtifactLibYAML))
	// The Feature and the Component the authored FeatureRecipe and Recipe implement,
	// so the directory is a catalog an eager load accepts whole.
	writeProvenanceFile(t, filepath.Join(dir, "feature", "core", "secret-value", "feature.yaml"), []byte(bridgeFeatureYAML))
	writeProvenanceFile(t, filepath.Join(dir, "component", "core", "datastore", "mariadb", "component.yaml"), []byte(bridgeComponentYAML))

	const (
		pulledPluginURI    = "pipeline-step/pulled-step:1.0.0"
		pulledRecipeURI    = "compose/mariadb:1.0.0"
		pulledFRURI        = "compose/dotenv-secret:1.0.0"
		pulledAggregateURI = "example/app:1.0.0"
		pulledSchemaURI    = "DbSizing:1.0.0"
		pulledMappingURI   = "BaseMappings:1.0.0"
		pulledWorkloadURI  = "BaseWorkloads:1.0.0"
		pulledArtifactsURI = "PinnedArtifacts:1.0.0"
		unpinnedRecipeURI  = "compose/unpinned-db:1.0.0"
		// The pulled plugin's .wasm, addressed by the entity's bare identity and
		// read by the entity's version.
		pulledArtifactURI     = "pipeline-step/pulled-step"
		pulledArtifactVersion = "1.0.0"
	)
	entityPins := Pins{
		{Kind: "Plugin", URI: pulledPluginURI}:       writePinnedSlot(t, ws, cacheDir, pulledPluginURI, oci.MediaTypePlugin, []byte(provenancePulledPluginYAML)),
		{Kind: "Recipe", URI: pulledRecipeURI}:       writePinnedSlot(t, ws, cacheDir, pulledRecipeURI, oci.MediaTypeRecipe, recipeBundle(t, bridgeRecipeYAML)),
		{Kind: "FeatureRecipe", URI: pulledFRURI}:    writePinnedSlot(t, ws, cacheDir, pulledFRURI, oci.MediaTypeFeatureRecipe, recipeBundle(t, bridgeFeatureRecipeYAML)),
		{Kind: "Aggregate", URI: pulledAggregateURI}: writePinnedSlot(t, ws, cacheDir, pulledAggregateURI, oci.MediaTypeAggregate, []byte(pinnedAggregateYAML)),
	}
	libraryPins := Pins{
		{Kind: "SchemaLibrary", URI: pulledSchemaURI}:      writePinnedSlot(t, ws, cacheDir, pulledSchemaURI, oci.MediaTypeSchemaLibrary, []byte(dbSizingLibYAML)),
		{Kind: "MappingLibrary", URI: pulledMappingURI}:    writePinnedSlot(t, ws, cacheDir, pulledMappingURI, oci.MediaTypeMappingLibrary, []byte(pinnedMappingLibYAML)),
		{Kind: "WorkloadLibrary", URI: pulledWorkloadURI}:  writePinnedSlot(t, ws, cacheDir, pulledWorkloadURI, oci.MediaTypeWorkloadLibrary, []byte(pinnedWorkloadLibYAML)),
		{Kind: "ArtifactLibrary", URI: pulledArtifactsURI}: writePinnedSlot(t, ws, cacheDir, pulledArtifactsURI, oci.MediaTypeArtifactLibrary, []byte(provenancePinnedArtifactLibYAML)),
	}
	// A cache slot no pin names: what an earlier pull left behind for an entity this
	// lockfile does not record.
	unpinnedBlob := recipeBundle(t, provenanceUnpinnedRecipeYAML)
	writePinnedSlot(t, ws, cacheDir, unpinnedRecipeURI, oci.MediaTypeRecipe, unpinnedBlob)
	artifactPin := writeCachedPluginArtifact(t, ws, cacheDir, pulledArtifactURI, []byte("\x00asm\x01\x00\x00\x00 pulled-step artifact"))

	pins := Pins{PluginExecutablePin(pulledArtifactURI): artifactPin}
	for uri, pin := range entityPins {
		pins[uri] = pin
	}
	for uri, pin := range libraryPins {
		pins[uri] = pin
	}
	// The same pins as a lockfile records them, each on its own axis, for the
	// construction a build performs: the view pinned to its lockfile.
	lf := &lockfile.Lockfile{
		Plugins: []lockfile.Plugin{{URI: pulledArtifactURI, ResolvedVersion: artifactPin.ResolvedVersion, Digest: artifactPin.Digest, ManifestDigest: artifactPin.ManifestDigest}},
	}
	for uri, pin := range entityPins {
		lf.Entities = append(lf.Entities, lockfile.Entity{Kind: EntityKindForCatalogKind(uri.Kind), URI: uri.URI, ResolvedVersion: pin.ResolvedVersion, Digest: pin.Digest, ManifestDigest: pin.ManifestDigest})
	}
	lf.SchemaLibraries = []lockfile.SchemaLibrary{{URI: pulledSchemaURI, ResolvedVersion: libraryPins[PinKey{Kind: "SchemaLibrary", URI: pulledSchemaURI}].ResolvedVersion, Digest: libraryPins[PinKey{Kind: "SchemaLibrary", URI: pulledSchemaURI}].Digest}}
	lf.MappingLibraries = []lockfile.MappingLibrary{{URI: pulledMappingURI, ResolvedVersion: libraryPins[PinKey{Kind: "MappingLibrary", URI: pulledMappingURI}].ResolvedVersion, Digest: libraryPins[PinKey{Kind: "MappingLibrary", URI: pulledMappingURI}].Digest}}
	lf.WorkloadLibraries = []lockfile.WorkloadLibrary{{URI: pulledWorkloadURI, ResolvedVersion: libraryPins[PinKey{Kind: "WorkloadLibrary", URI: pulledWorkloadURI}].ResolvedVersion, Digest: libraryPins[PinKey{Kind: "WorkloadLibrary", URI: pulledWorkloadURI}].Digest}}
	lf.ArtifactLibraries = []lockfile.ArtifactLibrary{{URI: pulledArtifactsURI, ResolvedVersion: libraryPins[PinKey{Kind: "ArtifactLibrary", URI: pulledArtifactsURI}].ResolvedVersion, Digest: libraryPins[PinKey{Kind: "ArtifactLibrary", URI: pulledArtifactsURI}].Digest}}

	embeddedID, embeddedVersion := provenanceEmbeddedProvider(t)

	entity := func(uri, kindHint string) func(context.Context, closure.Lookup) (closure.Resolution, error) {
		return func(ctx context.Context, lookup closure.Lookup) (closure.Resolution, error) {
			return lookup.ResolveCatalogEntity(ctx, uri, kindHint)
		}
	}
	aggregate := func(uri string) func(context.Context, closure.Lookup) (closure.Resolution, error) {
		return func(ctx context.Context, lookup closure.Lookup) (closure.Resolution, error) {
			return lookup.ResolveAggregate(ctx, uri)
		}
	}
	library := func(axis, uri string) func(context.Context, closure.Lookup) (closure.Resolution, error) {
		return func(ctx context.Context, lookup closure.Lookup) (closure.Resolution, error) {
			res, err := lookup.ResolveLibrary(ctx, closure.LibraryAxisNamed(axis), uri)
			return res.Resolution, err
		}
	}

	subjects := []provenanceSubject{
		{name: "catalog-authored plugin", want: closure.ResolutionPathCatalog, resolve: entity("pipeline-step/authored-step:1.0.0", closure.KindPlugin)},
		{name: "co-located plugin", want: closure.ResolutionPathCatalog, resolve: entity("pipeline-step/colocated-step:1.0.0", closure.KindPlugin)},
		{name: "cache-folded plugin", want: closure.ResolutionPathCache, wantDigest: pins[PinKey{Kind: "Plugin", URI: pulledPluginURI}].Digest, resolve: entity(pulledPluginURI, closure.KindPlugin)},
		{name: "embedded provider", want: closure.ResolutionPathEmbedded, resolve: entity(embeddedID+":"+embeddedVersion, closure.KindPlugin)},
		{name: "catalog-authored recipe", want: closure.ResolutionPathCatalog, resolve: entity("compose/authored-db:1.0.0", "recipe")},
		{name: "cache-folded recipe", want: closure.ResolutionPathCache, wantDigest: pins[PinKey{Kind: "Recipe", URI: pulledRecipeURI}].Digest, resolve: entity(pulledRecipeURI, "recipe")},
		{name: "catalog-authored feature recipe", want: closure.ResolutionPathCatalog, resolve: entity("compose/authored-secret:1.0.0", "feature_recipe")},
		{name: "cache-folded feature recipe", want: closure.ResolutionPathCache, wantDigest: pins[PinKey{Kind: "FeatureRecipe", URI: pulledFRURI}].Digest, resolve: entity(pulledFRURI, "feature_recipe")},
		{name: "catalog-authored aggregate", want: closure.ResolutionPathCatalog, resolve: aggregate("example/authored-app:1.0.0")},
		{name: "cache-folded aggregate", want: closure.ResolutionPathCache, wantDigest: pins[PinKey{Kind: "Aggregate", URI: pulledAggregateURI}].Digest, resolve: aggregate(pulledAggregateURI)},
		{name: "catalog-authored schema library", want: closure.ResolutionPathCatalog, resolve: library("SchemaLibraries", "AuthoredSizing:1.0.0")},
		{name: "cache-folded schema library", want: closure.ResolutionPathCache, wantDigest: pins[PinKey{Kind: "SchemaLibrary", URI: pulledSchemaURI}].Digest, resolve: library("SchemaLibraries", pulledSchemaURI)},
		{name: "catalog-authored mapping library", want: closure.ResolutionPathCatalog, resolve: library("MappingLibraries", "AuthoredMappings:1.0.0")},
		{name: "cache-folded mapping library", want: closure.ResolutionPathCache, wantDigest: pins[PinKey{Kind: "MappingLibrary", URI: pulledMappingURI}].Digest, resolve: library("MappingLibraries", pulledMappingURI)},
		{name: "catalog-authored workload library", want: closure.ResolutionPathCatalog, resolve: library("WorkloadLibraries", "AuthoredWorkloads:1.0.0")},
		{name: "cache-folded workload library", want: closure.ResolutionPathCache, wantDigest: pins[PinKey{Kind: "WorkloadLibrary", URI: pulledWorkloadURI}].Digest, resolve: library("WorkloadLibraries", pulledWorkloadURI)},
		{name: "catalog-authored artifacts library", want: closure.ResolutionPathCatalog, resolve: library("ArtifactLibraries", "AuthoredArtifacts:1.0.0")},
		{name: "cache-folded artifacts library", want: closure.ResolutionPathCache, wantDigest: pins[PinKey{Kind: "ArtifactLibrary", URI: pulledArtifactsURI}].Digest, resolve: library("ArtifactLibraries", pulledArtifactsURI)},
		{name: "cached entity no pin names", want: closure.ResolutionPathCache, wantDigest: rowDigestOf(unpinnedBlob), resolve: entity(unpinnedRecipeURI, "recipe")},
		{name: "cached plugin artifact", want: closure.ResolutionPathCache, wantDigest: artifactPin.Digest, artifact: true,
			resolve: func(ctx context.Context, lookup closure.Lookup) (closure.Resolution, error) {
				return lookup.ResolvePlugin(ctx, pulledArtifactURI, pulledArtifactVersion)
			}},
	}

	eager := func(t *testing.T) *Catalog {
		t.Helper()
		cat, err := LoadAllWithCacheDir(ws, cacheDir, dir)
		if err != nil {
			t.Fatalf("LoadAllWithCacheDir: %v", err)
		}
		return cat
	}
	pinnedView := func() *LazyLookup {
		view := NewLazyLookup(ws, cacheDir, []string{dir}, nil)
		view.SetPins(pins)
		return view
	}
	constructions := []provenanceConstruction{
		{name: "eager ClosureLookup", build: func(t *testing.T) closure.Lookup {
			return eager(t).ClosureLookup(nil, ws, cacheDir)
		}},
		{name: "eager ClosureLookupWithExternals", build: func(t *testing.T) closure.Lookup {
			return eager(t).ClosureLookupWithExternals(nil, ws, cacheDir, nil)
		}},
		{name: "eager ClosureLookupWithReuse", addressesArtifactPins: true, build: func(t *testing.T) closure.Lookup {
			return eager(t).ClosureLookupWithReuse(nil, ws, cacheDir, nil, nil, &ReuseVerification{PriorPins: pins})
		}},
		{name: "lazy ClosureLookup with no pins", build: func(t *testing.T) closure.Lookup {
			return NewLazyLookup(ws, cacheDir, []string{dir}, nil).ClosureLookup(nil, ws, cacheDir, nil, nil, nil)
		}},
		{name: "lazy ClosureLookup", build: func(t *testing.T) closure.Lookup {
			return pinnedView().ClosureLookup(nil, ws, cacheDir, nil, nil, nil)
		}},
		{name: "lazy view pinned to its lockfile", build: func(t *testing.T) closure.Lookup {
			view := NewLazyLookup(ws, cacheDir, []string{dir}, nil)
			lockPins, err := PinsFromLockfile(lf)
			if err != nil {
				t.Fatalf("PinsFromLockfile: %v", err)
			}
			view.SetPins(lockPins)
			return view.ClosureLookup(nil, ws, cacheDir, nil, nil, nil)
		}},
		{name: "lazy ClosureLookup with a reuse verification", addressesArtifactPins: true, build: func(t *testing.T) closure.Lookup {
			return pinnedView().ClosureLookup(nil, ws, cacheDir, nil, nil, &ReuseVerification{PriorPins: pins})
		}},
		{name: "lazy request view", build: func(t *testing.T) closure.Lookup {
			return NewLazyLookup(ws, cacheDir, []string{dir}, nil).ForRequest(pins).ClosureLookup(nil, ws, cacheDir, nil, nil, nil)
		}},
	}

	for _, c := range constructions {
		for _, s := range subjects {
			t.Run(c.name+"/"+s.name, func(t *testing.T) {
				t.Parallel()
				res, err := s.resolve(context.Background(), c.build(t))
				if err != nil {
					t.Fatalf("resolve: %v", err)
				}
				if s.artifact && !c.addressesArtifactPins {
					if !res.NotFound {
						t.Errorf("a lookup carrying no prior pins resolved the cached .wasm (%+v); its slot is keyed by a "+
							"digest only a pin names, so this cell is not found by construction", res)
					}
					return
				}
				if res.NotFound {
					t.Fatalf("the subject is not resolvable through this construction, so its provenance cannot be compared")
				}
				if res.ResolutionPath != s.want {
					t.Errorf("ResolutionPath = %q; want %q", res.ResolutionPath, s.want)
				}
				if s.wantDigest != "" && res.Digest != s.wantDigest {
					t.Errorf("Digest = %q; want %q — a cache-served entry is recorded by the row its slot's bytes are pinned by: "+
						"what an entity's publication carries, a library's or an executable's own bytes", res.Digest, s.wantDigest)
				}
			})
		}
	}
}

// writeCachedPluginArtifact lays down a plugin .wasm in the content-addressed slot a
// pull writes — the content blob and the manifest beside it — and returns the pin
// that addresses it, which names the content digest as both version and digest.
func writeCachedPluginArtifact(t *testing.T, ws, cacheDir, artifactURI string, wasm []byte) Pin {
	t.Helper()
	contentPin := contentDigest(wasm)
	manifest, manifestDigest := manifestBytesFor(t, oci.MediaTypeWasm, wasm)
	if err := oci.WriteCached(ws, cacheDir, oci.PluginArtifactKind(), artifactURI, contentPin, wasm); err != nil {
		t.Fatalf("write cached artifact %s: %v", artifactURI, err)
	}
	if err := oci.WriteCachedManifest(ws, cacheDir, oci.PluginArtifactKind(), artifactURI, contentPin, manifest); err != nil {
		t.Fatalf("write cached artifact manifest %s: %v", artifactURI, err)
	}
	return Pin{ResolvedVersion: contentPin, Digest: contentPin, ManifestDigest: manifestDigest}
}

// provenanceEmbeddedProvider returns the bare identity and version of one core
// provider this binary carries, read out of the binary so the version is never
// written down. Where the core plugins' entities were never generated because
// aggrega-hub is not checked out beside this repository the test skips; a binary
// carrying none otherwise fails it.
func provenanceEmbeddedProvider(t *testing.T) (identity, version string) {
	t.Helper()
	embeddedentities.Require(t)
	for _, uri := range embedded.EntityURIs() {
		raw, ok := embedded.LookupEntity(uri)
		if !ok {
			continue
		}
		var p types.Plugin
		if err := yaml.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.Metadata.PluginType != string(plugintype.Provider) || p.Metadata.Version == "" {
			continue
		}
		return p.Coordinate(), p.Metadata.Version
	}
	t.Fatal("this binary carries no core provider entities, though they were generated")
	return "", ""
}

func writeProvenanceFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
