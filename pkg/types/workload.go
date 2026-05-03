// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Workload struct surface — the platform-owned `aggrega.ai/v1alpha1` Workload
// kind referenced by Recipes that adopt the synthesized input kind. Field
// shapes mirror workload-v1alpha1.schema.json in the aggrega-schemas repo and are
// constrained at runtime by the embedded CUE schema at
// pkg/schemas/cue/workload.cue.
//
// Validation flow: yaml.Unmarshal decodes into typed Go fields; the consumer
// (Phase B.2) calls validator.ValidateWorkload to unify the parsed value
// against the embedded CUE. Closed-disjunction discriminators (Volume's five
// variants, Probe's three shapes, Lifecycle hook's two forms, ContainerSecret's
// env/file split) are not enforced at the Go type level — pointer fields can
// all be set simultaneously by programmatic construction. Mutual-exclusion is
// the validator's job; consumers MUST call ValidateWorkload before trusting
// the parsed shape.

package types

// Workload is the platform-owned synthesisable deployment unit.
type Workload struct {
	APIVersion         string                    `json:"apiVersion" yaml:"apiVersion"`
	Kind               string                    `json:"kind" yaml:"kind"`
	Metadata           WorkloadMetadata          `json:"metadata" yaml:"metadata"`
	Replicas           *int                      `json:"replicas,omitempty" yaml:"replicas,omitempty"`
	InitContainers     []ContainerSpec           `json:"initContainers,omitempty" yaml:"initContainers,omitempty"`
	Containers         OrderedMap[ContainerSpec] `json:"containers,omitempty,omitzero" yaml:"containers,omitempty"`
	Volumes            OrderedMap[VolumeSpec]    `json:"volumes,omitempty,omitzero" yaml:"volumes,omitempty"`
	Services           OrderedMap[ServiceSpec]   `json:"services,omitempty,omitzero" yaml:"services,omitempty"`
	Scheduling         *Scheduling               `json:"scheduling,omitempty" yaml:"scheduling,omitempty"`
	PodSecurityContext *PodSecurityContext       `json:"podSecurityContext,omitempty" yaml:"podSecurityContext,omitempty"`
	ServiceAccount     string                    `json:"serviceAccount,omitempty" yaml:"serviceAccount,omitempty"`
}

