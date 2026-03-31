package types

import "strings"

// Aggregate is the Domain 1 developer intent document (aggregate-v1alpha1).
type Aggregate struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   AggregateMetadata `yaml:"metadata"`
	Spec       AggregateSpec     `yaml:"spec"`
}

// AggregateMetadata carries aggregate identity and optional labels.
type AggregateMetadata struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels,omitempty"`
}

// AggregateSpec lists logical components and optional module inputs.
type AggregateSpec struct {
	Inputs     map[string]interface{}          `yaml:"inputs,omitempty"`
	Components map[string]AggregateComponent `yaml:"components"`
}

// AggregateComponent is one logical node in the aggregate topology.
type AggregateComponent struct {
	Type     string                        `yaml:"type,omitempty"`
	Features []AggregateFeatureAttachment  `yaml:"features,omitempty"`
	Values   map[string]interface{}        `yaml:"values,omitempty"`
	Secrets  map[string]AggregateSecretRef `yaml:"secrets,omitempty"`
}

// AggregateFeatureAttachment binds a Feature type and optional configuration.
// Either name or type must be set (name takes precedence for manifest keys).
type AggregateFeatureAttachment struct {
	Type string                 `yaml:"type,omitempty"`
	Name string                 `yaml:"name,omitempty"`
	With map[string]interface{} `yaml:"with,omitempty"`
}

// FeatureAttachmentKey returns the manifest / plugin identifier for a feature attachment.
func FeatureAttachmentKey(f AggregateFeatureAttachment) string {
	if n := strings.TrimSpace(f.Name); n != "" {
		return n
	}
	return strings.TrimSpace(f.Type)
}

// AggregateSecretRef references a SecretStore and optional store parameters.
type AggregateSecretRef struct {
	Store string                 `yaml:"store"`
	With  map[string]interface{} `yaml:"with,omitempty"`
}
