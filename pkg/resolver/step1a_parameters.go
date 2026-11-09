// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"text/template/parse"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
)

// LoadAggregateParameterContract reads the raw Aggregate YAML at path and
// extracts the spec.parameters: block into a per-key contract map. Thin
// wrapper around LoadAggregateParameterContractFromBytes — reads the file,
// delegates. Callers that already hold raw bytes (the resolve path and the
// catalog's sub-aggregate parameter resolution) call
// LoadAggregateParameterContractFromBytes directly with the bytes and a
// source-path string for error provenance.
func LoadAggregateParameterContract(path string) (types.OrderedMap[any], error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return types.OrderedMap[any]{}, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	return LoadAggregateParameterContractFromBytes(data, path)
}

// LoadAggregateParameterContractFromBytes parses an Aggregate YAML byte
// slice and extracts the spec.parameters: block into a per-key contract
// map. The extraction is shallow: it does not run template interpolation,
// so the contract is available BEFORE InterpolateParameters runs. Returns
// an empty map when the Aggregate carries no spec.parameters: block.
//
// The shallow parse is correct because contract values (type, enum, pattern,
// etc.) are static authoring data; they do not reference Release-supplied
// parameter values. Default-value derivation across parameters is not
// supported at v3 (the conditional-default and parameter-dependency
// alternatives were rejected; cross-parameter constraints
// land in the separate assertions: block).
//
// The contract is judged in two steps before it is returned, and a contract
// failing either is refused whether or not any Release supplies the parameter at
// fault. The envelope validation holds it to the schema's shape — which keys a
// declaration carries, the type of each, a non-negative minLength and maxLength
// and a supported format name on each parameter's own declaration. Then
// [refuseMalformedParameterContract] compiles every pattern the contract
// declares, holds every format name to the supported set and refuses a `min` or
// `max` on a boolean or object declaration, at every nesting, and refuses a fault
// with ErrParameterContractViolation.
//
// sourcePath is used only for ResolverError.Field provenance — it identifies
// where the bytes came from for error display, but the function performs no
// filesystem operations against it.
func LoadAggregateParameterContractFromBytes(data []byte, sourcePath string) (types.OrderedMap[any], error) {
	var shell struct {
		Spec struct {
			Parameters types.OrderedMap[any] `yaml:"parameters"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &shell); err != nil {
		return types.OrderedMap[any]{}, &ResolverError{
			Entity:  "Aggregate",
			Field:   sourcePath,
			Message: fmt.Sprintf("failed to unmarshal contract shell: %v", err),
			Err:     ErrYAMLLoad(),
		}
	}
	// Second-line-of-defense: envelope-validate the shallow-loaded contract
	// against the canonical aggregate-v1beta1 sub-schema. Catches malformed
	// contracts (wrong type for min:, invalid enum shape, etc.) at workspace
	// load rather than as contract-violation failures at parameter-contract
	// validation.
	if envErr := validateParameterContractEnvelope(shell.Spec.Parameters, sourcePath); envErr != nil {
		return types.OrderedMap[any]{}, envErr
	}
	if shapeErr := refuseMalformedParameterContract(shell.Spec.Parameters); shapeErr != nil {
		return types.OrderedMap[any]{}, shapeErr
	}
	return shell.Spec.Parameters, nil
}

// aggregateParameterViolations collapses a slice of parameter-contract
// violations into a single *ResolverError. Single-violation case returns
// the original error unchanged; multi-violation case constructs an
// aggregated error whose Summary lists every violation's Field and Message
// beneath a line carrying the violation count.
//
// That line carries the count and nothing else. What condition was met is what
// the group's Sentinel says, and the group's own header renders that sentence
// ahead of this summary — so naming the condition here would state it twice in
// one report.
//
// The single-*ResolverError surface preserves errors.As(*ResolverError)
// at the CLI's display layer (formatCompilerError at
// cmd/aggrega/error_format.go), which threads through fmt.Errorf
// wrapping and renders via ResolverError.ToCompilerError() — one report
// carrying the count line and every violation. That is a presentation
// choice rather than a workaround: the display layer also reaches an
// errors.Join beneath a plain wrap and renders every joined element.
//
// Caller invariant: never called with an empty slice; the call site
// gates on len(verrs) > 0.
func aggregateParameterViolations(verrs []*ResolverError) error {
	if len(verrs) == 1 {
		return verrs[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d violations:", len(verrs))
	for _, v := range verrs {
		fmt.Fprintf(&b, "\n  - %s: %s", v.Field, v.Message)
	}
	return &MultiResolverError{
		Entity:     "Aggregate",
		Field:      ".spec.parameters",
		Summary:    b.String(),
		Sentinel:   ErrParameterContractViolation(),
		Violations: verrs,
	}
}

// ApplyParameterDefaults walks the Aggregate's parameter contract and
// inserts the declared default value into the merged-parameters map for
// every slot the Release did not supply. Runs between MergeParameters
// and ValidateParametersAgainstContract so the contract validator sees
// resolved values rather than absent slots.
//
// The fill preserves the declared type — integer defaults arrive as
// integers, boolean defaults as booleans, object defaults as maps,
// array defaults as slices. Type coercion is not attempted; a default
// authored at the wrong type fails the contract validator downstream
// with a clear per-slot error.
//
// Release-supplied values take precedence: when params[key] already
// has a value, the function leaves the top-level value untouched and
// recurses into nested properties as described below.
//
// Three-case semantic for nested-object parameters:
//
//  1. Parent slot absent from the Release: if the parent's contract
//     declares Properties whose children carry defaults, the parent is
//     synthesized as a map and children's defaults fill into it. This
//     makes "omit the slot to get the contract's defaults" work at
//     every nesting level. Synthesis fires only when at least one
//     child has a default — synthesizing an empty parent map with no
//     useful content would add clutter rather than value.
//
//  2. Parent slot supplied as an empty map (e.g., `database: {}`):
//     the empty map is a deliberate opt-out signal. Children's
//     defaults are NOT filled; the slot remains empty. This mirrors
//     how Aggrega treats explicit null in annotations — an explicit
//     terminal value is distinct from omission.
//
//  3. Parent slot supplied as a non-empty map (e.g.,
//     `database: { host: "x" }`): the recursion fills children the
//     Release did not supply. Release-authored children win.
//
// Mutates params in place. Returns the same map for caller
// convenience. A nil params argument allocates a fresh map.
func ApplyParameterDefaults(contract types.OrderedMap[any], params map[string]any) map[string]any {
	if params == nil {
		params = make(map[string]any)
	}
	for _, key := range contract.Keys() {
		entry, _ := contract.Get(key)
		pc, derr := types.DecodeParameterContract(entry)
		if derr != nil {
			// Decode failures surface at ValidateParametersAgainstContract
			// with a per-slot ResolverError; quietly proceeding here
			// keeps that validator's diagnostic as the user's first
			// signal rather than introducing a competing failure mode.
			continue
		}

		if _, releaseHas := params[key]; !releaseHas {
			// Case 1 — parent absent from Release.
			if pc.Default != nil {
				params[key] = pc.Default
			} else if pc.Properties.Len() > 0 {
				synthesized := applyNestedDefaults(pc.Properties, map[string]any{})
				if len(synthesized) > 0 {
					params[key] = synthesized
				}
			}
		} else if pc.Properties.Len() > 0 {
			// Parent present in Release; recurse only when the value
			// is itself a map (Case 2 / Case 3 below). A non-map value
			// here is a type-mismatch; the contract validator surfaces
			// it with ErrParameterContractViolation. The default-fill
			// must not silently correct type errors.
			if sub, ok := params[key].(map[string]any); ok {
				if len(sub) > 0 {
					// Case 3 — parent supplied non-empty: recurse to
					// fill children the Release did not override.
					params[key] = applyNestedDefaults(pc.Properties, sub)
				}
				// Case 2 — parent supplied as empty map: leave it
				// empty. The explicit empty is a deliberate opt-out
				// from children's defaults.
			}
		}
	}
	return params
}

// applyNestedDefaults is a thin wrapper that calls ApplyParameterDefaults
// recursively. The wrapper exists to make the call sites at
// parent-dispatch points read naturally; the underlying recursion is
// the same three-case dispatch applied at depth N+1.
func applyNestedDefaults(properties types.OrderedMap[any], sub map[string]any) map[string]any {
	return ApplyParameterDefaults(properties, sub)
}

// ValidateParametersAgainstContract checks every Release-supplied parameter
// value against the Aggregate's typed parameter contract. This is
// parameter-contract validation — it runs after the Release parameter load
// (inline OR $import:) and before parameter interpolation. Returns
// ResolverError entries with file:line provenance for each failure.
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
// Per-entry dispatch: each contract entry must be a typed-form map
// decoded via DecodeParameterContract; bare-scalar shorthand is
// rejected at parse time with a directed error.
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
				Err: ErrParameterContractViolation(),
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
				Err:     ErrParameterContractViolation(),
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
				Err:     ErrParameterMissing(),
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
//
// An entry declaring no type is skipped entirely: it applies no constraints at
// all, and no type is inferred from the supplied value. An author who wants a
// parameter constrained declares its type; an untyped declaration accepts
// whatever the Release supplies, which is what makes it the escape hatch for a
// value whose shape the Aggregate does not wish to pin.
//
// A type mismatch short-circuits. When the value is not of the declared type the
// function returns that one violation and runs nothing further, so the author is
// not also told that a string constraint failed against a value that was never a
// string.
func validateEntry(path string, pc types.ParameterContract, value any) []*ResolverError {
	if pc.Type == "" {
		return nil
	}

	var errs []*ResolverError

	if !typeMatches(pc.Type, value) {
		errs = append(errs, &ResolverError{
			Entity:  "Aggregate",
			Field:   fmt.Sprintf(".spec.parameters.%s", path),
			Message: fmt.Sprintf("parameter %q expected type %s; got %s", path, pc.Type, authoredshape.Of(value)),
			Err:     ErrParameterContractViolation(),
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
	case "array":
		errs = append(errs, checkArrayConstraints(path, pc, value)...)
	}

	if len(pc.Enum) > 0 {
		if !enumMatches(pc.Enum, value) {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q value %v not in enum %v", path, value, pc.Enum),
				Err:     ErrParameterContractViolation(),
			})
		}
	}

	if pc.Validate != "" {
		if ok, perr := evalValidatePredicate(pc.Validate, value); perr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q validate predicate error: %v", path, perr),
				Err:     ErrParameterContractViolation(),
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
				Err:     ErrParameterContractViolation(),
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
//
// Those six spellings are the whole set, and no other spelling reaches here:
// every path to this function obtains its contract through
// [LoadAggregateParameterContractFromBytes], which envelope-validates the
// authored contract against the published Aggregate schema, where the parameter
// type is a closed enum of exactly these six. A misspelt type therefore halts at
// workspace load, naming the contract, rather than arriving as a Release-side
// contract violation. The permissive fall-through below is defensive against
// that guarantee being bypassed; it is not an authoring escape hatch.
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
			Err:     ErrParameterContractViolation(),
		})
	}
	if pc.MaxLength != nil && len(s) > *pc.MaxLength {
		errs = append(errs, &ResolverError{
			Entity:  "Aggregate",
			Field:   fmt.Sprintf(".spec.parameters.%s", path),
			Message: fmt.Sprintf("parameter %q length %d above maxLength %d", path, len(s), *pc.MaxLength),
			Err:     ErrParameterContractViolation(),
		})
	}
	// `min` and `max` on a string are its length, the same length measured above.
	errs = append(errs, checkMeasuredBounds(path, pc, len(s))...)
	if pc.Pattern != "" {
		// The contract load compiled this pattern and refused it had it not
		// compiled, so this is handed the expression compiled there. The error arm
		// is reached only by a contract that did not come through that load.
		re, perr := compileParameterPattern(pc.Pattern)
		if perr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: patternDoesNotCompile(path, pc.Pattern, perr),
				Err:     ErrParameterContractViolation(),
			})
		} else if !re.MatchString(s) {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q value %q does not match pattern %q", path, s, pc.Pattern),
				Err:     ErrParameterContractViolation(),
			})
		}
	}
	if pc.Format != "" {
		// The contract load refused a name outside the supported set, so the name
		// here is one the checkers know. The unsupported arm is reached only by a
		// contract that did not come through that load, and refuses in the load's
		// own words rather than letting an unknown name pass as a constraint.
		if nerr := stringformat.CheckName(pc.Format); nerr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: formatNotSupported(path, nerr),
				Err:     ErrParameterContractViolation(),
			})
		} else if ferr := stringformat.Check(pc.Format, s); ferr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q value %v", path, ferr),
				Err:     ErrParameterContractViolation(),
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
				Err:     ErrParameterContractViolation(),
			})
		}
	}
	if pc.Max != nil {
		if maxNum, mok := toFloat(pc.Max); mok && num > maxNum {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q value %v above max %v", path, v, pc.Max),
				Err:     ErrParameterContractViolation(),
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
// nested level — UNLESS the contract also declares ValuesType.
//
// ValuesType handling (typed-value-map):
//
// When the contract declares ValuesType, unknown keys are no longer
// violations — they are the typed-value-map's open extension surface.
// The closed-object unknown-key rejection is silenced; every key not in
// Properties is validated against the ValuesType sub-contract instead.
// If both Properties and ValuesType are declared, Properties takes
// precedence for named keys (matching JSON Schema's
// `properties + additionalProperties: <schema>` semantic at the
// typed-value-map level).
func checkObjectConstraints(path string, pc types.ParameterContract, v any) []*ResolverError {
	if pc.Properties.Len() == 0 && pc.ValuesType == nil {
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
	// Closed-object unknown-key rejection is silenced when ValuesType is
	// declared — typed-value-maps accept any key, validated under the
	// ValuesType sub-contract below.
	if pc.ValuesType == nil {
		for k := range childMap {
			if _, ok := declared[k]; !ok {
				errs = append(errs, &ResolverError{
					Entity: "Aggregate",
					Field:  fmt.Sprintf(".spec.parameters.%s.%s", path, k),
					Message: fmt.Sprintf(
						"Release supplies nested parameter %q under %q with no corresponding Properties declaration",
						k, path),
					Err: ErrParameterContractViolation(),
				})
			}
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
				Err:     ErrParameterContractViolation(),
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
				Err:     ErrParameterMissing(),
			})
		case childHas:
			errs = append(errs, validateEntry(childPath, childPC, childValue)...)
		}
	}

	// ValuesType pass: validate every key NOT in Properties against the
	// ValuesType sub-contract (Properties takes precedence for named keys).
	if pc.ValuesType != nil {
		valuesPC, derr := types.DecodeParameterContract(pc.ValuesType)
		if derr != nil {
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("failed to decode valuesType contract: %v", derr),
				Err:     ErrParameterContractViolation(),
			})
		} else {
			for k, val := range childMap {
				if _, named := declared[k]; named {
					continue
				}
				childPath := fmt.Sprintf("%s.%s", path, k)
				errs = append(errs, validateEntry(childPath, valuesPC, val)...)
			}
		}
	}

	return errs
}

// checkArrayConstraints validates a Release-supplied array against the
// contract: its item count against the contract's `min` and `max`, then each
// element against the declared Items sub-contract. Element paths use dot
// notation (parent.0, parent.1, ...) consistent with the Properties recursion
// convention; no element check runs if Items is unset (a contract declaring
// type: array without items: accepts any element).
func checkArrayConstraints(path string, pc types.ParameterContract, v any) []*ResolverError {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	errs := checkMeasuredBounds(path, pc, len(arr))
	if pc.Items == nil {
		return errs
	}
	itemsPC, derr := types.DecodeParameterContract(pc.Items)
	if derr != nil {
		return append(errs, &ResolverError{
			Entity:  "Aggregate",
			Field:   fmt.Sprintf(".spec.parameters.%s", path),
			Message: fmt.Sprintf("failed to decode items contract: %v", derr),
			Err:     ErrParameterContractViolation(),
		})
	}
	for i, elem := range arr {
		elemPath := fmt.Sprintf("%s.%d", path, i)
		errs = append(errs, validateEntry(elemPath, itemsPC, elem)...)
	}
	return errs
}

// checkMeasuredBounds holds a measured size of a Release-supplied value — the
// number of items in a list, the length of a string — to the contract's `min`
// and `max`.
//
// What `min` and `max` bound is read from the compact form's own statement of
// the pair rather than restated here: on an array they are the item count and on
// a string its length, as they are in every schema the compact form compiles, so
// a parameter declared `min: 1` refuses an empty list or an empty string exactly
// as a component's schema does. A string's length is measured as the
// `minLength` and `maxLength` checks measure it, by the caller passing the same
// length. A numeric parameter's bound is its value, which [checkNumericConstraints]
// holds; this ends silent for it. A bound written as anything other than a number
// bounds nothing here, as it bounds nothing for a numeric parameter.
func checkMeasuredBounds(path string, pc types.ParameterContract, size int) []*ResolverError {
	var errs []*ResolverError
	bounds := []struct {
		isMin bool
		limit any
	}{{true, pc.Min}, {false, pc.Max}}
	for _, b := range bounds {
		if b.limit == nil {
			continue
		}
		keyword, applies := compactform.MinMaxKeyword(pc.Type, b.isMin)
		if !applies {
			continue
		}
		var measured string
		switch keyword {
		case "minItems", "maxItems":
			measured = fmt.Sprintf("holds %d item(s)", size)
		case "minLength", "maxLength":
			measured = fmt.Sprintf("length %d", size)
		default:
			continue
		}
		limit, numeric := toFloat(b.limit)
		if !numeric {
			continue
		}
		n := float64(size)
		switch {
		case b.isMin && n < limit:
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q %s, below min %v", path, measured, b.limit),
				Err:     ErrParameterContractViolation(),
			})
		case !b.isMin && n > limit:
			errs = append(errs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s", path),
				Message: fmt.Sprintf("parameter %q %s, above max %v", path, measured, b.limit),
				Err:     ErrParameterContractViolation(),
			})
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

// predicateValueRoot is the one name a validate: predicate reaches the value
// under validation through. It spells the key the predicate context delivers AND
// the reference that context admits, so what a predicate is handed and what it
// may name cannot come to disagree.
const predicateValueRoot = "value"

// refusePredicateUnknownRoot halts a validate: predicate that names something the
// predicate context does not define, before the predicate runs.
//
// Without it such a predicate does not fail in a way that names the mistake. The
// template layer resolves an undefined name to nothing, and what the author is
// then told depends on where that nothing lands: it may surface as a predicate
// that rendered neither true nor false, and it may instead render "false" and be
// reported as the VALUE failing validation — which tells an author their data is
// wrong when it is their predicate that is. Naming the reference, and the set it
// was measured against, is what separates the two.
//
// References come from the shared reference walk, which attributes a reference to
// the context root conservatively. That is the only polarity an allow-list may
// use: a reference the walk does not attribute is simply not checked, whereas
// attributing one wrongly refuses legal authoring — a field read inside a range
// body over the value, or the trailing key of a chained fallback, is not a
// reference to a root and must not be treated as one.
//
// Only the ROOT is checked. What sits beneath .value is authored data whose shape
// the parameter contract does not declare, so a key under it has no declared set
// to be measured against and is left to the evaluation.
func refusePredicateUnknownRoot(tree parse.Node) error {
	for _, ref := range gotmpl.CollectRootRefs(tree) {
		if ref.Segs[0] == predicateValueRoot {
			continue
		}
		return fmt.Errorf(
			"references %q, which a validate: predicate does not define; it defines .%s",
			ref.Path, predicateValueRoot)
	}
	return nil
}

// evalValidatePredicate compiles the validate template against the value
// (bound as .value) and the function set an Aggregate's own templates are
// rendered with — Sprig's, with `index` halting on a map key the map does not
// hold. The template must render to one of the truthy/falsy literals "true" /
// "false" — anything else returns an error from the predicate evaluator.
//
// A predicate naming anything other than .value is refused before it runs, so a
// mistyped reference is reported as the naming mistake it is rather than through
// whatever its empty resolution happens to produce.
func evalValidatePredicate(predicate string, value any) (bool, error) {
	tmpl, err := template.New("validate").
		Funcs(aggregateTemplateFuncs()).
		Parse(predicate)
	if err != nil {
		return false, fmt.Errorf("parse: %w", err)
	}
	if err := refusePredicateUnknownRoot(tmpl.Tree.Root); err != nil {
		return false, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{predicateValueRoot: value}); err != nil {
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
