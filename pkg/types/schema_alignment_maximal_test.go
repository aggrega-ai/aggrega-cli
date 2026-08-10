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
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
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
		APIVersion: "aggrega.ai/v1alpha1",
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
			Capability: "compose",
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
			Capability:     "compose",
			Implements:     "core/secret-value",
			Locals:         om[string]("k", "v"),
			Provides:       om[string]("ref", "x"),
			InjectMapping:  "env:\n  {{ .values.key }}: \"{{ .values.origin }}\"",
			ExecutionScope: "component",
			Assertions:     om[string]("rule", "{{ true }}"),
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
			Provider: &types.PluginProvider{
				Capabilities:   []string{"helm", "compose"},
				WorkloadSchema: "type: object",
				WellKnownAnnotations: []types.WellKnownAnnotation{
					{Key: "aggrega.ai/namespace", Purpose: "target namespace", DefaultBehavior: "default namespace"},
				},
				Contract: &types.ProviderContract{
					InputKinds: types.PluginInputKinds{
						Sourced:             true,
						Synthesized:         true,
						ArtifactsStandalone: true,
					},
					BoundaryAssembly:   "merge",
					ArtifactDelimiters: &types.PluginArtifactDelimiters{Left: "[[", Right: "]]"},
					SourceFilter:       []string{"templates/"},
				},
			},
			PipelineStep: &types.PluginPipelineStep{Capabilities: []string{"helm"}},
			Parser:       &types.PluginParser{Capabilities: []string{"parse:yaml"}},
			Importer:     &types.PluginImporter{Capabilities: []string{"import:terraform"}},
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

func maximalWorkloadLibrary() types.WorkloadLibrary {
	return types.WorkloadLibrary{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "WorkloadLibrary",
		Metadata:   types.WorkloadLibraryMetadata{Name: "BaseWorkloads", Version: "1.0.0"},
		Spec: types.WorkloadLibrarySpec{
			Workloads: om[types.WorkloadEntry]("Frag", types.WorkloadEntry{Body: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"}),
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
		APIVersion: "aggrega.ai/v1alpha1",
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
			),
			EnvFrom: []types.EnvFromSource{
				{ConfigMap: "api-settings", Prefix: "CFG_"},
				{Secret: "db", Prefix: "DB_"},
			},
			Ports: om[int]("http", 8080),
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
		APIVersion: "aggrega.ai/v1alpha1",
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
func maximalAggregaConfig() workspaceconfig.Config {
	signing := workspaceconfig.SigningConfig{
		FulcioURL:    "https://fulcio.example.com",
		RekorURL:     "https://rekor.example.com",
		TimestampURL: "https://tsa.example.com",
		KeyPath:      "/keys/signing.pem",
		Required:     true,
		Kinds:        []string{"Component", "Plugin"},
	}
	attachment := []workspaceconfig.PluginRef{{
		URI:        "governance/label-normaliser",
		Capability: "helm",
		Function:   "mutate",
		Role:       plugintype.RoleLabelTransform,
		With: &workspaceconfig.PluginRefWith{
			Sources:  []string{"rules.rego"},
			Settings: map[string]any{"prefix": "example.com/"},
		},
	}}
	return workspaceconfig.Config{
		Workspace: workspaceconfig.WorkspaceConfig{
			CatalogDirs:       []string{"./catalog"},
			CatalogDirsAppend: []string{"./catalog-extra"},
		},
		DefaultRegistry:  "hub.aggrega.ai",
		Offline:          true,
		AutoPull:         true,
		ArtifactCacheDir: ".aggrega/cache",
		Build: workspaceconfig.BuildConfig{
			AuthoringRelease:         "./releases/prod.yaml",
			HaltOnDeprecatedSeverity: "error",
			YAMLIndent:               2,
		},
		Plugins: workspaceconfig.PluginsConfig{
			Governance:       attachment,
			GovernanceAppend: attachment,
			Linter:           attachment,
			LinterAppend:     attachment,
			Importer:         attachment,
			ImporterAppend:   attachment,
			Providers:        attachment,
			ProvidersAppend:  attachment,
			CLI: []workspaceconfig.CLIPluginRef{{
				URI:    "oci://hub.aggrega.ai/plugin/cli/greet:1.2.0",
				Digest: pinDigest,
			}},
		},
		Lint: workspaceconfig.LintConfig{
			PublishingDiscipline: workspaceconfig.PublishingDisciplineConfig{
				AllowDynamicRefs:              true,
				AllowIDOverrides:              true,
				AllowDirectXAggregaExtensions: true,
				AllowAggregaAIPrefix:          true,
				AllowNonstandardNames:         true,
				AllowCategoryMismatches:       true,
			},
		},
		ParseCache:  workspaceconfig.ParseCacheConfig{MaxEntries: 1000, MaxBytes: 100_000_000},
		Concurrency: workspaceconfig.ConcurrencyConfig{PoolSize: 4},
		Serve:       workspaceconfig.ServeConfig{BuildOnInspect: true},
		Signing:     signing,
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
// schema would reject surfaces here rather than at a user's pull.
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
		Entities: []lockfile.Entity{{
			URI:             "core/compute/api-server:1.0.0",
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
		ArtifactsLibraries: []lockfile.ArtifactsLibrary{{
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
			ResolvedVersion: "1.0.0",
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
