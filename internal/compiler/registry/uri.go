// Package registry hosts OCI registry resolution helpers for Aggrega manifests.
package registry

import (
	"fmt"
	"strings"
)

const defaultRegistryHost = "hub.aggrega.ai"
const officialNamespace = "aggrega"

// Core component kinds (Domain 1 taxonomy) — first path segment may omit registry/namespace.
var coreComponents = map[string]struct{}{
	"ai-agent":         {},
	"ai-datastore":     {},
	"ai-gateway":       {},
	"ai-model":         {},
	"ai-pipeline":      {},
	"ai-tool":          {},
	"server":           {},
	"worker":           {},
	"task":             {},
	"function":         {},
	"stateful-server":  {},
	"resource":         {},
	"messaging":        {},
}

// Core catalog feature IDs (first segment may omit registry/namespace for official recipes).
//
// Gateway Intelligence:
// semantic-routing, fallback-chain, retry-policy, token-budget, semantic-caching,
// concurrency-limit, response-validation, traffic-shadowing
//
// Data-Plane Security:
// data-masking, prompt-injection-shield, apply-untrusted-label, block-untrusted-payloads
//
// Trust & Isolation:
// tls-pinning, cryptographic-signature, human-approval-gate, hardware-sandbox,
// context-propagation, model-provenance, multi-sig-quorum
//
// Observability:
// audit-logging, llm-tracing
//
// Cloud-Native:
// public-ingress, prometheus-exporter
var coreFeatures = map[string]struct{}{
	// Gateway Intelligence
	"semantic-routing":    {},
	"fallback-chain":      {},
	"retry-policy":        {},
	"token-budget":        {},
	"semantic-caching":    {},
	"concurrency-limit":   {},
	"response-validation": {},
	"traffic-shadowing":   {},
	// Data-Plane Security
	"data-masking":               {},
	"prompt-injection-shield":    {},
	"apply-untrusted-label":      {},
	"block-untrusted-payloads":     {},
	// Trust & Isolation
	"tls-pinning":             {},
	"cryptographic-signature": {},
	"human-approval-gate":     {},
	"hardware-sandbox":        {},
	"context-propagation":     {},
	"model-provenance":        {},
	"multi-sig-quorum":        {},
	// Observability
	"audit-logging": {},
	"llm-tracing":   {},
	// Cloud-Native
	"public-ingress":       {},
	"prometheus-exporter": {},
}

// ParseRecipeURI expands manifest recipe shorthand into a fully qualified registry path
// on hub.aggrega.ai. recipeType must be "component" or "feature".
//
// Routing:
//   - If the first slash-separated segment is a known core type for that recipe kind, the URI
//     is placed under the official aggrega namespace: hub.aggrega.ai/aggrega/<kind>-recipes/<rawURI>.
//   - Otherwise the first segment is treated as a third-party namespace:
//     hub.aggrega.ai/<namespace>/<kind>-recipes/<remainder>.
func ParseRecipeURI(recipeType, rawURI string) string {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return ""
	}

	var recipesDir string
	switch recipeType {
	case "component":
		recipesDir = "component-recipes"
	case "feature":
		recipesDir = "feature-recipes"
	default:
		return rawURI
	}

	segments := strings.Split(rawURI, "/")
	if len(segments) == 0 {
		return ""
	}
	first := segments[0]
	if first == "" {
		return rawURI
	}

	isCore := false
	switch recipeType {
	case "component":
		isCore = isCoreComponent(first)
	case "feature":
		isCore = isCoreFeature(first)
	}
	if isCore {
		return fmt.Sprintf("%s/%s/%s/%s", defaultRegistryHost, officialNamespace, recipesDir, rawURI)
	}
	return thirdPartyURI(first, segments[1:], recipesDir)
}

func thirdPartyURI(namespace string, rest []string, recipesDir string) string {
	tail := strings.Join(rest, "/")
	if tail == "" {
		return fmt.Sprintf("%s/%s/%s/", defaultRegistryHost, namespace, recipesDir)
	}
	return fmt.Sprintf("%s/%s/%s/%s", defaultRegistryHost, namespace, recipesDir, tail)
}

func isCoreComponent(s string) bool {
	_, ok := coreComponents[s]
	return ok
}

func isCoreFeature(s string) bool {
	_, ok := coreFeatures[s]
	return ok
}
