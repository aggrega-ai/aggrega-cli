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
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

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
		APIVersion: "aggrega.ai/v1beta1",
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
			Labels:      om[string]("component.aggrega.ai/type", "server", "component.aggrega.ai/category", "compute"),
			Annotations: om[string]("a", "b"),
		},
		Spec: types.ComponentSpec{
			Extends:        "core/compute/base",
			FeaturesSchema: om[types.AttachmentSlot]("api_key", types.AttachmentSlot{Required: true, Inject: []string{"env"}, Description: "key"}),
			Inject:         []string{"env", "file"},
			UsesSchema: om[types.UsesSchemaEntry]("databases", types.UsesSchemaEntry{
				Selector:    "component.aggrega.ai/type=stateful-server",
				Required:    true,
				Inject:      []string{"env"},
				ValueSchema: mustNode("protocol: { type: string }"),
				InfraSchema: mustNode("pool_size: { type: integer }"),
				FeaturesSchema: om[types.AttachmentSlot]("credential", types.AttachmentSlot{
					Required:    true,
					Inject:      []string{"env"},
					Description: "the key this connection presents",
					Sensitive:   true,
					Selector:    "feature.aggrega.ai/kind=secret",
				}),
			}),
			// extends and authored pillar content are mutually exclusive on a
			// Component: an extending one receives its base's pillar block and
			// writes the inheritance marker instead. Both fields stay populated
			// here — Extends above, Schema as the marker — so neither loses its
			// admit-the-Go-field coverage.
			Schema:     mustNode(validator.InheritedSchemaMarker),
			Assertions: om[string]("rule", "{{ if lt .values.port 1024 }}{{ fail \"low\" }}{{ end }}"),
		},
	}
}

func maximalFeature() types.Feature {
	return types.Feature{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Feature",
		Metadata: types.FeatureMetadata{
			Name:    "core/data-masking",
			Version: "1.0.0",
			Labels:  om[string]("feature.aggrega.ai/kind", "secret"),
		},
		Spec: types.FeatureSpec{
			Extends:          "core/base-feature",
			FeaturesSchema:   om[types.AttachmentSlot]("token", types.AttachmentSlot{Required: true}),
			AppliesTo:        "component.aggrega.ai/type=server",
			AppliesToAnyOf:   []types.SelectorTerm{types.StringTerm("component.aggrega.ai/type=worker")},
			Interoperability: &types.Interoperability{ConflictsWith: []string{"core/other"}},
			UsesSchema:       om[types.UsesSchemaEntry]("backend", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"}),
			Schema:           mustNode("values:\n  level: { type: string }"),
			Assertions:       om[string]("rule", "{{ true }}"),
		},
	}
}

func maximalRecipe() types.Recipe {
	return types.Recipe{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: "mariadb", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "compose",
			Implements: "core/datastore/mariadb",
			Source:     "oci://example/chart:1.0.0",
			Locals:     om[string]("svc", "{{ .context.instance_id }}"),
			Provides:   om[string]("host", "{{ .context.instance_id }}"),
			Workload:   types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1beta1\nkind: Workload\ncontainers:\n  main:\n    image: x:1"},
			Pipeline:   []types.PipelineStep{{ID: "s1", Plugin: "p.wasm", Function: "render", With: "{}", Optional: true}},
			Omits:      []types.OmitEntry{{Kind: "Secret", Name: "{{ .context.rendered_name }}-token", Namespace: "apps", Subchart: "auth", When: "{{ true }}"}},
			Boundary: types.RecipeSpecBoundary{
				Artifacts: om[types.ArtifactEntry]("providers.tf", types.ArtifactEntry{Inline: "provider \"azurerm\" {\n  features {}\n}\n"}),
			},
			Assertions: om[string]("rule", "{{ true }}"),
		},
	}
}

