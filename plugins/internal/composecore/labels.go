// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// composeOmittedProvenanceKeys are the provenance keys the shared builders emit
// that compose deliberately does not carry. Compose has no annotation concept and
// its service-label values are unconstrained, so it folds the whole provenance
// set — labels plus annotations — into a single labels map rather than splitting
// on the Kubernetes label-value charset, and drops the keys only a Kubernetes
// runtime consumes. The plain `aggrega.ai/sync-wave` provenance entry is
// retained.
//
// That retained entry is provenance and nothing reads it back. Service ordering
// takes the wave from the typed value the render payload carries, never from
// this label — see applyWaveOrdering for why decoding it there would order
// against an empty predecessor set.
//
// The set itself is the shared one, because the question "which provenance keys
// mean nothing off Kubernetes" has one answer for every such target rather than a
// compose answer and a per-provider answer beside it. The name is kept here
// because it is the vocabulary this package's own assertions read.
var composeOmittedProvenanceKeys = rendercore.KubernetesOnlyProvenanceKeys

// injectAggregaLabelsIntoServices walks the services: map of a YAML-
// decoded compose document and stamps the canonical aggrega.ai/* label
// set onto each service whose origin is recorded in the origins map.
// Used by sourced-mode rendering after the base + valuesMapping deep-
// merge so labels land on services with provenance derived from the
// service's actual emission origin, not from a single shared nodeCtx.
//
// Origin contract. The origins map is keyed by service name; the value
// is the NodeRenderContext that emitted that service. The caller is
// responsible for populating the map with one entry per service whose
// labels should be compiler-managed:
//
//   - Sourced-mode caller (this package's renderSourced): every service
//     in the merged base+overlay maps to the sole consumer nodeCtx —
//     the single-origin case. All services get consumer labels.
//   - A future merge of fragments drawn from several contexts would
//     populate one entry per service pointing at the originating
//     fragment's nodeCtx — services emitted by an FR map to the FR's
//     nodeCtx; services emitted by a regular Recipe map to that
//     Recipe's nodeCtx; services authored deliberately at higher levels
//     map to the appropriate authoring context.
//
// Services NOT present in the origins map are left untouched. That
// skip serves multi-FeatureRecipe composition, where one
// FeatureRecipe's fragment may carry services contributed by an
// upstream FeatureRecipe: the upstream emission step would have
// already populated origins for its services, and the downstream
// injection step skips those, deferring to the authoritative
// provenance set upstream. It awaits a caller that builds the map per
// service; the single-origin builder below is the only one that ships.
//
// The skip is not a way to preserve a catalog author's hand-set
// aggrega.ai/* labels on a base compose service. Single-origin
// rendering maps every service in the merged document to the one node
// context, so no service is absent from origins and the origin-derived
// keys overwrite the author's aggrega.ai/* values. The one repair that
// would change that is reading label presence as a claim of ownership,
// and it is refused: it would have the compiler infer meaning from
// label content. Author-set keys outside the aggrega.ai/* space
// survive regardless, as the merge rule below states.
//
// Existing label keys on a service are merged with — not replaced by —
// the origin-derived labels, in either form Compose accepts: every
// author-set non-aggrega.ai/* key (`tier: backend`, etc.) is preserved, a
// key written as null among them as a null, and an entry of the list form
// joins the mapping as Compose reads it (see authoredServiceLabels);
// origin-derived aggrega.ai/* keys overwrite any pre-existing aggrega.ai/*
// values for the same key.
//
// A value is carried as written, a number or a true/false value included.
// The labels mapping is emitted and then read back into the service's labels
// when the file is assembled, and that read takes every scalar by the text it
// is written as — the one conversion an artifacts: fragment's labels pass
// through too — so the assembled file holds each value as text. A value that
// is a list or a map is carried as written as well, and that same read
// refuses it in the author's words.
//
// The provenance merge here does not go through
// [rendercore.MergeProvenanceIntoMappingChild], which an artifacts: fragment's
// services use: that primitive merges into a YAML node, and a sourced
// service's labels are a decoded Go value by this point, because the overlay
// merge before this step needs decoded values. The rule is the same one —
// authored keys stay where no provenance key names them, and a provenance key
// overwrites an authored one — and the assembled file sorts the keys of a
// labels mapping either way, so the two paths emit the same labels.
//
// A labels value Compose takes in neither form — text, a number, a
// true/false value — and a list entry that is not text are refused, naming
// the service, rather than replaced by the provenance labels. Anything else
// malformed in the document is tolerated silently: sourced mode treats the
// input as authored YAML, and the decode at assembly is where its shape is
// judged.
func injectAggregaLabelsIntoServices(doc map[string]any, origins map[string]rendercore.NodeRenderContext, nodeID, sourcePath string) error {
	if doc == nil || len(origins) == 0 {
		return nil
	}
	servicesRaw, ok := doc["services"]
	if !ok {
		return nil
	}
	services, ok := asStringMap(servicesRaw)
	if !ok {
		return nil
	}
	for name, svcRaw := range services {
		origin, hasOrigin := origins[name]
		if !hasOrigin {
			// No recorded origin means no provenance to stamp; an
			// upstream contributor's labels stand.
			continue
		}
		svc, ok := asStringMap(svcRaw)
		if !ok {
			continue
		}
		merged, err := authoredServiceLabels(svc["labels"])
		if err != nil {
			return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: sourcePath},
				fmt.Sprintf("compose: node %q: source %q: services.%s.labels: %s", nodeID, sourcePath, name, err.Error()),
				nil, nil)
		}
		for k, v := range buildAggregaLabels(origin) {
			merged[k] = v
		}
		svc["labels"] = merged
		services[name] = svc
	}
	doc["services"] = services
	return nil
}

