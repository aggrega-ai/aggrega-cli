// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"gopkg.in/yaml.v3"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// LoadAggregateParameterContract reads the raw Aggregate YAML at path and
// extracts the spec.parameters: block into a per-key contract map. The
// extraction is shallow: it does not run template interpolation, so the
// contract is available BEFORE InterpolateParameters runs. Returns an empty
// map when the Aggregate carries no spec.parameters: block.
//
// The shallow parse is correct because contract values (type, enum, pattern,
// etc.) are static authoring data; they do not reference Release-supplied
// parameter values. Default-value derivation across parameters is not
// supported at v3 (the conditional-default and parameter-dependency
// alternatives were rejected; cross-parameter constraints
// land in the separate assertions: block).
func LoadAggregateParameterContract(path string) (types.OrderedMap[any], error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return types.OrderedMap[any]{}, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	var shell struct {
		Spec struct {
			Parameters types.OrderedMap[any] `yaml:"parameters"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &shell); err != nil {
		return types.OrderedMap[any]{}, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal contract shell: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	return shell.Spec.Parameters, nil
}

// ValidateParametersAgainstContract checks every Release-supplied parameter
// value against the Aggregate's typed parameter contract. Runs at Step 1a —
// after Release parameter load (inline OR $import:), before parameter
// interpolation. Returns ResolverError entries with file:line provenance
// for each failure.
//
// Closed-contract policy: every Release-supplied key must have a
// corresponding Aggregate declaration. Undeclared Release keys produce
// ErrParameterContractViolation regardless of whether the Aggregate's
// parameters: block is empty.
//
// Required-marker policy: typed-form parameters with required: true that
// supply neither an Aggregate default nor a Release value produce
// ErrParameterMissing.
//
// Per-entry dispatch: scalar Aggregate parameter values produce a
// SimpleParameterContract (type inferred from scalar kind, no constraints);
// map values decode as typed-form contracts via DecodeParameterContract.
//
// Nested object recursion is lazy: when a typed parameter declares
// type: object with Properties, the validator descends into each child
// pairing the Properties sub-contract against the Release-supplied
// sub-map.
func ValidateParametersAgainstContract(
	contract types.OrderedMap[any],
	params map[string]any,
	aggregatePath string,
) []*ResolverError {
	var errs []*ResolverError

	declared := make(map[string]struct{}, contract.Len())
	for _, k := range contract.Keys() {
		declared[k] = struct{}{}
	}

	for k := range params {
		if _, ok := declared[k]; !ok {
			errs = append(errs, &ResolverError{
				Entity: "Aggregate",
				Field:  fmt.Sprintf(".spec.parameters.%s", k),
				Message: fmt.Sprintf(
					"Release supplies parameter %q with no corresponding Aggregate declaration",
					k),
				Err: ErrParameterContractViolation,
			})
		}
	}

	for _, key := range contract.Keys() {
		entry, _ := contract.Get(key)
		pc, derr := types.DecodeParameterContract(entry)
		if derr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", key),
				Message: fmt.Sprintf("failed to decode parameter contract: %v", derr),
				Err:     ErrParameterContractViolation,
			})
			continue
		}

		releaseValue, releaseHas := params[key]
		switch {
		case !releaseHas && pc.Required && pc.Default == nil:
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", key),
				Message: fmt.Sprintf("parameter %q is required but not supplied by Release", key),
				Err:     ErrParameterMissing,
			})
		case releaseHas:
			errs = append(errs, validateEntry(key, pc, releaseValue)...)
		}
	}

	return errs
}

// validateEntry runs the contract checks against one Release-supplied
// value. Order: type-kind dispatch first (rejects type mismatches early),
// then per-shape constraints, then enum membership, then validate-predicate.
func validateEntry(path string, pc types.ParameterContract, value any) []*ResolverError {
	if pc.Type == "" {
		return nil
	}

	var errs []*ResolverError

	if !typeMatches(pc.Type, value) {
		errs = append(errs, &ResolverError{
			Entity:  "Aggregate",
			Field:   fmt.Sprintf(".spec.parameters.%s", path),
			Message: fmt.Sprintf("parameter %q expected type %s; got %T", path, pc.Type, value),
			Err:     ErrParameterContractViolation,
		})
		return errs
	}

	switch pc.Type {
	case "string":
		errs = append(errs, checkStringConstraints(path, pc, value.(string))...)
	case "integer", "number":
		errs = append(errs, checkNumericConstraints(path, pc, value)...)
	case "object":
		errs = append(errs, checkObjectConstraints(path, pc, value)...)
	}

	if len(pc.Enum) > 0 {
		if !enumMatches(pc.Enum, value) {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q value %v not in enum %v", path, value, pc.Enum),
				Err:     ErrParameterContractViolation,
			})
		}
	}

	if pc.Validate != "" {
		if ok, perr := evalValidatePredicate(pc.Validate, value); perr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q validate predicate error: %v", path, perr),
				Err:     ErrParameterContractViolation,
			})
		} else if !ok {
			msg := pc.ValidateMessage
			if msg == "" {
				msg = fmt.Sprintf("parameter %q failed validate predicate", path)
			}
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: msg,
				Err:     ErrParameterContractViolation,
			})
		}
	}

	return errs
}

// typeMatches dispatches by contract type against the actual Go type
// produced by yaml.v3 decode. Allowed conversions:
//
//   - type: integer    accepts int, int8..int64, uint..uint64
//   - type: number     accepts the integer types AND float32, float64
//   - type: string     accepts string only
//   - type: boolean    accepts bool only
//   - type: array      accepts []any
//   - type: object     accepts map[string]any
func typeMatches(declaredType string, v any) bool {
	switch declaredType {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer":
		switch v.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		}
		return false
	case "number":
		switch v.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
			float32, float64:
			return true
		}
		return false
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	}
	return true
}

func checkStringConstraints(path string, pc types.ParameterContract, s string) []*ResolverError {
	var errs []*ResolverError
	if pc.MinLength != nil && len(s) < *pc.MinLength {
		errs = append(errs, &ResolverError{
			Entity:  "Aggregate",
			Field:   fmt.Sprintf(".spec.parameters.%s", path),
			Message: fmt.Sprintf("parameter %q length %d below minLength %d", path, len(s), *pc.MinLength),
			Err:     ErrParameterContractViolation,
		})
	}
	if pc.MaxLength != nil && len(s) > *pc.MaxLength {
		errs = append(errs, &ResolverError{
			Entity:  "Aggregate",
			Field:   fmt.Sprintf(".spec.parameters.%s", path),
			Message: fmt.Sprintf("parameter %q length %d above maxLength %d", path, len(s), *pc.MaxLength),
			Err:     ErrParameterContractViolation,
		})
	}
	if pc.Pattern != "" {
		re, perr := regexp.Compile(pc.Pattern)
		if perr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q pattern compile error: %v", path, perr),
				Err:     ErrParameterContractViolation,
			})
		} else if !re.MatchString(s) {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q value %q does not match pattern %q", path, s, pc.Pattern),
				Err:     ErrParameterContractViolation,
			})
		}
	}
	return errs
}

func checkNumericConstraints(path string, pc types.ParameterContract, v any) []*ResolverError {
	var errs []*ResolverError
	num, ok := toFloat(v)
	if !ok {
		return errs
	}
	if pc.Min != nil {
		if minNum, mok := toFloat(pc.Min); mok && num < minNum {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q value %v below min %v", path, v, pc.Min),
				Err:     ErrParameterContractViolation,
			})
		}
	}
	if pc.Max != nil {
		if maxNum, mok := toFloat(pc.Max); mok && num > maxNum {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q value %v above max %v", path, v, pc.Max),
				Err:     ErrParameterContractViolation,
			})
		}
	}
	return errs
}

// checkObjectConstraints handles the lazy nested-Properties recursion:
// when a typed parameter declares type: object with Properties, each
// child value gets validated against its declared child contract.
// Children declared in Properties but absent from the Release map check
// their own required-marker; children present in the Release map but
// absent from Properties violate the closed-contract policy at the
// nested level.
func checkObjectConstraints(path string, pc types.ParameterContract, v any) []*ResolverError {
	if pc.Properties.Len() == 0 {
		return nil
	}
	childMap, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	var errs []*ResolverError

	declared := make(map[string]struct{}, pc.Properties.Len())
	for _, k := range pc.Properties.Keys() {
		declared[k] = struct{}{}
	}
	for k := range childMap {
		if _, ok := declared[k]; !ok {
			errs = append(errs, &ResolverError{
				Entity: "Aggregate",
				Field:  fmt.Sprintf(".spec.parameters.%s.%s", path, k),
				Message: fmt.Sprintf(
					"Release supplies nested parameter %q under %q with no corresponding Properties declaration",
					k, path),
				Err: ErrParameterContractViolation,
			})
		}
	}

	for _, childKey := range pc.Properties.Keys() {
		entry, _ := pc.Properties.Get(childKey)
		childPC, derr := types.DecodeParameterContract(entry)
		if derr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s.%s", path, childKey),
				Message: fmt.Sprintf("failed to decode nested parameter contract: %v", derr),
				Err:     ErrParameterContractViolation,
			})
			continue
		}
		childPath := fmt.Sprintf("%s.%s", path, childKey)
		childValue, childHas := childMap[childKey]
		switch {
		case !childHas && childPC.Required && childPC.Default == nil:
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", childPath),
				Message: fmt.Sprintf("nested parameter %q is required but not supplied by Release", childPath),
				Err:     ErrParameterMissing,
			})
		case childHas:
			errs = append(errs, validateEntry(childPath, childPC, childValue)...)
		}
	}
	return errs
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func enumMatches(allowed []any, value any) bool {
	for _, a := range allowed {
		if a == value {
			return true
		}
		af, aok := toFloat(a)
		vf, vok := toFloat(value)
		if aok && vok && af == vf {
			return true
		}
	}
	return false
}

// evalValidatePredicate compiles the validate template against the value
// (bound as .value) and Sprig functions. The template must render to one
// of the truthy/falsy literals "true" / "false" — anything else returns an
// error from the predicate evaluator.
func evalValidatePredicate(predicate string, value any) (bool, error) {
	tmpl, err := template.New("validate").
		Funcs(sprig.FuncMap()).
		Parse(predicate)
	if err != nil {
		return false, fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{"value": value}); err != nil {
		return false, fmt.Errorf("execute: %w", err)
	}
	switch buf.String() {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("predicate must render true/false; got %q", buf.String())
}
