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

// InterpolateParameters processes Go template expressions in raw YAML bytes
// using the provided parameter map. Sprig template functions are available.
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

// LoadParametersFrom reads external parameter YAML files and deep-merges them
// in order (later files override earlier). Returns the merged parameter map.
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

// MergeParameters merges Release parameters (OrderedMap) with externally loaded
// parameters (plain map). External parameters are applied first, then Release
// parameters override. Author order of Release parameters is lost here at the
// boundary with the Go template engine — template data does not preserve order.
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

// PurgeParameters zeroes and nils the parameter map for security enforcement.
// After Step 1 completes, parameter values must not linger in memory.
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
