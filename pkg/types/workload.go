// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Workload struct surface — the platform-owned `aggrega.ai/v1alpha1` Workload
// kind referenced by Recipes that adopt the synthesized input kind. The Workload
// declaration is generated from workload-v1alpha1.schema.json in the
// aggrega-schemas repo, so it cannot drift from it; the value types it
// references are hand-written here and mirror that same schema. At runtime the
// parsed value is constrained by that schema's source, which
// pkg/schemas/jsonschema rebinds from the aggrega-schemas module as
// WorkloadJSONSchema.
//
// Validation flow: yaml.Unmarshal decodes into typed Go fields; the consumer
// (workload synthesis) calls validator.ValidateWorkload to validate the parsed
// value against the embedded JSON Schema via the pkg/validator/jsonschema adapter.
// Closed-disjunction discriminators (Volume's five variants, Probe's three
// shapes, Lifecycle hook's two forms, ContainerSecret's env/file split) are
// not enforced at the Go type level — pointer fields can all be set
// simultaneously by programmatic construction. Mutual-exclusion is the
// validator's job; consumers MUST call ValidateWorkload before trusting
// the parsed shape.

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

//go:generate go run ../../internal/tools/typegen workload_gen.go

// Workload's struct declaration is generated into workload_gen.go from the
// published workload-v1alpha1 schema, and the schema's descriptions become its
// godoc. This hand-written companion holds the value types that declaration
// references — ContainerSpec, VolumeSpec, ServiceSpec, Scheduling,
// PodSecurityContext, DNSConfig and HostAlias, together with the types those
// reference in turn — plus the custom yaml and json codecs ResourceList
// carries.
//
// Scheduling.NodeSelector is typed map[string]string rather than
// OrderedMap[string], the typing that preserves author key order. It is the
// synthesis-intermediate shape, parallel to the K8s-shaped pass-through fields
// documented at aggrega-docs/language-reference/entities/workload.md
// § Validation surface: Workload values are programmatically constructed by
// Recipe synthesis or parsed from a default yaml.v3 unmarshal of templated
// output rather than arriving as author-declared YAML, so author key order is
// not load-bearing at this layer. AggregateMetadata.Labels uses the
// order-preserving typing, because module-level labels there are
// author-declared.

// DNSConfig mirrors the K8s pod dnsConfig shape. Nameservers and Searches have
// compose analogs (dns / dns_search); Options is opaque K8s-shaped pass-through
// with no compose analog.
type DNSConfig struct {
	Nameservers []string `json:"nameservers,omitempty" yaml:"nameservers,omitempty"`
	Searches    []string `json:"searches,omitempty" yaml:"searches,omitempty"`
	Options     []any    `json:"options,omitempty" yaml:"options,omitempty"`
}

// HostAlias is a static hostname-to-IP mapping. helm emits pod.hostAliases;
// compose emits one extra_hosts entry per hostname.
type HostAlias struct {
	IP        string   `json:"ip,omitempty" yaml:"ip,omitempty"`
	Hostnames []string `json:"hostnames,omitempty" yaml:"hostnames,omitempty"`
}

