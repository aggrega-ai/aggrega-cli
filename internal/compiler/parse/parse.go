// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package parse centralises the two encoding-boundary decoders the compiler
// uses to read structured data into Go values. The named helpers force
// callers to commit, at the call site, to which encoding their bytes came
// from — yaml-encoded template output versus json-encoded IR
// payloads. The wrong choice surfaces as a readable name
// mismatch rather than a silent semantic drift between the two formats.
package parse

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// TemplateOutput decodes YAML emitted by Go-template execution at any of the
// compiler's template-output parse sites (Phase A injectMapping output, Phase
// B Recipe valuesMapping output, Phase B FeatureRecipe valuesMapping output,
// and forward-looking Workload / Artifacts parse sites). per the contract the
// compiler standardises on yaml.v3 here; YAML is a strict JSON superset, so
// JSON-shaped Recipe output continues to parse cleanly while authors gain
// access to YAML-native authoring features (block scalars, comments,
// unquoted scalars).
func TemplateOutput(b []byte, dst any) error {
	return yaml.Unmarshal(b, dst)
}

// IRPayload decodes JSON read from a json.RawMessage stored on the
// IR graph. The IR is JSON-encoded per the contract — every consumer reading
// IR-stored fields (the orchestrator's value/infra hydration, the renderer's
// template-context assembly and provides evaluation, the helm and compose
// provider engines' parseValuesPayload) decodes through this helper.
func IRPayload(b []byte, dst any) error {
	return json.Unmarshal(b, dst)
}
