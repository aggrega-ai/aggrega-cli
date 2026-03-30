package security

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestTranslateFromIR_table(t *testing.T) {
	t.Parallel()

	baseIR := func() *types.IRGraph {
		return &types.IRGraph{
			APIVersion: types.IRAPIVersion,
			Kind:       types.IRKind,
			Metadata:   types.IRMetadata{AppName: "test"},
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
				Annotations: map[string]json.RawMessage{},
			},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*types.IRGraph)
		wantErr error
	}{
		{
			name: "happy_empty_routing",
			mutate: func(ir *types.IRGraph) {
				ir.Spec.Routing = nil
			},
		},
		{
			name: "happy_routing_with_trust_and_context",
			mutate: func(ir *types.IRGraph) {
				ir.Spec.Annotations = map[string]json.RawMessage{
					AnnotTrustPrefix + "gw":         json.RawMessage(`{}`),
					AnnotContextPropagation + "gw": json.RawMessage(`{}`),
				}
			},
		},
		{
			name:    "nil_ir",
			mutate:  func(ir *types.IRGraph) { ir.Spec.Routing = nil },
			wantErr: ErrNilIR,
		},
		{
			name: "missing_trust_verification",
			mutate: func(ir *types.IRGraph) {
				ir.Spec.Annotations = map[string]json.RawMessage{
					AnnotContextPropagation + "gw": json.RawMessage(`{}`),
				}
			},
			wantErr: ErrMissingTrustVerification,
		},
		{
			name: "confused_deputy_missing_context_propagation",
			mutate: func(ir *types.IRGraph) {
				ir.Spec.Annotations = map[string]json.RawMessage{
					AnnotTrustPrefix + "gw": json.RawMessage(`{}`),
				}
			},
			wantErr: ErrConfusedDeputyPath,
		},
		{
			name: "unknown_gateway",
			mutate: func(ir *types.IRGraph) {
				ir.Spec.Routing[0].GatewayNodeID = "missing"
				ir.Spec.Annotations = map[string]json.RawMessage{
					AnnotTrustPrefix + "missing":         json.RawMessage(`{}`),
					AnnotContextPropagation + "missing": json.RawMessage(`{}`),
				}
			},
			wantErr: ErrUnknownRoutingGateway,
		},
		{
			name: "unknown_sink",
			mutate: func(ir *types.IRGraph) {
				ir.Spec.Routing[0].SinkNodeID = "nope"
				ir.Spec.Annotations = map[string]json.RawMessage{
					AnnotTrustPrefix + "gw":         json.RawMessage(`{}`),
					AnnotContextPropagation + "gw": json.RawMessage(`{}`),
				}
			},
			wantErr: ErrUnknownRoutingSink,
		},
		{
			name: "unknown_source",
			mutate: func(ir *types.IRGraph) {
				ir.Spec.Routing[0].SourceNodeID = "gone"
				ir.Spec.Annotations = map[string]json.RawMessage{
					AnnotTrustPrefix + "gw":         json.RawMessage(`{}`),
					AnnotContextPropagation + "gw": json.RawMessage(`{}`),
				}
			},
			wantErr: ErrUnknownRoutingSource,
		},
		{
			name: "non_agent_sink_skips_context_requirement",
			mutate: func(ir *types.IRGraph) {
				ir.Spec.Nodes["bot"] = types.IRNode{ResolvedType: "ai-tool", Component: "tool"}
				ir.Spec.Annotations = map[string]json.RawMessage{
					AnnotTrustPrefix + "gw": json.RawMessage(`{}`),
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.name == "nil_ir" {
				_, err := TranslateFromIR(nil)
				assertWrapped(t, err, tc.wantErr)
				return
			}

			ir := baseIR()
			tc.mutate(ir)
			matrix, err := TranslateFromIR(ir)

			if tc.wantErr != nil {
				assertWrapped(t, err, tc.wantErr)
				if err != nil && !strings.Contains(err.Error(), "security.translate") {
					t.Fatalf("expected wrapped error, got %v", err)
				}
				if matrix != nil {
					t.Fatal("expected nil matrix on error")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if matrix == nil {
				t.Fatal("expected non-nil matrix")
			}
		})
	}
}

func assertWrapped(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, target) {
		t.Fatalf("errors.Is: got %v, want chain with %v", err, target)
	}
}