// authoredServiceLabels returns a sourced service's authored labels value as
// the mapping the provenance labels are merged into, holding every label the
// author wrote. A mapping keeps each entry as written. A list holds one label
// per entry, read as Compose reads one: the text before the first `=` is the
// key and the text after it the value, and an entry with no `=` is a label
// whose value is empty; where two entries name one key, the later one holds.
// No value — the key absent, or written as null — is no authored label.
//
// It returns an error, phrased for the author, for a value of any other shape
// and for a list entry that is not text.
func authoredServiceLabels(raw any) (map[string]any, error) {
	out := map[string]any{}
	if raw == nil {
		return out, nil
	}
	if m, ok := asStringMap(raw); ok {
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s; a service's labels take a list of key=value entries or a map",
			authoredshape.WrittenWhereExpected(authoredshape.Of(raw), listOrMap))
	}
	for i, entry := range entries {
		text, ok := entry.(string)
		if !ok {
			return nil, fmt.Errorf("entry %d: %s; a list-form label is written as key=value",
				i+1, authoredshape.WrittenWhereExpected(authoredshape.Of(entry), authoredshape.Text.Phrase()))
		}
		key, value, _ := strings.Cut(text, "=")
		out[key] = value
	}
	return out, nil
}

// buildSingleOriginMap returns a per-service origins map for the simple
// case where every service in a merged compose document shares one
// nodeCtx — the single-origin scenario for sourced-mode rendering.
// Future multi-origin call sites build the map directly per service.
func buildSingleOriginMap(doc map[string]any, nodeCtx rendercore.NodeRenderContext) map[string]rendercore.NodeRenderContext {
	origins := map[string]rendercore.NodeRenderContext{}
	if doc == nil {
		return origins
	}
	services, ok := asStringMap(doc["services"])
	if !ok {
		return origins
	}
	for name := range services {
		origins[name] = nodeCtx
	}
	return origins
}

// buildAggregaLabels produces the aggrega.ai/* service-level labels for
// compose services. It derives its set from the shared provenance builders
// rather than re-listing keys, so adding a provenance key in one place flows
// to every engine: compose carries the entire provenance set — the
// label-safe entries unioned with the charset-unsafe ones — as a single
// service-labels map, because compose's label values are unconstrained and
// it has no annotation concept. The two Kubernetes-only keys
// (composeOmittedProvenanceKeys) are dropped; the remaining set matches the
// helm and helm-chart engines' provenance for the same node.
func buildAggregaLabels(nodeCtx rendercore.NodeRenderContext) map[string]string {
	return foldComposeProvenanceSet(
		rendercore.BuildAggregaProvenanceLabels(nodeCtx),
		rendercore.BuildAggregaProvenanceAnnotations(nodeCtx, rendercore.ProvenanceRecipeURIFromNodeCtx(nodeCtx)),
	)
}

// foldComposeProvenanceSet folds a Kubernetes-shaped provenance split — the
// label-safe half and the charset-unsafe half — into the single service-labels
// map compose emits. Every compose surface reaches the fold through this one
// function, so the services this package synthesizes and merges and the services
// an artifact fragment contributes carry the same key set from the same rule
// rather than from two agreeing copies of it.
//
// The fold itself is the shared one ([rendercore.FoldProvenanceSet]), which every
// target whose metadata surface constrains neither charset nor annotation concept
// reads: the union of the two halves, minus the keys only a Kubernetes runtime
// consumes. Compose adds nothing to it, so this is a name rather than a second
// implementation.
func foldComposeProvenanceSet(labels, annotations map[string]string) map[string]string {
	return rendercore.FoldProvenanceSet(labels, annotations)
}
