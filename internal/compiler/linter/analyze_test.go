package linter

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/security"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestAnalyze_table(t *testing.T) {
	t.Parallel()

	minIR := func() *types.IRGraph {
		return &types.IRGraph{
			APIVersion: types.IRAPIVersion,
			Kind:       types.IRKind,
			Metadata:   types.IRMetadata{AppName: "lint"},
			Spec: types.IRSpec{
				Nodes: map[string]types.IRNode{
					"tool1": {ResolvedType: "ai-tool", Component: "t"},
				},
				FeatureMarkers: []string{},
				FeatureValuesMapping: map[string]map[string]json.RawMessage{
					"tool1": {},
				},
			},
		}
	}

	minMatrix := func() *types.SecurityMatrix {
		return &types.SecurityMatrix{
			APIVersion: types.SecurityMatrixAPIVersion,
			Kind:       types.SecurityMatrixKind,
			Spec:       types.SecurityMatrixSpec{},
		}
	}

	tests := []struct {
		name    string
		setup   func(*types.IRGraph, *types.SecurityMatrix)
		wantErr error
	}{
		{
			name: "happy_no_violation",
			setup: func(ir *types.IRGraph, m *types.SecurityMatrix) {
				ir.Spec.FeatureMarkers = []string{"prompt_guard"}
				_ = m
			},
		},
		{
			name: "nil_ir",
			setup: func(ir *types.IRGraph, m *types.SecurityMatrix) {
				// handled in runner
				_ = ir
				_ = m
			},
			wantErr: ErrNilInput,
		},
		{
			name: "nil_matrix",
			setup: func(ir *types.IRGraph, m *types.SecurityMatrix) {
				_ = ir
				_ = m
			},
			wantErr: ErrNilInput,
		},
		{
			name: "capability_purity_forbidden_on_ai_tool",
			setup: func(ir *types.IRGraph, m *types.SecurityMatrix) {
				ir.Spec.FeatureValuesMapping["tool1"] = map[string]json.RawMessage{
					"privileged_network": json.RawMessage(`{}`),
				}
			},
			wantErr: ErrCapabilityPurity,
		},
		{
			name: "conflictsWith_prompt_guard_and_raw_llm_output",
			setup: func(ir *types.IRGraph, m *types.SecurityMatrix) {
				ir.Spec.FeatureMarkers = []string{"prompt_guard", "raw_llm_output"}
			},
			wantErr: ErrFeatureConflict,
		},
		{
			name: "conflictsWith_data_masking_and_plaintext_audit",
			setup: func(ir *types.IRGraph, m *types.SecurityMatrix) {
				ir.Spec.FeatureMarkers = []string{"data_masking", "plaintext_audit_export"}
			},
			wantErr: ErrFeatureConflict,
		},
		{
			name: "missing_rbac_for_routing",
			setup: func(ir *types.IRGraph, m *types.SecurityMatrix) {
				ir.Spec.Nodes["gw"] = types.IRNode{ResolvedType: "ai-gateway", Component: "g"}
				ir.Spec.Nodes["ag"] = types.IRNode{ResolvedType: "ai-agent", Component: "a"}
				ir.Spec.Routing = []types.IRRouting{{
					SourceNodeID:  "gw",
					GatewayNodeID: "gw",
					SinkNodeID:    "ag",
				}}
				m.Spec.RBAC = nil
			},
			wantErr: ErrMissingSecurityInRBAC,
		},
		{
			name: "happy_routing_with_matching_rbac",
			setup: func(ir *types.IRGraph, m *types.SecurityMatrix) {
				ir.Spec.Nodes["gw"] = types.IRNode{ResolvedType: "ai-gateway", Component: "g"}
				ir.Spec.Nodes["ag"] = types.IRNode{ResolvedType: "ai-agent", Component: "a"}
				ir.Spec.Routing = []types.IRRouting{{
					SourceNodeID:  "gw",
					GatewayNodeID: "gw",
					SinkNodeID:    "ag",
				}}
				m.Spec.RBAC = []types.RBACBinding{{
					SubjectNode: "gw",
					TargetNode:  "ag",
					Actions:     []string{"invoke"},
				}}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			switch tc.name {
			case "nil_ir":
				err := Analyze(nil, minMatrix())
				assertLintWrapped(t, err, tc.wantErr)
				return
			case "nil_matrix":
				err := Analyze(minIR(), nil)
				assertLintWrapped(t, err, tc.wantErr)
				return
			}

			ir := minIR()
			mx := minMatrix()
			tc.setup(ir, mx)

			err := Analyze(ir, mx)
			if tc.wantErr != nil {
				assertLintWrapped(t, err, tc.wantErr)
				if err != nil && !strings.Contains(err.Error(), "linter") {
					t.Fatalf("expected wrapped linter error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
		})
	}
}

func assertLintWrapped(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, target) {
		t.Fatalf("errors.Is: got %v, want %v", err, target)
	}
}

// TestPipeline_translateThenLint asserts dependency order: security matrix before lint.
func TestPipeline_translateThenLint(t *testing.T) {
	t.Parallel()

	ir := &types.IRGraph{
		APIVersion: types.IRAPIVersion,
		Kind:       types.IRKind,
		Metadata:   types.IRMetadata{AppName: "pipe"},
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"src": {ResolvedType: "ai-gateway", Component: "gw"},
				"gw":  {ResolvedType: "ai-gateway", Component: "gw"},
				"bot": {ResolvedType: "ai-agent", Component: "agent"},
			},
			Routing: []types.IRRouting{{
				SourceNodeID:  "src",
				GatewayNodeID: "gw",
				SinkNodeID:    "bot",
			}},
			Annotations: map[string]json.RawMessage{
				security.AnnotTrustPrefix + "gw":       json.RawMessage(`{}`),
				security.AnnotContextPropagation + "gw": json.RawMessage(`{}`),
			},
			FeatureMarkers: []string{"prompt_guard"},
			FeatureValuesMapping: map[string]map[string]json.RawMessage{
				"src": {},
				"gw":  {},
				"bot": {},
			},
		},
	}

	mx, err := security.TranslateFromIR(ir)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if err := Analyze(ir, mx); err != nil {
		t.Fatalf("analyze: %v", err)
	}
}
