// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

// Chart is the Helm Chart.yaml document (apiVersion v2).
type Chart struct {
	APIVersion string `yaml:"apiVersion"`
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
}

// Deployment is a minimal apps/v1 Deployment for YAML marshaling.
type Deployment struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   ObjectMeta     `yaml:"metadata"`
	Spec       DeploymentSpec `yaml:"spec"`
}

// Service is a minimal v1 Service for YAML marshaling.
type Service struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   ObjectMeta  `yaml:"metadata"`
	Spec       ServiceSpec `yaml:"spec"`
}

// ObjectMeta carries K8s resource metadata.
type ObjectMeta struct {
	Name        string            `yaml:"name"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// DeploymentSpec is a reduced Deployment spec.
type DeploymentSpec struct {
	Replicas *int32          `yaml:"replicas"`
	Selector *LabelSelector  `yaml:"selector"`
	Template PodTemplateSpec `yaml:"template"`
}

// LabelSelector with matchLabels only.
type LabelSelector struct {
	MatchLabels map[string]string `yaml:"matchLabels"`
}

// PodTemplateSpec wraps pod metadata and spec.
type PodTemplateSpec struct {
	Metadata ObjectMeta `yaml:"metadata"`
	Spec     PodSpec    `yaml:"spec"`
}

// PodSpec holds containers.
type PodSpec struct {
	Containers []Container `yaml:"containers"`
}

// Container is one workload container.
type Container struct {
	Name  string          `yaml:"name"`
	Image string          `yaml:"image"`
	Ports []ContainerPort `yaml:"ports,omitempty"`
	Env   []EnvVar        `yaml:"env,omitempty"`
}

// ContainerPort exposes a port on the container.
type ContainerPort struct {
	ContainerPort int32  `yaml:"containerPort"`
	Protocol      string `yaml:"protocol,omitempty"`
}

// EnvVar is a key/value environment variable.
type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// ServiceSpec is a reduced Service spec.
type ServiceSpec struct {
	Type     string            `yaml:"type"`
	Selector map[string]string `yaml:"selector"`
	Ports    []ServicePort     `yaml:"ports"`
}

// ServicePort maps Service port to target port.
type ServicePort struct {
	Name       string `yaml:"name,omitempty"`
	Port       int32  `yaml:"port"`
	TargetPort int32  `yaml:"targetPort"`
	Protocol   string `yaml:"protocol,omitempty"`
}
