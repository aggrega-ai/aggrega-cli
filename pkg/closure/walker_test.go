// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// fakeLookup is the in-memory mock Lookup implementation used by
// every walker test. Each field maps URI → Resolution, simulating
// the behaviour of a fully wired Lookup composed of *catalog.Catalog
// + *oci.Client without requiring either to be present.
type fakeLookup struct {
	entities      map[string]closure.Resolution
	plugins       map[string]closure.Resolution
	schemaLibs    map[string]closure.LibraryResolution
	mappingLibs   map[string]closure.LibraryResolution
	workloadLibs  map[string]closure.LibraryResolution
	artifactsLibs map[string]closure.LibraryResolution
}

func (f *fakeLookup) ResolveCatalogEntity(uri, kindHint string) (closure.Resolution, error) {
	if res, ok := f.entities[uri]; ok {
		return res, nil
	}
	return closure.Resolution{NotFound: true}, nil
}

func (f *fakeLookup) ResolveAggregate(uri string) (closure.Resolution, error) {
	return f.ResolveCatalogEntity(uri, "aggregate")
}

func (f *fakeLookup) ResolveSchemaLibrary(uri string) (closure.LibraryResolution, error) {
	if res, ok := f.schemaLibs[fakeBaseName(uri)]; ok {
		return res, nil
	}
	return closure.LibraryResolution{Resolution: closure.Resolution{NotFound: true}}, nil
}

// fakeBaseName strips an optional `:version` so the mock resolves a versioned
// library URI by its bare name, mirroring the production lookup's version-blind
// schemaLibraryNameFromURI.
func fakeBaseName(uri string) string {
	if i := strings.LastIndex(uri, ":"); i >= 0 {
		return uri[:i]
	}
	return uri
}

func (f *fakeLookup) ResolveMappingLibrary(uri string) (closure.LibraryResolution, error) {
	if res, ok := f.mappingLibs[fakeBaseName(uri)]; ok {
		return res, nil
	}
	return closure.LibraryResolution{Resolution: closure.Resolution{NotFound: true}}, nil
}

func (f *fakeLookup) ResolveWorkloadLibrary(uri string) (closure.LibraryResolution, error) {
	if res, ok := f.workloadLibs[fakeBaseName(uri)]; ok {
		return res, nil
	}
	return closure.LibraryResolution{Resolution: closure.Resolution{NotFound: true}}, nil
}

func (f *fakeLookup) ResolveArtifactsLibrary(uri string) (closure.LibraryResolution, error) {
	if res, ok := f.artifactsLibs[fakeBaseName(uri)]; ok {
		return res, nil
	}
	return closure.LibraryResolution{Resolution: closure.Resolution{NotFound: true}}, nil
}

func (f *fakeLookup) ResolvePlugin(uri, _ string) (closure.Resolution, error) {
	if res, ok := f.plugins[uri]; ok {
		return res, nil
	}
	return closure.Resolution{NotFound: true}, nil
}

// loadRelease / loadManifest / loadAggregate are inline helpers
// that unmarshal a YAML fixture string into the typed structure.
// Tests author small fixture YAML inline as Go raw strings; no
// testdata directory dependency.

func loadRelease(t *testing.T, src string) *types.Release {
	t.Helper()
	var r types.Release
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal release: %v", err)
	}
	return &r
}

func loadManifest(t *testing.T, src string) *types.Manifest {
	t.Helper()
	var m types.Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return &m
}

func loadAggregate(t *testing.T, src string) *types.Aggregate {
	t.Helper()
	var a types.Aggregate
	if err := yaml.Unmarshal([]byte(src), &a); err != nil {
		t.Fatalf("unmarshal aggregate: %v", err)
	}
	return &a
}

// resolutionOK builds a successfully-resolved Resolution stub.
func resolutionOK(version, digest, tier string) closure.Resolution {
	return closure.Resolution{
		ResolvedVersion: version,
		Digest:          digest,
		ResolutionPath:  tier,
	}
}

// ----- Test fixtures (inline YAML) -----

