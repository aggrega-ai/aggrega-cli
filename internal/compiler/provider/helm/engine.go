package helm

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
	helmChartAPIVersion = "v2"
	chartVersion        = "0.1.0"
	defaultChartName    = "application"
	defaultImage        = "aggrega/placeholder:latest"

	k8sDeploymentAPIVersion = "apps/v1"
	k8sServiceAPIVersion    = "v1"
)

// Chart is the Helm Chart.yaml document (apiVersion v2).
type Chart struct {
	APIVersion string `yaml:"apiVersion"`
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
}

// Deployment is a minimal apps/v1 Deployment for marshaling to YAML.
type Deployment struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   ObjectMeta     `yaml:"metadata"`
	Spec       DeploymentSpec `yaml:"spec"`
}

// Service is a minimal v1 Service for marshaling to YAML.
type Service struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   ObjectMeta  `yaml:"metadata"`
	Spec       ServiceSpec `yaml:"spec"`
}

// ObjectMeta carries the smallest metadata Helm templates need.
type ObjectMeta struct {
	Name        string            `yaml:"name"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// DeploymentSpec is a reduced Deployment spec (replicas, selector, pod template).
type DeploymentSpec struct {
	Replicas *int32          `yaml:"replicas"`
	Selector *LabelSelector  `yaml:"selector"`
	Template PodTemplateSpec `yaml:"template"`
}

// LabelSelector with matchLabels only.
type LabelSelector struct {
	MatchLabels map[string]string `yaml:"matchLabels"`
}

// PodTemplateSpec wraps pod metadata and spec.
type PodTemplateSpec struct {
	Metadata ObjectMeta `yaml:"metadata"`
	Spec     PodSpec    `yaml:"spec"`
}

// PodSpec holds a single container (sufficient for IR translation).
type PodSpec struct {
	Containers []Container `yaml:"containers"`
}

// Container is one workload container.
type Container struct {
	Name  string          `yaml:"name"`
	Image string          `yaml:"image"`
	Ports []ContainerPort `yaml:"ports,omitempty"`
	Env   []EnvVar        `yaml:"env,omitempty"`
}

// ContainerPort exposes a port on the container.
type ContainerPort struct {
	ContainerPort int32  `yaml:"containerPort"`
	Protocol      string `yaml:"protocol,omitempty"`
}

// EnvVar is a key/value environment variable.
type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// ServiceSpec is a reduced Service spec.
type ServiceSpec struct {
	Type     string            `yaml:"type"`
	Selector map[string]string `yaml:"selector"`
	Ports    []ServicePort     `yaml:"ports"`
}

// ServicePort maps Service port to target port.
type ServicePort struct {
	Name       string `yaml:"name,omitempty"`
	Port       int32  `yaml:"port"`
	TargetPort int32  `yaml:"targetPort"`
	Protocol   string `yaml:"protocol,omitempty"`
}

// HelmEngine renders IR graphs into Helm chart files.
type HelmEngine struct{}

// Render emits Chart.yaml and templates/*.yaml for the given IR graph.
func (e *HelmEngine) Render(ctx context.Context, graph *types.IRGraph, release *types.Release, scope types.Scope) (map[string][]byte, error) {
	_ = scope
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, fmt.Errorf("helm: graph is nil")
	}

	chartName := chartNameFromRelease(release)
	strict := strictNetworkingEnabled(release)

	chart := Chart{
		APIVersion: helmChartAPIVersion,
		Name:       chartName,
		Version:    chartVersion,
	}
	chartYAML, err := yaml.Marshal(&chart)
	if err != nil {
		return nil, fmt.Errorf("helm: marshal Chart.yaml: %w", err)
	}

	out := map[string][]byte{
		"Chart.yaml": chartYAML,
	}

	ids := nodeIDsSorted(graph.Spec.Nodes)
	for _, nodeID := range ids {
		node := graph.Spec.Nodes[nodeID]
		raw := graph.Spec.ValuesMapping[nodeID]
		payload, err := parseValuesPayload(raw, nodeID)
		if err != nil {
			return nil, err
		}

		image := payload.image
		if image == "" {
			image = defaultImage
		}

		replicas := int32(1)
		selectorLabels := map[string]string{"app": nodeID}

		containerPorts, svcPorts, err := portsToK8s(payload.ports, nodeID)
		if err != nil {
			return nil, err
		}

		env := envVarsFromMap(payload.environment)

		deployMeta := ObjectMeta{
			Name:   nodeID,
			Labels: map[string]string{"app.kubernetes.io/name": nodeID},
		}
		if strict {
			deployMeta.Annotations = map[string]string{"aggrega.ai/strict-networking": "true"}
		}

		deploy := Deployment{
			APIVersion: k8sDeploymentAPIVersion,
			Kind:       "Deployment",
			Metadata:   deployMeta,
			Spec: DeploymentSpec{
				Replicas: &replicas,
				Selector: &LabelSelector{MatchLabels: selectorLabels},
				Template: PodTemplateSpec{
					Metadata: ObjectMeta{Labels: selectorLabels},
					Spec: PodSpec{
						Containers: []Container{
							{
								Name:  sanitizeContainerName(nodeID),
								Image: image,
								Ports: containerPorts,
								Env:   env,
							},
						},
					},
				},
			},
		}

		depYAML, err := yaml.Marshal(&deploy)
		if err != nil {
			return nil, fmt.Errorf("helm: node %q: marshal deployment: %w", nodeID, err)
		}
		out[fmt.Sprintf("templates/%s-deployment.yaml", nodeID)] = depYAML

		if len(svcPorts) > 0 {
			svcType := serviceTypeForNode(node)
			svcMeta := ObjectMeta{
				Name:   nodeID,
				Labels: map[string]string{"app.kubernetes.io/name": nodeID},
			}
			if strict {
				svcMeta.Annotations = map[string]string{"aggrega.ai/strict-networking": "true"}
			}
			svc := Service{
				APIVersion: k8sServiceAPIVersion,
				Kind:       "Service",
				Metadata:   svcMeta,
				Spec: ServiceSpec{
					Type:     svcType,
					Selector: selectorLabels,
					Ports:    svcPorts,
				},
			}
			svcYAML, err := yaml.Marshal(&svc)
			if err != nil {
				return nil, fmt.Errorf("helm: node %q: marshal service: %w", nodeID, err)
			}
			out[fmt.Sprintf("templates/%s-svc.yaml", nodeID)] = svcYAML
		}
	}

	return out, nil
}

func chartNameFromRelease(release *types.Release) string {
	if release == nil {
		return defaultChartName
	}
	name := strings.TrimSpace(release.Spec.Aggregate)
	if name == "" {
		return defaultChartName
	}
	return name
}

func strictNetworkingEnabled(release *types.Release) bool {
	if release == nil || release.Spec.Policies == nil || release.Spec.Policies.StrictNetworking == nil {
		return true
	}
	return *release.Spec.Policies.StrictNetworking
}

func serviceTypeForNode(node types.IRNode) string {
	if node.ResolvedType == "gateway" || node.ResolvedType == "ai-gateway" {
		return "LoadBalancer"
	}
	return "ClusterIP"
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

func parseValuesPayload(raw json.RawMessage, nodeID string) (extractedPayload, error) {
	var out extractedPayload
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}

	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return extractedPayload{}, fmt.Errorf("helm: node %q: values_mapping: %w", nodeID, err)
	}

	if v, ok := m["image"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return extractedPayload{}, fmt.Errorf("helm: node %q: values_mapping.image must be a string", nodeID)
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
			return extractedPayload{}, fmt.Errorf("helm: node %q: values_mapping.environment must be a JSON object", nodeID)
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
		return nil, fmt.Errorf("helm: node %q: values_mapping.%s must be a JSON array", nodeID, field)
	}
	out := make([]string, 0, len(arr))
	for i, el := range arr {
		s, err := scalarToString(el)
		if err != nil {
			return nil, fmt.Errorf("helm: node %q: values_mapping.%s[%d]: %w", nodeID, field, i, err)
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
			return nil, fmt.Errorf("helm: node %q: values_mapping.environment[%q]: %w", nodeID, k, err)
		}
		out[k] = s
	}
	return out, nil
}

func envVarsFromMap(m map[string]string) []EnvVar {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]EnvVar, 0, len(keys))
	for _, k := range keys {
		out = append(out, EnvVar{Name: k, Value: m[k]})
	}
	return out
}

// portsToK8s converts compose-style port strings into container and service ports.
func portsToK8s(ports []string, nodeID string) ([]ContainerPort, []ServicePort, error) {
	if len(ports) == 0 {
		return nil, nil, nil
	}
	containerPorts := make([]ContainerPort, 0, len(ports))
	svcPorts := make([]ServicePort, 0, len(ports))
	for i, p := range ports {
		hostPort, containerPort, err := splitHostContainerPort(p)
		if err != nil {
			return nil, nil, fmt.Errorf("helm: node %q: ports[%d]: %w", nodeID, i, err)
		}
		containerPorts = append(containerPorts, ContainerPort{
			ContainerPort: int32(containerPort),
			Protocol:      "TCP",
		})
		svcName := fmt.Sprintf("port-%d", i)
		svcPorts = append(svcPorts, ServicePort{
			Name:       svcName,
			Port:       int32(hostPort),
			TargetPort: int32(containerPort),
			Protocol:   "TCP",
		})
	}
	return containerPorts, svcPorts, nil
}

func splitHostContainerPort(p string) (hostPort, containerPort int, err error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return 0, 0, fmt.Errorf("empty port")
	}
	if strings.Contains(p, ":") {
		parts := strings.Split(p, ":")
		if len(parts) != 2 {
			return 0, 0, fmt.Errorf("expected host:container or single port, got %q", p)
		}
		hostPort, err = strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, 0, fmt.Errorf("host port: %w", err)
		}
		containerPort, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, 0, fmt.Errorf("container port: %w", err)
		}
		return hostPort, containerPort, nil
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0, 0, err
	}
	return n, n, nil
}

func sanitizeContainerName(nodeID string) string {
	s := strings.ReplaceAll(nodeID, "_", "-")
	if len(s) > 63 {
		s = s[:63]
	}
	if s == "" {
		return "workload"
	}
	return s
}
