package builder

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/schema"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	expectedAPIVersion = "aggrega.ai/v1alpha1"
	aggregateKind      = "Aggregate"
	manifestKind       = "Manifest"
	releaseKind        = "Release"
)

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
// spec.components is a map keyed by component id (validated by CUE as {[string]: #Component}).
type AggregateSpec struct {
	Inputs     map[string]interface{}          `yaml:"inputs,omitempty"`
	Components map[string]AggregateComponent `yaml:"components"`
}

// AggregateComponent is one logical node in the aggregate topology.
// Infrastructure bindings (image, ports, env) belong to recipes/manifests, not the aggregate.
type AggregateComponent struct {
	Type     string                        `yaml:"type,omitempty"`
	Features []AggregateFeatureAttachment  `yaml:"features,omitempty"`
	Values   map[string]interface{}        `yaml:"values,omitempty"`
	Secrets  map[string]AggregateSecretRef `yaml:"secrets,omitempty"`
}

// AggregateFeatureAttachment binds a Feature type and optional configuration.
type AggregateFeatureAttachment struct {
	Type string                 `yaml:"type"`
	With map[string]interface{} `yaml:"with,omitempty"`
}

// AggregateSecretRef references a SecretStore and optional store parameters.
type AggregateSecretRef struct {
	Store string                 `yaml:"store"`
	With  map[string]interface{} `yaml:"with,omitempty"`
}

// Manifest is the Domain 4 dependency lockfile (manifest-v1alpha1).
type Manifest struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   ManifestMeta `yaml:"metadata"`
	Spec       ManifestSpec `yaml:"spec"`
}

// ManifestMeta identifies the manifest instance.
type ManifestMeta struct {
	Name string `yaml:"name"`
}

// ManifestSpec holds abstract URI to concrete recipe mappings.
type ManifestSpec struct {
	Mappings map[string]ManifestMapping `yaml:"mappings"`
}

// ManifestMapping locks one abstract URI to a semantic version and at least one recipe URI.
// The JSON schema requires anyOf componentRecipe | featureRecipe | secretRecipe; callers
// enforce which recipe kind applies to each resolution path.
type ManifestMapping struct {
	Version         string `yaml:"version"`
	ComponentRecipe string `yaml:"componentRecipe,omitempty"`
	FeatureRecipe   string `yaml:"featureRecipe,omitempty"`
	SecretRecipe    string `yaml:"secretRecipe,omitempty"`
}

// ParseAggregate decodes Aggregate YAML and applies structural checks expected by v1alpha1.
func ParseAggregate(data []byte) (*Aggregate, error) {
	if err := schema.Validate(data, aggregateKind); err != nil {
		return nil, err
	}
	var doc Aggregate
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse aggregate yaml: %w", err)
	}
	if err := validateAggregate(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ParseAggregateFile reads a path and parses an Aggregate document.
func ParseAggregateFile(path string) (*Aggregate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read aggregate file %q: %w", path, err)
	}
	doc, err := ParseAggregate(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// ParseManifest decodes Manifest YAML and applies structural checks expected by v1alpha1.
func ParseManifest(data []byte) (*Manifest, error) {
	var doc Manifest
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse manifest yaml: %w", err)
	}
	if err := validateManifest(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ParseManifestFile reads a path and parses a Manifest document.
func ParseManifestFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest file %q: %w", path, err)
	}
	doc, err := ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

func validateAggregate(doc *Aggregate) error {
	if doc == nil {
		return fmt.Errorf("aggregate: document is nil")
	}
	if doc.APIVersion != expectedAPIVersion {
		return fmt.Errorf("aggregate: apiVersion must be %q, got %q", expectedAPIVersion, doc.APIVersion)
	}
	if doc.Kind != aggregateKind {
		return fmt.Errorf("aggregate: kind must be %q, got %q", aggregateKind, doc.Kind)
	}
	if strings.TrimSpace(doc.Metadata.Name) == "" {
		return fmt.Errorf("aggregate: metadata.name is required")
	}
	if len(doc.Spec.Components) == 0 {
		return fmt.Errorf("aggregate: spec.components must not be empty")
	}
	for id, c := range doc.Spec.Components {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("aggregate: spec.components contains an empty key")
		}
		if strings.TrimSpace(c.Type) == "" {
			c.Type = "server"
			doc.Spec.Components[id] = c
		}
		for j, f := range c.Features {
			if strings.TrimSpace(f.Type) == "" {
				return fmt.Errorf("aggregate: spec.components[%q].features[%d].type is required", id, j)
			}
		}
		for secretName, s := range c.Secrets {
			if strings.TrimSpace(s.Store) == "" {
				return fmt.Errorf("aggregate: spec.components[%q].secrets[%q].store is required", id, secretName)
			}
		}
	}
	return nil
}

func validateManifest(doc *Manifest) error {
	if doc == nil {
		return fmt.Errorf("manifest: document is nil")
	}
	if doc.APIVersion != expectedAPIVersion {
		return fmt.Errorf("manifest: apiVersion must be %q, got %q", expectedAPIVersion, doc.APIVersion)
	}
	if doc.Kind != manifestKind {
		return fmt.Errorf("manifest: kind must be %q, got %q", manifestKind, doc.Kind)
	}
	if strings.TrimSpace(doc.Metadata.Name) == "" {
		return fmt.Errorf("manifest: metadata.name is required")
	}
	if len(doc.Spec.Mappings) == 0 {
		return fmt.Errorf("manifest: spec.mappings must not be empty")
	}
	for key, m := range doc.Spec.Mappings {
		if strings.TrimSpace(m.Version) == "" {
			return fmt.Errorf("manifest: mappings[%q].version is required", key)
		}
		if m.ComponentRecipe == "" && m.FeatureRecipe == "" && m.SecretRecipe == "" {
			return fmt.Errorf("manifest: mappings[%q] must set one of componentRecipe, featureRecipe, or secretRecipe", key)
		}
	}
	return nil
}

// releaseYAMLFile is the YAML shape of release-v1alpha1 (internal to parsing).
type releaseYAMLFile struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Aggregate string                 `yaml:"aggregate"`
		Manifest  string                 `yaml:"manifest"`
		Inputs    map[string]interface{} `yaml:"inputs,omitempty"`
		Policies  *releasePoliciesYAML   `yaml:"policies,omitempty"`
		Scopes    []releaseScopeYAML     `yaml:"scopes"`
	} `yaml:"spec"`
}