func maximalFeatureRecipe() types.FeatureRecipe {
	return types.FeatureRecipe{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "FeatureRecipe",
		Metadata:   types.FeatureRecipeMetadata{Name: "dotenv-secret", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "compose",
			Implements:     "core/secret-value",
			Source:         "oci://example/chart:1.0.0",
			Locals:         om[string]("k", "v"),
			Provides:       om[string]("ref", "x"),
			InjectMapping:  "env:\n  {{ .values.key }}: \"{{ .values.origin }}\"",
			ExecutionScope: "component",
			Omits:          []types.OmitEntry{{Kind: "Secret", Name: "token", Namespace: "apps", Subchart: "auth", When: "{{ true }}"}},
			Assertions:     om[string]("rule", "{{ true }}"),
		},
	}
}

func maximalPlugin() types.Plugin {
	cfg := om[string]("LOG_LEVEL", "info")
	return types.Plugin{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Plugin",
		Metadata: types.PluginMetadata{
			Name:        "publishing-linter",
			Version:     "1.0.0",
			PluginType:  "linter",
			Description: "maximal wasm plugin",
			Maintainers: []types.Maintainer{{Name: "M", Email: "m@example.com", URL: "https://example.com"}},
			License:     "Apache-2.0",
			Repository:  "https://example.com/repo",
			Home:        "https://example.com",
			Keywords:    []string{"linter"},
		},
		Spec: types.PluginSpec{
			AbiVersion: "v1alpha1",
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
			Provider: types.PluginProvider{
				Capabilities:   []string{"helm", "compose"},
				WorkloadSchema: "type: object",
				WellKnownAnnotations: []types.WellKnownAnnotation{
					{
						Key:             "aggrega.ai/namespace",
						Purpose:         "target namespace",
						DefaultBehavior: "default namespace",
						Scope: []string{
							types.WellKnownAnnotationScopeSourced,
							types.WellKnownAnnotationScopeSynthesized,
							types.WellKnownAnnotationScopeArtifactsStandalone,
						},
					},
				},
				Contract: types.ProviderContract{
					InputKinds: types.PluginInputKinds{
						Sourced:             true,
						Synthesized:         true,
						ArtifactsStandalone: true,
					},
					BoundaryAssembly:   "merge",
					ArtifactDelimiters: &types.PluginArtifactDelimiters{Left: "[[", Right: "]]"},
				},
			},
			PipelineStep: &types.PluginPipelineStep{Capabilities: []string{"helm"}},
			Parser:       &types.PluginParser{Capabilities: []string{"parse:yaml"}},
			Importer: &types.PluginImporter{
				Capabilities: []string{"import:terraform"},
				Detect: []types.PluginDetectSignature{{
					Syntax: "json",
					Files:  []string{"*.tf.json"},
					Match: []types.PluginPathMatch{
						{Path: "resource", Is: types.IsObject},
						{Path: "terraform.required_version", Is: types.IsString, Prefix: ">="},
						{Path: "kind", Is: types.IsString, Equals: "Module"},
					},
				}},
			},
		},
	}
}

func maximalSchemaLibrary() types.SchemaLibrary {
	return types.SchemaLibrary{
		APIVersion: "aggrega.ai/v1beta1",
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
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "MappingLibrary",
		Metadata:   types.MappingLibraryMetadata{Name: "BaseMappings", Version: "1.0.0"},
		Spec: types.MappingLibrarySpec{
			Mappings: om[types.MappingEntry]("Frag", types.MappingEntry{Body: "key: value"}),
		},
	}
}

func maximalWorkloadLibrary() types.WorkloadLibrary {
	return types.WorkloadLibrary{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "WorkloadLibrary",
		Metadata:   types.WorkloadLibraryMetadata{Name: "BaseWorkloads", Version: "1.0.0"},
		Spec: types.WorkloadLibrarySpec{
			Workloads: om[types.WorkloadEntry]("Frag", types.WorkloadEntry{Body: "apiVersion: aggrega.ai/v1beta1\nkind: Workload"}),
		},
	}
}

