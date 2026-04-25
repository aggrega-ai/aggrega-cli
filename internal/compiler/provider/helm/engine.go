// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	helmChartAPIVersion = "v2"
	chartVersion        = "0.1.0"
	defaultChartName    = "application"
	defaultImage        = "aggrega/placeholder:latest"
	injectedEnvPrefix   = "_aggrega_injected_env_"

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
func (e *HelmEngine) Render(ctx context.Context, graph *types.IRGraph, release *types.Release, boundary string) (map[string][]byte, error) {
	_ = boundary
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

	ids := graph.Spec.Nodes.Keys()
	sort.Strings(ids)
	for _, nodeID := range ids {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		raw, _ := graph.Spec.ValuesMapping.Get(nodeID)
		payload, err := parseValuesPayload(raw, nodeID)
		if err != nil {
			return nil, err
		}
		payload = helmPortFallbacks(node, payload)

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

// strictNetworkingEnabled returns true (the v2 default). The  Policies struct was
// abolished; strict networking is now determined by boundary annotations in the Release.
// This function will be rewritten in Intent 3 when the provider engines are overhauled.
func strictNetworkingEnabled(_ *types.Release) bool {
	return true
}

func serviceTypeForNode(node types.IRNode) string {
	rt, _ := node.Labels.Get(types.ComponentTypeLabel)
	if rt == "gateway" || rt == "ai-gateway" {
		return "LoadBalancer"
	}
	return "ClusterIP"
}

type extractedPayload struct {
	image       string
	ports       []string
	environment map[string]string
}

// helmPortFallbacks supplies service/container ports when values_mapping omits them (recipe not wired).
func helmPortFallbacks(node types.IRNode, p extractedPayload) extractedPayload {
	if len(p.ports) > 0 {
		return p
	}
	rt, _ := node.Labels.Get(types.ComponentTypeLabel)
	if rt == "ai-gateway" || rt == "gateway" {
		p.ports = []string{"443:8443"}
		return p
	}
	p.ports = []string{"8080:8080"}
	return p
}

func parseValuesPayload(raw json.RawMessage, nodeID string) (extractedPayload, error) {
	var out extractedPayload
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}

	var m map[string]interface{}
	if err := parse.IRPayload(raw, &m); err != nil {
		return extractedPayload{}, fmt.Errorf("helm: node %q: values_mapping: %w", nodeID, err)
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

	if err := mergeInjectedEnvFromValuesMap(&out, m, nodeID); err != nil {
		return extractedPayload{}, err
	}

	return out, nil
}

func mergeInjectedEnvFromValuesMap(out *extractedPayload, m map[string]interface{}, nodeID string) error {
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
			return fmt.Errorf("helm: node %q: values_mapping[%q]: %w", nodeID, k, err)
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