const fullFixtureRelease = `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: prod
  version: 1.0.0
spec:
  aggregate: hub.aggrega.ai/aggregate/example/app:1.0.0
  manifest: hub.aggrega.ai/manifest/example/app:1.0.0
  boundaries:
    primary:
      components:
        api:
          features:
            ingress:
              type: hub.aggrega.ai/feature/core/ingress:1.0.0
`

const fullFixtureManifest = `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        hub.aggrega.ai/component/core/api-server: hub.aggrega.ai/recipe/core/helm/api-server:1.2.0
        hub.aggrega.ai/component/core/datastore: hub.aggrega.ai/recipe/core/helm/postgres:1.0.0
    features:
      type:
        hub.aggrega.ai/feature/core/ingress: hub.aggrega.ai/feature-recipe/core/helm/ingress-nginx:1.0.0
`

const fullFixtureAggregate = `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app
  version: 1.0.0
spec:
  components:
    api:
      type: hub.aggrega.ai/component/core/api-server
    db:
      type: hub.aggrega.ai/component/core/datastore
`

// ----- Tests -----

func TestComputeClosureEnumeratesEveryReferenceSite(t *testing.T) {
	t.Parallel()
	rel := loadRelease(t, fullFixtureRelease)
	man := loadManifest(t, fullFixtureManifest)
	agg := loadAggregate(t, fullFixtureAggregate)

	cfg := workspaceconfig.Default()
	// A label-transform governance attachment references a Plugin ENTITY URI.
	// Because a label-transform plugin rewrites node labels and shapes the rendered
	// output bytes, its entity rides the catalog-entity axis (resolved via
	// ResolveCatalogEntity) and is pinned in the closure; its .wasm artifact is
	// pinned later at pull time by the pipeline-plugin descent. So ComputeClosure
	// records the entity in c.Entities, not c.Plugins.
	const transformPluginEntity = "core/governance/aggrega-inject-baseline:1.0.0"
	// A verdict governance attachment renders an accept/reject verdict without
	// shaping output bytes, so its entity is resolved at its point of use and is
	// NOT pinned in the closure. Configuring it here proves ComputeClosure does not
	// enumerate it.
	const verdictPluginEntity = "core/governance/aggrega-security-baseline:1.0.0"
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{
		{URI: transformPluginEntity, Role: plugintype.RoleLabelTransform},
		{URI: verdictPluginEntity, Role: plugintype.RoleIRVerdict},
	}

	lookup := &fakeLookup{
		entities: map[string]closure.Resolution{
			"hub.aggrega.ai/aggregate/example/app:1.0.0":                  resolutionOK("1.0.0", "sha256:aaa", "tier3-registry"),
			"hub.aggrega.ai/manifest/example/app:1.0.0":                   resolutionOK("1.0.0", "sha256:bbb", "tier3-registry"),
			"hub.aggrega.ai/component/core/api-server":                    resolutionOK("1.0.0", "sha256:ccc", "tier3-registry"),
			"hub.aggrega.ai/component/core/datastore":                     resolutionOK("1.0.0", "sha256:ddd", "tier3-registry"),
			"hub.aggrega.ai/feature/core/ingress:1.0.0":                   resolutionOK("1.0.0", "sha256:eee", "tier3-registry"),
			"hub.aggrega.ai/feature/core/ingress":                         resolutionOK("1.0.0", "sha256:eee", "tier3-registry"),
			"hub.aggrega.ai/recipe/core/helm/api-server:1.2.0":            resolutionOK("1.2.0", "sha256:fff", "tier3-registry"),
			"hub.aggrega.ai/recipe/core/helm/postgres:1.0.0":              resolutionOK("1.0.0", "sha256:ggg", "tier3-registry"),
			"hub.aggrega.ai/feature-recipe/core/helm/ingress-nginx:1.0.0": resolutionOK("1.0.0", "sha256:hhh", "tier3-registry"),
			transformPluginEntity:                                         resolutionOK("1.0.0", "sha256:wasm", "tier3-registry"),
			// A resolution for the verdict attachment is provided too; the closure
			// must still not enumerate it, proving non-pinning is not merely a lookup
			// miss.
			verdictPluginEntity: resolutionOK("1.0.0", "sha256:policy-wasm", "tier3-registry"),
		},
	}

	c, err := closure.ComputeClosure(rel, man, types.Normalize(agg), cfg, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}

	// Expect entities: Aggregate, Manifest, 2 Components, 1 Feature (the
	// release-attached ingress on the api component plus the manifest-side
	// hub.aggrega.ai/feature/core/ingress mapping key — currently dedup'd
	// to one entry because both reference the same URI). Plus 2 Recipes
	// and 1 FeatureRecipe from the manifest, plus the output-shaping
	// label-transform governance Plugin entity. The verdict attachment is NOT
	// enumerated.
	wantEntities := []string{
		transformPluginEntity,
		"hub.aggrega.ai/aggregate/example/app:1.0.0",
		"hub.aggrega.ai/component/core/api-server",
		"hub.aggrega.ai/component/core/datastore",
		"hub.aggrega.ai/feature-recipe/core/helm/ingress-nginx:1.0.0",
		"hub.aggrega.ai/feature/core/ingress",
		"hub.aggrega.ai/feature/core/ingress:1.0.0",
		"hub.aggrega.ai/manifest/example/app:1.0.0",
		"hub.aggrega.ai/recipe/core/helm/api-server:1.2.0",
		"hub.aggrega.ai/recipe/core/helm/postgres:1.0.0",
	}
	got := make([]string, 0, len(c.Entities))
	for _, e := range c.Entities {
		got = append(got, e.URI)
	}
	if !reflect.DeepEqual(got, wantEntities) {
		t.Errorf("entities mismatch.\n  got:  %v\n  want: %v", got, wantEntities)
	}

	// The verdict-role governance attachment must not ride the pinned axis.
	for _, e := range c.Entities {
		if e.URI == verdictPluginEntity {
			t.Errorf("verdict attachment entity %q was pinned; verdict roles are resolved at point of use, not pinned", verdictPluginEntity)
		}
	}

	// ComputeClosure pins only the entities; the .wasm artifact axis is populated
	// at pull time by the pipeline-plugin descent, so c.Plugins is empty here.
	if len(c.Plugins) != 0 {
		t.Errorf("wasm plugins: got %d; want 0 (the workspace plugin rides the entity axis)", len(c.Plugins))
	}

	if len(c.SchemaLibraries) != 0 || len(c.MappingLibraries) != 0 {
		t.Errorf("libraries: expected zero (no $ref / $import in fixture); got schema=%d, mapping=%d",
			len(c.SchemaLibraries), len(c.MappingLibraries))
	}
}

