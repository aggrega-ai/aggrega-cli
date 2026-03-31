package types

import "strings"

// Catalog is a materialized Platform Catalog of abstract component definitions (Domain 1).
type Catalog struct {
	// ComponentDefinitions is keyed by ontology id (component.aggrega.ai/type value),
	// falling back to metadata.name when building the index.
	ComponentDefinitions map[string]ComponentDefinition `yaml:"componentDefinitions,omitempty"`
}

// ComponentDefinition is the catalog record for a component interface (YAML envelope).
type ComponentDefinition struct {
	APIVersion string            `yaml:"apiVersion" json:"apiVersion"`
	Kind       string            `yaml:"kind" json:"kind"`
	Metadata   ComponentMetadata `yaml:"metadata" json:"metadata"`
	Spec       ComponentSpec     `yaml:"spec" json:"spec"`
}

// ComponentMetadata is catalog metadata for a component definition.
type ComponentMetadata struct {
	Name    string            `yaml:"name" json:"name"`
	Labels  map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Version string            `yaml:"version,omitempty" json:"version,omitempty"`
}

// ComponentSpec is the spec block of a catalog Component / ComponentDefinition.
type ComponentSpec struct {
	SecretNames []string `json:"secretNames,omitempty" yaml:"secretNames,omitempty"`

	// RequiredFeatures lists feature types that must be attached on every Aggregate instance
	// of this component (compiler hook; OAM safety boundary).
	RequiredFeatures []string `json:"requiredFeatures,omitempty" yaml:"requiredFeatures,omitempty"`

	CueSchema    string `json:"cueSchema,omitempty" yaml:"cueSchema,omitempty"`
	CueSchemaRef string `json:"cueSchemaRef,omitempty" yaml:"cueSchemaRef,omitempty"`
}

// SpecForComponent returns the catalog spec for an aggregate component's ontology type.
func (c *Catalog) SpecForComponent(ontologyType string) (*ComponentSpec, bool) {
	if c == nil || len(c.ComponentDefinitions) == 0 {
		return nil, false
	}
	ontologyType = strings.TrimSpace(ontologyType)
	if def, ok := c.ComponentDefinitions[ontologyType]; ok {
		return &def.Spec, true
	}
	for _, def := range c.ComponentDefinitions {
		if def.Metadata.Labels != nil && def.Metadata.Labels["component.aggrega.ai/type"] == ontologyType {
			return &def.Spec, true
		}
		if strings.TrimSpace(def.Metadata.Name) == ontologyType {
			return &def.Spec, true
		}
	}
	return nil, false
}