// WorkloadMetadata carries optional labels and annotations on the
// Workload. Both fields are typed map[string]string — the
// synthesis-intermediate shape, parallel to the K8s-shaped
// pass-through fields documented at
// aggrega-docs/language-reference/entities/workload.md § Validation
// surface. Workload values are programmatically constructed by Recipe
// synthesis or parsed from yaml.v3 default unmarshal of templated
// output; they do not originate as author-declared YAML, so
// author-key-order preservation is not load-bearing at this layer
// (which would otherwise call for OrderedMap[string], the typing
// AggregateMetadata.Labels uses for author-declared module-level
// labels).
type WorkloadMetadata struct {
	Name        string            `json:"name,omitempty" yaml:"name,omitempty"`
	Version     string            `json:"version,omitempty" yaml:"version,omitempty"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// ContainerSpec describes one container in a Workload.
//
// Variables / Secrets / Ports preserve author order (OrderedMap); other
// fields are typed Go values decoded directly by yaml.Unmarshal.
type ContainerSpec struct {
	Image           string                    `json:"image,omitempty" yaml:"image,omitempty"`
	Command         []string                  `json:"command,omitempty" yaml:"command,omitempty"`
	Args            []string                  `json:"args,omitempty" yaml:"args,omitempty"`
	Variables       OrderedMap[string]        `json:"variables,omitempty,omitzero" yaml:"variables,omitempty"`
	Secrets         OrderedMap[SecretRef]     `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
	Ports           OrderedMap[int]           `json:"ports,omitempty,omitzero" yaml:"ports,omitempty"`
	Resources       *ResourceSpec             `json:"resources,omitempty" yaml:"resources,omitempty"`
	Health          *HealthSpec               `json:"health,omitempty" yaml:"health,omitempty"`
	VolumeMounts    []VolumeMount             `json:"volumeMounts,omitempty" yaml:"volumeMounts,omitempty"`
	RestartPolicy   string                    `json:"restartPolicy,omitempty" yaml:"restartPolicy,omitempty"`
	Lifecycle       *LifecycleSpec            `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
	SecurityContext *ContainerSecurityContext `json:"securityContext,omitempty" yaml:"securityContext,omitempty"`
}

// SecretRef references a Component secretsSchema slot. As is "env" or "file";
// when As == "file", Path is required (the in-container mount path).
//
// Mutual exclusion of env vs file is a CUE-validation rule, not a Go-type
// constraint — both fields can be set simultaneously by programmatic
// construction. Validate via validator.ValidateWorkload.
type SecretRef struct {
	From string `json:"from,omitempty" yaml:"from,omitempty"`
	As   string `json:"as,omitempty" yaml:"as,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// VolumeSpec is a closed disjunction over five variants. Exactly one pointer
// field should be set; mutual exclusion is enforced by CUE validation, not
// Go-type structure. Programmatic construction can produce invalid states
// (multiple variants set) that bypass validation if no explicit
// validator.ValidateWorkload call follows — provider synthesisers should
// treat "more than one variant set" as a render-time error.
type VolumeSpec struct {
	Ephemeral          *EphemeralVolume          `json:"ephemeral,omitempty" yaml:"ephemeral,omitempty"`
	ConfigMap          *ConfigMapVolume          `json:"configmap,omitempty" yaml:"configmap,omitempty"`
	Secret             *SecretVolume             `json:"secret,omitempty" yaml:"secret,omitempty"`
	Persistent         *PersistentVolume         `json:"persistent,omitempty" yaml:"persistent,omitempty"`
	PersistentTemplate *PersistentTemplateVolume `json:"persistentTemplate,omitempty" yaml:"persistentTemplate,omitempty"`
}

// EphemeralVolume — emptyDir-equivalent.
type EphemeralVolume struct {
	SizeLimit string `json:"sizeLimit,omitempty" yaml:"sizeLimit,omitempty"`
}

// ConfigMapVolume — references an existing ConfigMap by name.
type ConfigMapVolume struct {
	Source string `json:"source" yaml:"source"`
	Items  []any  `json:"items,omitempty" yaml:"items,omitempty"`
}

// SecretVolume — references an existing Secret by name.
type SecretVolume struct {
	Source string `json:"source" yaml:"source"`
	Items  []any  `json:"items,omitempty" yaml:"items,omitempty"`
}

// PersistentVolume — Deployment-compatible PVC.
type PersistentVolume struct {
	Size  string `json:"size" yaml:"size"`
	Class string `json:"class,omitempty" yaml:"class,omitempty"`
}

// PersistentTemplateVolume — StatefulSet-compatible volumeClaimTemplate.
// Triggers StatefulSet selection when any volume in the Workload uses this
// variant.
type PersistentTemplateVolume struct {
	Size  string `json:"size" yaml:"size"`
	Class string `json:"class,omitempty" yaml:"class,omitempty"`
}

// ServiceSpec describes one Service in a Workload. Type is one of
// ClusterIP / NodePort / LoadBalancer / Headless (validated by CUE).
type ServiceSpec struct {
	Type  string          `json:"type" yaml:"type"`
	Ports OrderedMap[int] `json:"ports,omitempty,omitzero" yaml:"ports,omitempty"`
}

// ResourceSpec carries container resource requests and limits.
type ResourceSpec struct {
	Requests *ResourceList `json:"requests,omitempty" yaml:"requests,omitempty"`
	Limits   *ResourceList `json:"limits,omitempty" yaml:"limits,omitempty"`
}

// ResourceList — CPU and memory as Kubernetes-shaped quantity strings.
type ResourceList struct {
	CPU    string `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory string `json:"memory,omitempty" yaml:"memory,omitempty"`
}

// HealthSpec carries readiness, liveness, and startup probes.
type HealthSpec struct {
	Ready   *ProbeSpec `json:"ready,omitempty" yaml:"ready,omitempty"`
	Live    *ProbeSpec `json:"live,omitempty" yaml:"live,omitempty"`
	Startup *ProbeSpec `json:"startup,omitempty" yaml:"startup,omitempty"`
}

// ProbeSpec is a closed disjunction over three shapes (http / tcp / exec).
// Mutual exclusion is enforced by CUE validation, not Go-type structure.
type ProbeSpec struct {
	HTTP                string   `json:"http,omitempty" yaml:"http,omitempty"`
	TCP                 int      `json:"tcp,omitempty" yaml:"tcp,omitempty"`
	Exec                []string `json:"exec,omitempty" yaml:"exec,omitempty"`
	TimeoutSeconds      *int     `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
	InitialDelaySeconds *int     `json:"initialDelaySeconds,omitempty" yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       *int     `json:"periodSeconds,omitempty" yaml:"periodSeconds,omitempty"`
	FailureThreshold    *int     `json:"failureThreshold,omitempty" yaml:"failureThreshold,omitempty"`
	SuccessThreshold    *int     `json:"successThreshold,omitempty" yaml:"successThreshold,omitempty"`
}

// VolumeMount references a Workload Volume by name and mounts it at Path.
type VolumeMount struct {
	Name     string `json:"name" yaml:"name"`
	Path     string `json:"path" yaml:"path"`
	ReadOnly bool   `json:"readonly,omitempty" yaml:"readonly,omitempty"`
}

// LifecycleSpec carries postStart and preStop hooks.
type LifecycleSpec struct {
	PostStart *LifecycleHookSpec `json:"postStart,omitempty" yaml:"postStart,omitempty"`
	PreStop   *LifecycleHookSpec `json:"preStop,omitempty" yaml:"preStop,omitempty"`
}

// LifecycleHookSpec is a closed disjunction over two shapes (exec / httpGet).
// Mutual exclusion enforced by CUE.
//
// HTTPGet is K8s-shaped pass-through (validated structurally only by CUE;
// internal shape is the provider's responsibility).
type LifecycleHookSpec struct {
	Exec    []string       `json:"exec,omitempty" yaml:"exec,omitempty"`
	HTTPGet map[string]any `json:"httpGet,omitempty" yaml:"httpGet,omitempty"`
}

// Scheduling carries pod placement intent.
//
// Affinity / Tolerations / TopologySpreadConstraints are typed `any` (opaque
// pass-through to the provider rendering layer). Aggrega's CUE schema
// constrains structural presence only; internal shape validation is delegated
// to provider engines (helm's K8s pod spec, compose's restart policy, etc.).
type Scheduling struct {
	NodeSelector              map[string]string `json:"nodeSelector,omitempty" yaml:"nodeSelector,omitempty"`
	Affinity                  any               `json:"affinity,omitempty" yaml:"affinity,omitempty"`
	Tolerations               []any             `json:"tolerations,omitempty" yaml:"tolerations,omitempty"`
	TopologySpreadConstraints []any             `json:"topologySpreadConstraints,omitempty" yaml:"topologySpreadConstraints,omitempty"`
}

// PodSecurityContext carries pod-level security identity.
type PodSecurityContext struct {
	RunAsUser          *int  `json:"runAsUser,omitempty" yaml:"runAsUser,omitempty"`
	RunAsGroup         *int  `json:"runAsGroup,omitempty" yaml:"runAsGroup,omitempty"`
	FSGroup            *int  `json:"fsGroup,omitempty" yaml:"fsGroup,omitempty"`
	SupplementalGroups []int `json:"supplementalGroups,omitempty" yaml:"supplementalGroups,omitempty"`
}

// ContainerSecurityContext carries container-level security identity. Adds
// runAsNonRoot, readOnlyRootFilesystem, allowPrivilegeEscalation, and
// capabilities on top of the pod-level shape.
type ContainerSecurityContext struct {
	RunAsUser                *int          `json:"runAsUser,omitempty" yaml:"runAsUser,omitempty"`
	RunAsGroup               *int          `json:"runAsGroup,omitempty" yaml:"runAsGroup,omitempty"`
	RunAsNonRoot             *bool         `json:"runAsNonRoot,omitempty" yaml:"runAsNonRoot,omitempty"`
	ReadOnlyRootFilesystem   *bool         `json:"readOnlyRootFilesystem,omitempty" yaml:"readOnlyRootFilesystem,omitempty"`
	AllowPrivilegeEscalation *bool         `json:"allowPrivilegeEscalation,omitempty" yaml:"allowPrivilegeEscalation,omitempty"`
	Capabilities             *Capabilities `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

// Capabilities — Linux capability add/drop lists.
type Capabilities struct {
	Add  []string `json:"add,omitempty" yaml:"add,omitempty"`
	Drop []string `json:"drop,omitempty" yaml:"drop,omitempty"`
}
