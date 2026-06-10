// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// BuildIRConfig is the input bundle for BuildIR. The four fields are all
// required for a successful build:
//
//   - Topology supplies the flattened node graph plus edges and per-node
//     feature/secret attachments. Step 2 produces it; the IR builder reads
//     Nodes (in author order, OrderedMap-backed), Edges (already sorted),
//     FeatureAttachments, and SecretAttachments.
//   - ResolveResult supplies the original Aggregate / Manifest / Release
//     plus per-node BoundaryInfra. The IR builder reads Aggregate for
//     subsystem traversal (Tier 3), Release for boundary traversal (Tiers
//     4 + 5), and BoundaryInfra for the IRSpec InfraMapping, the
//     release-annotations bucket, and per-node sync-wave assignment
//     (BoundaryInfra.SyncWave is populated by the resolver from each
//     boundary's syncWave field; default 0).
//   - CatalogLookup is the same callback shape produced by pkg/catalog. The
//     IR builder uses it to resolve component `metadata.version` (for
//     IRNode.ResolvedVersion), to extract catalog annotations and the
//     CatalogMetadata enterprise suite, and to test feature catalog
//     entities for the credential label that routes them into Secrets.
//   - CompilerVersion is plumbed through to provenance generation so the
//     emitted IRGraph records the binary that produced it. The orchestrator
//     at cmd/aggrega/build.go is the production caller and supplies the
//     CLI's version string here.
type BuildIRConfig struct {
	Topology        *topology.TopologyResult
	ResolveResult   *resolver.ResolveResult
	CatalogLookup   resolver.CatalogLookup
	CompilerVersion string
}

// FeatureOrder is BuildIR's secondary return value: per-node, the ordered
// list of Feature instance names in cascade-tier sequence (sub-aggregate
// component → root-aggregate component → root-aggregate subsystem →
// release boundary → release component-specific). The map keys by
// nodeID; the slice value is the deterministic execution order Phase A
// and Phase B of the renderer must follow.
//
// FeatureOrder exists as a separate slice rather than relying on the Go
// map iteration order of IRSpec.FeatureValuesMapping because Go map
// iteration is deliberately randomised and would defeat the byte-for-byte
// determinism contract the compiler commits to. The renderer's Phase A
// reads FeatureOrder[nodeID] to dispatch FeatureRecipes in tier order —
// see internal/compiler/renderer/phase_a.go's ExecutePhaseA.
type FeatureOrder map[string][]string

// BuildIR executes Step 3 of the compiler pipeline: graph finalization.
//
// Steps performed (numbered to match the body's section comments):
//
//  1. Build IR nodes from topology nodes — including labels, paths,
//     annotation buckets, catalog metadata, ResolvedVersion, SyncWave
//     (per-boundary from the resolver), and the SelfInject pipe.
//  2. Convert topology edges to IR edges — preserving Source / Target /
//     Socket / Alias / OriginFeature (the per-edge origin marker that
//     keeps Feature-authored edges separable from Component-authored
//     edges) and converting the typed inject block via convertInjectBlock.
//  3. Collect and normalize features through the per-node feature
//     cascade via collectNodeFeatures.
//  4. Populate values / infra / feature mappings on the IRSpec.
//  5. Build the three annotation buckets via buildAnnotationBuckets
//     (catalog / release / aggregate; the aggregate bucket is empty
//     today because the schema bans annotations on Aggregate
//     ComponentInstance blocks).
//  6. Generate provenance via buildProvenance.
//
// Determinism. Iteration over the topology's flattened nodes preserves the
// Aggregate's author-declared component order — topology.Nodes is an
// OrderedMap populated during subsystem flattening in author order, and
// author order flows through to IR JSON output via OrderedMap.MarshalJSON.
// The feature_markers slice is accumulated into a set then sort-extracted
// for stability across runs; secrets and features within a node iterate
// in the topology attachment order; the edges slice is inherited from
// topology in compositional author order (component sequence ×
// within-component `uses:` socket order × within-socket alias order ×
// per-node feature/secret attachments — topology.Build step 4 drives
// the cross-product via OrderedMap.All()). Both inheritance commitments
// are the consumer side of the cross-package determinism contract — see
// this package's doc.go for the full statement.
//
// Error model. The function only returns an error if collectNodeFeatures
// fails — which today happens exclusively on ErrFeatureTypeMismatch.
// All other operations either succeed or fall through (missing
// CatalogLookup, missing Release, JSON-marshal failures on values/infra
// mapping leave the slot empty rather than aborting). Callers wrap the
// error with the Step 3 prefix; see cmd/aggrega/build.go.
func BuildIR(cfg BuildIRConfig) (*types.IRGraph, FeatureOrder, error) {
	return BuildIRContext(context.Background(), cfg)
}

