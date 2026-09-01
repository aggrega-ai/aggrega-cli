// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// manifestRelPath is the declarative embedded-plugin manifest, relative to the
// module root. It names which guests ship inside the binary, so the provider set
// is read from a declaration rather than from a directory listing that would
// silently pick up an unshipped experiment.
const manifestRelPath = "internal/wasm/embedded/embedded-plugins.tsv"

// providerSocket is the manifest's socket value for a provider-rendering guest.
// Rows for other sockets describe guests that render nothing and declare no
// input kinds.
const providerSocket = "provider"

// contractTypeName is the descriptor field type whose literal carries a guest's
// render-behavior declaration. It is matched on the selector's last segment so
// the extraction does not depend on the alias a guest imports the plugin
// development kit under.
const contractTypeName = "ProviderContract"

// provider is one provider guest's published capability claim: the name it is
// reachable under, and the parts of its render-behavior declaration the grid
// projects.
type provider struct {
	// Name is the provider name a Recipe targets.
	Name string
	// PluginInputKinds carries the Recipe input kinds exactly as the guest declares
	// them. It is EMBEDDED rather than restated as booleans of its own, so this
	// projection cannot fall behind the one home the claim has: a kind added there
	// appears here with no edit. The fields promote, so a reader still writes
	// p.Sourced.
	types.PluginInputKinds
	// DelimLeft and DelimRight are the template delimiter pair the guest applies
	// to imported artifact files, empty when it takes the default pair.
	DelimLeft  string
	DelimRight string
}

// AcceptsAlongsideArtifacts reports whether a Recipe targeting this provider may
// carry artifacts riding alongside a source or workload primary.
//
// It is derived rather than declared, and the derivation belongs to the
// input-kind gate rather than to this projection: the gate classifies a node by
// source, then workload, then artifacts, so artifacts declared alongside a
// primary are carried by that primary and are never matched against the
// standalone claim. A provider can therefore carry alongside artifacts exactly
// when it can render a primary for them to ride with.
//
// Kept here rather than unified with the gate because the two answer different
// questions — the gate admits one node against one provider at render time,
// this projects every provider offline — but the gate is the rule's home, and a
// change to its classification order lands here too.
func (p provider) AcceptsAlongsideArtifacts() bool {
	return p.Sourced || p.Synthesized
}

// scan reads the embedded-plugin manifest under root and extracts the
// render-behavior declaration of every provider guest it names, in manifest
// order.
//
// Manifest order is author order and is preserved rather than sorted, so the
// published grid's row order is a property of the declaration and moves only
// when the declaration moves.
func scan(root string) ([]provider, error) {
	rows, err := providerRows(filepath.Join(root, manifestRelPath))
	if err != nil {
		return nil, err
	}

	out := make([]provider, 0, len(rows))
	for _, r := range rows {
		p, err := contractOf(filepath.Join(root, filepath.FromSlash(r.sourcePath)))
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", r.name, err)
		}
		p.Name = r.name
		out = append(out, p)
	}
	return out, nil
}

// manifestRow is one provider row of the embedded-plugin manifest.
type manifestRow struct {
	name       string
	sourcePath string
}

// providerRows parses the tab-separated embedded-plugin manifest and returns its
// provider rows.
//
// This reads the same manifest the embedded-plugin registry reads, and the
// duplication is deliberate rather than an oversight: that reader is unexported
// and keeps only the socket and name, discarding the guest source path this
// generator needs to reach a declaration. The manifest's own header records that
// it is read by several consumers by design — the registry, the guest build, the
// freshness check and the drift guard — which is what makes one more reader
// correct here. Do not "unify" these by widening the registry's parser: its
// panic-on-malformed contract is right for a compile-time embed and wrong for a
// generator that must report a bad row as an error.
func providerRows(path string) ([]manifestRow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin manifest: %w", err)
	}

	var out []manifestRow
	for n, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimRight(raw, "\r")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			return nil, fmt.Errorf("%s:%d: row has %d fields, need at least 3", path, n+1, len(fields))
		}
		socket := strings.TrimSpace(fields[0])
		if socket != providerSocket {
			continue
		}
		name, sourcePath := strings.TrimSpace(fields[1]), strings.TrimSpace(fields[2])
		if name == "" || sourcePath == "" {
			return nil, fmt.Errorf("%s:%d: provider row missing name or source path", path, n+1)
		}
		out = append(out, manifestRow{name: name, sourcePath: sourcePath})
	}
	return out, nil
}

