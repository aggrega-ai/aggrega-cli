// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ComposeFile is a Compose Specification document (no `version:` key — the
// modern compose-spec deprecates it; Compose v2.2+ ignores it). Fragment
// outputs from SynthesizeWorkload populate Services/Volumes/Networks with the
// node's contributions; the Release-level merger collects fragments and emits a
// single ComposeFile at the Release output root.
type ComposeFile struct {
	Services map[string]Service `yaml:"services,omitempty"`
	Volumes  map[string]Volume  `yaml:"volumes,omitempty"`
	Networks map[string]Network `yaml:"networks,omitempty"`
	Configs  map[string]Config  `yaml:"configs,omitempty"`
}

// Service is one compose service definition. Fields populated only as needed
// — omitempty on every field so per-node fragments stay minimal.
//
// The set of fields here is the currently synthesisable surface. Expansion
// (e.g., secrets, deploy) lands as needed in future iterations.
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
	Deploy          *Deploy                  `yaml:"deploy,omitempty"`
	StopGracePeriod string                   `yaml:"stop_grace_period,omitempty"`
	DNS             []string                 `yaml:"dns,omitempty"`
	DNSSearch       []string                 `yaml:"dns_search,omitempty"`
	ExtraHosts      []string                 `yaml:"extra_hosts,omitempty"`
}

// Compose `depends_on` conditions, as the Compose Specification defines them.
// conditionStarted is the specification's stated equivalent of the short form;
// conditionCompletedSuccessfully waits until the dependency has run to
// successful completion, which is the primitive a run-to-completion init
// service is waited on with.
const (
	conditionStarted               = "service_started"
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
			var body struct {
				Condition string `yaml:"condition"`
			}
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

// Deploy is the compose service deploy block. Only the resources sub-block is
// synthesised today: it is honored by `docker compose` in non-swarm mode
// (unlike deploy.replicas, which is swarm-only). Workload container resource
// limits map to deploy.resources.limits and requests map to
// deploy.resources.reservations.
type Deploy struct {
	Resources *DeployResources `yaml:"resources,omitempty"`
}

// DeployResources holds the compose deploy.resources limits/reservations pair.
type DeployResources struct {
	Limits       *ResourceQuantities `yaml:"limits,omitempty"`
	Reservations *ResourceQuantities `yaml:"reservations,omitempty"`
}

// ResourceQuantities is a compose deploy.resources cpus/memory pair. cpus is a
// fractional-core string ("0.5", "2"); memory is an exact byte count string.
type ResourceQuantities struct {
	CPUs   string `yaml:"cpus,omitempty"`
	Memory string `yaml:"memory,omitempty"`
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
// minimum viable form (empty body — `<name>: {}`) today. Future expansion
// covers driver options when Workload's persistent.class signals a
// non-default storage class.
type Volume struct {
	Driver     string            `yaml:"driver,omitempty"`
	DriverOpts map[string]string `yaml:"driver_opts,omitempty"`
	Labels     map[string]string `yaml:"labels,omitempty"`
}

// Network is a top-level docker network declaration. The Release-level merger
// emits one network per boundary, named after the boundary's
// `aggrega.ai/namespace` annotation when present; per-node fragments do not
// declare networks.
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
