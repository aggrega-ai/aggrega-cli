package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	composeFileVersion  = "3.8"
	defaultServiceImage = "aggrega/placeholder:latest"
	injectedEnvPrefix   = "_aggrega_injected_env_"
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
func (e *ComposeEngine) Render(ctx context.Context, graph *types.IRGraph, release *types.Release, boundary string) (map[string][]byte, error) {
	_ = boundary
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
			if node.Labels[types.ComponentTypeLabel] == gatewayResolvedType && len(payload.ports) > 0 {
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

// strictNetworkingEnabled returns true (the v2 default). The  Policies struct was
// abolished; strict networking is now determined by boundary annotations in the Release.
// This function will be rewritten in Intent 3.
func strictNetworkingEnabled(_ *types.Release) bool {
	return true
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
		if node.Labels[types.ComponentTypeLabel] == gatewayResolvedType {
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

	out.image = extractImageFromValuesMap(m)

	if v, ok := m["ports"]; ok && v != nil {
		ports, err := coerceStringSlice(v, nodeID, "ports")
		if err != nil {
			return extractedPayload{}, err
		}
		out.ports = ports
	}

	mergeStandardEnvironmentInto(&out.environment, m["environment"])

	if err := mergeInjectedEnvFromValuesMap(&out, m, nodeID, "compose"); err != nil {
		return extractedPayload{}, err
	}

	return out, nil
}

func mergeInjectedEnvFromValuesMap(out *extractedPayload, m map[string]interface{}, nodeID, engine string) error {
	prefix := injectedEnvPrefix
	for k, v := range m {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		envName := strings.TrimPrefix(k, prefix)
		if envName == "" {
			continue
		}
		s, err := scalarToString(v)
		if err != nil {
			return fmt.Errorf("%s: node %q: values_mapping[%q]: %w", engine, nodeID, k, err)
		}
		if out.environment == nil {
			out.environment = make(map[string]string)
		}
		out.environment[envName] = s
	}
	return nil
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

func extractImageFromValuesMap(m map[string]interface{}) string {
	v, ok := m["image"]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

// mergeStandardEnvironmentInto copies values["environment"] into dst when it is a map; skips wrong types.
func mergeStandardEnvironmentInto(dst *map[string]string, v interface{}) {
	if v == nil {
		return
	}
	var src map[string]string
	switch em := v.(type) {
	case map[string]interface{}:
		src = stringifyEnvironmentLenient(em)
	case map[string]string:
		src = em
	default:
		return
	}
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]string)
	}
	for k, val := range src {
		(*dst)[k] = val
	}
}

func stringifyEnvironmentLenient(m map[string]interface{}) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, err := scalarToString(v)
		if err != nil {
			out[k] = fmt.Sprintf("%v", v)
			continue
		}
		out[k] = s
	}
	return out
}
