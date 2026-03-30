package security

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// Sentinel errors for table-driven tests and upstream error matching.
var (
	ErrNilIR                    = errors.New("nil IR graph")
	ErrMissingTrustVerification = errors.New("missing trust verification for gateway-mediated routing")
	ErrConfusedDeputyPath       = errors.New("confused deputy risk: cross-boundary invoke without context propagation")
	ErrUnknownRoutingGateway    = errors.New("routing references unknown gateway node")
	ErrUnknownRoutingSink       = errors.New("routing references unknown sink node")
	ErrUnknownRoutingSource     = errors.New("routing references unknown source node")
)

const (
	// AnnotTrustPrefix is the IR annotation key prefix for mandatory trust verification on a gateway.
	AnnotTrustPrefix = "aggrega.ai/trust_verification/"
	// AnnotContextPropagation is the IR annotation key prefix for confused-deputy context binding.
	AnnotContextPropagation = "aggrega.ai/context_propagation/"
)

// TranslateFromIR builds a SecurityMatrix from the IR graph (Intent 004).
// It fails closed on gateway-mediated paths that omit mandatory trust or
// confused-deputy guards when the sink is an interactive agent.
func TranslateFromIR(ir *types.IRGraph) (*types.SecurityMatrix, error) {
	if ir == nil {
		return nil, fmt.Errorf("security.translate: %w", ErrNilIR)
	}

	m := &types.SecurityMatrix{
		APIVersion: types.SecurityMatrixAPIVersion,
		Kind:       types.SecurityMatrixKind,
		Metadata: types.SecurityMatrixMetadata{
			IR: ir.Spec.Provenance.SchemaVersion,
		},
		Spec: types.SecurityMatrixSpec{
			TrustBoundaries:            make(map[string]types.TrustBoundary),
			DataPlaneGuardrails:        nil,
			DataExfiltrationPrevention: nil,
			TrustDirectives:            nil,
			IsolationDirectives:        nil,
			ObservabilityDirectives:    nil,
			Provenance: types.SecurityMatrixProvenance{
				IRIdentifier:     ir.Spec.Provenance.SchemaVersion,
				Intent004Version: "1",
			},
		},
	}

	if len(ir.Spec.Routing) == 0 {
		return m, nil
	}

	m.Spec.RBAC = make([]types.RBACBinding, 0, len(ir.Spec.Routing))
	m.Spec.TrustDirectives = make([]types.TrustDirectiveBinding, 0, len(ir.Spec.Routing))

	for _, route := range ir.Spec.Routing {
		if err := materializeRoute(ir, m, route); err != nil {
			return nil, err
		}
	}

	return m, nil
}

func materializeRoute(ir *types.IRGraph, m *types.SecurityMatrix, route types.IRRouting) error {
	if _, ok := ir.Spec.Nodes[route.SourceNodeID]; !ok {
		return fmt.Errorf("security.translate: route source %q: %w", route.SourceNodeID, ErrUnknownRoutingSource)
	}
	gw, ok := ir.Spec.Nodes[route.GatewayNodeID]
	if !ok {
		return fmt.Errorf("security.translate: route gateway %q: %w", route.GatewayNodeID, ErrUnknownRoutingGateway)
	}
	sink, ok := ir.Spec.Nodes[route.SinkNodeID]
	if !ok {
		return fmt.Errorf("security.translate: route sink %q: %w", route.SinkNodeID, ErrUnknownRoutingSink)
	}

	trustKey := AnnotTrustPrefix + route.GatewayNodeID
	if !annotationPresent(ir.Spec.Annotations, trustKey) {
		return fmt.Errorf(
			"security.translate: gateway %q mediates traffic but annotation %q is missing: %w",
			route.GatewayNodeID,
			trustKey,
			ErrMissingTrustVerification,
		)
	}

	// Trust directive emitted for linter / late binding consumers.
	m.Spec.TrustDirectives = append(m.Spec.TrustDirectives, types.TrustDirectiveBinding{
		PathOrTool: route.GatewayNodeID,
		Directives: []types.NamedTrustDirective{
			{Name: "gateway_trust_anchor"},
		},
	})

	invoke := types.RBACBinding{
		SubjectNode: route.GatewayNodeID,
		TargetNode:  route.SinkNodeID,
		Actions:     []string{"invoke"},
	}

	if sink.ResolvedType == "ai-agent" {
		ctxKey := AnnotContextPropagation + route.GatewayNodeID
		if !annotationPresent(ir.Spec.Annotations, ctxKey) {
			return fmt.Errorf(
				"security.translate: gateway %q invokes ai-agent %q without %q: %w",
				route.GatewayNodeID,
				route.SinkNodeID,
				ctxKey,
				ErrConfusedDeputyPath,
			)
		}
		invoke.ContextPropagation = []string{"jwt_role_chain", "request_attestation"}
	}

	_ = gw
	m.Spec.RBAC = append(m.Spec.RBAC, invoke)

	if _, ok := m.Spec.TrustBoundaries[route.GatewayNodeID]; !ok {
		m.Spec.TrustBoundaries[route.GatewayNodeID] = types.TrustBoundary{
			AllowIngressFrom: []string{route.SourceNodeID},
		}
	}
	if _, ok := m.Spec.TrustBoundaries[route.SinkNodeID]; !ok {
		m.Spec.TrustBoundaries[route.SinkNodeID] = types.TrustBoundary{
			AllowIngressFrom: []string{route.GatewayNodeID},
		}
	}

	return nil
}

func annotationPresent(ann map[string]json.RawMessage, key string) bool {
	if ann == nil {
		return false
	}
	_, ok := ann[key]
	return ok
}
