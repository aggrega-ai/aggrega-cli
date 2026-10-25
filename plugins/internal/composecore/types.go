// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"fmt"
	"reflect"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ComposeFile is a Compose Specification document (no `version:` key — the
// modern compose-spec deprecates it; Compose v2.2+ ignores it). It is the shape
// on both sides of the merge: SynthesizeWorkloadCore fills one with a single
// node's contributions, and AssembleBoundaryCore folds those into the one this
// package emits.
//
// Models is never filled by the synthesizer — a Workload declares no model — and
// reaches the merge only from a fragment that writes Compose's own top-level
// models: section, a sourced compose file or an artifacts: body.
//
// Extensions holds the top level's extension members (keys beginning x-), in
// the order written, and is emitted after every other section; ComposeFile
// decodes and encodes itself to carry them.
type ComposeFile struct {
	Services   map[string]Service `yaml:"services,omitempty"`
	Volumes    map[string]Volume  `yaml:"volumes,omitempty"`
	Networks   map[string]Network `yaml:"networks,omitempty"`
	Configs    map[string]Config  `yaml:"configs,omitempty"`
	Models     map[string]Model   `yaml:"models,omitempty"`
	Extensions Extensions         `yaml:"-"`
}

// Service is one compose service definition. Fields populated only as needed
// — omitempty on every field so per-node fragments stay minimal.
//
// The set of fields here is the surface the assembled file carries, and it
// grows as needed: deploy is carried today, while a service-level secrets block
// is not yet part of it. With the extension members, it is also the whole set of
// service keys a fragment may write — a sourced compose file or an artifacts:
// body — because decodeFragment refuses a fragment writing a key no field here
// declares rather than letting the assembled file leave it out.
//
// Profiles is carried as the fragment lists it, in that order, and never
// written by the synthesizer: Compose starts a service gated behind a profile
// only when that profile is enabled or the service is named on the command line.
//
// Extensions holds the service's extension members (keys beginning x-), in the
// order written, and is emitted after every other key; Service decodes and
// encodes itself to carry them.
type Service struct {
	Image           string                   `yaml:"image,omitempty"`
	PullPolicy      string                   `yaml:"pull_policy,omitempty"`
	Command         []string                 `yaml:"command,omitempty"`
	Entrypoint      []string                 `yaml:"entrypoint,omitempty"`
	Environment     types.OrderedMap[string] `yaml:"environment,omitempty"`
	EnvFile         []string                 `yaml:"env_file,omitempty"`
	Ports           []string                 `yaml:"ports,omitempty"`
	Labels          map[string]string        `yaml:"labels,omitempty"`
	Volumes         []ServiceVolume          `yaml:"volumes,omitempty"`
	Tmpfs           []string                 `yaml:"tmpfs,omitempty"`
	Networks        []string                 `yaml:"networks,omitempty"`
	NetworkMode     string                   `yaml:"network_mode,omitempty"`
	DependsOn       DependsOn                `yaml:"depends_on,omitempty"`
	Healthcheck     *Healthcheck             `yaml:"healthcheck,omitempty"`
	Restart         string                   `yaml:"restart,omitempty"`
	User            string                   `yaml:"user,omitempty"`
	WorkingDir      string                   `yaml:"working_dir,omitempty"`
	Privileged      bool                     `yaml:"privileged,omitempty"`
	ReadOnly        bool                     `yaml:"read_only,omitempty"`
	CapAdd          []string                 `yaml:"cap_add,omitempty"`
	CapDrop         []string                 `yaml:"cap_drop,omitempty"`
	Configs         []ServiceConfig          `yaml:"configs,omitempty"`
	Models          ServiceModels            `yaml:"models,omitempty"`
	Deploy          *Deploy                  `yaml:"deploy,omitempty"`
	StopGracePeriod string                   `yaml:"stop_grace_period,omitempty"`
	DNS             []string                 `yaml:"dns,omitempty"`
	DNSSearch       []string                 `yaml:"dns_search,omitempty"`
	ExtraHosts      []string                 `yaml:"extra_hosts,omitempty"`
	Profiles        []string                 `yaml:"profiles,omitempty"`
	Extensions      Extensions               `yaml:"-"`
}

// Compose `depends_on` conditions, as the Compose Specification defines them.
// conditionStarted is the specification's stated equivalent of the short form;
// conditionHealthy waits until the dependency's own healthcheck reports healthy,
// which is readiness rather than process launch and is therefore what a
// deployment wave means; conditionCompletedSuccessfully waits until the
// dependency has run to successful completion, which is the primitive a
// run-to-completion init service is waited on with.
//
// conditionHealthy is satisfiable only by a service that declares a
// `healthcheck`. A service without one can never report healthy, so a producer
// choosing this condition checks [Service.Healthcheck] on the service it names
// and falls back to conditionStarted rather than emitting a wait nothing can
// satisfy.
const (
	conditionStarted               = "service_started"
	conditionHealthy               = "service_healthy"
	conditionCompletedSuccessfully = "service_completed_successfully"
)

// DependsOn is a compose service's `depends_on` field — the service's ordering
// expression. It is the one home for both the merge rule and the emitted shape,
// so every producer of service ordering agrees on them; the entry list is
// unexported so no producer can append past this type's Require method and
// reach the emitted bytes with its own shape or its own ordering.
//
// Emitted shape. Compose accepts a short sequence of service names and a long
// mapping of service name to a body, and the specification requires `condition`
// on every entry of the long form. So the field emits in one form or the other,
// never a mix: the short sequence when no entry carries a condition, the long
// mapping when any entry does — and there, a condition-less entry emits the
// explicit `service_started`, which the specification defines as the short
// form's equivalent. The condition-less case, which is every dependency derived
// from a `uses` edge, therefore stays a readable list.
//
// Emitted order is insertion order. Two producers write this field with two
// different orderings — dependencies inferred from `uses` edges arrive in
// alphabetical target order, an init-container chain arrives in the order the
// author wrote the list, because that list is ordered and the ordering is the
// meaning. Insertion order keeps both intact without this type arbitrating
// between them: each producer orders its own Require calls, and the field
// concatenates the contributions in the order they arrive. No map is iterated
// on the path to the output.
type DependsOn struct {
	entries []dependsOnEntry
}

// dependsOnEntry is one dependency: the service waited on, and the condition
// that satisfies the wait (empty selects the short form).
type dependsOnEntry struct {
	service   string
	condition string
}

// Require records that the service owning this field waits on service, gated on
// condition (empty for an ungated wait). It is the only way to populate the
// field.
//
// Require is idempotent per service name: the first call fixes that service's
// place in the emitted order, and a later call for the same service supplies a
// condition only when no condition is recorded yet. So a specific wait survives
// a subsequent unconditional one, and neither producer can silently reorder or
// weaken what the other recorded.
func (d *DependsOn) Require(service, condition string) {
	for i := range d.entries {
		if d.entries[i].service != service {
			continue
		}
		if d.entries[i].condition == "" {
			d.entries[i].condition = condition
		}
		return
	}
	d.entries = append(d.entries, dependsOnEntry{service: service, condition: condition})
}

// Len returns the number of recorded dependencies.
func (d DependsOn) Len() int { return len(d.entries) }

// IsZero reports whether the field records no dependency. yaml.v3 reads it
// through the yaml.IsZeroer interface to honor the `omitempty` tag — the
// reflective fallback it would otherwise use skips unexported fields and would
// judge every DependsOn empty.
func (d DependsOn) IsZero() bool { return len(d.entries) == 0 }

// MarshalYAML emits the short sequence form when no entry carries a condition
// and the long mapping form otherwise, both in insertion order.
//
// Value receiver so dispatch works when DependsOn is a struct field of a
// non-addressable map value, matching the OrderedMap fields alongside it.
func (d DependsOn) MarshalYAML() (any, error) {
	long := false
	for _, e := range d.entries {
		if e.condition != "" {
			long = true
			break
		}
	}

	if !long {
		names := make([]string, 0, len(d.entries))
		for _, e := range d.entries {
			names = append(names, e.service)
		}
		return names, nil
	}

	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	out.Content = make([]*yaml.Node, 0, 2*len(d.entries))
	for _, e := range d.entries {
		condition := e.condition
		if condition == "" {
			condition = conditionStarted
		}
		out.Content = append(out.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.service},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "condition"},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: condition},
			}},
		)
	}
	return out, nil
}

