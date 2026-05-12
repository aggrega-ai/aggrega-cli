// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// Schema is the resolved JSON Schema 2020-12 output produced by Compile.
// It is a generic map type so callers can serialise to JSON or YAML
// without further conversion. The validator adapter at
// pkg/validator/jsonschema/ accepts this shape and adapts it to the
// google/jsonschema-go typed surface internally.
type Schema = map[string]any

// compileCtx carries the cross-document expansion state through the
// compactform pipeline: the catalog's SchemaLibraries map for $ref target
// resolution, the consumer file location for error provenance, and the
// in-flight resolution stack for cycle detection. A nil or zero-value ctx
// disables cross-document expansion — references with a document portion
// fall through to the existing reference passthrough, and a $ref against
// any library produces ErrUnresolvedSchemaLibraryRef.
type compileCtx struct {
	libraries       map[string]types.SchemaLibrary
	consumerFile    string
	resolutionStack []string
}

// CompileOption configures a Compile invocation. Options are functional;
// each closure mutates the compile context built up before the root node
// dispatch fires. The pattern lets the public Compile signature stay
// variadic-stable: callers without cross-document expansion needs invoke
// Compile(node) unchanged; callers consuming a SchemaLibrary catalog
// invoke Compile(node, WithSchemaLibraries(...), WithConsumerFile(...)).
type CompileOption func(*compileCtx)

// WithSchemaLibraries supplies the SchemaLibraries map the compactform
// expander consults when resolving cross-document $ref URIs. The map is
// the catalog's Catalog.SchemaLibraries (keyed by metadata.name). Absent
// this option, cross-document $ref expansion is disabled.
func WithSchemaLibraries(libs map[string]types.SchemaLibrary) CompileOption {
	return func(ctx *compileCtx) {
		ctx.libraries = libs
	}
}

// WithConsumerFile records the source file path of the consumer entity
// whose schema is being compiled. The path appears in cross-document $ref
// expansion error messages so callers can locate the offending authoring
// site without further plumbing.
func WithConsumerFile(path string) CompileOption {
	return func(ctx *compileCtx) {
		ctx.consumerFile = path
	}
}

// Compile transforms a compact-form (or JSON Schema YAML, or mixed)
// input yaml.Node into a resolved JSON Schema 2020-12 document. The
// returned schema is ready for the validator pipeline; the compiler
// does not invoke the validator itself.
//
// Compile is a pure function over its input. Same input produces
// byte-identical output across runs. Map-typed input nodes preserve
// author order via yaml.Node's natural sequential representation.
//
// Errors are *CompileError values wrapping one of the package
// sentinels (ErrUnknownKeyword / ErrAmbiguousDispatch /
// ErrEscapeContentInvalid / ErrConstraintTypeMismatch /
// ErrReferenceUnresolvable / ErrUnresolvedSchemaLibraryRef /
// ErrCircularRef / ErrOCINotYetSupported). Use errors.Is to match.
func Compile(input *yaml.Node, opts ...CompileOption) (Schema, error) {
	if input == nil {
		return nil, &CompileError{
			Message: "input is nil",
			Err:     ErrEscapeContentInvalid,
		}
	}

	ctx := &compileCtx{}
	for _, opt := range opts {
		opt(ctx)
	}

	// Unwrap a document node — top-level YAML documents wrap content
	// in a DocumentNode; downstream dispatch operates on the mapping
	// inside.
	node := input
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}

	out, err := compileNode(node, "", ctx)
	if err != nil {
		return nil, err
	}
	if m, ok := out.(Schema); ok {
		return m, nil
	}
	// A scalar at the root is not a valid schema; wrap into a minimal
	// schema if the caller passed (say) `{type: string}` as a flow-map.
	return Schema{"value": out}, nil
}

// compileNode dispatches a single node to the appropriate emission
// pathway based on its content shape. The path argument carries the
// YAML-path-like location for error provenance (e.g.
// "spec.schema.values.port"). The ctx argument carries the cross-document
// expansion state; a nil ctx disables cross-document $ref expansion.
func compileNode(node *yaml.Node, path string, ctx *compileCtx) (any, error) {
	if node == nil {
		return nil, nil
	}

	switch node.Kind {
	case yaml.ScalarNode:
		return scalarValue(node), nil
	case yaml.SequenceNode:
		return emitSequence(node, path, ctx)
	case yaml.MappingNode:
		return dispatchMapping(node, path, ctx)
	case yaml.AliasNode:
		// YAML aliases dereference to their target; treat as the target.
		if node.Alias != nil {
			return compileNode(node.Alias, path, ctx)
		}
		return nil, nil
	default:
		return nil, &CompileError{
			Path:    path,
			Line:    node.Line,
			Message: "unsupported YAML node kind",
			Err:     ErrEscapeContentInvalid,
		}
	}
}

// scalarValue decodes a yaml.Node scalar into the appropriate Go type
// (string, bool, int, float, nil) per yaml.v3's standard decoding.
func scalarValue(node *yaml.Node) any {
	var v any
	if err := node.Decode(&v); err == nil {
		return v
	}
	return node.Value
}

// emitSequence emits a YAML sequence as a Go slice. Each element
// recurses through compileNode for per-entry dispatch.
func emitSequence(node *yaml.Node, path string, ctx *compileCtx) ([]any, error) {
	out := make([]any, 0, len(node.Content))
	for i, child := range node.Content {
		childPath := pathJoin(path, indexSegment(i))
		v, err := compileNode(child, childPath, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// pathJoin concatenates path segments with dots. Used for error
// provenance only — not consumed by the validator pipeline.
func pathJoin(base, segment string) string {
	if base == "" {
		return segment
	}
	return base + "." + segment
}

// indexSegment formats an array index for path provenance.
func indexSegment(i int) string {
	switch i {
	case 0:
		return "[0]"
	case 1:
		return "[1]"
	default:
		// Fallback: rare-enough that allocating is acceptable.
		return "[" + intToString(i) + "]"
	}
}

// intToString formats a positive int for path-provenance segments.
// indexSegment handles indices 0 and 1 directly and only delegates
// here for i >= 2, so the zero-and-negative branches are unreachable
// and intentionally omitted.
func intToString(i int) string {
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
