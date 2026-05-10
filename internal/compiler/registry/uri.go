// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package registry expands manifest recipe-URI shorthand into fully-qualified
// hub.aggrega.ai paths. The single exported entry point is ParseRecipeURI; the
// closed sets of recognised core component / feature identifiers live in the
// coreComponents and coreFeatures maps below and decide whether a shorthand
// routes to the official "aggrega" namespace or to a third-party namespace.
//
// The default registry host (hub.aggrega.ai) is the same default the
// resolver's remote OCI fallback uses (the third tier of the resolver's
// reference-resolution cascade — local path, workspace+cache, remote
// registry). The package-side and catalog-side naming conventions
// ultimately read from
// aggrega-hub/docs/catalog-conventions-and-patches.md, which is the
// authoritative document for catalog directory structure and the canonical
// hub.aggrega.ai URI examples.
//
// As of this writing the package has no production caller — no code under
// aggrega-cli/cmd/ or aggrega-cli/internal/ imports it. The original work
// authoring it explicitly deferred wiring into the manifest resolution
// loop; the package shipped as a tested, reusable utility expected to
// anchor a later integration. Tests in uri_test.go are the only consumers
// today.
package registry

import (
	"fmt"
	"strings"
)

// defaultRegistryHost is the OCI host the shorthand expander prepends. Same
// default the resolver's remote OCI fallback uses.
const defaultRegistryHost = "hub.aggrega.ai"

// officialNamespace is the registry namespace under which standard-library
// recipes live; a shorthand whose first segment names a recognised core type
// routes here rather than treating the segment as a third-party namespace.
const officialNamespace = "aggrega"

// coreComponents is the single source of truth for which Component ontology
// labels the URI router treats as official-namespace candidates. A recipe
// shorthand whose first path segment matches one of these keys routes under
// "aggrega/" (Scenario A in ParseRecipeURI); any other first segment is
// treated as a third-party namespace (Scenario B).
//
// The set spans the seven base cloud-native ontology labels (server, worker,
// task, function, stateful-server, resource, messaging) plus six
// AI-cognitive-layer labels in active use across the standard-library
// catalog at aggrega-hub/aggrega-catalog/component/core/ (ai-agent,
// ai-datastore, ai-gateway, ai-model, ai-pipeline, ai-tool). Adding a new
// ontology label to the catalog requires adding the same key here, otherwise
// shorthand inputs naming the new label fall through to third-party routing.
var coreComponents = map[string]struct{}{
	"ai-agent":        {},
	"ai-datastore":    {},
	"ai-gateway":      {},
	"ai-model":        {},
	"ai-pipeline":     {},
	"ai-tool":         {},
	"server":          {},
	"worker":          {},
	"task":            {},
	"function":        {},
	"stateful-server": {},
	"resource":        {},
	"messaging":       {},
}

// coreFeatures is the single source of truth for which Feature identifiers
// the URI router treats as official-namespace candidates. Same role as
// coreComponents but for shorthand inputs declaring a feature kind. Adding a
// new core Feature to the standard-library catalog requires adding its
// identifier here, otherwise shorthand inputs naming the new Feature fall
// through to third-party routing.
//
// The categorised list below is the authoritative grouping for what counts
// as a "core" Feature today; do not refactor the categories without
// re-anchoring the catalog vocabulary.
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
	"data-masking":             {},
	"prompt-injection-shield":  {},
	"apply-untrusted-label":    {},
	"block-untrusted-payloads": {},
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
	"public-ingress":      {},
	"prometheus-exporter": {},
}

// ParseRecipeURI expands a manifest recipe shorthand into a fully-qualified
// hub.aggrega.ai path. recipeType selects the recipe-kind sub-tree
// ("component" → "component-recipes"; "feature" → "feature-recipes"); rawURI
// is the shorthand the manifest authored.
//
// Routing decision (the "scenario" terminology mirrors the source FIRE work
// item):
//
//   - Scenario A — first slash-separated segment is in the recipe-kind's core
//     map (coreComponents for "component", coreFeatures for "feature"). The
//     URI lands under the official "aggrega" namespace, with rawURI joined
//     verbatim:
//
//     hub.aggrega.ai/aggrega/<kind>-recipes/<rawURI>
//
//   - Scenario B — otherwise the first segment IS the third-party namespace,
//     and the remaining segments are joined as the path tail:
//
//     hub.aggrega.ai/<namespace>/<kind>-recipes/<tail>
//
// The closed-set membership check is the only signal driving the choice;
// neither the catalog nor the network is consulted.
//
// Error model. The function returns a plain string and never errors.
// Degenerate inputs pass through deliberately:
//
//   - empty rawURI (after trim) → "" (caller's empty in, empty out)
//   - recipeType outside {"component", "feature"} → rawURI unchanged
//   - rawURI whose first segment is empty (e.g. a leading "/") → rawURI
//     unchanged
//
// Callers that need an error signal must validate inputs before calling.
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