// ContainerSpec describes one container in a Workload.
//
// Name is consumed for the positional init list, where an authored value
// sets the emitted container name and an empty value falls back to a
// generated init-<index> name. Map containers derive their name from the
// map key, so this field is ignored there.
//
// Variables / Attachments / Ports preserve author order (OrderedMap); other
// fields are typed Go values decoded directly by yaml.Unmarshal.
type ContainerSpec struct {
	Name            string                    `json:"name,omitempty" yaml:"name,omitempty"`
	Image           string                    `json:"image,omitempty" yaml:"image,omitempty"`
	ImagePullPolicy string                    `json:"imagePullPolicy,omitempty" yaml:"imagePullPolicy,omitempty"`
	Command         []string                  `json:"command,omitempty" yaml:"command,omitempty"`
	Args            []string                  `json:"args,omitempty" yaml:"args,omitempty"`
	Variables       OrderedMap[string]        `json:"variables,omitempty,omitzero" yaml:"variables,omitempty"`
	Attachments     OrderedMap[AttachmentRef] `json:"attachments,omitempty,omitzero" yaml:"attachments,omitempty"`
	EnvFrom         []EnvFromSource           `json:"envFrom,omitempty" yaml:"envFrom,omitempty"`
	Ports           OrderedMap[int]           `json:"ports,omitempty,omitzero" yaml:"ports,omitempty"`
	Resources       *ResourceSpec             `json:"resources,omitempty" yaml:"resources,omitempty"`
	Health          *HealthSpec               `json:"health,omitempty" yaml:"health,omitempty"`
	VolumeMounts    []VolumeMount             `json:"volumeMounts,omitempty" yaml:"volumeMounts,omitempty"`
	RestartPolicy   string                    `json:"restartPolicy,omitempty" yaml:"restartPolicy,omitempty"`
	Lifecycle       *LifecycleSpec            `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
	SecurityContext *ContainerSecurityContext `json:"securityContext,omitempty" yaml:"securityContext,omitempty"`
}

// EnvFromSource imports a whole ConfigMap or Secret source as environment
// variables. Exactly one of ConfigMap / Secret is set (schema-enforced
// disjunction, not a Go-type constraint). ConfigMap names a ConfigMap source;
// Secret names a featuresSchema slot whose provides resolve the Secret name.
// Prefix, when set, is prepended to every imported variable name.
type EnvFromSource struct {
	ConfigMap string `json:"configMap,omitempty" yaml:"configMap,omitempty"`
	Secret    string `json:"secret,omitempty" yaml:"secret,omitempty"`
	Prefix    string `json:"prefix,omitempty" yaml:"prefix,omitempty"`
}

// AttachmentRef references a featuresSchema slot on the entity that owns the
// Workload, delivering what the attachment filling that slot published. As is
// "env" or "file"; when As == "file", Path is required (the in-container mount
// path).
//
// Key, meaningful only when As == "env", names the specific data key of the
// referenced slot's published resource this entry reads. When set, the entry
// reads that key; two entries may reference the same slot with different Key
// values to consume several keys from one multi-key resource. When empty, the
// entry reads the slot's single published data key.
//
// Mutual exclusion of env vs file is a schema-validation rule, not a Go-type
// constraint — both fields can be set simultaneously by programmatic
// construction. Validate via validator.ValidateWorkload.
type AttachmentRef struct {
	From string `json:"from,omitempty" yaml:"from,omitempty"`
	As   string `json:"as,omitempty" yaml:"as,omitempty"`
	Key  string `json:"key,omitempty" yaml:"key,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// VolumeSpec is a closed disjunction over five variants. Exactly one pointer
// field should be set; mutual exclusion is enforced by schema validation, not
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

// PersistentVolume — Deployment-compatible PVC. AccessModes, when set, override
// the default [ReadWriteOnce] on the helm PVC; compose has no access-mode
// concept.
type PersistentVolume struct {
	Size        string   `json:"size" yaml:"size"`
	Class       string   `json:"class,omitempty" yaml:"class,omitempty"`
	AccessModes []string `json:"accessModes,omitempty" yaml:"accessModes,omitempty"`
}

// PersistentTemplateVolume — per-replica claim template (a StatefulSet
// volumeClaimTemplate on the Kubernetes shape). It is a companion of
// form: stateful; declaring it on a workload of any other form is a
// loud error.
type PersistentTemplateVolume struct {
	Size        string   `json:"size" yaml:"size"`
	Class       string   `json:"class,omitempty" yaml:"class,omitempty"`
	AccessModes []string `json:"accessModes,omitempty" yaml:"accessModes,omitempty"`
}

// ServiceSpec describes one Service in a Workload. Type is one of
// ClusterIP / NodePort / LoadBalancer / Headless (enum-validated by
// the embedded Workload schema).
type ServiceSpec struct {
	Type  string          `json:"type" yaml:"type"`
	Ports OrderedMap[int] `json:"ports,omitempty,omitzero" yaml:"ports,omitempty"`
}

// ResourceSpec carries container resource requests and limits.
type ResourceSpec struct {
	Requests *ResourceList `json:"requests,omitempty" yaml:"requests,omitempty"`
	Limits   *ResourceList `json:"limits,omitempty" yaml:"limits,omitempty"`
}

// ResourceList carries Kubernetes-shaped resource quantities: the named cpu and
// memory quantities, plus vendor/domain-scoped extended-resource quantities (for
// example nvidia.com/gpu) routed into Extended. Extended holds the fully-qualified
// <domain>/<name> extended-resource names the schema admits alongside cpu and
// memory, in author order; every quantity is a string piped through verbatim.
//
// The custom yaml and json codecs are load-bearing. A workload crosses two decode
// surfaces — a yaml.Unmarshal at recipe parse and an encoding/json round-trip when
// the render payload carries the workload projection — and encoding/json has no
// inline-map support, so an inline map would be dropped on the json crossing. Both
// surfaces are handled explicitly here, routing cpu and memory to the typed fields
// and every other key into Extended. A ResourceList with no extended keys emits
// cpu then memory, byte-identical to a plain two-field struct.
type ResourceList struct {
	CPU      string             `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory   string             `json:"memory,omitempty" yaml:"memory,omitempty"`
	Extended OrderedMap[string] `json:"-" yaml:"-"`
}

// UnmarshalYAML decodes a mapping node, routing cpu and memory to the typed
// fields and every other key into Extended preserving author order.
func (r *ResourceList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("ResourceList: expected mapping node, got kind %d at line %d", node.Kind, node.Line)
	}
	for i := 0; i < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		var key string
		if err := keyNode.Decode(&key); err != nil {
			return fmt.Errorf("ResourceList: decoding key at line %d: %w", keyNode.Line, err)
		}
		var val string
		if err := valNode.Decode(&val); err != nil {
			return fmt.Errorf("ResourceList.%s at line %d: %w", key, valNode.Line, err)
		}
		switch key {
		case "cpu":
			r.CPU = val
		case "memory":
			r.Memory = val
		default:
			r.Extended.Set(key, val)
		}
	}
	return nil
}

// UnmarshalJSON decodes a JSON object with the same dispatch as UnmarshalYAML,
// using a streaming decoder so extended keys survive the render-payload json
// crossing (encoding/json's default object decode would drop keys with no
// matching struct field).
func (r *ResourceList) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("ResourceList: reading JSON opening token: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("ResourceList: expected JSON object, got token %v", tok)
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("ResourceList: reading JSON key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("ResourceList: expected string key, got token %v", keyTok)
		}
		var val string
		if err := dec.Decode(&val); err != nil {
			return fmt.Errorf("ResourceList.%s: %w", key, err)
		}
		switch key {
		case "cpu":
			r.CPU = val
		case "memory":
			r.Memory = val
		default:
			r.Extended.Set(key, val)
		}
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return fmt.Errorf("ResourceList: reading JSON closing token: %w", err)
	}
	return nil
}

// MarshalYAML emits a mapping node with cpu then memory (when non-empty) followed
// by Extended entries in author order. Value receiver matches OrderedMap's
// dispatch rationale — yaml.v3 sees value-receiver methods regardless of
// addressability.
func (r ResourceList) MarshalYAML() (any, error) {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addPair := func(k, v string) error {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		valNode := &yaml.Node{}
		if err := valNode.Encode(v); err != nil {
			return fmt.Errorf("ResourceList: encoding value for key %q: %w", k, err)
		}
		out.Content = append(out.Content, keyNode, valNode)
		return nil
	}
	if r.CPU != "" {
		if err := addPair("cpu", r.CPU); err != nil {
			return nil, err
		}
	}
	if r.Memory != "" {
		if err := addPair("memory", r.Memory); err != nil {
			return nil, err
		}
	}
	for k, v := range r.Extended.All() {
		if err := addPair(k, v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MarshalJSON emits a JSON object preserving the same emission order as
// MarshalYAML — cpu then memory (when non-empty), then Extended entries in author
// order. Builds the object manually because encoding/json does not expose map-key
// ordering control.
func (r ResourceList) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	addPair := func(k, v string) error {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		kb, err := json.Marshal(k)
		if err != nil {
			return fmt.Errorf("ResourceList: encoding JSON key %q: %w", k, err)
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("ResourceList: encoding JSON value for key %q: %w", k, err)
		}
		buf.Write(vb)
		return nil
	}
	if r.CPU != "" {
		if err := addPair("cpu", r.CPU); err != nil {
			return nil, err
		}
	}
	if r.Memory != "" {
		if err := addPair("memory", r.Memory); err != nil {
			return nil, err
		}
	}
	for k, v := range r.Extended.All() {
		if err := addPair(k, v); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// HealthSpec carries readiness, liveness, and startup probes.
type HealthSpec struct {
	Ready   *ProbeSpec `json:"ready,omitempty" yaml:"ready,omitempty"`
	Live    *ProbeSpec `json:"live,omitempty" yaml:"live,omitempty"`
	Startup *ProbeSpec `json:"startup,omitempty" yaml:"startup,omitempty"`
}

// ProbeSpec is a closed disjunction over three shapes (http / tcp / exec).
// Mutual exclusion is enforced by schema validation, not Go-type structure.
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
	Name string `json:"name" yaml:"name"`
	Path string `json:"path" yaml:"path"`
	// SubPath mounts a single file or subdirectory from the volume at Path,
	// leaving the other files already present in the target directory visible.
	// Without it, mounting the whole volume masks any sibling files the image
	// placed in that directory.
	SubPath  string `json:"subPath,omitempty" yaml:"subPath,omitempty"`
	ReadOnly bool   `json:"readonly,omitempty" yaml:"readonly,omitempty"`
}

// LifecycleSpec carries postStart and preStop hooks.
type LifecycleSpec struct {
	PostStart *LifecycleHookSpec `json:"postStart,omitempty" yaml:"postStart,omitempty"`
	PreStop   *LifecycleHookSpec `json:"preStop,omitempty" yaml:"preStop,omitempty"`
}

// LifecycleHookSpec is a closed disjunction over two shapes (exec / httpGet).
// Mutual exclusion enforced by schema validation.
//
// HTTPGet is K8s-shaped pass-through (validated structurally only by the
// embedded Workload schema; internal shape is the provider's responsibility).
type LifecycleHookSpec struct {
	Exec    []string       `json:"exec,omitempty" yaml:"exec,omitempty"`
	HTTPGet map[string]any `json:"httpGet,omitempty" yaml:"httpGet,omitempty"`
}

// Scheduling carries pod placement intent.
//
// Affinity / Tolerations / TopologySpreadConstraints are typed `any` (opaque
// pass-through to the provider rendering layer). The embedded Workload
// schema constrains structural presence only; internal shape validation is
// delegated to provider engines (helm's K8s pod spec, compose's restart
// policy, etc.).
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
