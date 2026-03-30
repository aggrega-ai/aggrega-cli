package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	composeFileVersion  = "3.8"
	defaultServiceImage = "aggrega/placeholder:latest"
	strictNetworkName   = "aggrega"
	gatewayResolvedType = "ai-gateway"
)

// ComposeFile is a minimal Compose Specification V3-compatible document for marshaling.
type ComposeFile struct {
	Version  string             `yaml:"version"`
	Services map[string]Service `yaml:"services"`
	Networks map[string]Network `yaml:"networks,omitempty"`
	Volumes  map[string]Volume  `yaml:"volumes,omitempty"`
}

// Service is one compose service definition.
type Service struct {
	Image       string            `yaml:"image"`
	Ports       []string          `yaml:"ports,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Networks    []string          `yaml:"networks,omitempty"`
}

// Network describes a user-defined network (driver, etc.).
type Network struct {
	Driver string `yaml:"driver,omitempty"`
}

// Volume is a named volume placeholder for future driver options.
type Volume struct{}

// ComposeEngine renders IR graphs into Docker Compose files.
type ComposeEngine struct{}

// Render emits compose.yaml from the IR graph and release policies.
func (e *ComposeEngine) Render(ctx context.Context, graph *types.IRGraph, release *types.Release, scope types.Scope) (map[string][]byte, error) {
	_ = scope
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, fmt.Errorf("compose: graph is nil")
	}

	strictNet := strictNetworkingEnabled(release)

	file := ComposeFile{
		Version:  composeFileVersion,
		Services: make(map[string]Service),
	}
	if strictNet {
		file.Networks = map[string]Network{
			strictNetworkName: {Driver: "bridge"},
		}
	}

	ids := nodeIDsSorted(graph.Spec.Nodes)
	for _, nodeID := range ids {
		node := graph.Spec.Nodes[nodeID]
		raw := graph.Spec.ValuesMapping[nodeID]
		payload, err := parseValuesPayload(raw, nodeID)
		if err != nil {
			return nil, err
		}
		payload = composePortFallbacks(node, strictNet, payload)

		svc := Service{
			Image:       payload.image,
			Environment: payload.environment,
		}
		if svc.Image == "" {
			svc.Image = defaultServiceImage
		}

		if strictNet {
			svc.Networks = []string{strictNetworkName}
			if node.ResolvedType == gatewayResolvedType && len(payload.ports) > 0 {
				svc.Ports = append([]string(nil), payload.ports...)
			}
		} else {
			if len(payload.ports) > 0 {
				svc.Ports = append([]string(nil), payload.ports...)
			}
		}

		file.Services[nodeID] = svc
	}

	yamlBytes, err := yaml.Marshal(&file)
	if err != nil {
		return nil, fmt.Errorf("compose: marshal yaml: %w", err)
	}
	return map[string][]byte{"compose.yaml": yamlBytes}, nil
}

func strictNetworkingEnabled(release *types.Release) bool {
	if release == nil || release.Spec.Policies == nil || release.Spec.Policies.StrictNetworking == nil {
		return true
	}
	return *release.Spec.Policies.StrictNetworking
}

func nodeIDsSorted(nodes map[string]types.IRNode) []string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

type extractedPayload struct {
	image       string
	ports       []string
	environment map[string]string
}

// composePortFallbacks supplies published ports when values_mapping omits them (recipe not wired).
// Domain-1 aggregates never carry ports; the engine must not require them on the IR node struct.
func composePortFallbacks(node types.IRNode, strict bool, p extractedPayload) extractedPayload {
	if len(p.ports) > 0 {
		return p
	}
	if strict {
		if node.ResolvedType == gatewayResolvedType {
			p.ports = []string{"8080:8080"}
		}
		return p
	}
	p.ports = []string{"3000:3000"}
	return p
}

func parseValuesPayload(raw json.RawMessage, nodeID string) (extractedPayload, error) {
	var out extractedPayload
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}

	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return extractedPayload{}, fmt.Errorf("compose: node %q: values_mapping: %w", nodeID, err)
	}

	if v, ok := m["image"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return extractedPayload{}, fmt.Errorf("compose: node %q: values_mapping.image must be a string", nodeID)
		}
		out.image = s
	}

	if v, ok := m["ports"]; ok && v != nil {
		ports, err := coerceStringSlice(v, nodeID, "ports")
		if err != nil {
			return extractedPayload{}, err
		}
		out.ports = ports
	}

	if v, ok := m["environment"]; ok && v != nil {
		envMap, ok := v.(map[string]interface{})
		if !ok {
			return extractedPayload{}, fmt.Errorf("compose: node %q: values_mapping.environment must be a JSON object", nodeID)
		}
		env, err := stringifyEnvironment(envMap, nodeID)
		if err != nil {
			return extractedPayload{}, err
		}
		out.environment = env
	}

	return out, nil
}

func coerceStringSlice(v interface{}, nodeID, field string) ([]string, error) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("compose: node %q: values_mapping.%s must be a JSON array", nodeID, field)
	}
	out := make([]string, 0, len(arr))
	for i, el := range arr {
		s, err := scalarToString(el)
		if err != nil {
			return nil, fmt.Errorf("compose: node %q: values_mapping.%s[%d]: %w", nodeID, field, i, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func scalarToString(v interface{}) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(t), nil
	case nil:
		return "", fmt.Errorf("null element")
	default:
		return "", fmt.Errorf("unsupported type %T", v)
	}
}

func stringifyEnvironment(m map[string]interface{}, nodeID string) (map[string]string, error) {
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, err := scalarToString(v)
		if err != nil {
			return nil, fmt.Errorf("compose: node %q: values_mapping.environment[%q]: %w", nodeID, k, err)
		}
		out[k] = s
	}
	return out, nil
}
