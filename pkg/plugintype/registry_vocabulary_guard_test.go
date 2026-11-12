// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package plugintype_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// guardModuleRoot is the aggrega-cli module root relative to this package
// directory (pkg/plugintype). The source scans below read the vocabulary-bearing
// files by module-root-relative path rather than importing them, so the guard
// inspects how each surface is spelled, not merely the value it resolves to.
const guardModuleRoot = "../.."

// TestPluginTypeVocabularySurfacesMatchRegistry pins the plugin-type vocabulary
// surfaces to the pkg/plugintype registry, so adding a plugin type, removing one,
// or giving one a workspace socket fails loudly in exactly one place instead of
// drifting silently across the code base.
//
// Four surfaces re-express the plugin-type vocabulary; this guard covers two of
// them, and the sibling schema guard covers the other two:
//
//   - (a) the describe-layer PluginType* consts in internal/wasm/describe.go —
//     covered here (subtest describe_consts).
//   - (d) the workspace Socket member set in pkg/workspaceconfig/socket.go plus
//     the sockets Config.PluginSockets() enumerates — covered here (subtest
//     workspace_sockets).
//   - (b) the entity schema's metadata.pluginType enum and (c) the wire
//     descriptor schema's plugin_type enum — covered by
//     TestSchemaPluginTypeEnumsMatchRegistry, which composes with this guard
//     rather than duplicating it. Together the two tests close the loop: every
//     surface that names the plugin-type or socket vocabulary derives from the
//     one registry, and none re-spells a literal that could drift.
//
// The registry draws two distinct sets, and the guard encodes the asymmetry
// precisely:
//
//   - The type set is the Type of every registry entry. The describe consts
//     express exactly this set — one PluginType* const per entry, each assigned
//     a plugintype.* value rather than a re-spelled literal.
//   - The socket-attached subset is the entries whose Socket is non-empty. It is
//     a proper subset: a type that attaches by another mechanism — a catalog
//     pipeline step, an ambient capability, a runtime-installed command — carries
//     no socket name and is absent from it. The workspace Socket set expresses
//     exactly this subset.
func TestPluginTypeVocabularySurfacesMatchRegistry(t *testing.T) {
	// pluginTypeConstStrings maps each registry plugin-type const identifier to
	// its canonical string, read from the registry's own source. It bridges the
	// identifier a surface references (plugintype.Provider) to the canonical
	// string the registry enumerates ("provider"), so the source scans below can
	// be compared against the runtime registry set.
	identToString := pluginTypeConstStrings(t)

	t.Run("describe_consts", func(t *testing.T) {
		// Surface (a): every PluginType* const in describe.go must reference a
		// plugintype.* value (never a re-spelled string literal), and the set of
		// referenced types must equal the registry's type set exactly.
		refs := describePluginTypeConstRefs(t)

		var got []string
		for constName, selIdent := range refs {
			canonical, ok := identToString[selIdent]
			if !ok {
				t.Errorf("internal/wasm/describe.go const %s references plugintype.%s, "+
					"which is not a plugin-type const in the pkg/plugintype registry; "+
					"reconcile describe.go against the registry", constName, selIdent)
				continue
			}
			got = append(got, canonical)
		}
		sort.Strings(got)

		want := registryTypeStrings()
		if !equalStringSlices(got, want) {
			t.Errorf("internal/wasm/describe.go PluginType* consts = %v; want %v "+
				"(the plugin types the registry declares)\n"+
				"the describe consts and the pkg/plugintype registry disagree; reconcile "+
				"describe.go against the registry — every registry type appears exactly "+
				"once as a plugintype.* reference, and no const names a type the registry "+
				"does not carry", got, want)
		}
	})

	t.Run("workspace_sockets", func(t *testing.T) {
		// Surface (d) part 1: the sockets Config.PluginSockets() enumerates at
		// runtime must equal the registry's socket-attached subset exactly. This
		// also catches PluginSockets() silently omitting a socket, since it no
		// longer iterates the registry.
		var enumerated []string
		for _, ps := range (workspaceconfig.Config{}).PluginSockets() {
			enumerated = append(enumerated, string(ps.Socket))
		}
		sort.Strings(enumerated)

		wantSockets := socketAttachedRegistrySocketNames()
		if !equalStringSlices(enumerated, wantSockets) {
			t.Errorf("workspaceconfig.Config{}.PluginSockets() sockets = %v; want %v "+
				"(the registry's socket-attached subset)\n"+
				"PluginSockets() and the pkg/plugintype registry disagree; reconcile "+
				"PluginSockets() against the registry — it must enumerate exactly the "+
				"entries whose Socket is set", enumerated, wantSockets)
		}

		// Surface (d) part 2: every Socket* var in socket.go must derive its name
		// from socketName(plugintype.*) (never a re-spelled socket-name literal),
		// and the set of plugin types those vars name must equal the registry's
		// socket-attached subset exactly.
		refs := socketVarPluginTypeRefs(t)

		var got []string
		for varName, selIdent := range refs {
			canonical, ok := identToString[selIdent]
			if !ok {
				t.Errorf("pkg/workspaceconfig/socket.go var %s references plugintype.%s, "+
					"which is not a plugin-type const in the pkg/plugintype registry; "+
					"reconcile socket.go against the registry", varName, selIdent)
				continue
			}
			got = append(got, canonical)
		}
		sort.Strings(got)

		wantTypes := socketAttachedRegistryTypeStrings()
		if !equalStringSlices(got, wantTypes) {
			t.Errorf("pkg/workspaceconfig/socket.go Socket* vars name plugin types %v; want %v "+
				"(the plugin types of the registry's socket-attached subset)\n"+
				"the socket vars and the pkg/plugintype registry disagree; reconcile "+
				"socket.go against the registry — every socket-attached type has exactly one "+
				"Socket* var, each spelled socketName(plugintype.<Type>)", got, wantTypes)
		}
	})
}