// UnmarshalYAML accepts both compose forms, so a fragment this package emitted
// and a fragment authored by hand both round-trip into the same shape. Mapping
// entries are read in document order, which is the order they were emitted in.
func (d *DependsOn) UnmarshalYAML(node *yaml.Node) error {
	d.entries = nil
	switch node.Kind {
	case yaml.SequenceNode:
		for _, item := range node.Content {
			var service string
			if err := item.Decode(&service); err != nil {
				return fmt.Errorf("depends_on: decoding entry at line %d: %w", item.Line, err)
			}
			d.Require(service, "")
		}
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			var service string
			if err := node.Content[i].Decode(&service); err != nil {
				return fmt.Errorf("depends_on: decoding key at line %d: %w", node.Content[i].Line, err)
			}
			var body dependsOnBody
			if err := node.Content[i+1].Decode(&body); err != nil {
				return fmt.Errorf("depends_on: decoding entry %q at line %d: %w", service, node.Content[i+1].Line, err)
			}
			d.Require(service, body.Condition)
		}
		return nil
	default:
		return fmt.Errorf("depends_on: expected a sequence or a mapping, got kind %d at line %d", node.Kind, node.Line)
	}
}

// dependsOnBody is the body of one long-form depends_on entry as the field
// carries it: the condition alone. It is the one declaration both the decode
// above and the fragment key walk read, so a member Compose defines for the
// entry and this body does not declare is refused rather than dropped.
type dependsOnBody struct {
	Condition string `yaml:"condition"`
}

