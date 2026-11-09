// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

// The document an authored entity was declared in, per kind.
//
// Every kind here is enumerated or indexed by a different walk, under a
// differently-shaped key, so a single row proves only its own arm. The table is
// what makes the set enumerable from the failure output: a kind whose retention
// is dropped fails its own row and names itself.

import (
	"path/filepath"
	"testing"
)

const (
	localPathComponentYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: "1.0.0"
spec:
  inject: ["env"]
`
	localPathFeatureYAML = `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: core/secret-value
  version: "1.0.0"
spec: {}
`
	localPathRecipeYAML = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: mariadb
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
	localPathFeatureRecipeYAML = `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: dotenv-secret
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/secret-value"
`
	localPathAggregateYAML = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: example/app
  version: "1.0.0"
spec: {}
`
	localPathSchemaLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`
	localPathMappingLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
`
	localPathWorkloadLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: WorkloadLibrary
metadata:
  name: BaseWorkloads
  version: 1.0.0
spec:
  workloads:
    SimpleService: 'kind: Deployment'
`
	localPathArtifactsLibYAML = `apiVersion: aggrega.ai/v1beta1
kind: ArtifactLibrary
metadata:
  name: SharedCRDs
  version: 1.0.0
spec:
  artifacts:
    WidgetCRD:
      widget-config.yaml: "kind: ConfigMap"
`
)

// localPathWorkspace lays one authored document of every kind into a catalog
// directory inside a workspace root, and returns a lookup over it.
//
// The catalog sits inside the root so a recorded path is directly reportable;
// the withholding rules — a directory outside the root, an entity that was never
// authored on this machine — are asserted by the surfaces that report the value
// to a reader, which is where the reader-facing judgement lives.
func localPathWorkspace(t *testing.T) (l *LazyLookup, ws string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	for rel, body := range map[string]string{
		"components/mariadb.yaml":    localPathComponentYAML,
		"features/secret-value.yaml": localPathFeatureYAML,
		"recipes/mariadb.yaml":       localPathRecipeYAML,
		"recipes/dotenv-secret.yaml": localPathFeatureRecipeYAML,
		"aggregates/app.yaml":        localPathAggregateYAML,
		"libraries/db-sizing.yaml":   localPathSchemaLibYAML,
		"libraries/mappings.yaml":    localPathMappingLibYAML,
		"libraries/workloads.yaml":   localPathWorkloadLibYAML,
		"libraries/artifacts.yaml":   localPathArtifactsLibYAML,
		"plugins/helm.yaml":          newShapePlugin("helm", "provider"),
	} {
		writeCatalogFile(t, catalogDir, rel, body)
	}
	return NewLazyLookup(ws, "", []string{catalogDir}, nil), ws
}

// TestLocalEntityPathReportsTheDocumentPerKind pins the document report for every
// kind that carries one, each row naming the identity a by-name resolution reads
// off the matched entity.
//
// The rows do not agree on their answer, which is what makes them discriminate: a
// lookup returning one recorded path for everything would satisfy at most one row.
func TestLocalEntityPathReportsTheDocumentPerKind(t *testing.T) {
	t.Parallel()
	l, _ := localPathWorkspace(t)

	for _, tc := range []struct {
		kind, scope, name, version string
		want                       string
	}{
		{"Component", "", "core/datastore/mariadb", "1.0.0", "catalog/components/mariadb.yaml"},
		{"Feature", "", "core/secret-value", "1.0.0", "catalog/features/secret-value.yaml"},
		{"Recipe", "compose", "mariadb", "1.0.0", "catalog/recipes/mariadb.yaml"},
		{"FeatureRecipe", "compose", "dotenv-secret", "1.0.0", "catalog/recipes/dotenv-secret.yaml"},
		{"Plugin", "provider", "helm", "1.0.0", "catalog/plugins/helm.yaml"},
		{"SchemaLibrary", "", "DbSizing", "1.0.0", "catalog/libraries/db-sizing.yaml"},
		{"MappingLibrary", "", "BaseMappings", "1.0.0", "catalog/libraries/mappings.yaml"},
		{"WorkloadLibrary", "", "BaseWorkloads", "1.0.0", "catalog/libraries/workloads.yaml"},
		{"ArtifactLibrary", "", "SharedCRDs", "1.0.0", "catalog/libraries/artifacts.yaml"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			got, ok := l.LocalEntityPath(tc.kind, tc.scope, tc.name, tc.version)
			if !ok {
				t.Fatalf("%s %q was authored in %s and no document is reported for it", tc.kind, tc.name, tc.want)
			}
			if filepath.IsAbs(got) {
				t.Errorf("the reported document %q names a location on this machine rather than one "+
					"relative to the workspace root", got)
			}
			if got != tc.want {
				t.Errorf("the document reported for %s %q is %q; it was authored in %q",
					tc.kind, tc.name, got, tc.want)
			}
		})
	}
}

// TestLocalEntityPathReportsNothingItHasNoDocumentFor pins the three negatives, each
// with a positive control on the same lookup so a refusal cannot be a walk that
// found nothing at all.
func TestLocalEntityPathReportsNothingItHasNoDocumentFor(t *testing.T) {
	t.Parallel()
	l, _ := localPathWorkspace(t)

	if _, ok := l.LocalEntityPath("Component", "", "core/datastore/mariadb", "1.0.0"); !ok {
		t.Fatal("positive control: the lookup reports no document for an entity it did walk, so the " +
			"refusals below prove nothing")
	}

	// The Aggregate is answered by its own accessor rather than here, so one fact
	// has one producer. The control is that the accessor does answer: without it,
	// a walk that never saw the Aggregate would satisfy this.
	if _, ok := l.LocalEntityPath("Aggregate", "", "example/app", "1.0.0"); ok {
		t.Error("the Aggregate's document was reported here as well as by its own accessor, so one " +
			"fact has two producers")
	}
	if agg, ok := l.AggregateLookup()("example/app"); !ok || agg.Path == "" {
		t.Errorf("control: the Aggregate accessor reports resolved=%t path=%q, so the line above says "+
			"nothing about where the Aggregate's document is answered", ok, agg.Path)
	}

	// A kind the lookup indexes nothing for reports nothing rather than guessing.
	if got, ok := l.LocalEntityPath("Release", "", "example/app", "1.0.0"); ok {
		t.Errorf("a kind this lookup indexes no document for was answered with %q", got)
	}

	// A name nothing declares reports nothing, which is what separates a map
	// lookup from a constant.
	if got, ok := l.LocalEntityPath("Component", "", "core/datastore/absent", "1.0.0"); ok {
		t.Errorf("a Component no document declares was answered with %q", got)
	}
}

// TestLocalRecipePathReportsTheWritablePath pins the difference between the two
// accessors over the one shared lookup: the reporting one renders its answer
// against the workspace root, and the writable one hands back the path a caller
// can open from anywhere.
//
// They are asserted together because they read the same recorded value, and a
// change that made either render its answer the other's way would leave one
// caller opening a path that does not resolve from where it stands.
func TestLocalRecipePathReportsTheWritablePath(t *testing.T) {
	t.Parallel()
	l, ws := localPathWorkspace(t)

	writable, ok := l.LocalRecipePath("compose", "mariadb", "1.0.0")
	if !ok {
		t.Fatal("no writable path is recorded for a Recipe authored in the catalog directory")
	}
	if !filepath.IsAbs(writable) {
		t.Errorf("the writable path %q is not one a caller can open from any working directory", writable)
	}

	reported, ok := l.LocalEntityPath("Recipe", "compose", "mariadb", "1.0.0")
	if !ok {
		t.Fatal("no document is reported for the same Recipe")
	}
	if filepath.Join(ws, reported) != writable {
		t.Errorf("the reported document %q anchored against the root is %q; the writable path is %q, "+
			"so the two accessors do not name one file", reported, filepath.Join(ws, reported), writable)
	}
}
