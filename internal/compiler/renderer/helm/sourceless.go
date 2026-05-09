// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/types"
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
	aggreraAnnotations := buildAggreraAnnotations(nodeCtx)
	selectorLabels := map[string]string{"app": nodeCtx.NodeID}
	resourceLabels := map[string]string{}
	for k, v := range selectorLabels {
		resourceLabels[k] = v
	}
	for k, v := range aggreraLabels {
		resourceLabels[k] = v
	}
	// Sourceless mode produces synthesized resources (Deployment, Service)
	// — never Helm hooks — so hook-weight emission is N/A here. The Deployment
	// and Service get the always-emit annotations (currently
	// argocd.argoproj.io/sync-wave when SyncWave != 0).
	resourceAnnotations := map[string]string{}
	for k, v := range aggreraAnnotations {
		resourceAnnotations[k] = v
	}

	containerPorts, svcPorts := parsePorts(ports)
	envVars := envVarsFromMap(env)

	namespace := renderer.NamespaceFromAnnotations(nodeCtx.Annotations)

	replicasInt32 := int32(replicas)
	deploy := Deployment{
		APIVersion: k8sDeploymentAPIVersion,
		Kind:       "Deployment",
		Metadata:   ObjectMeta{Name: nodeCtx.NodeID, Namespace: namespace, Labels: resourceLabels, Annotations: nilIfEmpty(resourceAnnotations)},
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
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q deployment marshal", nodeCtx.NodeID))
	}
	out[nodeCtx.NodeID+"-deployment.yaml"] = depYAML

	if len(svcPorts) > 0 {
		svc := Service{
			APIVersion: k8sServiceAPIVersion,
			Kind:       "Service",
			Metadata:   ObjectMeta{Name: nodeCtx.NodeID, Namespace: namespace, Labels: resourceLabels, Annotations: nilIfEmpty(resourceAnnotations)},
			Spec: ServiceSpec{
				Type:     "ClusterIP",
				Selector: selectorLabels,
				Ports:    svcPorts,
			},
		}
		svcYAML, err := yaml.Marshal(&svc)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q service marshal", nodeCtx.NodeID))
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

// mapStringFromValues plucks an order-preserving map under `key` from the
// caller-supplied values dict. Returns *types.OrderedMap[string] so downstream
// consumers (envVarsFromMap) iterate in author order via OrderedMap.All().
//
// Detection rules:
//   - *types.OrderedMap[string] / types.OrderedMap[string]: returned as-is
//     (author order preserved through the chain).
//   - types.OrderedMap[any] / *types.OrderedMap[any]: unwrapped via Keys()
//     + Get(), values coerced to string via fmt.Sprintf; author order
//     preserved.
//   - map[string]interface{} / map[string]string: input has no author order
//     (Go map iteration is hash-random), so keys are inserted into the
//     result alphabetically — the deterministic choice when no author
//     order is available upstream.
//   - Everything else: nil.
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
// order matters for correctness, not just for determinism. The
// ordered-semantic-data principle applies: ordering that the runtime
// observes belongs in lists or OrderedMaps in the schema, not in
// template iteration.
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
			TargetPort: strconv.Itoa(container),
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

// nilIfEmpty returns nil when m is empty so yaml `omitempty` elides the
// metadata.annotations key entirely (preserves byte-identity for the
// default-0 sync-wave case across rendered fixtures).
func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}
