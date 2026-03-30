package linter

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// Sentinel errors for table-driven tests.
var (
	ErrNilInput            = errors.New("nil input")
	ErrCapabilityPurity    = errors.New("capability purity violation on catalog tool")
	ErrFeatureConflict     = errors.New("conflictsWith: incompatible features active")
	ErrMissingSecurityInRBAC = errors.New("security matrix rbac missing for routed gateway invoke")
)

// conflictsWith lists feature markers (normalized lowercase) that must not appear together.
var conflictsWith = map[string][]string{
	"prompt_guard":         {"raw_llm_output"},
	"data_masking":         {"plaintext_audit_export"},
	"semantic_routing":     {"legacy_static_route_only"},
}

// forbiddenToolCapabilities are feature keys (see FeatureValuesMapping) disallowed on ai-tool nodes.
var forbiddenToolCapabilities = map[string]struct{}{
	"privileged_network": {},
}

// Analyze runs Intent 005 static checks over IR and the security matrix.
func Analyze(ir *types.IRGraph, matrix *types.SecurityMatrix) error {
	if ir == nil {
		return fmt.Errorf("linter.analyze: ir: %w", ErrNilInput)
	}
	if matrix == nil {
		return fmt.Errorf("linter.analyze: matrix: %w", ErrNilInput)
	}

	if err := checkCapabilityPurity(ir); err != nil {
		return err
	}
	if err := checkConflictsWith(ir); err != nil {
		return err
	}
	if err := checkMatrixAgainstRouting(ir, matrix); err != nil {
		return err
	}
	return nil
}

func checkCapabilityPurity(ir *types.IRGraph) error {
	for id, node := range ir.Spec.Nodes {
		if node.ResolvedType != "ai-tool" {
			continue
		}
		fm := ir.Spec.FeatureValuesMapping[id]
		if fm == nil {
			continue
		}
		for capKey := range fm {
			nk := strings.ToLower(strings.TrimSpace(capKey))
			if _, bad := forbiddenToolCapabilities[nk]; bad {
				return fmt.Errorf("linter: node %q (ai-tool) must not attach capability %q: %w", id, capKey, ErrCapabilityPurity)
			}
		}
	}
	return nil
}

func checkConflictsWith(ir *types.IRGraph) error {
	markers := make(map[string]struct{}, len(ir.Spec.FeatureMarkers))
	for _, mk := range ir.Spec.FeatureMarkers {
		markers[normalizeMarker(mk)] = struct{}{}
	}
	for a := range markers {
		for _, other := range conflictsWith[a] {
			if _, clash := markers[normalizeMarker(other)]; clash {
				return fmt.Errorf("linter: features %q and %q are incompatible: %w", a, other, ErrFeatureConflict)
			}
		}
	}
	return nil
}

func checkMatrixAgainstRouting(ir *types.IRGraph, matrix *types.SecurityMatrix) error {
	if len(ir.Spec.Routing) == 0 {
		return nil
	}
	for _, route := range ir.Spec.Routing {
		if !rbacCoversGatewayInvoke(matrix, route.GatewayNodeID, route.SinkNodeID) {
			return fmt.Errorf(
				"linter: missing rbac invoke from gateway %q to sink %q for declared routing: %w",
				route.GatewayNodeID,
				route.SinkNodeID,
				ErrMissingSecurityInRBAC,
			)
		}
	}
	return nil
}

func rbacCoversGatewayInvoke(m *types.SecurityMatrix, gatewayID, sinkID string) bool {
	for _, b := range m.Spec.RBAC {
		if b.SubjectNode != gatewayID || b.TargetNode != sinkID {
			continue
		}
		for _, a := range b.Actions {
			if strings.EqualFold(a, "invoke") {
				return true
			}
		}
	}
	return false
}

func normalizeMarker(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
