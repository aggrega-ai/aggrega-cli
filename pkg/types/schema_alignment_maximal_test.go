// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Maximal instance constructors for the Direction-1 leg of the
// schema↔struct alignment suite. Each populates every top-level spec
// field of its kind (plus representative nested content) with
// schema-valid values, so marshalling and validating it exercises the
// kind's full Go surface against its envelope schema. Kept adjacent to
// the suite per the "one constructor per kind" discipline.
package types_test

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"go.yaml.in/yaml/v3"
)

// mustNode parses a YAML document string into the inner content node —
// the shape the content-dispatch fields (Schema, ValueSchema,
// SchemaEntry.Content, …) hold.
func mustNode(s string) yaml.Node {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		panic("mustNode: " + err.Error())
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return *n.Content[0]
	}
	return n
}

func om[V any](pairs ...any) types.OrderedMap[V] {
	m := types.NewOrderedMap[V]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1].(V))
	}
	return *m
}

func maximalComponent() types.Component {
	return types.Component{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Component",
		Metadata: types.ComponentMetadata{
			Name:        "core/compute/api-server",
			Version:     "1.0.0",
			Description: "maximal component",
			Maintainers: []types.Maintainer{{Name: "M", Email: "m@example.com", URL: "https://example.com"}},
			License:     "Apache-2.0",
			Repository:  "https://example.com/repo",
			Home:        "https://example.com",
			Keywords:    []string{"k"},
			Deprecated:  true,
			Labels:      om[string]("component.aggrega.ai/type", "server", "component.aggrega.ai/category", "compute"),
			Annotations: om[string]("a", "b"),
		},
		Spec: types.ComponentSpec{
			Extends:          "core/compute/base",
			SecretsSchema:    om[types.SecretSlot]("api_key", types.SecretSlot{Required: true, Inject: []string{"env"}, Description: "key"}),
			RequiredFeatures: []string{"core/logging"},
			Inject:           []string{"env", "file"},
			UsesSchema: om[types.UsesSchemaEntry]("databases", types.UsesSchemaEntry{
				Selector:    "component.aggrega.ai/type=stateful-server",
				Required:    true,
				Inject:      []string{"env"},
				ValueSchema: mustNode("protocol: { type: string }"),
				InfraSchema: mustNode("pool_size: { type: integer }"),
			}),
			Schema:     mustNode("values:\n  port: { type: integer }\ninfra:\n  replicas: { type: integer }\nprovides:\n  host: { type: string }"),
			Assertions: om[string]("rule", "{{ if lt .values.port 1024 }}{{ fail \"low\" }}{{ end }}"),
		},
	}
}

func maximalFeature() types.Feature {
	return types.Feature{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Feature",
		Metadata: types.FeatureMetadata{
			Name:    "core/data-masking",
			Version: "1.0.0",
			Labels:  om[string]("feature.aggrega.ai/kind", "secret"),
		},
		Spec: types.FeatureSpec{
			Extends:          "core/base-feature",
			SecretsSchema:    om[types.SecretSlot]("token", types.SecretSlot{Required: true}),
			AppliesTo:        "component.aggrega.ai/type=server",
			AppliesToAnyOf:   []types.SelectorTerm{types.StringTerm("component.aggrega.ai/type=worker")},
			Interoperability: &types.Interoperability{ConflictsWith: []string{"core/other"}, ComponentRefPath: "spec.target"},
			UsesSchema:       om[types.UsesSchemaEntry]("backend", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"}),
			Schema:           mustNode("values:\n  level: { type: string }"),
			Assertions:       om[string]("rule", "{{ true }}"),
		},
	}
}

func maximalRecipe() types.Recipe {
	return types.Recipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: "mariadb", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Provider:   "compose",
			Implements: "core/datastore/mariadb",
			Source:     "oci://example/chart:1.0.0",
			Locals:     om[string]("svc", "{{ .context.instance_id }}"),
			Provides:   om[string]("host", "{{ .context.instance_id }}"),
			Workload:   types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ncontainers:\n  main:\n    image: x:1"},
			Pipeline:   []types.PipelineStep{{ID: "s1", Plugin: "p.wasm", Function: "render", With: "{}", Optional: true}},
			Assertions: om[string]("rule", "{{ true }}"),
		},
	}
}

func maximalFeatureRecipe() types.FeatureRecipe {
	return types.FeatureRecipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "FeatureRecipe",
		Metadata:   types.FeatureRecipeMetadata{Name: "dotenv-secret", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Provider:       "compose",
			Implements:     "core/secret-value",
			Locals:         om[string]("k", "v"),
			Provides:       om[string]("ref", "x"),
			InjectMapping:  "env:\n  {{ .values.key }}: \"{{ .values.origin }}\"",
			ExecutionScope: "component",
			Assertions:     om[string]("rule", "{{ true }}"),
		},
	}
}

