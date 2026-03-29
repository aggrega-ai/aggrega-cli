package types

import "encoding/json"

// API version constant for SecurityMatrix documents validated against
// securitymatrix-v1alpha1.schema.json.
const SecurityMatrixAPIVersion = "aggrega.ai/v1alpha1"

// SecurityMatrixKind is the canonical kind value for SecurityMatrix documents.
const SecurityMatrixKind = "SecurityMatrix"

// SecurityMatrix is the canonical, target-agnostic security blueprint produced by the
// security policy translator (Intent 004). It strictly mirrors
// securitymatrix-v1alpha1.schema.json and is consumed together with IRGraph by the
// linter (Intent 005) and provider engine (Intent 002).
type SecurityMatrix struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Metadata   SecurityMatrixMetadata `json:"metadata"`
	Spec       SecurityMatrixSpec     `json:"spec"`
}

// SecurityMatrixMetadata links the matrix to the IR build that produced it.
type SecurityMatrixMetadata struct {
	// IR identifies the specific IR graph instance this matrix was derived from.
	IR string `json:"ir"`
}

// SecurityMatrixSpec holds trust boundaries, RBAC, guardrails, and related directives.
type SecurityMatrixSpec struct {
	// TrustBoundaries maps node IDs to ingress/egress allow lists and exposure flags.
	TrustBoundaries map[string]TrustBoundary `json:"trust_boundaries"`

	// RBAC lists least-privilege relationships between subject and target nodes.
	RBAC []RBACBinding `json:"rbac"`

	// DataPlaneGuardrails enumerates L7 policies enforced at data-plane plugins.
	DataPlaneGuardrails []DataPlaneGuardrail `json:"data_plane_guardrails"`

	// DataExfiltrationPrevention defines label-based flow rules per enforcement node.
	DataExfiltrationPrevention []DataExfiltrationRule `json:"data_exfiltration_prevention"`

	TrustDirectives []TrustDirectiveBinding `json:"trust_directives"`

	IsolationDirectives []IsolationDirective `json:"isolation_directives"`

	ObservabilityDirectives []ObservabilityDirective `json:"observability_directives"`

	// Provenance carries traceability metadata. The schema allows additional properties.
	Provenance SecurityMatrixProvenance `json:"provenance"`
}

// TrustBoundary captures ingress/egress policy for one node.
type TrustBoundary struct {
	AllowIngressFrom []string `json:"allow_ingress_from,omitempty"`
	AllowEgressTo    []string `json:"allow_egress_to,omitempty"`
	PublicFacing     bool     `json:"public_facing,omitempty"`
}

// RBACBinding describes allowed actions from a subject node on a target node.
type RBACBinding struct {
	SubjectNode string `json:"subject_node"`
	TargetNode  string `json:"target_node"`
	// Actions lists permitted verbs (for example invoke, read_state, bind).
	Actions []string `json:"actions"`
	// ContextPropagation lists cryptographic identities (for example JWT roles) that
	// must be present in the payload for transitive trust (confused-deputy mitigation).
	ContextPropagation []string `json:"context_propagation,omitempty"`
}

// DataPlaneGuardrail is an L7 instruction applied at a specific enforcement node.
type DataPlaneGuardrail struct {
	// EnforcementNode is the node ID (often a gateway or agent) where this applies.
	EnforcementNode string `json:"enforcement_node"`
	// PolicyType names the guardrail (for example data_masking, prompt_injection_shield).
	PolicyType string `json:"policy_type"`
	// Config holds CUE-validated settings as arbitrary JSON.
	Config map[string]json.RawMessage `json:"config"`
}

// DataExfiltrationRule applies data-plane label tracking at an enforcement node.
type DataExfiltrationRule struct {
	// EnforcementNode is the node whose proxy enforces these flow rules.
	EnforcementNode string `json:"enforcement_node"`
	// AppliedLabels are labels attached to outbound payloads from this node.
	AppliedLabels []string `json:"applied_labels,omitempty"`
	// BlockedLabels are labels that must not pass through or terminate at this node.
	BlockedLabels []string `json:"blocked_labels,omitempty"`
}

// TrustDirectiveBinding groups required trust checks for a path or tool.
type TrustDirectiveBinding struct {
	// PathOrTool identifies the logical node or edge requiring verification.
	PathOrTool string                `json:"path_or_tool"`
	Directives []NamedTrustDirective `json:"directives"`
}

// NamedTrustDirective is one named trust requirement with optional CUE-validated config.
type NamedTrustDirective struct {
	// Name identifies the directive (for example cryptographic_signature, human_approval_gate).
	Name string `json:"name"`
	// Config holds directive-specific settings as arbitrary JSON.
	Config map[string]json.RawMessage `json:"config,omitempty"`
}

// IsolationDirective constrains physical execution for a target node.
type IsolationDirective struct {
	TargetNode     string `json:"target_node"`
	IsolationLevel string `json:"isolation_level"`
}

// ObservabilityDirective configures compliance or telemetry extraction for a node.
type ObservabilityDirective struct {
	TargetNode string `json:"target_node"`
	// Type names the directive (for example audit_logging, llm_tracing).
	Type string `json:"type"`
	// Config holds CUE-validated routing settings as arbitrary JSON.
	Config map[string]json.RawMessage `json:"config"`
}

// SecurityMatrixProvenance records matrix lineage relative to the IR and compiler.
// The schema allows additional properties beyond the listed fields.
type SecurityMatrixProvenance struct {
	IRIdentifier     string                     `json:"ir_identifier,omitempty"`
	Intent004Version string                     `json:"intent_004_version,omitempty"`
	SourceLocations  map[string]json.RawMessage `json:"source_locations,omitempty"`
}
