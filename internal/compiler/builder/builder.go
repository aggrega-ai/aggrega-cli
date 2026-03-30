package builder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

var (
	irNodeIDPattern     = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	irSecretNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
)

// GraphBuilder orchestrates YAML parsing, manifest resolution, and IR graph construction.
type GraphBuilder struct {
	// CompilerVersion is recorded in IR provenance (e.g. git tag or "dev").
	CompilerVersion string
	// IRSchemaVersion identifies the IR JSON Schema generation (v1alpha1).
	IRSchemaVersion string
}

// BuildFromPaths reads Aggregate.yaml and Manifest.yaml, resolves lockfile entries, and
// returns an in-memory IR graph. Configuration payloads never appear on IRNode; they live
// only in spec.values_mapping (split-brain / single source of truth).
func (b *GraphBuilder) BuildFromPaths(aggregatePath, manifestPath string) (*types.IRGraph, error) {
	agg, err := ParseAggregateFile(aggregatePath)
	if err != nil {
		return nil, err
	}
	man, err := ParseManifestFile(manifestPath)
	if err != nil {
		return nil, err
	}
	if err := ResolveManifestAgainstAggregate(agg, man); err != nil {
		return nil, fmt.Errorf("manifest resolution: %w", err)
	}
	ir, err := b.buildIR(agg, aggregatePath, manifestPath)
	if err != nil {
		return nil, err
	}
	return ir, nil
}

// Build is like BuildFromPaths but accepts raw YAML for tests and programmatic callers.
func (b *GraphBuilder) Build(aggregateYAML, manifestYAML []byte) (*types.IRGraph, error) {
	agg, err := ParseAggregate(aggregateYAML)
	if err != nil {
		return nil, err
	}
	man, err := ParseManifest(manifestYAML)
	if err != nil {
		return nil, err
	}
	if err := ResolveManifestAgainstAggregate(agg, man); err != nil {
		return nil, fmt.Errorf("manifest resolution: %w", err)
	}
	return b.buildIR(agg, "", "")
}

// BuildIRFromParsed constructs the IR graph from documents that were already parsed and for
// which ResolveManifestAgainstAggregate(agg, man) has succeeded. The manifest is accepted for
// API symmetry with the rest of the pipeline; IR emission currently depends only on the aggregate.
func (b *GraphBuilder) BuildIRFromParsed(agg *Aggregate, man *Manifest, aggregatePath, manifestPath string) (*types.IRGraph, error) {
	_ = man
	return b.buildIR(agg, aggregatePath, manifestPath)
}

func (b *GraphBuilder) buildIR(agg *Aggregate, aggregatePath, manifestPath string) (*types.IRGraph, error) {
	nodes := make(map[string]types.IRNode, len(agg.Spec.Components))
	valuesMapping := make(map[string]json.RawMessage, len(agg.Spec.Components))
	featureValuesMapping := make(map[string]map[string]json.RawMessage, len(agg.Spec.Components))
	secretsOut := make(map[string]map[string]types.IRSecretRequest)
	featureMarkers := make([]string, 0)

	for id, comp := range agg.Spec.Components {
		if !irNodeIDPattern.MatchString(id) {
			return nil, fmt.Errorf("component id %q must match %s", id, irNodeIDPattern.String())
		}

		nodes[id] = types.IRNode{
			ResolvedType: InferResolvedType(comp.Type),
			Component:    comp.Type,
		}

		var valuesRaw json.RawMessage
		if len(comp.Values) > 0 {
			var err error
			valuesRaw, err = json.Marshal(comp.Values)
			if err != nil {
				return nil, fmt.Errorf("component %q: marshal values: %w", id, err)
			}
		} else {
			valuesRaw = json.RawMessage(`{}`)
		}
		valuesMapping[id] = valuesRaw

		perNodeFeatures := make(map[string]json.RawMessage)
		for _, f := range comp.Features {
			featureMarkers = append(featureMarkers, normalizeFeatureMarker(f.Type))
			if f.With != nil {
				raw, err := json.Marshal(f.With)
				if err != nil {
					return nil, fmt.Errorf("component %q feature %q: marshal with: %w", id, f.Type, err)
				}
				perNodeFeatures[f.Type] = raw
			}
		}
		featureValuesMapping[id] = perNodeFeatures

		if len(comp.Secrets) > 0 {
			secMap, err := convertSecrets(id, comp.Secrets)
			if err != nil {
				return nil, err
			}
			secretsOut[id] = secMap
		}
	}

	annotations := map[string]json.RawMessage{}
	if len(agg.Metadata.Labels) > 0 {
		raw, err := json.Marshal(agg.Metadata.Labels)
		if err != nil {
			return nil, fmt.Errorf("marshal aggregate metadata labels: %w", err)
		}
		annotations["aggrega.aggregate.metadata.labels"] = raw
	}

	compilerVer := b.CompilerVersion
	if compilerVer == "" {
		compilerVer = "unknown"
	}
	schemaVer := b.IRSchemaVersion
	if schemaVer == "" {
		schemaVer = "ir-v1alpha1"
	}

	srcIDs := make([]string, 0, 2)
	if aggregatePath != "" {
		srcIDs = append(srcIDs, aggregatePath)
	}
	if manifestPath != "" {
		srcIDs = append(srcIDs, manifestPath)
	}

	graph := &types.IRGraph{
		APIVersion: types.IRAPIVersion,
		Kind:       types.IRKind,
		Metadata: types.IRMetadata{
			AppName: agg.Metadata.Name,
		},
		Spec: types.IRSpec{
			Nodes:                nodes,
			Edges:                []types.IREdge{},
			Routing:              []types.IRRouting{},
			Annotations:          annotations,
			FeatureMarkers:       featureMarkers,
			ValuesMapping:        valuesMapping,
			FeatureValuesMapping: featureValuesMapping,
			Secrets:              secretsOut,
			Artifact:             map[string]json.RawMessage{},
			Provenance: types.IRProvenance{
				SchemaVersion:     schemaVer,
				CompilerVersion:   compilerVer,
				SourceIdentifiers: srcIDs,
			},
		},
	}
	return graph, nil
}

func convertSecrets(componentID string, in map[string]AggregateSecretRef) (map[string]types.IRSecretRequest, error) {
	out := make(map[string]types.IRSecretRequest, len(in))
	for name, ref := range in {
		if !irSecretNamePattern.MatchString(name) {
			return nil, fmt.Errorf("component %q: secret name %q must match %s", componentID, name, irSecretNamePattern.String())
		}
		with := ref.With
		if with == nil {
			with = map[string]interface{}{}
		}
		withRM, err := mapToRawMessageMap(with)
		if err != nil {
			return nil, fmt.Errorf("component %q secret %q: %w", componentID, name, err)
		}
		out[name] = types.IRSecretRequest{
			Store: ref.Store,
			With:  withRM,
		}
	}
	return out, nil
}

func mapToRawMessageMap(m map[string]interface{}) (map[string]json.RawMessage, error) {
	if len(m) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		out[k] = raw
	}
	return out, nil
}

func normalizeFeatureMarker(featureType string) string {
	return strings.ToLower(strings.TrimSpace(featureType))
}