// BuildIRContext is BuildIR with cooperative cancellation: it checks
// ctx.Err() at the per-node build boundary so a long IR-assembly pass
// over a large graph can be interrupted (e.g. by the daemon's shutdown
// drain). BuildIR is the background-context convenience wrapper for
// callers that do not participate in cancellation.
func BuildIRContext(ctx context.Context, cfg BuildIRConfig) (*types.IRGraph, FeatureOrder, error) {
	topo := cfg.Topology
	res := cfg.ResolveResult

	// Defense-in-depth: catch any verbose-form inject entry that carries
	// both `value:` and `from:` simultaneously. Schema validation at
	// envelope load preempts this case in production paths; the validator
	// here covers schema-bypass paths (in-memory fixtures, programmatic
	// catalog builders) per ErrInjectEntryAmbiguous's contract.
	if res != nil {
		if err := ValidateInjectBlocks(res.Aggregate, res.Release); err != nil {
			return nil, nil, err
		}
	}

	irNodes := types.OrderedMap[types.IRNode]{}
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping := types.OrderedMap[json.RawMessage]{}
	featureValuesMapping := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	secrets := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
	featureOrder := make(FeatureOrder)
	allMarkers := map[string]bool{}

	// Feature/Secret attachment schema-conformance gate (Step 3). The merged
	// per-attachment values/infra only exist after the 5-Tier merge below, so
	// this gate runs here rather than in the Step-1 RunGuards. compiledFeature
	// memoises the compiled Feature schema per type (Secrets resolve through the
	// same Feature lookup); attachmentConformanceErrs collects across all nodes
	// for one joined halt.
	featureSchemaCache := map[string]compactform.Schema{}
	featureSchemaMiss := map[string]bool{}
	compiledFeature := func(featureType string) (compactform.Schema, bool) {
		if c, ok := featureSchemaCache[featureType]; ok {
			return c, true
		}
		if featureSchemaMiss[featureType] {
			return nil, false
		}
		// Prefer the composed effective schema for an extends-ing Feature, so an
		// inherited constraint is enforced on the attachment.
		if cfg.CatalogLookup.EffectiveSchema != nil {
			if eff, ok := cfg.CatalogLookup.EffectiveSchema(featureType); ok {
				featureSchemaCache[featureType] = eff
				return eff, true
			}
		}
		if cfg.CatalogLookup.Feature == nil {
			return nil, false
		}
		feat, ok := cfg.CatalogLookup.Feature(featureType)
		if !ok || feat.Spec.Schema.Kind == 0 {
			featureSchemaMiss[featureType] = true
			return nil, false
		}
		var opts []compactform.CompileOption
		if len(cfg.CatalogLookup.SchemaLibraries) > 0 {
			opts = append(opts, compactform.WithSchemaLibraries(cfg.CatalogLookup.SchemaLibraries))
		}
		compiled, err := compactform.Compile(&feat.Spec.Schema, opts...)
		if err != nil {
			featureSchemaMiss[featureType] = true
			return nil, false
		}
		featureSchemaCache[featureType] = compiled
		return compiled, true
	}
	var attachmentConformanceErrs []*resolver.ResolverError
	var attachmentParams map[string]any
	attachmentEntity := "Aggregate"
	if res != nil {
		attachmentParams = res.Parameters
		if res.Aggregate != nil {
			attachmentEntity = fmt.Sprintf("Aggregate %s", res.Aggregate.Metadata.Name)
		}
	}

	// 1. Build IR nodes in author order (topo.Nodes is OrderedMap-backed).
	for nodeID, topoNode := range topo.Nodes.All() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		catalogAnnotations, releaseAnnotations, aggregateAnnotations := buildAnnotationBuckets(
			nodeID, topoNode.ComponentType, cfg.CatalogLookup, res.BoundaryInfra,
		)

		irNode := types.IRNode{
			Labels:               topoNode.Labels,
			Component:            topoNode.ComponentType,
			SubsystemPath:        topoNode.SubsystemPath,
			BoundaryPath:         topoNode.BoundaryPath,
			CatalogAnnotations:   catalogAnnotations,
			ReleaseAnnotations:   releaseAnnotations,
			AggregateAnnotations: aggregateAnnotations,
			CatalogMetadata:      buildCatalogMetadata(topoNode.ComponentType, cfg.CatalogLookup),
			SelfInject:           topoNode.SelfInject,
			SourceFile:           topoNode.SourceFile,
		}

		// Resolved version + schema-declared pillar defaults from catalog.
		// Prefer the composed effective schema for an extends-ing Component so
		// inherited defaults materialise into the rendered output (consistent
		// with the conformance gate validating against the same effective
		// schema); fall back to the authored schema for a non-extends Component.
		var valueDefaults, infraDefaults types.OrderedMap[any]
		if cfg.CatalogLookup.Component != nil {
			catComp, found := cfg.CatalogLookup.Component(topoNode.ComponentType)
			if found {
				irNode.ResolvedVersion = catComp.Metadata.Version
				if eff, ok := effectiveComponentSchema(cfg.CatalogLookup, topoNode.ComponentType); ok {
					valueDefaults = compactform.PillarDefaults(eff, "values")
					infraDefaults = compactform.PillarDefaults(eff, "infra")
				} else {
					valueDefaults, infraDefaults = componentPillarDefaults(catComp, cfg.CatalogLookup.SchemaLibraries)
				}
			}
		}

		// Sync wave from the node's containing boundary's per-boundary SyncWave field.
		// Resolver pre-populates BoundaryInfra.SyncWave during walkBoundary; default 0
		// for boundaries that omit syncWave or for nodes without BoundaryInfra context.
		if bi, ok := res.BoundaryInfra[nodeID]; ok && bi != nil {
			irNode.SyncWave = bi.SyncWave
		}

		irNodes.Set(nodeID, irNode)

		// 4. Values mapping — author values plus declared schema defaults for
		// keys the Aggregate omitted (author values win).
		filledValues := mergeSchemaDefaults(topoNode.Values, valueDefaults)
		if filledValues.Len() > 0 {
			raw, err := json.Marshal(filledValues)
			if err == nil {
				valuesMapping.Set(nodeID, raw)
			}
		}

		// Infra mapping from boundary infra plus declared schema defaults for
		// keys the Release omitted (Release values win).
		var baseInfra types.OrderedMap[any]
		if bi, ok := res.BoundaryInfra[nodeID]; ok && bi != nil {
			baseInfra = bi.Infra
		}
		filledInfra := mergeSchemaDefaults(baseInfra, infraDefaults)
		if filledInfra.Len() > 0 {
			raw, err := json.Marshal(filledInfra)
			if err == nil {
				infraMapping.Set(nodeID, raw)
			}
		}

		// 3. Feature normalization (5-Tier Onion Model)
		featResult, err := collectNodeFeatures(
			nodeID, topoNode, topo, res, cfg.CatalogLookup,
		)
		if err != nil {
			return nil, nil, err
		}

		if featResult.Features.Len() > 0 {
			featureValuesMapping.Set(nodeID, featResult.Features)
			featureOrder[nodeID] = featResult.FeatureOrder
		}
		if featResult.Secrets.Len() > 0 {
			secrets.Set(nodeID, featResult.Secrets)
		}
		for _, m := range featResult.Markers {
			allMarkers[m] = true
		}

		// Validate the merged attachment pillars against each attachment's
		// declared Feature/Secret schema (collected across all nodes for one
		// joined halt below).
		attachmentConformanceErrs = append(attachmentConformanceErrs,
			validateNodeAttachments(nodeID, featResult.Features, featResult.Secrets,
				attachmentParams, attachmentEntity, compiledFeature)...)
	}

	if len(attachmentConformanceErrs) > 0 {
		return nil, nil, joinAttachmentConformance(attachmentConformanceErrs)
	}

	// 2. Convert topology edges to IR edges
	irEdges := make([]types.IREdge, 0, len(topo.Edges))
	for _, e := range topo.Edges {
		irEdge := types.IREdge{
			Source:                e.Source,
			Target:                e.Target,
			Socket:                e.Socket,
			Alias:                 e.Alias,
			OriginFeature:         e.OriginFeature,
			OriginFeatureInstance: e.OriginFeatureInstance,
			OriginRelease:         e.OriginRelease,
			Values:                e.Values,
			Infra:                 e.Infra,
			Inject:                convertInjectBlock(e.Inject),
		}
		irEdges = append(irEdges, irEdge)
	}

	// Feature markers (sorted)
	markers := make([]string, 0, len(allMarkers))
	for m := range allMarkers {
		if m != "" {
			markers = append(markers, m)
		}
	}
	sort.Strings(markers)

	// 6. Provenance
	prov := buildProvenance(res, cfg.CompilerVersion)

	// Boundary-feature projection: the un-folded Release boundary attachments,
	// the single source both lock-time source-descent and the render-time
	// boundary-FR dispatch read (no Release re-walk on either side).
	var boundaryFeatures types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]
	if res.Release != nil {
		boundaryFeatures = buildBoundaryFeaturesProjection(res.Release.Spec.Boundaries)
	}

	graph := &types.IRGraph{
		APIVersion: types.IRAPIVersion,
		Kind:       types.IRKind,
		Metadata: types.IRMetadata{
			AggregateName: res.Aggregate.Metadata.Name,
		},
		Spec: types.IRSpec{
			Nodes:                irNodes,
			Edges:                irEdges,
			FeatureMarkers:       markers,
			ValuesMapping:        valuesMapping,
			InfraMapping:         infraMapping,
			FeatureValuesMapping: featureValuesMapping,
			Secrets:              secrets,
			BoundaryFeatures:     boundaryFeatures,
			Provenance:           prov,
		},
	}

	return graph, featureOrder, nil
}