// maximalArtifactLibrary carries a two-file bundle rather than the single body
// its sibling libraries hold, because a bundle addressing many files under one
// reference is the shape that distinguishes this kind. One body spans lines and
// one does not, so marshalling exercises both arms of the entry's literal-style
// choice; the whole metadata surface is populated so Direction-1 validates every
// optional field the schema must admit rather than only the required two.
func maximalArtifactLibrary() types.ArtifactLibrary {
	return types.ArtifactLibrary{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "ArtifactLibrary",
		Metadata: types.ArtifactLibraryMetadata{
			Name:        "BaseArtifacts",
			Version:     "1.0.0",
			Description: "maximal artifact library",
			Labels:      om[string]("l", "v"),
			Annotations: om[string]("a", "b"),
		},
		Spec: types.ArtifactLibrarySpec{
			Artifacts: om[types.ArtifactLibraryEntry]("OperatorSupport", types.ArtifactLibraryEntry{
				Files: om[string](
					"crd.yaml", "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition",
					"rbac.yaml", "kind: Role",
				),
			}),
		},
	}
}

func maximalAggregate() types.Aggregate {
	return types.Aggregate{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Aggregate",
		Metadata:   types.AggregateMetadata{Name: "app", Version: "1.0.0"},
		Spec: types.AggregateSpec{
			Parameters:         om[any]("db_size", map[string]any{"type": "string", "default": "10Gi"}),
			Aggregates:         om[types.SubAggregate]("data", types.SubAggregate{Type: "core/data-module:1.0.0", Parameters: om[any]("k", "v")}),
			ExportedComponents: om[string]("public-db", "internal-db"),
			Components: om[types.ComponentInstance]("api", types.ComponentInstance{
				Type:   "core/compute/api-server",
				Values: om[any]("port", 8080),
				// A connection carrying an attachment, so the connection entry's
				// attachment surface is marshalled and validated against the
				// component's connection def rather than left unexercised.
				Uses: om[types.UsesSocketValue]("databases", types.UsesSocketValue{
					Verbose: om[types.VerboseUsesEntry]("primary", types.VerboseUsesEntry{
						Target: "db",
						Values: om[any]("pool", 5),
						Features: om[types.ConnectionAttachment]("credential", types.ConnectionAttachment{
							Type:        "core/referenced-secret",
							Description: "the key this connection presents",
							Values:      om[any]("origin", "team-a"),
						}),
					}),
				}),
			}),
		},
	}
}

func maximalManifest() types.Manifest {
	comp := &types.MappingTier{}
	comp.Type.Set("core/datastore/mariadb:1.0.0", "compose/mariadb:1.0.0")
	return types.Manifest{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Manifest",
		Metadata:   types.ManifestMetadata{Name: "prod", Version: "1.0.0"},
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: comp},
		},
	}
}

func maximalRelease() types.Release {
	return types.Release{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Release",
		Metadata:   types.ReleaseMetadata{Name: "prod", Environment: "production", Version: "1.0.0"},
		Spec: types.ReleaseSpec{
			Aggregate: "app",
			Manifest:  "prod",
			Boundaries: om[types.Boundary]("main", types.Boundary{
				Directory: "./dist/main",
				Components: om[types.BoundaryComponent]("api", types.BoundaryComponent{
					Infra: om[any]("replicas", 2),
					Uses: om[types.OrderedMap[types.ReleaseUsesEntry]]("databases",
						om[types.ReleaseUsesEntry]("primary", types.ReleaseUsesEntry{
							Target: "db",
							Infra:  om[any]("storage_class", "fast"),
							Features: om[types.ReleaseConnectionAttachment]("credential", types.ReleaseConnectionAttachment{
								Type:        "core/referenced-secret",
								Description: "sized by the operator",
								Infra:       om[any]("rotation", "hourly"),
							}),
						})),
				}),
			}),
		},
	}
}

func ptr[T any](v T) *T { return &v }

