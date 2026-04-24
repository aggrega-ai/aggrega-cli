// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	k8sDeploymentAPIVersion = "apps/v1"
	k8sServiceAPIVersion    = "v1"
	defaultImage            = "aggrega/placeholder:latest"
)

// HelmEngine renders a values dictionary into Helm chart files (Deployment, Service YAML).
type HelmEngine struct{}

var _ renderer.Engine = (*HelmEngine)(nil)

// Render produces the sub-chart contents for one node.
//
//   - Generative mode (nodeCtx.Source is nil): generate Chart.yaml + templates/*.yaml
//     from the values dictionary (current behaviour).
//   - Sourced mode (nodeCtx.Source is a non-empty string): copy the user's local
//     Helm chart verbatim. The umbrella builder emits a dependency entry using
//     the user's chart name with `alias: <instance_id>` so values propagation
//     works while allowing multiple instances of the same source chart.
func (e *HelmEngine) Render(ctx context.Context, values map[string]interface{}, nodeCtx renderer.NodeRenderContext) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Sourced mode — copy the chart verbatim.
	if s, ok := nodeCtx.Source.(string); ok && s != "" {
		return copySourcedChart(s)
	}

	out := map[string][]byte{}

	// Chart.yaml (generated once per chart, but we include it per-node for simplicity;
	// the orchestrator will deduplicate)
	chartYAML, err := generateChartYAML(nodeCtx)
	if err != nil {
		return nil, fmt.Errorf("helm: chart.yaml: %w", err)
	}
	out["Chart.yaml"] = chartYAML

	// Extract values
	image := stringFromValues(values, "image", defaultImage)
	env := mapStringFromValues(values, "environment")
	ports := stringSliceFromValues(values, "ports")
	replicas := intFromValues(values, "replicas", 1)

	// Build labels
	aggreraLabels := buildAggreraLabels(nodeCtx)
	selectorLabels := map[string]string{"app": nodeCtx.NodeID}

	// Merge aggrera labels into resource labels
	resourceLabels := make(map[string]string)
	for k, v := range selectorLabels {
		resourceLabels[k] = v
	}
	for k, v := range aggreraLabels {
		resourceLabels[k] = v
	}

	// Container ports + service ports
	containerPorts, svcPorts := parsePorts(ports)

	// Env vars (sorted for determinism)
	envVars := envVarsFromMap(env)

	namespace := renderer.NamespaceFromAnnotations(nodeCtx.Annotations)

	// Deployment
	replicasInt32 := int32(replicas)
	deploy := Deployment{
		APIVersion: k8sDeploymentAPIVersion,
		Kind:       "Deployment",
		Metadata: ObjectMeta{
			Name:      nodeCtx.NodeID,
			Namespace: namespace,
			Labels:    resourceLabels,
		},
		Spec: DeploymentSpec{
			Replicas: &replicasInt32,
			Selector: &LabelSelector{MatchLabels: selectorLabels},
			Template: PodTemplateSpec{
				Metadata: ObjectMeta{Labels: resourceLabels},
				Spec: PodSpec{
					Containers: []Container{
						{
							Name:  sanitizeContainerName(nodeCtx.NodeID),
							Image: image,
							Ports: containerPorts,
							Env:   envVars,
						},
					},
				},
			},
		},
	}

	depYAML, err := yaml.Marshal(&deploy)
	if err != nil {
		return nil, fmt.Errorf("helm: node %q deployment marshal: %w", nodeCtx.NodeID, err)
	}
	out["templates/deployment.yaml"] = depYAML

	// Service (only if ports exist)
	if len(svcPorts) > 0 {
		svc := Service{
			APIVersion: k8sServiceAPIVersion,
			Kind:       "Service",
			Metadata: ObjectMeta{
				Name:      nodeCtx.NodeID,
				Namespace: namespace,
				Labels:    resourceLabels,
			},
			Spec: ServiceSpec{
				Type:     "ClusterIP",
				Selector: selectorLabels,
				Ports:    svcPorts,
			},
		}
		svcYAML, err := yaml.Marshal(&svc)
		if err != nil {
			return nil, fmt.Errorf("helm: node %q service marshal: %w", nodeCtx.NodeID, err)
		}
		out["templates/svc.yaml"] = svcYAML
	}

	return out, nil
}