// contractOf extracts the render-behavior declaration from the guest sources in
// dir.
//
// The guest builds only for the WebAssembly target, so its declaration is read
// from the source text rather than by compiling and calling it. Exactly one
// declaration must be found: none means the descriptor form moved and the
// extraction would otherwise report a provider that accepts nothing, and more
// than one means the guest declares two and this projection cannot know which
// one describes it.
func contractOf(dir string) (provider, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return provider{}, fmt.Errorf("read guest source: %w", err)
	}

	fset := token.NewFileSet()
	var found []provider
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return provider{}, fmt.Errorf("parse %s: %w", path, err)
		}
		found = append(found, contractLiterals(file)...)
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return provider{}, fmt.Errorf("no %s literal in %s; the descriptor form moved", contractTypeName, dir)
	default:
		return provider{}, fmt.Errorf("%d %s literals in %s; a guest declares exactly one",
			len(found), contractTypeName, dir)
	}
}

// contractLiterals collects every render-behavior declaration in one parsed
// file.
func contractLiterals(file *ast.File) []provider {
	var out []provider
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isContractType(lit.Type) {
			return true
		}
		out = append(out, readContract(lit))
		return true
	})
	return out
}

// isContractType reports whether a composite literal's type names the
// render-behavior declaration, under whatever alias the guest imports it.
func isContractType(t ast.Expr) bool {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == contractTypeName
}

// readContract reads the fields the grid projects out of one declaration
// literal. A field the literal omits keeps its zero value, which is what the
// running compiler reads for it too.
func readContract(lit *ast.CompositeLit) provider {
	var p provider

	if kinds, ok := field(lit, "InputKinds").(*ast.CompositeLit); ok {
		readInputKinds(kinds, &p.PluginInputKinds)
	}
	if delims, ok := unwrap(field(lit, "ArtifactDelimiters")).(*ast.CompositeLit); ok {
		p.DelimLeft = stringValue(field(delims, "Left"))
		p.DelimRight = stringValue(field(delims, "Right"))
	}
	return p
}

// readInputKinds reads every input kind the home type declares out of a guest's
// input-kinds literal.
//
// The kinds are enumerated by REFLECTING over the home type rather than by naming
// them, because this reader crosses a boundary no Go type can span: it matches a
// guest's fields as source text, so a field list written here would be one more
// copy of the claim — and the copy would fail silently, reporting a guest as not
// declaring a kind it does declare. Reflecting means a kind added to the home type
// is read here with no edit.
//
// Field order is the home type's declaration order, which reflect preserves, so
// nothing rendered from this depends on map iteration.
func readInputKinds(lit *ast.CompositeLit, out *types.PluginInputKinds) {
	v := reflect.ValueOf(out).Elem()
	for _, f := range reflect.VisibleFields(v.Type()) {
		if f.Type.Kind() != reflect.Bool {
			// The grid projects an input-kind claim as a set of independent boolean
			// claims. A non-boolean kind would need a rendering decision this
			// projection cannot invent, so it halts rather than reporting a zero.
			panic(fmt.Sprintf("input kind %q is %s, not a bool; the grid projects boolean claims only",
				f.Name, f.Type.Kind()))
		}
		v.FieldByIndex(f.Index).SetBool(boolValue(field(lit, f.Name)))
	}
}

// field returns the value a composite literal assigns to the named field, or nil
// when it assigns none.
//
// The literal's elements are walked in source order rather than indexed through
// a map, so nothing this generator renders depends on map iteration order. A
// literal with positional elements yields nil for every name, which surfaces as
// a provider claiming no input kind rather than as a silently wrong row.
func field(lit *ast.CompositeLit, name string) ast.Expr {
	if lit == nil {
		return nil
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == name {
			return kv.Value
		}
	}
	return nil
}

// unwrap strips a leading address-of from a value, so a pointer field declared
// as &T{...} reads the same as T{...}.
func unwrap(e ast.Expr) ast.Expr {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		return u.X
	}
	return e
}

func boolValue(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "true"
}

func stringValue(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}