// maximalWorkloadCompute populates the compute arm of the workload surface. The
// document root makes the compute fields and the reference fields mutually
// exclusive, so the two arms are separate instances; this one carries the
// containers, volumes, services and pod-level settings, and the reference
// constructor below carries what this one may not declare.
//
// The instance maximises schema surface rather than modelling a deployment
// anyone would author: it takes the richest form and then populates every field
// that form admits. Each closed disjunction is spread across sibling positions
// rather than stacked onto one — the five volume variants become five volumes,
// the three probe shapes become the three probe slots, the two lifecycle-hook
// forms become the two hooks, and the two bulk-environment sources become two
// entries — because stacking two variants on one position satisfies two branches
// of an exactly-one disjunction and is rejected.
func maximalWorkloadCompute() types.Workload {
	return types.Workload{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Workload",
		Form:       "stateful",
		Schedule:   "@daily",
		Replicas:   ptr(2),
		InitContainers: []types.ContainerSpec{{
			Name:    "await-db",
			Image:   "busybox:1.36",
			Command: []string{"sh", "-c"},
			Args:    []string{"until nc -z db 5432; do sleep 1; done"},
		}},
		Containers: om[types.ContainerSpec]("api", types.ContainerSpec{
			Name:            "api",
			Image:           "example.com/api:1.0.0",
			ImagePullPolicy: "IfNotPresent",
			Command:         []string{"/bin/api"},
			Args:            []string{"--serve"},
			Variables:       om[string]("LOG_LEVEL", "info"),
			Attachments: om[types.AttachmentRef](
				"db", types.AttachmentRef{From: "db", As: "env", Key: "password"},
				"tls", types.AttachmentRef{From: "tls", As: "file", Path: "/etc/tls/server.crt"},
				"model", types.AttachmentRef{From: "credential", As: "env", Connection: "gpt"},
			),
			EnvFrom: []types.EnvFromSource{
				{ConfigMap: "api-settings", Prefix: "CFG_"},
				{Secret: "db", Prefix: "DB_"},
				{Secret: "credential", Connection: "gpt", Prefix: "GPT_"},
			},
			Ports: om[types.PortSpec]("http", types.PortSpec{Port: 8080}),
			Resources: &types.ResourceSpec{
				Requests: &types.ResourceList{CPU: "100m", Memory: "128Mi", Extended: om[string]("nvidia.com/gpu", "1")},
				Limits:   &types.ResourceList{CPU: "500m", Memory: "512Mi"},
			},
			Health: &types.HealthSpec{
				Ready:   &types.ProbeSpec{HTTP: "/readyz", TimeoutSeconds: ptr(2), PeriodSeconds: ptr(10)},
				Live:    &types.ProbeSpec{TCP: 8080, InitialDelaySeconds: ptr(5), FailureThreshold: ptr(3)},
				Startup: &types.ProbeSpec{Exec: []string{"/bin/api", "--check"}, SuccessThreshold: ptr(1)},
			},
			VolumeMounts: []types.VolumeMount{{
				Name: "data", Path: "/var/lib/api", SubPath: "current", ReadOnly: true,
			}},
			RestartPolicy: "Always",
			Lifecycle: &types.LifecycleSpec{
				PostStart: &types.LifecycleHookSpec{Exec: []string{"/bin/warm"}},
				PreStop:   &types.LifecycleHookSpec{HTTPGet: map[string]any{"path": "/drain", "port": 8080}},
			},
			SecurityContext: &types.ContainerSecurityContext{
				RunAsUser:                ptr(1000),
				RunAsGroup:               ptr(1000),
				RunAsNonRoot:             ptr(true),
				ReadOnlyRootFilesystem:   ptr(true),
				AllowPrivilegeEscalation: ptr(false),
				Capabilities:             &types.Capabilities{Add: []string{"NET_BIND_SERVICE"}, Drop: []string{"ALL"}},
			},
		}),
		Volumes: om[types.VolumeSpec](
			"scratch", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: "1Gi"}},
			"settings", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "api-settings", Items: []any{map[string]any{"key": "app.conf", "path": "app.conf"}}}},
			"certs", types.VolumeSpec{Secret: &types.SecretVolume{Source: "api-tls", Items: []any{map[string]any{"key": "tls.crt", "path": "tls.crt"}}}},
			"shared", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10Gi", Class: "fast", AccessModes: []string{"ReadWriteMany"}}},
			"data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "20Gi", Class: "fast", AccessModes: []string{"ReadWriteOnce"}}},
		),
		Services: om[types.ServiceSpec]("api", types.ServiceSpec{
			Type:  "ClusterIP",
			Ports: om[int]("http", 8080),
		}),
		Scheduling: &types.Scheduling{
			NodeSelector:              map[string]string{"kubernetes.io/arch": "amd64"},
			Affinity:                  map[string]any{"podAntiAffinity": map[string]any{}},
			Tolerations:               []any{map[string]any{"key": "dedicated", "operator": "Equal", "value": "api"}},
			TopologySpreadConstraints: []any{map[string]any{"maxSkew": 1, "topologyKey": "kubernetes.io/hostname"}},
		},
		PodSecurityContext: &types.PodSecurityContext{
			RunAsUser:          ptr(1000),
			RunAsGroup:         ptr(1000),
			FSGroup:            ptr(2000),
			SupplementalGroups: []int{3000},
		},
		ServiceAccount:                "api-server",
		ImagePullSecrets:              []string{"registry-credentials"},
		PriorityClassName:             "high-priority",
		RuntimeClassName:              "nvidia",
		TerminationGracePeriodSeconds: ptr(30),
		DNSPolicy:                     "ClusterFirst",
		DNSConfig: &types.DNSConfig{
			Nameservers: []string{"10.0.0.10"},
			Searches:    []string{"svc.cluster.local"},
			Options:     []any{map[string]any{"name": "ndots", "value": "2"}},
		},
		HostAliases: []types.HostAlias{{IP: "10.0.0.20", Hostnames: []string{"legacy.internal"}}},
	}
}

