// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"gopkg.in/yaml.v3"

	"github.com/cognitio/aggrega-cli/pkg/types"
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
func InterpolateParameters(raw []byte, params map[string]interface{}) ([]byte, error) {
	tmpl, err := template.New("aggregate").
		Option("missingkey=zero").
		Funcs(sprig.FuncMap()).
		Parse(string(raw))
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Message: fmt.Sprintf("template parse error: %v", err),
			Err:     ErrInvalidParameterRef,
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]interface{}{
		"parameters": params,
	}); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Message: fmt.Sprintf("template execution error: %v", err),
			Err:     ErrInvalidParameterRef,
		}
	}

	return buf.Bytes(), nil
}

// LoadParametersFrom reads each external parameter YAML file in declared
// order and deep-merges them into a single map. The merge semantics are:
//
//   - For keys whose value is a map on both sides, the maps recurse.
//   - For keys whose value is a scalar or array on either side, the later
//     value replaces the earlier (no concat, no scalar-additive behavior).
//   - For keys present only on one side, that side's value is carried
//     through.
//
// Read or unmarshal errors are wrapped as ResolverError with
// ErrParametersFromLoad and the offending path attached.
func LoadParametersFrom(paths []string) (map[string]interface{}, error) {
	merged := map[string]interface{}{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, &ResolverError{
				Entity:  "Release",
				Field:   fmt.Sprintf(".spec.parametersFrom[%s]", path),
				Message: fmt.Sprintf("failed to read file: %v", err),
				Err:     ErrParametersFromLoad,
			}
		}
		var params map[string]interface{}
		if err := yaml.Unmarshal(data, &params); err != nil {
			return nil, &ResolverError{
				Entity:  "Release",
				Field:   fmt.Sprintf(".spec.parametersFrom[%s]", path),
				Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
				Err:     ErrParametersFromLoad,
			}
		}
		merged = deepMerge(merged, params)
	}
	return merged, nil
}

// MergeParameters layers Release.spec.parameters on top of the
// LoadParametersFrom result and returns the combined map for template use.
//
// Precedence is Release-wins-per-key: external parameter files supply the
// base, and Release.spec.parameters override on collision.
//
// Author order of Release.spec.parameters is lost at this boundary because
// Go's text/template data is plain map[string]interface{}, which does not
// preserve insertion order. The lost ordering does not affect the rendered
// output — template references are by key, not by iteration order.
func MergeParameters(release types.OrderedMap[interface{}], external map[string]interface{}) map[string]interface{} {
	releasePlain := orderedMapToPlain(release)
	if releasePlain == nil && external == nil {
		return map[string]interface{}{}
	}
	if external == nil {
		return copyMap(releasePlain)
	}
	if releasePlain == nil {
		return copyMap(external)
	}
	return deepMerge(external, releasePlain)
}

// orderedMapToPlain flattens an OrderedMap[interface{}] into a plain map for
// template evaluation. Returns nil when the input is empty.
func orderedMapToPlain(m types.OrderedMap[interface{}]) map[string]interface{} {
	if m.Len() == 0 {
		return nil
	}
	out := make(map[string]interface{}, m.Len())
	for k, v := range m.All() {
		out[k] = v
	}
	return out
}

// PurgeParameters actively zeroes every value, deletes every key, and nils
// the map pointer. The function runs as the last step of Resolve. Removes
// parameter values from memory before the result is returned so secret-bearing
// values do not linger past the interpolation step.
//
// The function is a no-op on a nil pointer or nil map.
func PurgeParameters(params *map[string]interface{}) {
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
func deepMerge(base, child map[string]interface{}) map[string]interface{} {
	if base == nil && child == nil {
		return map[string]interface{}{}
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
		bMap, bIsMap := bv.(map[string]interface{})
		cMap, cIsMap := cv.(map[string]interface{})
		if bIsMap && cIsMap {
			result[k] = deepMerge(bMap, cMap)
		} else {
			result[k] = cv
		}
	}
	return result
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