// walkFragmentKeys reports every key a long-form entry writes that its body does
// not carry. The short form lists names and writes no key.
func (DependsOn) walkFragmentKeys(w *fragmentKeyWalk, node *yaml.Node, path string) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for _, kv := range fragmentPairs(node) {
		w.walk(kv.value, reflect.TypeOf(dependsOnBody{}), joinFragmentPath(path, kv.key))
	}
}

// Deploy is the compose service deploy block. Only the resources sub-block is
// synthesized today: it is honored by `docker compose` in non-swarm mode
// (unlike deploy.replicas, which is swarm-only). Workload container resource
// limits map to deploy.resources.limits and requests map to
// deploy.resources.reservations; a container's nvidia.com/gpu request maps to a
// device entry under deploy.resources.reservations.
type Deploy struct {
	Resources *DeployResources `yaml:"resources,omitempty"`
}

// DeployResources holds the compose deploy.resources limits/reservations pair.
type DeployResources struct {
	Limits       *ResourceQuantities   `yaml:"limits,omitempty"`
	Reservations *ResourceReservations `yaml:"reservations,omitempty"`
}

// ResourceQuantities is a compose deploy.resources cpus/memory pair. cpus is a
// fractional-core string ("0.5", "2"); memory is an exact byte count string.
type ResourceQuantities struct {
	CPUs   string `yaml:"cpus,omitempty"`
	Memory string `yaml:"memory,omitempty"`
}

// ResourceReservations is a compose deploy.resources.reservations block: the
// cpus/memory pair a limits block also carries, followed by the devices the
// service reserves. Devices sit here and not on limits because the Compose
// Specification declares them only under reservations — a device is reserved
// for a service, never capped.
type ResourceReservations struct {
	ResourceQuantities `yaml:",inline"`
	Devices            []DeviceRequest `yaml:"devices,omitempty"`
}

// DeviceRequest is one entry of a compose reservations devices list, with the
// members the Compose Specification declares for it, in the order they are
// emitted. A fragment's entry — a sourced compose file's included — is carried
// through the merge as written: Count holds the scalar the fragment wrote, a
// number or the word all, and Options carries the driver-specific options as
// written.
//
// The synthesizer writes one shape: driver nvidia, a whole Count, and the gpu
// capability. It never writes an entry without a Count, because Compose reads
// an absent count as every GPU on the host, and never writes one without
// capabilities, which Compose requires on every entry.
type DeviceRequest struct {
	Driver       string         `yaml:"driver,omitempty"`
	Count        any            `yaml:"count,omitempty"`
	DeviceIDs    []string       `yaml:"device_ids,omitempty"`
	Capabilities []string       `yaml:"capabilities,omitempty"`
	Options      map[string]any `yaml:"options,omitempty"`
}

