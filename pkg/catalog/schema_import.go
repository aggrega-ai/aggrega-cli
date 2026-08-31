// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// schemaImportKey is the authored key that replaces a schema surface's whole
// content with the content of a file beside the entity that declared it.
const schemaImportKey = "$import"

// schemaPillars names the three data-pillar keys a spec.schema map may carry.
// The import resolution reaches each pillar body as well as the schema root, so
// an author may import the whole contract or one pillar of it.
var schemaPillars = [...]string{"values", "infra", "provides"}

// resolveEntitySchemaImports substitutes the content of every authored
// `$import:` on one entity's schema surfaces, at catalog load, so every later
// reader sees the contract the author imported rather than an unread pointer.
//
// It is called from the shared per-entity parse, which is the one site both the
// eager and the lazy catalog backing route through — so the substitution happens
// once per entity and serves every consumer of that entity's schema (the pillar
// validator, the conformance compile, IR defaults materialisation, inspect, lint
// and the editor's language server) with no per-consumer wiring. Resolving in the
// schema compiler instead would give a function documented as pure over its input
// filesystem access, and resolving per consumer is what leaves one consumer
// reading an unresolved contract.
//
// The surfaces reached are the entity's `spec.schema` — its root and each pillar
// body — and each `usesSchema` socket's `valueSchema` and `infraSchema`, which
// carry bare pillar content and so have a root only. Reaching further down is
// deliberately not done: below a pillar every mapping is a property's own schema,
// and the import vocabulary replaces whole content rather than splicing fragments.
func resolveEntitySchemaImports(entityPath string, schema *yaml.Node, sockets *types.OrderedMap[types.UsesSchemaEntry]) error {
	if err := resolveSchemaSurfaceImports(entityPath, schema, "spec.schema", true); err != nil {
		return err
	}
	if sockets == nil {
		return nil
	}
	for _, name := range sockets.Keys() {
		entry, _ := sockets.Get(name)
		if err := resolveSchemaSurfaceImports(
			entityPath, &entry.ValueSchema, "spec.usesSchema."+name+".valueSchema", false,
		); err != nil {
			return err
		}
		if err := resolveSchemaSurfaceImports(
			entityPath, &entry.InfraSchema, "spec.usesSchema."+name+".infraSchema", false,
		); err != nil {
			return err
		}
		sockets.Set(name, entry)
	}
	return nil
}

// resolveSchemaSurfaceImports resolves one schema-bearing node: the node itself,
// and — when withPillars is set, which is the entity-level `spec.schema` shape —
// each of its three pillar bodies.
//
// A node that is not a mapping is left alone: an absent schema carries no node at
// all, and the inheritance marker a Component declaring `extends` writes is a
// scalar. Neither can carry an import, and neither is this function's business to
// reject — the pillar validator owns what a schema surface may be.
func resolveSchemaSurfaceImports(entityPath string, node *yaml.Node, where string, withPillars bool) error {
	if node == nil || node.Kind == 0 {
		return nil
	}
	if err := substituteSchemaImport(entityPath, node, where, nil); err != nil {
		return err
	}
	if !withPillars {
		return nil
	}
	target := node
	if target.Kind == yaml.DocumentNode && len(target.Content) > 0 {
		target = target.Content[0]
	}
	if target.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(target.Content); i += 2 {
		key := target.Content[i]
		if key.Kind != yaml.ScalarNode || !isSchemaPillar(key.Value) {
			continue
		}
		if err := substituteSchemaImport(
			entityPath, target.Content[i+1], where+"."+key.Value, nil,
		); err != nil {
			return err
		}
	}
	return nil
}

// isSchemaPillar reports whether a key at a spec.schema root names one of the
// three data pillars. The import resolution consults it so it descends only into
// pillar bodies; a key naming anything else is the pillar validator's to refuse,
// and treating it as a schema surface here would resolve an import under a key
// the entity is not allowed to declare.
func isSchemaPillar(key string) bool {
	for _, p := range schemaPillars {
		if key == p {
			return true
		}
	}
	return false
}

// substituteSchemaImport replaces node with the parsed content of the file it
// imports, and repeats for as long as the imported content is itself an import.
// A node that declares no import is left exactly as authored.
//
// The chain is guarded by resolution stack: an import that reaches a file already
// in flight halts naming the chain, rather than recursing until the process dies.
func substituteSchemaImport(entityPath string, node *yaml.Node, where string, stack []string) error {
	target, declared, err := schemaImportTarget(node, where, entityPath)
	if err != nil {
		return err
	}
	if !declared {
		return nil
	}

	resolved, err := resolveAgainstEntityDir(entityPath, target)
	if err != nil {
		return schemaImportError(entityPath, node,
			where+": resolve imported schema path "+quoteTarget(target)+": "+err.Error())
	}
	if chain, cyclic := refcycle.Check(stack, resolved); cyclic {
		return cerrors.Wrap(
			refcycle.ErrCycle(),
			cerrors.CatalogError,
			cerrors.SourceLocation{File: entityPath, Line: nodeLineOf(node)},
			where+": circular schema import: "+strings.Join(chain, " -> "),
		)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return schemaImportError(entityPath, node,
			where+": read imported schema "+quoteTarget(target)+": "+err.Error())
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return schemaImportError(entityPath, node,
			where+": parse imported schema "+quoteTarget(target)+": "+err.Error())
	}
	content := &doc
	if content.Kind == yaml.DocumentNode && len(content.Content) > 0 {
		content = content.Content[0]
	}
	if content.Kind != yaml.MappingNode {
		return schemaImportError(entityPath, node,
			where+": imported schema "+quoteTarget(target)+
				" must be a YAML map of schema content; it is not")
	}

	*node = *content
	return substituteSchemaImport(entityPath, node, where, refcycle.Push(stack, resolved))
}

