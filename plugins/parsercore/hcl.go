// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsercore

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ParseHCL reads HCL (HashiCorp Configuration Language) native syntax into a
// canonical JSON tree. An attribute becomes an object member; a block nests under
// its type and then each of its labels, and repeated blocks sharing a type and
// labels collect into an array — so `resource "aws_db_instance" "main" { ... }`
// reads back as tree["resource"]["aws_db_instance"]["main"][0]. An attribute whose
// value is a literal (a string, number, boolean, list, or object of literals)
// encodes as that JSON value; an attribute whose value references a variable or
// calls a function — which cannot be evaluated ahead of time — encodes as the raw
// source text of the expression, so a Terraform interpolation such as
// ${aws_db_instance.main.endpoint} is preserved verbatim and uninterpreted.
//
// Malformed HCL yields error diagnostics (each with a source span) rather than a
// crash; any portion that did parse is still returned as the tree, so a parse
// error does not discard the structure the parser could recover.
func ParseHCL(src []byte) abi.ParseOutput {
	file, diags := hclsyntax.ParseConfig(src, "", hcl.Pos{Line: 1, Column: 1})

	out := abi.ParseOutput{Tree: cloneRaw(nullTree)}
	out.Diagnostics = hclDiagnostics(diags)

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return out
	}
	tree := convertHCLBody(body, src)
	raw, err := marshalDeterministic(tree)
	if err != nil {
		out.Diagnostics = append(out.Diagnostics, abi.Diagnostic{Severity: abi.SeverityHalt, Message: "encode parsed tree: " + err.Error()})
		return out
	}
	out.Tree = raw
	return out
}

// convertHCLBody converts one HCL body — its attributes and nested blocks — into a
// map. Attribute values evaluate as literals where possible and fall back to their
// raw source text otherwise; blocks nest under their type and labels with repeated
// blocks collecting into arrays.
func convertHCLBody(body *hclsyntax.Body, src []byte) map[string]any {
	out := map[string]any{}
	for name, attr := range body.Attributes {
		out[name] = convertHCLExpr(attr.Expr, src)
	}
	for _, block := range body.Blocks {
		nestHCLBlock(out, block.Type, block.Labels, convertHCLBody(block.Body, src))
	}
	return out
}

// convertHCLExpr evaluates an attribute expression as a literal, converting the
// resulting cty value to a JSON-encodable Go value. An expression that cannot be
// evaluated ahead of time — one referencing a variable or calling a function —
// falls back to the raw source text between its start and end offsets, so the
// deterministic pointer is preserved verbatim rather than lost.
func convertHCLExpr(expr hclsyntax.Expression, src []byte) any {
	value, diags := expr.Value(nil)
	if diags.HasErrors() {
		rng := expr.Range()
		return string(src[rng.Start.Byte:rng.End.Byte])
	}
	return ctyToGo(value)
}

// ctyToGo converts a cty value into a JSON-encodable Go value. Objects and maps
// become map[string]any (encoded with sorted keys), lists, tuples, and sets become
// slices, numbers become json.Number so exact digits survive, and strings and
// booleans become their Go scalars. Set iteration is cty's own deterministic
// order, so the emitted tree is byte-identical across runs.
func ctyToGo(value cty.Value) any {
	if value.IsNull() {
		return nil
	}
	ty := value.Type()
	switch {
	case ty == cty.String:
		return value.AsString()
	case ty == cty.Bool:
		return value.True()
	case ty == cty.Number:
		return json.Number(value.AsBigFloat().Text('f', -1))
	case ty.IsListType(), ty.IsTupleType(), ty.IsSetType():
		out := []any{}
		for it := value.ElementIterator(); it.Next(); {
			_, elem := it.Element()
			out = append(out, ctyToGo(elem))
		}
		return out
	case ty.IsMapType(), ty.IsObjectType():
		out := map[string]any{}
		for it := value.ElementIterator(); it.Next(); {
			key, elem := it.Element()
			out[key.AsString()] = ctyToGo(elem)
		}
		return out
	default:
		return nil
	}
}

// nestHCLBlock places a converted block body under its type and labels, appending
// to an array at the leaf so multiple blocks sharing a type and labels are all
// preserved in source order. A block with no labels collects directly under its
// type.
func nestHCLBlock(parent map[string]any, blockType string, labels []string, child map[string]any) {
	if len(labels) == 0 {
		appendHCLBlock(parent, blockType, child)
		return
	}
	level := ensureHCLMap(parent, blockType)
	for _, label := range labels[:len(labels)-1] {
		level = ensureHCLMap(level, label)
	}
	appendHCLBlock(level, labels[len(labels)-1], child)
}

// ensureHCLMap returns the nested map at key, creating it when absent. When key
// already holds a non-map value (an attribute colliding with a block type) a fresh
// map replaces it, so block nesting always descends into a map.
func ensureHCLMap(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	nested := map[string]any{}
	parent[key] = nested
	return nested
}

// appendHCLBlock appends a converted block body to the array at key, creating the
// array on the first block so repeated blocks accumulate in source order.
func appendHCLBlock(parent map[string]any, key string, child map[string]any) {
	if existing, ok := parent[key].([]any); ok {
		parent[key] = append(existing, child)
		return
	}
	parent[key] = []any{child}
}

// hclDiagnostics converts HCL parse diagnostics into the parser wire shape,
// preserving order. A diagnostic's source range becomes a span carrying both the
// byte offsets (the exact location) and the line and column numbers (for
// human-readable messages).
func hclDiagnostics(diags hcl.Diagnostics) []abi.Diagnostic {
	if len(diags) == 0 {
		return nil
	}
	out := make([]abi.Diagnostic, 0, len(diags))
	for _, d := range diags {
		severity := abi.SeverityWarn
		if d.Severity == hcl.DiagError {
			severity = abi.SeverityHalt
		}
		message := d.Summary
		if d.Detail != "" {
			message = d.Summary + ": " + d.Detail
		}
		diag := abi.Diagnostic{Severity: severity, Message: message}
		if d.Subject != nil {
			diag.Location = &abi.Location{Span: &abi.Span{
				StartOffset: d.Subject.Start.Byte,
				EndOffset:   d.Subject.End.Byte,
				StartLine:   d.Subject.Start.Line,
				StartColumn: d.Subject.Start.Column,
				EndLine:     d.Subject.End.Line,
				EndColumn:   d.Subject.End.Column,
			}}
		}
		out = append(out, diag)
	}
	return out
}
