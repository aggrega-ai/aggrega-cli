// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compactform

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
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

	// jsonSchema is set while the body of a `$jsonschema:` escape is compiled.
	// There every mapping in a schema position is a JSON Schema node whose keys
	// are keywords — never an inferred object, never a property list — and an
	// `x-aggrega-*` extension's value is carried as written, as a data keyword's
	// is everywhere. See dispatchMapping, emitEscape and readsAsValue.
	jsonSchema bool
}

// inJSONSchema reports whether ctx compiles the body of a `$jsonschema:`
// escape. A nil ctx compiles compact form.
func (ctx *compileCtx) inJSONSchema() bool {
	return ctx != nil && ctx.jsonSchema
}

// escapeContext is the context an escape body compiles under: the caller's
// library catalog, consumer file and resolution stack, read as JSON Schema.
func escapeContext(ctx *compileCtx) *compileCtx {
	out := &compileCtx{jsonSchema: true}
	if ctx != nil {
		out.libraries = ctx.libraries
		out.consumerFile = ctx.consumerFile
		out.resolutionStack = ctx.resolutionStack
	}
	return out
}

// CompileOption configures a Compile invocation. Options are functional;
// each closure mutates the compile context built up before the root node
// dispatch fires. The pattern lets the public Compile signature stay
// variadic-stable: callers without cross-document expansion needs invoke
// Compile(node) unchanged; callers consuming a SchemaLibrary catalog
// invoke Compile(node, WithSchemaLibraries(...), WithConsumerFile(...)).
type CompileOption func(*compileCtx)

// WithSchemaLibraries supplies the SchemaLibraries map the compactform
// expander consults when resolving cross-document $ref URIs, keyed by
// metadata.name with one version per name — a catalog's selection of its
// SchemaLibraries. Absent this option, cross-document $ref expansion is
// disabled.
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
// byte-identical output across runs, and this is the single home of what
// supplies that.
//
// The walk over a map-typed input node follows the order the author wrote,
// because a parsed node holds its entries as a sequence. The compiled document
// does not retain that order: Schema is a plain Go map, whose iteration order is
// neither the author's nor stable. Byte-identity is supplied by whatever
// serialises the result — both the JSON and the YAML encoder write a Go map's
// keys in key order — so the order a reader sees is key order and never author
// order.
//
// An input whose anchor is aliased inside its own content is refused,
// naming that anchor: it describes an infinite tree rather than a schema,
// so there is nothing to compile. Depth on its own is never a reason for
// this refusal — a legitimately deep schema compiles normally.
//
// Errors are *CompileError values wrapping one of the package
// sentinels (ErrUnknownKeyword / ErrAmbiguousDispatch /
// ErrEscapeContentInvalid / ErrConstraintTypeMismatch /
// ErrReferenceUnresolvable / ErrUnresolvedSchemaLibraryRef /
// ErrCircularRef / ErrHostInLibraryRef). Use errors.Is to match.
func Compile(input *yaml.Node, opts ...CompileOption) (Schema, error) {
	if input == nil {
		return nil, &CompileError{
			Message: "input is nil",
			Err:     ErrEscapeContentInvalid(),
		}
	}

	// The tree arrives from a parse this package did not perform, and a
	// recursive anchor survives a parse untouched — it only bites when
	// something follows the alias links, which is exactly what compileNode
	// does. So the refusal sits here, on the near side of the walk.
	if anchor, found := anchorcycle.Find(input); found {
		return nil, &CompileError{
			Line:    nodeLine(input),
			Message: anchorcycle.Explain(anchor),
			Err:     ErrCircularRef(),
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
//
// It follows alias links without tracking where it has been, so it
// terminates only on a tree whose anchors are free of recursion. Every tree
// that reaches it has been checked with anchorcycle.Find — at Compile for
// the caller's input, and at the escape emitter for a body parsed out of a
// block scalar, which is a tree Compile never saw. The check is not repeated
// here, because a guarantee that has one home cannot drift out of agreement
// with itself.
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
			Err:     ErrEscapeContentInvalid(),
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