// schemaImportTarget reads the `$import:` directive off a schema-bearing node.
// It reports (target, true, nil) for a well-formed one, (_, false, nil) for a node
// that declares none, and an error for a node that declares one badly.
//
// The accepted shape is the one the entity contract documents: a map of exactly
// one key, `$import`, whose value is a non-empty string naming a file. Two other
// spellings are named rather than treated as paths, because both are reference
// forms an author may reasonably reach for here and a missing-file report would
// hide which mistake they made: a value carrying a registry host addresses a
// library by location, and a bare-name library identity is what `$ref` resolves.
func schemaImportTarget(node *yaml.Node, where, entityPath string) (string, bool, error) {
	if node == nil {
		return "", false, nil
	}
	target := node
	if target.Kind == yaml.DocumentNode && len(target.Content) > 0 {
		target = target.Content[0]
	}
	if target.Kind != yaml.MappingNode {
		return "", false, nil
	}

	var value *yaml.Node
	for i := 0; i+1 < len(target.Content); i += 2 {
		k := target.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == schemaImportKey {
			value = target.Content[i+1]
			break
		}
	}
	if value == nil {
		return "", false, nil
	}
	if len(target.Content) != 2 {
		return "", false, schemaImportError(entityPath, target,
			where+": `$import:` must be the only key when a schema is imported from a file")
	}
	if value.Kind != yaml.ScalarNode || strings.TrimSpace(value.Value) == "" {
		return "", false, schemaImportError(entityPath, value,
			where+": `$import:` value must be a path to a file")
	}
	authored := strings.TrimSpace(value.Value)
	if strings.HasPrefix(authored, "oci://") {
		return "", false, schemaImportError(entityPath, value,
			where+": `$import:` takes a file path beside this entity, not a registry location "+
				quoteTarget(authored)+"; reference a SchemaLibrary entry with `$ref: <Library>#/schemas/<Entry>`")
	}
	if strings.Contains(authored, "#/schemas/") {
		return "", false, schemaImportError(entityPath, value,
			where+": `$import:` takes a file path beside this entity; reference the SchemaLibrary entry "+
				quoteTarget(authored)+" with `$ref:` instead")
	}
	return authored, true, nil
}

// quoteTarget renders an authored import target for a diagnostic.
func quoteTarget(target string) string { return "\"" + target + "\"" }

// ErrUnusableSchemaImport fires when an authored `$import:` on a schema surface
// cannot be applied, so the surface would be left holding an unread pointer
// instead of the contract the author meant to import.
//
// It covers every way the directive fails to yield content, because each of them
// leaves the same surface unusable and gives the author the same thing to do —
// correct the directive or the file it names. The condition's sentence therefore
// states only that the import could not be applied, and each raise path supplies
// its own clause saying which way: a directive sharing its mapping with another
// key, a value that is not a file path, a value that addresses a registry or a
// library entry rather than a file, a path that does not resolve, a file that
// cannot be read or parsed, and content that is not a map of schema content.
// Registering one sentence per way would put advice that is true at one raise
// path into a sentence that leads at all seven.
//
// Category: CATALOG_ERROR — the fault is in an authored catalog document's
// schema surface, or in the file beside it that the surface names. Match with
// errors.Is(err, ErrUnusableSchemaImport()).
func ErrUnusableSchemaImport() error { return errUnusableSchemaImport }

var errUnusableSchemaImport = cerrors.Register("catalog/unusable-schema-import", cerrors.Meta{
	ShortCode: "E0266",
	Message:   "a schema `$import:` could not be applied",
})

// schemaImportError shapes one import failure as the catalog-load diagnostic the
// rest of this package produces, located at the authored node.
//
// It composes through the condition's registered sentence rather than emitting
// message alone, so the diagnostic opens with the sentence and carries the raise
// path's own clause after it. Leading with the sentence is structural rather than
// stylistic: the chain walk that renders a halt prints the outermost frame whose
// text the output does not already carry, so a message that did not lead with it
// would print the sentence a second time as a redundant continuation line.
func schemaImportError(entityPath string, node *yaml.Node, message string) error {
	return cerrors.Wrap(
		ErrUnusableSchemaImport(),
		cerrors.CatalogError,
		cerrors.SourceLocation{File: entityPath, Line: nodeLineOf(node)},
		cerrors.ComposeMessage(ErrUnusableSchemaImport(), nil, message),
	)
}

// nodeLineOf reads a node's source line for a diagnostic, answering zero for a
// node that is absent — the shape a caller reporting on a surface the entity did
// not author would otherwise have to test for at every site.
func nodeLineOf(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	return node.Line
}