func TestMVSResolvesLibraryVersionConflictsToMaxVersion(t *testing.T) {
	t.Parallel()
	c := closure.Closure{}

	schemaA := mustParse(t, `$ref: "BaseTypes:1.0.0#/schemas/PortSpec"`)
	if err := c.WalkSchemaRefs(schemaA, "hub.aggrega.ai/component/core/api-server"); err != nil {
		t.Fatalf("walk consumer A: %v", err)
	}
	schemaB := mustParse(t, `$ref: "BaseTypes:1.5.0#/schemas/PortSpec"`)
	if err := c.WalkSchemaRefs(schemaB, "hub.aggrega.ai/component/core/worker"); err != nil {
		t.Fatalf("walk consumer B: %v", err)
	}

	if len(c.SchemaLibraries) != 1 {
		t.Fatalf("expected 1 schema library; got %d", len(c.SchemaLibraries))
	}
	lib := c.SchemaLibraries[0]
	if lib.URI != "BaseTypes" {
		t.Errorf("library URI: %q", lib.URI)
	}
	if len(lib.Consumers) != 2 {
		t.Errorf("consumers: %d; want 2", len(lib.Consumers))
	}
}

// TestSchemaRefDiscoveryRecordsIdentityFormOnly asserts the discovery walk
// records a host-less identity $ref (`<Library>#/schemas/<Entry>`) and skips both
// an in-document anchor (`#/$defs/...`) and a host-bearing `oci://…` reference —
// the latter is rejected at the cross-document expander and is never a
// library-closure edge. Keying discovery on the bare-name identity shape (not the
// old `oci://` prefix) is what lets a remote bare-name library be discovered and
// pinned.
func TestSchemaRefDiscoveryRecordsIdentityFormOnly(t *testing.T) {
	t.Parallel()
	c := closure.Closure{}
	schema := mustParse(t, `
properties:
  good:
    $ref: "BaseTypes#/schemas/PortSpec"
  local:
    $ref: "#/$defs/Local"
  hosted:
    $ref: "oci://hub.aggrega.ai/schema-library/core/base-types@1.0.0#/schemas/PortSpec"
`)
	if err := c.WalkSchemaRefs(schema, "hub.aggrega.ai/component/core/api-server"); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(c.SchemaLibraries) != 1 {
		t.Fatalf("expected exactly 1 library (only the identity-form ref); got %d", len(c.SchemaLibraries))
	}
	if c.SchemaLibraries[0].URI != "BaseTypes" {
		t.Errorf("library URI: got %q; want BaseTypes (bare-name identity)", c.SchemaLibraries[0].URI)
	}
}