// convertInjectBlock translates the topology layer's typed *types.InjectBlock
// into the IR edge's per-mechanism shape — an outer OrderedMap keyed by
// mechanism name whose values are inner OrderedMaps of delivery-key → resolved
// string. The three standard mechanisms (env, file, arg) populate first in
// that hardcoded struct-field order; custom mechanisms from InjectBlock.Additional
// follow in author order via OrderedMap.All(), so the IR-edge per-mechanism
// map carries env/file/arg first and any custom mechanisms (e.g., ssm_param,
// vault_secret) the Developer authored after.
//
// Mechanism enumeration draws from four sources: the three typed fields and
// the Additional OrderedMap. The Developer's authoring shape — compact
// map-of-strings or verbose array-of-objects per the Aggregate schema's
// inject_delivery definition — flows through the InjectBlock unmarshal hooks
// at pkg/types/common.go, so by the time this function runs every value has
// been decoded into the polymorphic Go shapes the type-switch below handles.
//
// Type-coercion contract. The inner anonymous function copies only
// string-valued entries: map[string]string is copied verbatim;
// *types.OrderedMap[any] (the post-round-trip-fidelity decode shape for
// nested objects) and map[string]any (legacy in-memory test-fixture shape)
// are copied entry-by-entry, keeping only the entries whose values
// type-assert to string. Non-string values are silently dropped. The
// string-only restriction matches the IR's IREdge.Inject schema (string
// values per delivery key); the silent drop is the function's current
// behaviour and may need revisiting if upstream coercion bugs surface as
// missing inject entries downstream. The verbose-array form (encoded as
// []any with elements {in, from?, value?}) decodes by iterating entries
// and copying (in, value-or-from) pairs into the same OrderedMap[string]
// shape. The verbose form is the canonical authoring shape after the
// schema flip retired compact-form authoring at inject_delivery.
func convertInjectBlock(inject *types.InjectBlock) types.OrderedMap[types.OrderedMap[string]] {
	result := types.OrderedMap[types.OrderedMap[string]]{}
	if inject == nil {
		return result
	}
	convert := func(mech string, raw any) {
		if raw == nil {
			return
		}
		switch v := raw.(type) {
		case *types.OrderedMap[any]:
			if v == nil {
				return
			}
			m := types.OrderedMap[string]{}
			for k, val := range v.All() {
				if s, ok := val.(string); ok {
					m.Set(k, s)
				}
			}
			if m.Len() > 0 {
				result.Set(mech, m)
			}
		case map[string]any:
			m := types.OrderedMap[string]{}
			for k, val := range v {
				if s, ok := val.(string); ok {
					m.Set(k, s)
				}
			}
			if m.Len() > 0 {
				result.Set(mech, m)
			}
		case map[string]string:
			if len(v) > 0 {
				m := types.OrderedMap[string]{}
				for k, val := range v {
					m.Set(k, val)
				}
				result.Set(mech, m)
			}
		case []any:
			m := types.OrderedMap[string]{}
			for _, entry := range v {
				in, value, from, hasValue, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
				if !ok {
					continue
				}
				if hasValue {
					m.Set(in, value)
					continue
				}
				if hasFrom {
					m.Set(in, from)
				}
			}
			if m.Len() > 0 {
				result.Set(mech, m)
			}
		}
	}
	convert("env", inject.Env)
	convert("file", inject.File)
	convert("arg", inject.Arg)
	for mech, payload := range inject.Additional.All() {
		convert(mech, payload)
	}
	return result
}
