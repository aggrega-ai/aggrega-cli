// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

// Workload is the platform-owned kind describing a synthesizable deployment unit.
// Full struct body (UnmarshalYAML with CUE validation) lands in synthesis-foundations.
// Here we declare type slots only so the deterministic-ordering refactor covers them.

type Workload struct {
	APIVersion string                    `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                    `json:"kind" yaml:"kind"`
	Metadata   WorkloadMetadata          `json:"metadata" yaml:"metadata"`
	Containers OrderedMap[ContainerSpec] `json:"containers,omitempty,omitzero" yaml:"containers,omitempty"`
	Volumes    OrderedMap[VolumeSpec]    `json:"volumes,omitempty,omitzero" yaml:"volumes,omitempty"`
	Services   OrderedMap[ServiceSpec]   `json:"services,omitempty,omitzero" yaml:"services,omitempty"`
}

type WorkloadMetadata struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

type ContainerSpec struct {
	// Body lands in synthesis-foundations. Slots only here.
	Image     string                `json:"image,omitempty" yaml:"image,omitempty"`
	Variables OrderedMap[string]    `json:"variables,omitempty,omitzero" yaml:"variables,omitempty"`
	Secrets   OrderedMap[SecretRef] `json:"secrets,omitempty,omitzero" yaml:"secrets,omitempty"`
	Ports     OrderedMap[PortSpec]  `json:"ports,omitempty,omitzero" yaml:"ports,omitempty"`
}

type VolumeSpec struct {
	// Body lands in synthesis-foundations.
}

type ServiceSpec struct {
	Ports OrderedMap[ServicePortSpec] `json:"ports,omitempty,omitzero" yaml:"ports,omitempty"`
}

type PortSpec struct {
	// Body lands in synthesis-foundations.
}

type ServicePortSpec struct {
	// Body lands in synthesis-foundations.
}

type SecretRef struct {
	// Body lands in synthesis-foundations.
}
