package types

import "encoding/json"

// API version constant for Release documents validated against release-v1alpha1.schema.json.
const ReleaseAPIVersion = "aggrega.ai/v1alpha1"

// ReleaseKind is the canonical kind value for Release documents (Domain 4 deployment router).
const ReleaseKind = "Release"

// Release binds an Aggregate to a Manifest, assigns Providers, and scopes components to
// physical targets. It strictly mirrors release-v1alpha1.schema.json and is the typed
// contract between release configuration and the provider engine.
type Release struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   ReleaseMetadata `json:"metadata"`
	Spec       ReleaseSpec     `json:"spec"`
}

// ReleaseMetadata identifies this release instance.
type ReleaseMetadata struct {
	Name string `json:"name"`
}

// ReleaseSpec is the spec subtree: aggregate reference, manifest, optional inputs and
// policies, and placement scopes.
type ReleaseSpec struct {
	// Aggregate is the name of the Aggregate to deploy.
	Aggregate string `json:"aggregate"`
	// Manifest is the Manifest used for software version resolution.
	Manifest string `json:"manifest"`
	// Inputs carries environment-specific variables injected into the Aggregate.
	Inputs map[string]json.RawMessage `json:"inputs,omitempty"`
	// Policies holds permissive execution overrides (for example local testing).
	Policies *ReleasePolicies `json:"policies,omitempty"`
	// Scopes group logical components into physical deployment destinations.
	Scopes []Scope `json:"scopes"`
}

// ReleasePolicies toggles enforcement behaviors in the provider engine. JSON booleans
// are optional; nil means “use schema defaults” (all true in v1alpha1).
type ReleasePolicies struct {
	// StrictNetworking, when false, allows the provider to ignore trust boundaries and
	// provision a flat network.
	StrictNetworking *bool `json:"strict_networking,omitempty"`
	// EnforceGuardrails, when false, skips provisioning L7 data-plane proxies.
	EnforceGuardrails *bool `json:"enforce_guardrails,omitempty"`
	// EnforceTrustDirectives, when false, skips cryptographic checks and approval gates.
	EnforceTrustDirectives *bool `json:"enforce_trust_directives,omitempty"`
}

// Scope is one physical placement rule: a named destination, provider engine, and
// optional explicit components or label selectors.
type Scope struct {
	// Name is the physical destination (for example aws-eks-us-east-ns).
	Name string `json:"name"`
	// Provider names the Provider that selects the rendering engine for this scope.
	Provider string `json:"provider"`
	// Components, when set, lists Component IDs placed explicitly in this scope.
	Components []string `json:"components,omitempty"`
	// MatchLabels selects components dynamically by label.
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}
