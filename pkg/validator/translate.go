package validator

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
)

// translateCUEErrors converts CUE validation errors into Aggrega ValidationErrors.
// Raw CUE error strings are never exposed to the caller.
func translateCUEErrors(err error, entity string, sentinel error) []*ValidationError {
	if err == nil {
		return nil
	}

	cueErrs := cueerrors.Errors(err)
	if len(cueErrs) == 0 {
		return []*ValidationError{{
			Entity:     entity,
			FieldPath:  ".",
			Constraint: "validation failed",
			Err:        sentinel,
		}}
	}

	out := make([]*ValidationError, 0, len(cueErrs))
	for _, ce := range cueErrs {
		ve := translateOneCUEError(ce, entity, sentinel)
		out = append(out, ve)
	}
	return out
}

func translateOneCUEError(ce error, entity string, sentinel error) *ValidationError {
	path := cueErrorPath(ce)
	msg := cueerrors.Details(ce, nil)
	msg = strings.TrimSpace(msg)

	ve := &ValidationError{
		Entity:    entity,
		FieldPath: path,
		Err:       sentinel,
	}

	switch {
	case strings.Contains(msg, "conflicting values"):
		ve.Constraint = extractConflict(msg)
		ve.Suggestion = "Check that the value matches the expected type"
	case strings.Contains(msg, "incomplete value"):
		ve.Constraint = "required field is missing"
		ve.Suggestion = fmt.Sprintf("Add the required field at %s", path)
	case strings.Contains(msg, "not allowed"):
		ve.Constraint = "field not allowed by schema"
		ve.Suggestion = fmt.Sprintf("Remove the unexpected field at %s", path)
	default:
		ve.Constraint = sanitizeCUEMessage(msg)
	}

	return ve
}

func cueErrorPath(err error) string {
	positions := cueerrors.Positions(err)
	path := cueerrors.Path(err)
	if len(path) > 0 {
		parts := make([]string, len(path))
		for i, sel := range path {
			parts[i] = string(sel)
		}
		return "." + strings.Join(parts, ".")
	}
	if len(positions) > 0 {
		return positions[0].String()
	}
	return "."
}

// extractConflict pulls the constraint from a "conflicting values X and Y" message.
func extractConflict(msg string) string {
	// CUE format: "conflicting values X and Y"
	if idx := strings.Index(msg, "conflicting values"); idx >= 0 {
		line := msg[idx:]
		if nl := strings.IndexByte(line, '\n'); nl > 0 {
			line = line[:nl]
		}
		return line
	}
	return "type mismatch"
}

// sanitizeCUEMessage removes file positions and internal CUE details.
func sanitizeCUEMessage(msg string) string {
	// Remove lines starting with "    " (CUE source locations)
	lines := strings.Split(msg, "\n")
	var clean []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(line, "    ") && strings.Contains(line, ".cue:") {
			continue
		}
		clean = append(clean, trimmed)
	}
	result := strings.Join(clean, "; ")
	if len(result) > 200 {
		result = result[:200] + "..."
	}
	return result
}

// validateCUEValue validates a CUE value and returns translated errors.
func validateCUEValue(v cue.Value, entity string, sentinel error) []*ValidationError {
	if err := v.Validate(cue.Concrete(true), cue.Final()); err != nil {
		return translateCUEErrors(err, entity, sentinel)
	}
	return nil
}