// Note: TestSelectMVSVersionDetectsMajorVersionDisagreement lives in
// the internal parse_test.go (package closure) because it exercises
// the unexported selectMVSVersion helper directly. The end-to-end
// MVS conflict path through ComputeClosure lands when the
// Lookup-side Component resolution surfaces schema bodies with
// $ref entries — an integration that arrives with the pull-command
// wiring.

func TestLibraryConsumersTrackedPerEntityWithFragments(t *testing.T) {
	t.Parallel()
	c := closure.Closure{}

	schemaA := mustParse(t, `$ref: "BaseTypes:1.0.0#/schemas/PortSpec"`)
	if err := c.WalkSchemaRefs(schemaA, "hub.aggrega.ai/component/core/api-server"); err != nil {
		t.Fatalf("walk A: %v", err)
	}
	schemaB := mustParse(t, `
properties:
  port:
    $ref: "BaseTypes:1.0.0#/schemas/PortSpec"
  endpoint:
    $ref: "BaseTypes:1.0.0#/schemas/EndpointSpec"
`)
	if err := c.WalkSchemaRefs(schemaB, "hub.aggrega.ai/component/core/gateway"); err != nil {
		t.Fatalf("walk B: %v", err)
	}

	if len(c.SchemaLibraries) != 1 {
		t.Fatalf("schema libraries: %d; want 1", len(c.SchemaLibraries))
	}
	lib := c.SchemaLibraries[0]
	if len(lib.Consumers) != 2 {
		t.Fatalf("consumers: %d; want 2", len(lib.Consumers))
	}
	// Consumers are sorted by ConsumerURI; api-server < gateway.
	if lib.Consumers[0].ConsumerURI != "hub.aggrega.ai/component/core/api-server" {
		t.Errorf("consumer[0]: %q", lib.Consumers[0].ConsumerURI)
	}
	if lib.Consumers[1].ConsumerURI != "hub.aggrega.ai/component/core/gateway" {
		t.Errorf("consumer[1]: %q", lib.Consumers[1].ConsumerURI)
	}
	if len(lib.Consumers[1].Fragments) != 2 {
		t.Errorf("gateway fragments: %d; want 2", len(lib.Consumers[1].Fragments))
	}
	// Fragments sort lexicographically.
	if lib.Consumers[1].Fragments[0] != "#/schemas/EndpointSpec" {
		t.Errorf("gateway fragment[0]: %q", lib.Consumers[1].Fragments[0])
	}
}