// Config is a top-level compose config declaration with inline content. The
// content is mounted into a service as a file via a ServiceConfig reference.
// Inline content requires Docker Compose >= 2.23.1.
type Config struct {
	Content string `yaml:"content"`
}

// ServiceConfig is one entry in a service's configs: list — the long form that
// mounts a top-level Config at an explicit path inside the container.
type ServiceConfig struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// Model is one entry of a compose file's top-level models: section — an AI model
// the application uses, which the platform running the file pulls and serves.
// The body is carried as the fragment writes it: model (the OCI artifact
// identifier), context_size and runtime_flags in the specification's own
// spelling, and every other member — an x- extension among them — in Other.
//
// Nothing here reads what a model is. The identifier is not parsed, no member
// is checked against what any engine accepts, and nothing about the host that
// will serve the model is known at build time.
//
// ContextSize is held as the scalar the fragment wrote rather than as an
// integer, so a value spelled through compose variable interpolation reaches
// the assembled file as written instead of failing to decode.
type Model struct {
	Model        string   `yaml:"model,omitempty"`
	ContextSize  any      `yaml:"context_size,omitempty"`
	RuntimeFlags []string `yaml:"runtime_flags,omitempty"`
	// Other carries every member the fields above do not name. yaml.v3 emits an
	// inline map after the named fields with its keys sorted, so the emitted
	// order is fixed whatever order the fragment wrote them in.
	Other map[string]any `yaml:",inline"`
}

// ServiceModels is a compose service's models: field — the models the service
// is granted. Compose accepts a short sequence of model names and a long mapping
// of model name to a binding naming the environment variables the platform
// fills with the model's address and identifier.
//
// The field round-trips in the form the fragment wrote: a short list emits as a
// short list and a long mapping as a long mapping, in the order the fragment
// listed the models, so the assembled file grants exactly what was written. No
// map is iterated on the path to the output.
type ServiceModels struct {
	entries []serviceModelEntry
	long    bool
}

// serviceModelEntry is one granted model: its key under the top-level models:
// section, and the binding the long form wrote for it (zero for the short form).
type serviceModelEntry struct {
	name    string
	binding ServiceModelBinding
}

// ServiceModelBinding is the body of one long-form service models: entry:
// endpoint_var and model_var, the names of the variables the platform sets to
// the model runner's address and to the model's identifier, and every other
// member the entry writes, carried in Other. Compose names the variables itself
// where the entry leaves them out.
type ServiceModelBinding struct {
	EndpointVar string         `yaml:"endpoint_var,omitempty"`
	ModelVar    string         `yaml:"model_var,omitempty"`
	Other       map[string]any `yaml:",inline"`
}

// Names returns the models the service references, in the order the fragment
// listed them.
func (m ServiceModels) Names() []string {
	names := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		names = append(names, e.name)
	}
	return names
}

// IsZero reports whether the field grants no model. yaml.v3 reads it through the
// yaml.IsZeroer interface to honor the `omitempty` tag, for the reason
// DependsOn.IsZero gives.
func (m ServiceModels) IsZero() bool { return len(m.entries) == 0 }

// MarshalYAML emits the short sequence when the fragment wrote one and the long
// mapping when it wrote that, both in the order the models were listed.
//
// Value receiver so dispatch works when ServiceModels is a struct field of a
// non-addressable map value, matching the DependsOn field alongside it.
func (m ServiceModels) MarshalYAML() (any, error) {
	if !m.long {
		return m.Names(), nil
	}
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	out.Content = make([]*yaml.Node, 0, 2*len(m.entries))
	for _, e := range m.entries {
		var body yaml.Node
		if err := body.Encode(e.binding); err != nil {
			return nil, fmt.Errorf("models: encoding entry %q: %w", e.name, err)
		}
		out.Content = append(out.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.name},
			&body,
		)
	}
	return out, nil
}