// socketAttachedRegistrySocketNames returns the socket names of every registry
// entry that attaches at a workspace socket, sorted for order-independent
// comparison. An entry that attaches by another mechanism carries no socket name
// and is absent from the result.
func socketAttachedRegistrySocketNames() []string {
	var out []string
	for _, e := range plugintype.Entries() {
		if e.Socket == "" {
			continue
		}
		out = append(out, e.Socket)
	}
	sort.Strings(out)
	return out
}

// socketAttachedRegistryTypeStrings returns the canonical plugin-type strings of
// every registry entry that attaches at a workspace socket, sorted. It is the
// type-side projection of the socket-attached subset — the types a Socket* var
// must name — as distinct from the socket-name projection above.
func socketAttachedRegistryTypeStrings() []string {
	var out []string
	for _, e := range plugintype.Entries() {
		if e.Socket == "" {
			continue
		}
		out = append(out, string(e.Type))
	}
	sort.Strings(out)
	return out
}

// pluginTypeConstStrings parses the registry source and returns a map from each
// plugin-type const identifier (e.g. Provider) to its canonical string (e.g.
// "provider"). It reads only the const specs whose declared type is Type, so the
// map holds exactly the plugin-type vocabulary and none of the registry's other
// string-valued consts.
func pluginTypeConstStrings(t *testing.T) map[string]string {
	t.Helper()
	file := parseGuardGoFile(t, "pkg/plugintype/plugintype.go")
	out := map[string]string{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			typeIdent, ok := vs.Type.(*ast.Ident)
			if !ok || typeIdent.Name != "Type" {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote plugin-type const %s value %q: %v", name.Name, lit.Value, err)
				}
				out[name.Name] = s
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("pkg/plugintype/plugintype.go: found no Type-typed string consts; the guard cannot bridge identifiers to canonical strings")
	}
	return out
}

