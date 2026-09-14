// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrTemplateNotFound fires when a `$instantiate:` reference names a template
// that is not declared in the library's `spec.templates` map.
func ErrTemplateNotFound() error { return errTemplateNotFound }

var errTemplateNotFound = cerrors.Register("template-not-found", cerrors.Meta{
	ShortCode: "E0194",
	Message:   "schemalibrary: template not found",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrTemplateParamMissing fires when a `$instantiate:` `$args:` binding
// omits a parameter the template declares as required (no default).
func ErrTemplateParamMissing() error { return errTemplateParamMissing }

var errTemplateParamMissing = cerrors.Register("template-param-missing", cerrors.Meta{
	ShortCode: "E0195",
	Message:   "schemalibrary: template parameter missing",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrTemplateParamTypeMismatch fires when a `$instantiate:` `$args:` binding
// supplies a value whose type does not match the parameter's declared type.
func ErrTemplateParamTypeMismatch() error { return errTemplateParamTypeMismatch }

var errTemplateParamTypeMismatch = cerrors.Register("template-param-type-mismatch", cerrors.Meta{
	ShortCode: "E0196",
	Message:   "schemalibrary: template parameter type mismatch",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrForbiddenKeyword fires when a schema entry uses a JSON Schema keyword
// forbidden by default in SchemaLibrary content. Currently: $dynamicAnchor,
// $dynamicRef.
func ErrForbiddenKeyword() error { return errForbiddenKeyword }

var errForbiddenKeyword = cerrors.Register("forbidden-keyword", cerrors.Meta{
	ShortCode: "E0190",
	Message:   "schemalibrary: forbidden JSON Schema keyword",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// InstantiateTemplate substitutes the named template's body with the
// provided arguments and returns the resulting yaml.Node ready for the
// compact-form compiler's downstream processing. Substitution semantics:
//
//   - Literal scalar substitution only. `$<paramName>` scalar nodes in the
//     template body get replaced with the corresponding argument's literal
//     value. No Go templating, no conditionals, no loops, no expression
//     evaluation.
//   - Missing required parameters (declared without a default) produce
//     ErrTemplateParamMissing with the parameter name + the instantiation's
//     reference line.
//   - Type-mismatched arguments (parameter declared `type: integer`, argument
//     supplied as a string) produce ErrTemplateParamTypeMismatch.
//   - Forbidden keywords inside the substituted body (`$dynamicAnchor` /
//     `$dynamicRef`) produce ErrForbiddenKeyword. The post-substitution
//     check fires on the resolved body so authors can't bypass the rejection
//     by parameterizing the keyword.
//
// The substituted body is a deep copy of the template body; the template
// itself is not mutated. The substitution is single-pass — substituted
// values are inserted verbatim and not re-scanned for further substitution
// markers.
func InstantiateTemplate(lib *SchemaLibrary, templateName string, args map[string]any) (yaml.Node, error) {
	if lib == nil {
		return yaml.Node{}, fmt.Errorf("%w: nil library", ErrTemplateNotFound())
	}
	tmpl, ok := lib.Spec.Templates.Get(templateName)
	if !ok {
		return yaml.Node{}, fmt.Errorf("%w: %q not declared in spec.templates", ErrTemplateNotFound(), templateName)
	}

	// Resolve arguments + apply defaults; type-check.
	resolved := map[string]any{}
	for paramName, decl := range tmpl.Params.All() {
		argVal, supplied := args[paramName]
		if !supplied {
			if decl.Default != nil {
				resolved[paramName] = decl.Default
				continue
			}
			return yaml.Node{}, fmt.Errorf("%w: %q (template %q)", ErrTemplateParamMissing(), paramName, templateName)
		}
		if err := typecheckParam(paramName, decl, argVal); err != nil {
			return yaml.Node{}, fmt.Errorf("%w: %v", ErrTemplateParamTypeMismatch(), err)
		}
		resolved[paramName] = argVal
	}

	// Reject `$args:` keys that name no declared parameter — surface as a
	// type-mismatch-class error rather than silently ignore.
	for argName := range args {
		if _, declared := tmpl.Params.Get(argName); !declared {
			return yaml.Node{}, fmt.Errorf("%w: argument %q is not declared in template %q's $params", ErrTemplateParamTypeMismatch(), argName, templateName)
		}
	}

	body := deepCopyNode(&tmpl.Body)
	if err := substituteParams(body, resolved); err != nil {
		return yaml.Node{}, err
	}

	if err := scanForbiddenKeywords(body); err != nil {
		return yaml.Node{}, err
	}

	return *body, nil
}

// typecheckParam verifies the supplied argument value matches the parameter
// declaration's type. The supported types map to JSON Schema 2020-12's
// primitive types: string, integer, number, boolean. Object / array types
// are deferred — `$args:` literal scalars only, per the substitution
// semantic. Future relaxation lands in a future changes if needed.
func typecheckParam(paramName string, decl ParamDecl, argVal any) error {
	switch decl.Type {
	case "string":
		if _, ok := argVal.(string); !ok {
			return fmt.Errorf("%q expected string, got %T", paramName, argVal)
		}
	case "integer":
		switch argVal.(type) {
		case int, int32, int64:
			// OK
		default:
			return fmt.Errorf("%q expected integer, got %T", paramName, argVal)
		}
	case "number":
		switch argVal.(type) {
		case float32, float64, int, int32, int64:
			// OK
		default:
			return fmt.Errorf("%q expected number, got %T", paramName, argVal)
		}
	case "boolean":
		if _, ok := argVal.(bool); !ok {
			return fmt.Errorf("%q expected boolean, got %T", paramName, argVal)
		}
	case "":
		// Untyped parameter — accept any literal scalar. Authors who want
		// type-checked parameters declare an explicit type.
	default:
		return fmt.Errorf("%q has unsupported type %q (string/integer/number/boolean/<empty>)", paramName, decl.Type)
	}
	return nil
}

// substituteParams walks a yaml.Node tree in-place, replacing scalar values
// that match the `$<paramName>` placeholder pattern with the corresponding
// resolved argument value's literal representation. Other nodes pass
// through unchanged.
func substituteParams(node *yaml.Node, params map[string]any) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.HasPrefix(node.Value, "$") {
			paramName := strings.TrimPrefix(node.Value, "$")
			if val, ok := params[paramName]; ok {
				node.Value = fmt.Sprintf("%v", val)
				node.Tag = scalarTagFor(val)
			}
		}
	case yaml.MappingNode, yaml.SequenceNode, yaml.DocumentNode:
		for _, child := range node.Content {
			if err := substituteParams(child, params); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		// Aliases are not substitution targets.
	}
	return nil
}

// scalarTagFor returns the YAML tag string for a substituted scalar value.
// Drives the emitter to render integers as integers, booleans as booleans,
// etc., rather than as quoted strings.
func scalarTagFor(v any) string {
	switch v.(type) {
	case int, int32, int64:
		return "!!int"
	case float32, float64:
		return "!!float"
	case bool:
		return "!!bool"
	default:
		return "!!str"
	}
}

// scanForbiddenKeywords walks the substituted body and rejects nodes that
// use `$dynamicAnchor` or `$dynamicRef`. The check runs post-substitution
// so authors can't sneak the keywords in via parameterization.
func scanForbiddenKeywords(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Value == "$dynamicAnchor" || key.Value == "$dynamicRef" {
				return fmt.Errorf("%w: %q at line %d", ErrForbiddenKeyword(), key.Value, key.Line)
			}
		}
	}
	for _, child := range node.Content {
		if err := scanForbiddenKeywords(child); err != nil {
			return err
		}
	}
	return nil
}

// deepCopyNode produces a deep clone of a yaml.Node so substitution can
// mutate the copy without touching the template's original body.
func deepCopyNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	out := *node
	if len(node.Content) > 0 {
		out.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			out.Content[i] = deepCopyNode(child)
		}
	}
	return &out
}

// ValidateSchemaEntry walks a schema entry's content and rejects forbidden
// JSON Schema keywords. Run by the parser at SchemaLibrary parse time for
// entries that are NOT `$instantiate:` references (those run the post-
// substitution scan inside InstantiateTemplate).
func ValidateSchemaEntry(entry *SchemaEntry) error {
	if entry == nil || entry.Content.Kind == 0 {
		return nil
	}
	return scanForbiddenKeywords(&entry.Content)
}

// ValidateTemplateBody walks a template entry's body and rejects forbidden
// JSON Schema keywords. Run by the parser at SchemaLibrary parse time so
// templates with literal `$dynamicAnchor` / `$dynamicRef` in their bodies
// fail fast at workspace load rather than at first instantiation. The
// post-substitution scan inside InstantiateTemplate catches the
// parametric-introduction case (a parameter value containing the
// forbidden keyword); together the two scans close both static and
// parametric introduction routes.
func ValidateTemplateBody(tmpl *TemplateEntry) error {
	if tmpl == nil || tmpl.Body.Kind == 0 {
		return nil
	}
	return scanForbiddenKeywords(&tmpl.Body)
}
