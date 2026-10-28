// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package parse centralises the two encoding-boundary decoders the compiler
// uses to read structured data into Go values. The named helpers force
// callers to commit, at the call site, to which encoding their bytes came
// from — yaml-encoded template output versus json-encoded IR payloads.
// The wrong choice surfaces as a readable name mismatch rather than a
// silent semantic drift between the two formats.
package parse

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// TemplateOutput decodes YAML emitted by Go-template execution at any of the
// compiler's template-output parse sites (Phase A injectMapping output, Phase
// B Recipe valuesMapping output, Phase B FeatureRecipe valuesMapping output,
// and forward-looking Workload / Artifacts parse sites). The compiler
// standardises on yaml.v3 for template output; YAML is a strict JSON
// superset, so JSON-shaped Recipe output continues to parse cleanly while
// authors gain access to YAML-native authoring features (block scalars,
// comments, unquoted scalars).
//
// A value the output writes in a shape its position does not take is reported in
// the author's terms — what was written and what the position takes — rather than
// by the Go type the output is decoded into, which the author of the template
// never sees.
func TemplateOutput(b []byte, dst any) error {
	return authoredshape.DescribeYAMLDecodeError(yaml.Unmarshal(b, dst))
}

// IRPayload decodes JSON read from a json.RawMessage stored on the
// IR graph. The IR is JSON-encoded — every consumer reading IR-stored
// fields (the orchestrator's value/infra hydration, the renderer's
// template-context assembly and provides evaluation, the helm and compose
// provider engines' parseValuesPayload) decodes through this helper.
func IRPayload(b []byte, dst any) error {
	return json.Unmarshal(b, dst)
}

// NodePayloadMap extracts a node's IR-stored payload (a values_mapping or
// infra_mapping entry) as a flattened, deterministically-ordered
// map[string]any: it decodes the json.RawMessage through IRPayload and
// flattens via the ordered map's deep view. Returns an empty map when the
// node has no entry. The lock-time source descent and the build-time
// source-eval both call this, so they turn the materialized IR pillar into
// the template `.values` / `.infra` map identically — the two passes cannot
// drift on the conversion, which is what keeps a value/infra-driven source
// identity pinning the same at lock and build.
func NodePayloadMap(mapping types.OrderedMap[json.RawMessage], nodeID string) map[string]any {
	raw, ok := mapping.Get(nodeID)
	if !ok {
		return map[string]any{}
	}
	var om types.OrderedMap[any]
	if err := IRPayload(raw, &om); err != nil {
		return map[string]any{}
	}
	return om.TemplateAlphabeticalMapDeep()
}