func TestVersionBumpAffectsLibraryDigestChain(t *testing.T) {
	t.Parallel()
	// Snapshot 1: consumer A pins lib@1.0.0; lookup digest "old".
	c1 := closure.Closure{}
	refOld := mustParse(t, `$ref: "BaseTypes:1.0.0#/schemas/PortSpec"`)
	if err := c1.WalkSchemaRefs(refOld, "hub.aggrega.ai/component/core/api-server"); err != nil {
		t.Fatalf("walk old: %v", err)
	}
	// Snapshot 2: consumer A bumps to lib@2.0.0; lookup digest "new".
	c2 := closure.Closure{}
	refNew := mustParse(t, `$ref: "BaseTypes:2.0.0#/schemas/PortSpec"`)
	if err := c2.WalkSchemaRefs(refNew, "hub.aggrega.ai/component/core/api-server"); err != nil {
		t.Fatalf("walk new: %v", err)
	}

	// Both snapshots carry one SchemaLibraries entry; the URI is the
	// same; the Consumers list is the same; only the version (which
	// would be set by Lookup at resolveAll time) differs. The diff
	// surface — comparing the two ResolvedLibrary entries — clearly
	// identifies the consumer affected by the bump.
	if len(c1.SchemaLibraries) != 1 || len(c2.SchemaLibraries) != 1 {
		t.Fatalf("library counts: pre=%d post=%d", len(c1.SchemaLibraries), len(c2.SchemaLibraries))
	}
	if c1.SchemaLibraries[0].URI != c2.SchemaLibraries[0].URI {
		t.Errorf("library URI changed across version bump")
	}
	if len(c1.SchemaLibraries[0].Consumers) != 1 || len(c2.SchemaLibraries[0].Consumers) != 1 {
		t.Errorf("consumer counts changed")
	}
	if c1.SchemaLibraries[0].Consumers[0].ConsumerURI != c2.SchemaLibraries[0].Consumers[0].ConsumerURI {
		t.Errorf("consumer URI changed across version bump (it shouldn't)")
	}
}

func TestEmptyReleaseProducesEmptyClosure(t *testing.T) {
	t.Parallel()
	rel := &types.Release{}
	cfg := workspaceconfig.Default()
	lookup := &fakeLookup{
		entities: map[string]closure.Resolution{},
		plugins:  map[string]closure.Resolution{},
	}

	c, err := closure.ComputeClosure(rel, nil, types.NormalizedAggregate{}, cfg, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if len(c.Entities) != 0 || len(c.Plugins) != 0 ||
		len(c.SchemaLibraries) != 0 || len(c.MappingLibraries) != 0 {
		t.Errorf("expected empty closure; got %+v", c)
	}
}

func TestUnresolvableCatalogEntityHaltsWithWrappedError(t *testing.T) {
	t.Parallel()
	rel := loadRelease(t, fullFixtureRelease)
	man := loadManifest(t, fullFixtureManifest)
	agg := loadAggregate(t, fullFixtureAggregate)

	cfg := workspaceconfig.Default()
	// Lookup that knows nothing about the api-server Component but
	// everything else resolves. The walker should halt with
	// ErrUnresolvableURI naming the offending URI.
	lookup := newBaseLookup()
	delete(lookup.entities, "hub.aggrega.ai/component/core/api-server")

	_, err := closure.ComputeClosure(rel, man, types.Normalize(agg), cfg, lookup)
	if err == nil {
		t.Fatal("expected ErrUnresolvableURI; got nil")
	}
	if !errors.Is(err, closure.ErrUnresolvableURI()) {
		t.Errorf("error should wrap ErrUnresolvableURI; got %v", err)
	}
	if !strings.Contains(err.Error(), "api-server") {
		t.Errorf("error should name the unresolvable URI; got: %v", err)
	}
}

func TestDeterministicURIOrdering(t *testing.T) {
	t.Parallel()
	rel := loadRelease(t, fullFixtureRelease)
	man := loadManifest(t, fullFixtureManifest)
	agg := loadAggregate(t, fullFixtureAggregate)
	cfg := workspaceconfig.Default()
	lookup := newBaseLookup()

	c1, err := closure.ComputeClosure(rel, man, types.Normalize(agg), cfg, lookup)
	if err != nil {
		t.Fatalf("walk 1: %v", err)
	}
	c2, err := closure.ComputeClosure(rel, man, types.Normalize(agg), cfg, lookup)
	if err != nil {
		t.Fatalf("walk 2: %v", err)
	}
	// URIs must match in order for the two walks.
	if len(c1.Entities) != len(c2.Entities) {
		t.Fatalf("entity counts differ: %d vs %d", len(c1.Entities), len(c2.Entities))
	}
	for i := range c1.Entities {
		if c1.Entities[i].URI != c2.Entities[i].URI {
			t.Errorf("entity[%d] URI differs: %q vs %q", i, c1.Entities[i].URI, c2.Entities[i].URI)
		}
	}
	// Sorted ascending.
	for i := 1; i < len(c1.Entities); i++ {
		if c1.Entities[i].URI < c1.Entities[i-1].URI {
			t.Errorf("entities not sorted ascending at index %d: %q < %q",
				i, c1.Entities[i].URI, c1.Entities[i-1].URI)
		}
	}
}

func TestMappingImportRecordsConsumer(t *testing.T) {
	t.Parallel()
	c := closure.Closure{}
	tmpl := `$import: BaseMappings#/mappings/SimpleValues`
	if err := c.WalkMappingImport(tmpl, "hub.aggrega.ai/recipe/core/helm/postgres"); err != nil {
		t.Fatalf("WalkMappingImport: %v", err)
	}
	if len(c.MappingLibraries) != 1 {
		t.Fatalf("mapping libraries: %d; want 1", len(c.MappingLibraries))
	}
	lib := c.MappingLibraries[0]
	if lib.URI != "BaseMappings" {
		t.Errorf("library URI: %q; want the bare-name identity", lib.URI)
	}
	if len(lib.Consumers) != 1 || lib.Consumers[0].ConsumerURI != "hub.aggrega.ai/recipe/core/helm/postgres" {
		t.Errorf("consumer: %+v", lib.Consumers)
	}
	if len(lib.Consumers[0].Fragments) != 1 || lib.Consumers[0].Fragments[0] != "#/mappings/SimpleValues" {
		t.Errorf("fragment: %+v", lib.Consumers[0].Fragments)
	}
}

// TestMappingImportSkipsHostBearingForm pins that the discovery walker does not
// record a host-bearing $import as a cross-library edge — a library is addressed
// by identity, and the host form is rejected at its consumption site, not here.
func TestMappingImportSkipsHostBearingForm(t *testing.T) {
	t.Parallel()
	c := closure.Closure{}
	tmpl := `$import: oci://hub.aggrega.ai/mapping-library/core/base-mappings@1.0.0#/mappings/SimpleValues`
	if err := c.WalkMappingImport(tmpl, "hub.aggrega.ai/recipe/core/helm/postgres"); err != nil {
		t.Fatalf("WalkMappingImport: %v", err)
	}
	if len(c.MappingLibraries) != 0 {
		t.Errorf("mapping libraries: %d; want 0 (host-bearing form is not a library edge)", len(c.MappingLibraries))
	}
}

// ----- Helpers -----

// mustParse decodes a YAML source into a *yaml.Node tree (the shape
// Component / Feature Spec.Schema carries). Used to construct
// fixture schemas inline.
func mustParse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	return &node
}