func maximalProvider() types.Provider {
	return types.Provider{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Provider",
		Metadata:   types.ProviderMetadata{Name: "compose", Version: "1.0.0"},
		Spec: types.ProviderSpec{
			Plugin:         "hub.aggrega.ai/providers/compose-renderer:1.0.0",
			BaseSchema:     "type: object",
			WorkloadSchema: "type: object",
			WellKnownAnnotations: []types.WellKnownAnnotation{
				{Key: "aggrega.ai/namespace", Purpose: "target namespace", DefaultBehavior: "default namespace"},
			},
		},
	}
}

func maximalPlugin() types.Plugin {
	cfg := om[string]("LOG_LEVEL", "info")
	return types.Plugin{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Plugin",
		Metadata: types.PluginMetadata{
			Name:        "publishing-linter",
			Version:     "1.0.0",
			Description: "maximal wasm plugin",
			Maintainers: []types.Maintainer{{Name: "M", Email: "m@example.com", URL: "https://example.com"}},
			License:     "Apache-2.0",
			Repository:  "https://example.com/repo",
			Home:        "https://example.com",
			Keywords:    []string{"linter"},
			Deprecated:  false,
		},
		Spec: types.PluginSpec{
			Plugin:              "oci://hub.aggrega.ai/wasm/linter/publishing-linter@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			PluginType:          "linter",
			AbiVersion:          "v1alpha1",
			CompatibleProviders: []string{"helm", "compose"},
			Signature: &types.PluginSignature{
				Type:         "sigstore-cosign",
				SignatureRef: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				PublicKeyRef: "cosign.pub",
			},
			Runtime: &types.PluginRuntime{
				MaxMemoryPages: 16,
				TimeoutMs:      5000,
				HostFunctions:  []string{"log"},
				ConfigKV:       cfg,
			},
			Exports: []types.PluginExport{
				{Name: "lint_naming", Operates: "patch", Scope: "own", Selector: "workloads carrying a pod template"},
				{Name: "step", Operates: "emit", Scope: "host"},
			},
		},
	}
}

func maximalSchemaLibrary() types.SchemaLibrary {
	return types.SchemaLibrary{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "SchemaLibrary",
		Metadata:   types.SchemaLibraryMetadata{Name: "BaseComponents", Version: "1.0.0"},
		Spec: types.SchemaLibrarySpec{
			Schemas:   om[types.SchemaEntry]("Base", types.SchemaEntry{Content: mustNode("type: object\nproperties:\n  x: { type: string }")}),
			Templates: om[types.TemplateEntry]("Tmpl", types.TemplateEntry{Params: om[types.ParamDecl]("p", types.ParamDecl{}), Body: mustNode("type: object")}),
		},
	}
}

func maximalMappingLibrary() types.MappingLibrary {
	return types.MappingLibrary{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "MappingLibrary",
		Metadata:   types.MappingLibraryMetadata{Name: "BaseMappings", Version: "1.0.0"},
		Spec: types.MappingLibrarySpec{
			Mappings: om[types.MappingEntry]("Frag", types.MappingEntry{Body: "key: value"}),
		},
	}
}

func maximalAggregate() types.Aggregate {
	return types.Aggregate{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Aggregate",
		Metadata:   types.AggregateMetadata{Name: "app", Version: "1.0.0"},
		Spec: types.AggregateSpec{
			Parameters:         om[any]("db_size", map[string]any{"type": "string", "default": "10Gi"}),
			Aggregates:         om[types.SubAggregate]("data", types.SubAggregate{Type: "core/data-module:1.0.0", Parameters: om[any]("k", "v")}),
			ExportedComponents: om[string]("public-db", "internal-db"),
			Components: om[types.ComponentInstance]("api", types.ComponentInstance{
				Type:   "core/compute/api-server",
				Values: om[any]("port", 8080),
			}),
		},
	}
}

func maximalManifest() types.Manifest {
	comp := &types.MappingTier{}
	comp.Type.Set("core/datastore/mariadb:1.0.0", "compose/mariadb:1.0.0")
	return types.Manifest{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Manifest",
		Metadata:   types.ManifestMetadata{Name: "prod", Version: "1.0.0"},
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: comp},
		},
	}
}

func maximalRelease() types.Release {
	return types.Release{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Release",
		Metadata:   types.ReleaseMetadata{Name: "prod", Environment: "production", Version: "1.0.0"},
		Spec: types.ReleaseSpec{
			Aggregate: "app",
			Manifest:  "prod",
			Boundaries: om[types.Boundary]("main", types.Boundary{
				Directory: "./dist/main",
				Components: om[types.BoundaryComponent]("api", types.BoundaryComponent{
					Infra: om[any]("replicas", 2),
				}),
			}),
		},
	}
}