// maximalWorkloadReference populates the reference arm: an external endpoint
// with no compute. The document root forbids the compute fields here, so this
// instance exists to exercise the two Go fields the compute instance may not
// declare — without it, they would be checked in neither direction.
func maximalWorkloadReference() types.Workload {
	return types.Workload{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Workload",
		Form:       "reference",
		Endpoint:   "db.managed.example.com",
		Port:       5432,
	}
}

// maximalAggregaConfig populates every settings block of the workspace
// configuration, including each merge-time `_append` companion and both
// placements of the signing block (top level and per registry). The file carries
// no envelope — it is flat top-level keys — which is why it is constructed from
// the configuration root type rather than from an entity struct.
//
// The socket lists take three different attachment values rather than one shared
// value, because the sockets do not admit the same fields and no single value is
// valid at all eight positions. A socket whose entry function an attachment may
// name declares `function`; a socket whose entry point is fixed does not declare
// it, so the schema refuses an attachment carrying it there. The cross-cutting
// sockets attach by uri and reject a capability binding, while a provider binding
// is a capability binding and rejects both `function` and `with`. One capability
// binds one provider across the composed list, so the base and append provider
// lists name different capabilities. Each value therefore carries exactly what
// its own position admits, which makes this instance a configuration the product
// would really load rather than one only the schema tolerates.
//
// The MCP block fills the transport, the listener's address and certificate,
// the origins, both trusted-proxy headers and the caps, because the instance is
// what this direction validates: a field left at its zero value is a field the
// schema is never asked about, and it passes for want of a constraint rather
// than for being right. The trusted-proxy block is filled for that reason even
// though a configuration may leave it out: once present, its principal header
// must name a header, and an empty one is refused. Its values describe one
// server that could really run — a listener on loopback terminating TLS from
// the certificate and key it names, reading its caller from a proxy in front,
// which is why the address is loopback, an origin in the full form a browser
// sends, and every cap at its documented default. The authorization server's addresses and key
// file, the withdrawn reach, the announced tools and the health address are
// left at their zero values.
func maximalAggregaConfig() workspaceconfig.Config {
	signing := workspaceconfig.SigningConfig{
		FulcioURL:    "https://fulcio.example.com",
		RekorURL:     "https://rekor.example.com",
		TimestampURL: "https://tsa.example.com",
		KeyPath:      "/keys/signing.pem",
		Required:     true,
		Kinds:        []string{"Component", "Plugin"},
	}
	with := func() *workspaceconfig.PluginRefWith {
		return &workspaceconfig.PluginRefWith{
			Sources:  []string{"rules.rego"},
			Settings: map[string]any{"prefix": "example.com/"},
		}
	}
	selectableEntry := []workspaceconfig.PluginRef{{
		URI:      "governance/label-normaliser",
		Function: "mutate",
		Role:     plugintype.RoleLabelTransform,
		With:     with(),
	}}
	fixedEntry := []workspaceconfig.PluginRef{{
		URI:  "importer/demo",
		Role: plugintype.RoleLabelTransform,
		With: with(),
	}}
	providerBinding := func(capability string) []workspaceconfig.PluginRef {
		return []workspaceconfig.PluginRef{{
			URI:        "provider/" + capability,
			Capability: capability,
			Role:       plugintype.RoleLabelTransform,
		}}
	}
	return workspaceconfig.Config{
		Workspace: workspaceconfig.WorkspaceConfig{
			CatalogDirs:       []string{"./catalog"},
			CatalogDirsAppend: []string{"./catalog-extra"},
		},
		DefaultRegistry:  "hub.aggrega.ai",
		Offline:          true,
		AutoPull:         true,
		PinSourcesAtPull: true,
		PinSourcesAtPush: true,
		ArtifactCacheDir: ".aggrega/cache",
		Build: workspaceconfig.BuildConfig{
			AuthoringRelease:         "./releases/prod.yaml",
			HaltOnDeprecatedSeverity: "error",
			YAMLIndent:               2,
		},
		Plugins: workspaceconfig.PluginsConfig{
			Governance:       selectableEntry,
			GovernanceAppend: selectableEntry,
			Linter:           selectableEntry,
			LinterAppend:     selectableEntry,
			Importer:         fixedEntry,
			ImporterAppend:   fixedEntry,
			Providers:        providerBinding("helm"),
			ProvidersAppend:  providerBinding("compose"),
			CLI: []workspaceconfig.CLIPluginRef{{
				URI:    "oci://hub.aggrega.ai/plugin/cli/greet:1.2.0",
				Digest: pinDigest,
			}},
		},
		Lint: workspaceconfig.LintConfig{
			PublishingDiscipline: workspaceconfig.PublishingDisciplineConfig{
				AllowDynamicRefs:                    true,
				AllowIDOverrides:                    true,
				AllowDirectXAggregaExtensions:       true,
				AllowAggregaAIPrefix:                true,
				AllowNonstandardNames:               true,
				AllowCategoryMismatches:             true,
				AllowUndeclaredRecipeProvides:       true,
				AllowConnectionCredentialMismatches: true,
			},
		},
		ParseCache:  workspaceconfig.ParseCacheConfig{MaxEntries: 1000, MaxBytes: 100_000_000},
		Concurrency: workspaceconfig.ConcurrencyConfig{PoolSize: 4},
		Serve:       workspaceconfig.ServeConfig{BuildOnInspect: true, LintOnValidate: true},
		MCP: workspaceconfig.MCPConfig{
			Transport: workspaceconfig.MCPTransportHTTP,
			Listen:    "127.0.0.1:8443",
			TLS: workspaceconfig.MCPTLSConfig{
				CertFile: "/certs/mcp-chain.pem",
				KeyFile:  "/certs/mcp-key.pem",
			},
			AllowedOrigins: []string{"https://app.example.com"},
			Limits: workspaceconfig.MCPLimitsConfig{
				MaxBodyBytes:          4_194_304,
				MaxConcurrentRequests: 16,
				RequestsPerMinute:     120,
			},
			Authorization: workspaceconfig.MCPAuthorizationConfig{
				TrustedProxy: workspaceconfig.MCPTrustedProxyConfig{
					PrincipalHeader: "X-Forwarded-User",
					ScopesHeader:    "X-Forwarded-Scopes",
				},
			},
		},
		Signing: signing,
		Registries: []workspaceconfig.RegistryConfig{{
			Host:                         "hub.aggrega.ai",
			Name:                         "hub",
			TrustedIdentities:            []workspaceconfig.TrustedIdentity{{Identity: "signer@example.com", Issuer: "https://issuer.example.com"}},
			TrustedDeprecationIdentities: []workspaceconfig.TrustedIdentity{{IdentityRegexp: "^.*@example[.]com$", Issuer: "https://issuer.example.com"}},
			PublicKey:                    "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n",
			TrustedRoot:                  "/keys/trust-root.json",
			Advisory:                     true,
			SkipSignatureVerification:    true,
			Signing:                      signing,
		}},
		SkipSignatureVerification: true,
	}
}

