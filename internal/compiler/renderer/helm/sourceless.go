// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"gopkg.in/yaml.v3"
)

const (
	k8sDeploymentAPIVersion = "apps/v1"
	k8sServiceAPIVersion    = "v1"
	defaultImage            = "aggrega/placeholder:latest"
)

// renderSourceless generates a flat Deployment + optional Service from the
// values dictionary (already baseSchema-validated at the orchestrator level).
// Output keys: "<nodeID>-deployment.yaml", "<nodeID>-service.yaml".
func renderSourceless(values map[string]interface{}, nodeCtx renderer.NodeRenderContext) (map[string][]byte, error) {
	out := map[string][]byte{}

	image := stringFromValues(values, "image", defaultImage)
	env := mapStringFromValues(values, "environment")
	ports := stringSliceFromValues(values, "ports")
	replicas := intFromValues(values, "replicas", 1)

	aggreraLabels := buildAggreraLabels(nodeCtx)
	selectorLabels := map[string]string{"app": nodeCtx.NodeID}
	resourceLabels := map[string]string{}
	for k, v := range selectorLabels {
		resourceLabels[k] = v
	}
	for k, v := range aggreraLabels {
		resourceLabels[k] = v
	}

	containerPorts, svcPorts := parsePorts(ports)
	envVars := envVarsFromMap(env)

	replicasInt32 := int32(replicas)
	deploy := Deployment{
		APIVersion: k8sDeploymentAPIVersion,
		Kind:       "Deployment",
		Metadata:   ObjectMeta{Name: nodeCtx.NodeID, Labels: resourceLabels},
		Spec: DeploymentSpec{
			Replicas: &replicasInt32,
			Selector: &LabelSelector{MatchLabels: selectorLabels},
			Template: PodTemplateSpec{
				Metadata: ObjectMeta{Labels: resourceLabels},
				Spec: PodSpec{
					Containers: []Container{{
						Name:  sanitizeContainerName(nodeCtx.NodeID),
						Image: image,
						Ports: containerPorts,
						Env:   envVars,
					}},
				},
			},
		},
	}

	depYAML, err := yaml.Marshal(&deploy)
	if err != nil {
		return nil, fmt.Errorf("helm: node %q deployment marshal: %w", nodeCtx.NodeID, err)
	}
	out[nodeCtx.NodeID+"-deployment.yaml"] = depYAML

	if len(svcPorts) > 0 {
		svc := Service{
			APIVersion: k8sServiceAPIVersion,
			Kind:       "Service",
			Metadata:   ObjectMeta{Name: nodeCtx.NodeID, Labels: resourceLabels},
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
		out[nodeCtx.NodeID+"-service.yaml"] = svcYAML
	}

	return out, nil
}

// Values extraction helpers (duplicated from helmchart; kept local to avoid cross-provider coupling).

func stringFromValues(values map[string]interface{}, key, fallback string) string {
	if v, ok := values[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

func mapStringFromValues(values map[string]interface{}, key string) map[string]string {
	v, ok := values[key]
	if !ok || v == nil {
		return nil
	}
	switch m := v.(type) {
	case map[string]interface{}:
		out := make(map[string]string, len(m))
		for k, val := range m {
			out[k] = fmt.Sprintf("%v", val)
		}
		return out
	case map[string]string:
		return m
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