// UnmarshalYAML accepts both compose forms and records which one was written.
// Mapping entries are read in document order. An entry whose body is empty or
// null grants the model with Compose's own variable names.
func (m *ServiceModels) UnmarshalYAML(node *yaml.Node) error {
	m.entries = nil
	m.long = false
	switch node.Kind {
	case yaml.SequenceNode:
		for _, item := range node.Content {
			var name string
			if err := item.Decode(&name); err != nil {
				return fmt.Errorf("models: decoding entry at line %d: %w", item.Line, err)
			}
			m.entries = append(m.entries, serviceModelEntry{name: name})
		}
		return nil
	case yaml.MappingNode:
		m.long = true
		for i := 0; i+1 < len(node.Content); i += 2 {
			var name string
			if err := node.Content[i].Decode(&name); err != nil {
				return fmt.Errorf("models: decoding key at line %d: %w", node.Content[i].Line, err)
			}
			var binding ServiceModelBinding
			if err := node.Content[i+1].Decode(&binding); err != nil {
				return fmt.Errorf("models: decoding entry %q at line %d: %w", name, node.Content[i+1].Line, err)
			}
			m.entries = append(m.entries, serviceModelEntry{name: name, binding: binding})
		}
		return nil
	default:
		return fmt.Errorf("models: expected a sequence or a mapping, got kind %d at line %d", node.Kind, node.Line)
	}
}

// walkFragmentKeys reports every key a long-form entry writes that its binding
// does not carry. The binding carries every member it does not name in Other, so
// in practice a long-form entry reports none; the walk reads that from the
// binding's own declaration rather than assuming it. The short form lists names
// and writes no key.
func (ServiceModels) walkFragmentKeys(w *fragmentKeyWalk, node *yaml.Node, path string) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for _, kv := range fragmentPairs(node) {
		w.walk(kv.value, reflect.TypeOf(ServiceModelBinding{}), joinFragmentPath(path, kv.key))
	}
}

// ServiceVolume is one entry in a service's volumes: list. Compose's "long
// form" — the synthesizer always emits long-form for clarity and for fields
// (read_only, type) that the short form `host:container:mode` cannot express.
type ServiceVolume struct {
	Type     string `yaml:"type"` // "bind" or "volume" or "tmpfs"
	Source   string `yaml:"source,omitempty"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only,omitempty"`
}

// Volume is a top-level named volume declaration. Compose top-level volumes
// have driver/driver_opts/external/labels/name; the synthesizer emits the
// minimum viable form — an empty body, `<name>: {}`.
type Volume struct {
	Driver     string            `yaml:"driver,omitempty"`
	DriverOpts map[string]string `yaml:"driver_opts,omitempty"`
	Labels     map[string]string `yaml:"labels,omitempty"`
}

// Network is a top-level docker network declaration. The merge declares one
// network per distinct `aggrega.ai/namespace` annotation among the contributing
// nodes, and attaches each node's services to the network its own annotation
// names; a node carrying no such annotation attaches to a network named for the
// dispatch path instead. So a dispatch group holds as many networks as its nodes
// name between them, which is one only when they agree.
type Network struct {
	Driver     string            `yaml:"driver,omitempty"`
	DriverOpts map[string]string `yaml:"driver_opts,omitempty"`
	Labels     map[string]string `yaml:"labels,omitempty"`
}

// Healthcheck is the compose service healthcheck primitive. Aggrega's three
// probe shapes (http / tcp / exec) translate as:
//
//   - http: → ["CMD-SHELL", "curl -f http://localhost:<port><path> || exit 1"]
//   - tcp:  → ["CMD-SHELL", "nc -z localhost <port>"]
//   - exec: → ["CMD", <token>...] (CMD form when token list available)
//
// Compose only supports CMD/CMD-SHELL/NONE; no native HTTP or TCP probes.
type Healthcheck struct {
	Test        []string `yaml:"test,omitempty"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
}