// pinDigest is a syntactically well-formed content digest, and lockfileSHA256 a
// well-formed bare hex SHA-256. The lockfile schema constrains the external-input
// pin to bare lowercase hex, so the two spellings are not interchangeable.
const (
	pinDigest      = "sha256:" + lockfileSHA256
	lockfileSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// maximalLockfile populates every section of the pin record, including the
// optional per-entry fields, so Direction 1 exercises the whole surface of the
// kind that anchors reproducible builds. Each closed vocabulary — signature mode,
// deprecation severity, transform and verdict role, source-artifact kind,
// referencing-entity kind — is populated with an admitted member, so a value the
// schema would reject surfaces here rather than at a user's pull. The plugin row
// records its content digest as its resolved version, as a pull writes it: a
// plugin artifact is pinned by its bytes rather than named by a version.
func maximalLockfile() lockfile.Lockfile {
	sig := &lockfile.VerifiedSignature{
		BundleDigest: pinDigest,
		Identity:     "signer@example.com",
		Issuer:       "https://issuer.example.com",
		Mode:         "keyless",
	}
	deprecation := &lockfile.DeprecationRecord{
		Message:  "superseded; migrate to the 2.x line",
		Severity: "warning",
		Signer:   sig,
	}
	consumers := []lockfile.LibraryConsumer{{
		ConsumerURI: "core/compute/api-server:1.0.0",
		Fragments:   []string{"#/schemas/PortSpec"},
	}}
	return lockfile.Lockfile{
		Version:     1,
		Release:     "prod",
		GeneratedAt: "2026-01-01T00:00:00Z",
		ReleaseDocuments: []lockfile.ReleaseDocument{
			{Path: "prod.yaml", Digest: pinDigest},
			{Path: "../shared/base-release.yaml", Digest: pinDigest},
		},
		Entities: []lockfile.Entity{{
			Kind: "component", URI: "core/compute/api-server:1.0.0",
			ResolvedVersion: "1.0.0",
			Digest:          pinDigest,
			ManifestDigest:  pinDigest,
			ReferencedFrom:  []string{"release.spec.boundaries.main.components.api"},
			Deprecation:     deprecation,
			Signature:       sig,
		}},
		ExternalInputs: []lockfile.ExternalInput{{
			RequiredBy: []string{"recipe.helm.api-server.spec.pipeline"},
			Sha256:     lockfileSHA256,
			Size:       2048,
			URL:        "https://example.com/values.yaml",
		}},
		MappingLibraries: []lockfile.MappingLibrary{{
			URI:             "core/mappings/common:1.0.0",
			ResolvedVersion: "1.0.0",
			Digest:          pinDigest,
			Consumers:       consumers,
		}},
		SchemaLibraries: []lockfile.SchemaLibrary{{
			URI:             "core/schemas/common:1.0.0",
			ResolvedVersion: "1.0.0",
			Digest:          pinDigest,
			Consumers:       consumers,
		}},
		WorkloadLibraries: []lockfile.WorkloadLibrary{{
			URI:             "core/workloads/common:1.0.0",
			ResolvedVersion: "1.0.0",
			Digest:          pinDigest,
			Consumers:       consumers,
		}},
		ArtifactLibraries: []lockfile.ArtifactLibrary{{
			URI:             "core/artifacts/common:1.0.0",
			ResolvedVersion: "1.0.0",
			Digest:          pinDigest,
			Consumers:       consumers,
		}},
		Mutators: []lockfile.MutatorAttachment{{
			Function: "mutate",
			Plugin:   "governance/label-normaliser",
			Role:     "label-transform",
			Settings: map[string]any{"prefix": "example.com/"},
			Sources: []lockfile.MutatorSourcePin{{
				Content: "package example\n",
				Digest:  pinDigest,
				Path:    "rules.rego",
			}},
		}},
		VerdictInputs: []lockfile.VerdictInput{{
			Plugin: "oci://registry.example.com/governance/conftest:1.0.0",
			Role:   "ir-verdict",
			Sources: []lockfile.VerdictSourcePin{{
				Digest: pinDigest,
				Path:   "policy.rego",
			}},
		}},
		Plugins: []lockfile.Plugin{{
			URI:             "provider/helm:1.0.0",
			ResolvedVersion: pinDigest,
			Digest:          pinDigest,
			ManifestDigest:  pinDigest,
			ReferencedFrom:  []string{"recipe.helm.api-server.spec.capability"},
			Deprecation:     deprecation,
			Signature:       sig,
		}},
		Providers: []lockfile.ProviderBinding{{
			Capability: "helm",
			Plugin:     "provider/helm",
		}},
		SourceArtifacts: []lockfile.SourceArtifact{{
			Kind:           "helm-oci-chart",
			URL:            "oci://registry.example.com/charts",
			Chart:          "api-server",
			PinnedVersion:  "1.0.0",
			Digest:         pinDigest,
			ResolvedURL:    "oci://registry.example.com/charts/api-server:1.0.0",
			ManifestDigest: pinDigest,
			Mediatype:      "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
			ReferencedBy: []lockfile.SourceArtifactReference{{
				Entity:       "recipe",
				URI:          "helm/api-server:1.0.0",
				Field:        "spec.source",
				RequestedRef: "v1.0.0",
			}},
		}},
	}
}

// maximalEntityLock populates every field of the entity lock: the schema version
// and one fully-populated row of each pin class. The source row carries the two
// optional identity knobs (a chart within a repository root, a directory within
// a repository) and the optional manifest digest as well as the four required
// fields, because those optional positions are exactly what a maximal instance
// exists to reach; no real source carries both knobs at once, and the instance
// is a field-surface probe rather than an authored document.
func maximalEntityLock() recipepin.Lock {
	return recipepin.Lock{
		Version: recipepin.CurrentLockVersion,
		SourceDigests: []types.SourceDigest{{
			Kind:           "helm-oci-chart",
			URL:            "oci://registry-1.docker.io/bitnamicharts/mariadb",
			Chart:          "mariadb",
			ModuleSubdir:   "modules/database",
			Version:        "1.0.0",
			Digest:         pinDigest,
			ManifestDigest: pinDigest,
		}},
		ArtifactDigests: []types.ArtifactDigest{{
			Name:   "networkpolicy.yaml",
			Digest: pinDigest,
		}},
		LocalSourceDigests: []types.LocalSourceDigest{{
			Path:   "chart/templates/deployment.yaml",
			Digest: pinDigest,
		}},
	}
}

// maximalPluginsTrust populates every field of the trust record: the schema
// version and one fully-populated record. The record's three identity fields are
// required by the schema and its withdrawal and signature fact are optional, so a
// single record carrying all five covers the whole surface — the optional ones
// included, which are the fields a maximal instance exists to reach. The signature
// fact is keyless so its issuer, the one field a keyed fact omits, is populated.
func maximalPluginsTrust() pluginstrust.Trust {
	return pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          "governance/aggrega-security-baseline",
			ResolvedVersion: "1.0.0",
			Digest:          pinDigest,
			Deprecation: &pluginstrust.Deprecation{
				Message:  "withdrawn; migrate to governance/aggrega-security-baseline 2",
				Severity: "retracted",
			},
			Signature: &lockfile.VerifiedSignature{
				BundleDigest: pinDigest,
				Identity:     "signer@example.com",
				Issuer:       "https://issuer.example.com",
				Mode:         "keyless",
			},
		}},
	}
}