type releasePoliciesYAML struct {
	StrictNetworking       *bool `yaml:"strict_networking,omitempty"`
	EnforceGuardrails      *bool `yaml:"enforce_guardrails,omitempty"`
	EnforceTrustDirectives *bool `yaml:"enforce_trust_directives,omitempty"`
}

type releaseScopeYAML struct {
	Name        string            `yaml:"name"`
	Provider    string            `yaml:"provider"`
	Components  []string          `yaml:"components,omitempty"`
	MatchLabels map[string]string `yaml:"matchLabels,omitempty"`
}

// ParseRelease decodes Release YAML into types.Release.
func ParseRelease(data []byte) (*types.Release, error) {
	if err := schema.Validate(data, releaseKind); err != nil {
		return nil, err
	}
	var doc releaseYAMLFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse release yaml: %w", err)
	}
	if err := validateReleaseYAML(&doc); err != nil {
		return nil, err
	}
	return releaseYAMLToTypes(&doc)
}

// ParseReleaseFile reads a Release file from path and parses it.
func ParseReleaseFile(path string) (*types.Release, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read release file %q: %w", path, err)
	}
	rel, err := ParseRelease(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rel, nil
}

func validateReleaseYAML(doc *releaseYAMLFile) error {
	if doc == nil {
		return fmt.Errorf("release: document is nil")
	}
	if doc.APIVersion != expectedAPIVersion {
		return fmt.Errorf("release: apiVersion must be %q, got %q", expectedAPIVersion, doc.APIVersion)
	}
	if doc.Kind != releaseKind {
		return fmt.Errorf("release: kind must be %q, got %q", releaseKind, doc.Kind)
	}
	if strings.TrimSpace(doc.Metadata.Name) == "" {
		return fmt.Errorf("release: metadata.name is required")
	}
	if strings.TrimSpace(doc.Spec.Aggregate) == "" {
		return fmt.Errorf("release: spec.aggregate is required")
	}
	if strings.TrimSpace(doc.Spec.Manifest) == "" {
		return fmt.Errorf("release: spec.manifest is required")
	}
	if len(doc.Spec.Scopes) == 0 {
		return fmt.Errorf("release: spec.scopes must not be empty")
	}
	for i, s := range doc.Spec.Scopes {
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("release: spec.scopes[%d].name is required", i)
		}
		if strings.TrimSpace(s.Provider) == "" {
			return fmt.Errorf("release: spec.scopes[%d].provider is required", i)
		}
	}
	return nil
}

func releaseYAMLToTypes(doc *releaseYAMLFile) (*types.Release, error) {
	inputs, err := mapToReleaseInputs(doc.Spec.Inputs)
	if err != nil {
		return nil, err
	}
	var pol *types.ReleasePolicies
	if doc.Spec.Policies != nil {
		pol = &types.ReleasePolicies{
			StrictNetworking:       doc.Spec.Policies.StrictNetworking,
			EnforceGuardrails:      doc.Spec.Policies.EnforceGuardrails,
			EnforceTrustDirectives: doc.Spec.Policies.EnforceTrustDirectives,
		}
	}
	scopes := make([]types.Scope, len(doc.Spec.Scopes))
	for i, s := range doc.Spec.Scopes {
		scopes[i] = types.Scope{
			Name:        s.Name,
			Provider:    s.Provider,
			Components:  append([]string(nil), s.Components...),
			MatchLabels: s.MatchLabels,
		}
	}
	return &types.Release{
		APIVersion: doc.APIVersion,
		Kind:       doc.Kind,
		Metadata: types.ReleaseMetadata{
			Name: doc.Metadata.Name,
		},
		Spec: types.ReleaseSpec{
			Aggregate: doc.Spec.Aggregate,
			Manifest:  doc.Spec.Manifest,
			Inputs:    inputs,
			Policies:  pol,
			Scopes:    scopes,
		},
	}, nil
}

func mapToReleaseInputs(m map[string]interface{}) (map[string]json.RawMessage, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("release: spec.inputs[%q]: %w", k, err)
		}
		out[k] = b
	}
	return out, nil
}
