package types

// Provider is the global plugin registrar mapping a rendering engine name to an executable.
// Schema: provider-v1alpha1.schema.json
type Provider struct {
	APIVersion string           `json:"apiVersion" yaml:"apiVersion"`
	Kind       string           `json:"kind" yaml:"kind"`
	Metadata   ProviderMetadata `json:"metadata" yaml:"metadata"`
	Spec       ProviderSpec     `json:"spec" yaml:"spec"`
}

// ProviderMetadata carries identity for a Provider entity.
type ProviderMetadata struct {
	Name        string       `json:"name" yaml:"name"`
	Version     string       `json:"version" yaml:"version"`
	Description string       `json:"description,omitempty" yaml:"description,omitempty"`
	Maintainers []Maintainer `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	License     string       `json:"license,omitempty" yaml:"license,omitempty"`
	Repository  string       `json:"repository,omitempty" yaml:"repository,omitempty"`
	Home        string       `json:"home,omitempty" yaml:"home,omitempty"`
	Keywords    []string     `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Deprecated  bool         `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
}

// ProviderSpec contains the plugin URI for the rendering engine.
type ProviderSpec struct {
	Plugin string `json:"plugin" yaml:"plugin"`
}
