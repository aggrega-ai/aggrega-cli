// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// InterpolateParameters renders Go template expressions in raw Aggregate
// YAML bytes against a parameter map exposed under the .parameters key
// (e.g. {{ .parameters.db_size }}). Sprig template functions are available.
//
// The template runs with missingkey=zero: a reference to an undefined
// parameter renders as the zero value (empty string for string contexts)
// rather than producing an error.
//
// Both template-parse and template-execute errors are wrapped as
// ResolverError with ErrInvalidParameterRef.
func InterpolateParameters(raw []byte, params map[string]any) ([]byte, error) {
	tmpl, err := template.New("aggregate").
		Option("missingkey=zero").
		Funcs(sprig.FuncMap()).
		Parse(string(raw))
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Message: fmt.Sprintf("template parse error: %v", err),
			Err:     ErrInvalidParameterRef(),
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{
		"parameters": params,
	}); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Message: fmt.Sprintf("template execution error: %v", err),
			Err:     ErrInvalidParameterRef(),
		}
	}

	return buf.Bytes(), nil
}

// LoadParametersImport reads a single parameter YAML file from the
// path supplied at Release.spec.parameters.$import: and returns its
// decoded map. Single-source-only at this site; multi-source layering
// is the province of Release extends: chains where the parent Release
// carries base parameters and the child extends + overrides.
//
// Read or unmarshal errors are wrapped as ResolverError with
// ErrParametersImportLoad and the offending path attached.
func LoadParametersImport(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   fmt.Sprintf(".spec.parameters.$import: %s", path),
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrParametersImportLoad(),
		}
	}
	var params map[string]any
	if err := yaml.Unmarshal(data, &params); err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   fmt.Sprintf(".spec.parameters.$import: %s", path),
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrParametersImportLoad(),
		}
	}
	return params, nil
}

// MergeParameters returns the combined parameter map for template use,
// given the Release.spec.parameters discriminator and any external
// values loaded via LoadParametersImport.
//
// Precedence is Release-wins-per-key when both inline and external
// are present — but post-migration the discriminator enforces
// exactly-one-mode (inline OR import), so the two-side merge is
// vestigial. The function is preserved for the (currently unused)
// future case where extends: chains supply external + inline at the
// same Release.
//
// Author order of inline Release parameters is lost at this boundary
// because Go's text/template data is plain map[string]any, which does
// not preserve insertion order. The lost ordering does not affect the
// rendered output — template references are by key, not by iteration
// order.
func MergeParameters(release types.ParametersBlock, external map[string]any) map[string]any {
	releasePlain := orderedMapToPlain(release.Inline)
	if releasePlain == nil && external == nil {
		return map[string]any{}
	}
	if external == nil {
		return copyMap(releasePlain)
	}
	if releasePlain == nil {
		return copyMap(external)
	}
	return deepMerge(external, releasePlain)
}

// orderedMapToPlain flattens an OrderedMap[any] into a plain map for
// template evaluation, recursively unwrapping nested *OrderedMap[any]
// values so Go templates can walk the tree via field-access syntax
// (.foo.bar.baz). Returns nil when the input is empty.
func orderedMapToPlain(m types.OrderedMap[any]) map[string]any {
	if m.Len() == 0 {
		return nil
	}
	return m.TemplateAlphabeticalMapDeep()
}

// PurgeParameters actively zeroes every value, deletes every key, and nils
// the map pointer. The function runs as the last step of Resolve. Removes
// parameter values from memory before the result is returned so secret-bearing
// values do not linger past the interpolation step.
//
// The function is a no-op on a nil pointer or nil map.
func PurgeParameters(params *map[string]any) {
	if params == nil || *params == nil {
		return
	}
	for k := range *params {
		(*params)[k] = nil
		delete(*params, k)
	}
	*params = nil
}

// deepMerge recursively merges child into base. Maps recurse; scalars/arrays replace.
func deepMerge(base, child map[string]any) map[string]any {
	if base == nil && child == nil {
		return map[string]any{}
	}
	if base == nil {
		return copyMap(child)
	}
	if child == nil {
		return copyMap(base)
	}
	result := copyMap(base)
	for k, cv := range child {
		bv, exists := result[k]
		if !exists {
			result[k] = cv
			continue
		}
		bMap, bIsMap := bv.(map[string]any)
		cMap, cIsMap := cv.(map[string]any)
		if bIsMap && cIsMap {
			result[k] = deepMerge(bMap, cMap)
		} else {
			result[k] = cv
		}
	}
	return result
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