// describePluginTypeConstRefs parses internal/wasm/describe.go and returns, keyed
// by const name, the plugintype selector each PluginType* const references (e.g.
// PluginTypeProvider -> "Provider"). A PluginType* const whose right-hand side is
// not a plugintype.* selector — a re-spelled string literal, say — fails the test
// here, which is the regression guard that keeps the describe vocabulary single-
// sourced against the registry.
func describePluginTypeConstRefs(t *testing.T) map[string]string {
	t.Helper()
	file := parseGuardGoFile(t, "internal/wasm/describe.go")
	out := map[string]string{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "PluginType") {
					continue
				}
				if i >= len(vs.Values) {
					t.Errorf("internal/wasm/describe.go const %s has no right-hand side; "+
						"it must be assigned a plugintype.* value", name.Name)
					continue
				}
				ident := pluginTypeSelectorIdent(vs.Values[i])
				if ident == "" {
					t.Errorf("internal/wasm/describe.go const %s is not assigned a plugintype.* value "+
						"(a re-spelled literal breaks single-sourcing); reconcile it against the "+
						"pkg/plugintype registry", name.Name)
					continue
				}
				out[name.Name] = ident
			}
		}
	}
	return out
}

// socketVarPluginTypeRefs parses pkg/workspaceconfig/socket.go and returns, keyed
// by var name, the plugintype selector each Socket* var routes through
// socketName (e.g. SocketGovernance -> "Governance"). A Socket* var whose value does
// not call socketName(plugintype.*) — a re-spelled socket-name literal, say —
// fails the test here, which is the regression guard for the socket-name
// vocabulary.
func socketVarPluginTypeRefs(t *testing.T) map[string]string {
	t.Helper()
	file := parseGuardGoFile(t, "pkg/workspaceconfig/socket.go")
	out := map[string]string{}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Socket") {
					continue
				}
				if i >= len(vs.Values) {
					t.Errorf("pkg/workspaceconfig/socket.go var %s has no right-hand side; "+
						"it must derive its name from socketName(plugintype.*)", name.Name)
					continue
				}
				ident := socketNamePluginTypeArg(vs.Values[i])
				if ident == "" {
					t.Errorf("pkg/workspaceconfig/socket.go var %s does not derive its name from "+
						"socketName(plugintype.*) (a re-spelled literal breaks single-sourcing); "+
						"reconcile it against the pkg/plugintype registry", name.Name)
					continue
				}
				out[name.Name] = ident
			}
		}
	}
	return out
}

// pluginTypeSelectorIdent returns the selector name of a plugintype.<Name>
// expression, or "" if expr is not such a selector. It is how the guard asserts a
// surface references the registry vocabulary rather than re-spelling a literal.
func pluginTypeSelectorIdent(expr ast.Expr) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "plugintype" {
		return ""
	}
	return sel.Sel.Name
}

// socketNamePluginTypeArg walks expr for a socketName(plugintype.<Name>) call and
// returns the selector name, or "" if no such call is present. It matches the
// registry-sourced spelling the socket vars use — Socket(socketName(plugintype.X))
// — so a bare socket-name literal is rejected.
func socketNamePluginTypeArg(expr ast.Expr) string {
	var found string
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := call.Fun.(*ast.Ident)
		if !ok || fn.Name != "socketName" || len(call.Args) != 1 {
			return true
		}
		if ident := pluginTypeSelectorIdent(call.Args[0]); ident != "" {
			found = ident
			return false
		}
		return true
	})
	return found
}

// parseGuardGoFile parses a Go source file addressed by its module-root-relative
// slash path. It fails the test if the file is absent or unparseable, localizing
// a moved or renamed vocabulary surface to this guard rather than a silent skip.
func parseGuardGoFile(t *testing.T, relPath string) *ast.File {
	t.Helper()
	path := filepath.Join(guardModuleRoot, filepath.FromSlash(relPath))
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	return file
}