// Value extraction helpers.

func stringFromValues(values map[string]interface{}, key, fallback string) string {
	if v, ok := values[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

// mapStringFromValues plucks an order-preserving map under `key` from the
// caller-supplied values dict. Returns *types.OrderedMap[string] so downstream
// consumers (envVarsFromMap) iterate in author order via OrderedMap.All().
//
// Detection rules match the helm engine's equivalent — see
// internal/compiler/renderer/helm/sourceless.go for the full rationale.
// Plain Go maps are inserted alphabetically (the deterministic choice per
//  when no author order is carried in); OrderedMap variants are
// preserved as-is.
func mapStringFromValues(values map[string]interface{}, key string) *types.OrderedMap[string] {
	v, ok := values[key]
	if !ok || v == nil {
		return nil
	}
	switch m := v.(type) {
	case *types.OrderedMap[string]:
		return m
	case types.OrderedMap[string]:
		out := types.NewOrderedMap[string]()
		for k, val := range m.All() {
			out.Set(k, val)
		}
		return out
	case *types.OrderedMap[any]:
		if m == nil {
			return nil
		}
		out := types.NewOrderedMap[string]()
		for k, val := range m.All() {
			out.Set(k, fmt.Sprintf("%v", val))
		}
		return out
	case types.OrderedMap[any]:
		out := types.NewOrderedMap[string]()
		for k, val := range m.All() {
			out.Set(k, fmt.Sprintf("%v", val))
		}
		return out
	case map[string]interface{}:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := types.NewOrderedMap[string]()
		for _, k := range keys {
			out.Set(k, fmt.Sprintf("%v", m[k]))
		}
		return out
	case map[string]string:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := types.NewOrderedMap[string]()
		for _, k := range keys {
			out.Set(k, m[k])
		}
		return out
	default:
		return nil
	}
}

func stringSliceFromValues(values map[string]interface{}, key string) []string {
	v, ok := values[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		out = append(out, fmt.Sprintf("%v", item))
	}
	return out
}

func intFromValues(values map[string]interface{}, key string, fallback int) int {
	v, ok := values[key]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return fallback
}

// envVarsFromMap emits a []EnvVar in author order from the supplied
// OrderedMap. Kubernetes `env` is an ordered list — later entries can
// $(VAR)-reference earlier ones — so preserving the author's declared
// order matters for correctness, not just for determinism. See 
// for the ordered-semantic-data principle this method applies.
func envVarsFromMap(m *types.OrderedMap[string]) []EnvVar {
	if m == nil || m.Len() == 0 {
		return nil
	}
	out := make([]EnvVar, 0, m.Len())
	for k, v := range m.All() {
		out = append(out, EnvVar{Name: k, Value: v})
	}
	return out
}

func parsePorts(ports []string) ([]ContainerPort, []ServicePort) {
	if len(ports) == 0 {
		return nil, nil
	}
	var cPorts []ContainerPort
	var sPorts []ServicePort
	for i, p := range ports {
		host, container := splitPort(p)
		cPorts = append(cPorts, ContainerPort{ContainerPort: int32(container)})
		sPorts = append(sPorts, ServicePort{
			Name:       fmt.Sprintf("port-%d", i),
			Port:       int32(host),
			TargetPort: int32(container),
		})
	}
	return cPorts, sPorts
}

func splitPort(p string) (host, container int) {
	parts := strings.SplitN(p, ":", 2)
	if len(parts) == 2 {
		h, _ := strconv.Atoi(parts[0])
		c, _ := strconv.Atoi(parts[1])
		return h, c
	}
	n, _ := strconv.Atoi(parts[0])
	return n, n
}

func sanitizeContainerName(id string) string {
	r := strings.NewReplacer(".", "-", "_", "-")
	return r.Replace(id)
}