// newBaseLookup returns a fakeLookup pre-populated with the same
// resolution set the full-fixture tests need. Tests that want to
// remove or add specific entries can mutate the returned map.
func newBaseLookup() *fakeLookup {
	return &fakeLookup{
		entities: map[string]closure.Resolution{
			"hub.aggrega.ai/aggregate/example/app:1.0.0":                  resolutionOK("1.0.0", "sha256:aaa", "tier3-registry"),
			"hub.aggrega.ai/manifest/example/app:1.0.0":                   resolutionOK("1.0.0", "sha256:bbb", "tier3-registry"),
			"hub.aggrega.ai/component/core/api-server":                    resolutionOK("1.0.0", "sha256:ccc", "tier3-registry"),
			"hub.aggrega.ai/component/core/datastore":                     resolutionOK("1.0.0", "sha256:ddd", "tier3-registry"),
			"hub.aggrega.ai/feature/core/ingress:1.0.0":                   resolutionOK("1.0.0", "sha256:eee", "tier3-registry"),
			"hub.aggrega.ai/feature/core/ingress":                         resolutionOK("1.0.0", "sha256:eee", "tier3-registry"),
			"hub.aggrega.ai/recipe/core/helm/api-server:1.2.0":            resolutionOK("1.2.0", "sha256:fff", "tier3-registry"),
			"hub.aggrega.ai/recipe/core/helm/postgres:1.0.0":              resolutionOK("1.0.0", "sha256:ggg", "tier3-registry"),
			"hub.aggrega.ai/feature-recipe/core/helm/ingress-nginx:1.0.0": resolutionOK("1.0.0", "sha256:hhh", "tier3-registry"),
		},
		plugins:     map[string]closure.Resolution{},
		schemaLibs:  map[string]closure.LibraryResolution{},
		mappingLibs: map[string]closure.LibraryResolution{},
	}
}
