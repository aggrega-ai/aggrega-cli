// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginwire

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// The two socket-output decode entry points. There is one per encoding rather
// than one function told the encoding per call site, deliberately: naming the
// wrong one of two is a mistake a test can see, while telling one function the
// wrong encoding is a mistake nothing observes. This guard is that test.
const (
	textDecodeEntryPoint   = "DecodeGuestOutput"
	binaryDecodeEntryPoint = "DecodeGuestOutputCBOR"
)

// decodeSite is one socket-output decode call: where it is, which of the two
// entry points it names, and which exports the enclosing function dispatches.
type decodeSite struct {
	where   string
	binary  bool
	exports []string
}

// TestWireRegistryAgreesWithSocketDecodeEntryPoints is the pin between the two
// halves of one fact.
//
// The registry records, per export, whether that export's wire is binary. The
// call site records the same fact a second time, by naming one of two decode
// entry points. Nothing makes the two agree — the registry is a declaration and
// the call site is code — so a socket moved on one side and not the other
// produces a build that compiles, a suite that passes, and a wire that is
// broken in exactly one direction.
//
// The direction that matters most is the second one below. A socket flipping to
// the binary wire without its registry entry following leaves the
// direct-invocation command handing the guest text it cannot read, which is a
// failure with no red anywhere: the compiler's own path works, because it does
// not consult the registry.
//
// COVERAGE BOUND, stated because an unstated bound reads as full coverage: this
// guard reaches a socket whose output decode routes through one of the two entry
// points above. A socket that decodes through its generated codec directly — the
// provider render socket does — names neither, so its wire fact is pinned by
// nothing here. That is why the registry admits an entry only for a socket this
// guard can see: an entry it cannot see would be exactly the unpinned
// hand-written fact the registry exists to avoid rather than to relocate.
func TestWireRegistryAgreesWithSocketDecodeEntryPoints(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	sites := socketDecodeSites(t, root)
	if len(sites) == 0 {
		t.Fatalf("found no socket-output decode call site under %s; the census is looking in the "+
			"wrong place, and an empty census would pass this guard while proving nothing", root)
	}

	for _, d := range wireDisagreements(binaryExports(), sites) {
		t.Error(d)
	}
}

// TestWireCallSiteDisagreementCanFail proves the comparison above is capable of
// refusing. A guard whose failure mode nobody has demonstrated is decoration,
// and this one's inputs are two censuses that agree today — so the only way to
// show it fires is to feed it a pair that does not.
func TestWireCallSiteDisagreementCanFail(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		binary  map[string]bool
		sites   []decodeSite
		wantSub string
	}{
		{
			name:    "registry says binary, call site decodes text",
			binary:  map[string]bool{"mutate": true},
			sites:   []decodeSite{{where: "a.go:1", binary: false, exports: []string{"mutate"}}},
			wantSub: "decodes with " + textDecodeEntryPoint,
		},
		{
			name:    "registry says text, call site decodes binary",
			binary:  map[string]bool{"verdict_ir": false},
			sites:   []decodeSite{{where: "a.go:1", binary: true, exports: []string{"verdict_ir"}}},
			wantSub: "does not mark",
		},
		{
			name:    "registry marks an export binary and no call site decodes it that way",
			binary:  map[string]bool{"mutate": true},
			sites:   []decodeSite{{where: "a.go:1", binary: false, exports: []string{"verdict_ir"}}},
			wantSub: "no socket-output decode call site",
		},
		{
			name:    "a binary call site whose export nothing resolves",
			binary:  map[string]bool{"mutate": true},
			sites:   []decodeSite{{where: "a.go:1", binary: true, exports: nil}},
			wantSub: "names no attachment role",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := wireDisagreements(tc.binary, tc.sites)
			if len(got) == 0 {
				t.Fatalf("desynchronised input produced no complaint; the guard cannot fail, "+
					"so its green says nothing (binary=%v sites=%v)", tc.binary, tc.sites)
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, tc.wantSub) {
				t.Fatalf("complaint does not name the disagreement: got\n%s\nwant a mention of %q",
					joined, tc.wantSub)
			}
		})
	}
}

// wireDisagreements compares the registry's binary set against the entry point
// each socket-output decode call site names, and returns one complaint per
// disagreement. It is a pure function so a planted disagreement can prove it
// fires; the two censuses feeding it are gathered by the callers above.
//
// It reads in both directions on purpose. Only the second catches a socket that
// moved to the binary wire while its registry entry stayed behind, which is the
// failure the direct-invocation command shows and no other gate does.
func wireDisagreements(binary map[string]bool, sites []decodeSite) []string {
	var out []string

	// Direction one: every export the registry marks binary is decoded through the
	// binary entry point, and never through the text one.
	for _, export := range sortedBinaryExports(binary) {
		var binarySites, textSites []string
		for _, s := range sites {
			if !containsExport(s.exports, export) {
				continue
			}
			if s.binary {
				binarySites = append(binarySites, s.where)
			} else {
				textSites = append(textSites, s.where)
			}
		}
		for _, where := range textSites {
			out = append(out, fmt.Sprintf(
				"the plugin-wire registry marks export %q as carrying the binary wire, but its socket "+
					"call site at %s decodes with %s, the text entry point: the registry and the call "+
					"site disagree about that socket's encoding, and only one of them can be right",
				export, where, textDecodeEntryPoint))
		}
		if len(binarySites) == 0 {
			out = append(out, fmt.Sprintf(
				"the plugin-wire registry marks export %q as carrying the binary wire, but no "+
					"socket-output decode call site decodes it through %s: either the socket has not "+
					"moved and the registry is ahead of it, or the call site is not visible to this guard",
				export, binaryDecodeEntryPoint))
		}
	}

	// Direction two: every call site naming the binary entry point dispatches
	// exports the registry marks binary — and resolves at least one, since a site
	// whose export nothing resolves is a hole rather than a pass.
	for _, s := range sites {
		if !s.binary {
			continue
		}
		if len(s.exports) == 0 {
			out = append(out, fmt.Sprintf(
				"the socket-output decode call site at %s decodes through %s but names no attachment "+
					"role, so this guard cannot tell which export it serves and cannot check it against "+
					"the plugin-wire registry",
				s.where, binaryDecodeEntryPoint))
			continue
		}
		for _, export := range s.exports {
			if !binary[export] {
				out = append(out, fmt.Sprintf(
					"the socket-output decode call site at %s decodes export %q through %s, but the "+
						"plugin-wire registry does not mark that export as carrying the binary wire: a "+
						"socket that moved without its registry entry hands an authored payload to a guest "+
						"in the wrong encoding, and nothing else reports it",
					s.where, export, binaryDecodeEntryPoint))
			}
		}
	}

	sort.Strings(out)
	return out
}

// binaryExports reduces the generated registry to the export-to-encoding fact
// this guard checks. An export appearing more than once contributes one entry;
// a target is one wire, so its exports cannot disagree with themselves.
func binaryExports() map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		if prev, seen := out[e.export]; seen && prev != e.binary {
			// Not reachable from a generated table — the encoding sits on the target
			// and every entry from one target shares it — but left loud rather than
			// silently taking one of the two.
			panic("pluginwire: export " + e.export + " is registered with two different encodings")
		}
		out[e.export] = e.binary
	}
	return out
}

func sortedBinaryExports(binary map[string]bool) []string {
	var out []string
	for export, isBinary := range binary {
		if isBinary {
			out = append(out, export)
		}
	}
	sort.Strings(out)
	return out
}

func containsExport(exports []string, want string) bool {
	for _, e := range exports {
		if e == want {
			return true
		}
	}
	return false
}

// moduleRoot resolves the module root from this package's directory and refuses
// if it does not find one, so a census reading the wrong tree fails loudly
// rather than returning an empty result that reads as clean.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q carries no go.mod; the census is looking in the wrong place: %v", root, err)
	}
	return root
}

// socketDecodeSites walks every non-test Go file in the module and returns one
// entry per socket-output decode call, with the exports the enclosing function
// dispatches.
//
// Test files are excluded: a test drives a decode entry point directly with a
// literal export name and no attachment role, so including them would report a
// hole at every such test rather than at a real call site.
func socketDecodeSites(t *testing.T, root string) []decodeSite {
	t.Helper()
	roleValues := governanceRoleIdents(t, root)

	var sites []decodeSite
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var found []decodeSite
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch calleeName(call.Fun) {
				case binaryDecodeEntryPoint:
					found = append(found, decodeSite{
						where:  fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line),
						binary: true,
					})
				case textDecodeEntryPoint:
					found = append(found, decodeSite{
						where:  fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line),
						binary: false,
					})
				}
				return true
			})
			if len(found) == 0 {
				continue
			}
			exports := dispatchedExports(fn, file, roleValues)
			for i := range found {
				found[i].exports = exports
			}
			sites = append(sites, found...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module for socket-output decode call sites: %v", err)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].where < sites[j].where })
	return sites
}

// dispatchedExports returns the default export names the function dispatches,
// read from the attachment roles it names.
//
// The route is role to export rather than a search for the export name itself,
// and that is what keeps the name single-sourced: a socket function names the
// role it serves, and the canonical role-to-export rule turns that into an
// export. Nothing here spells an export name, so nothing here can drift from the
// rule the host's own dispatch default reads.
//
// It looks one hop through a same-file helper, because a socket function that
// shares its export default with a sibling reads it from a small function beside
// them rather than inline.
func dispatchedExports(fn *ast.FuncDecl, file *ast.File, roleValues map[string]plugintype.Role) []string {
	roles := map[plugintype.Role]bool{}
	collectRoles(fn.Body, roleValues, roles)

	sameFileHelpers := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if helper, ok := decl.(*ast.FuncDecl); ok && helper.Body != nil && helper.Recv == nil {
			sameFileHelpers[helper.Name.Name] = helper
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if helper, ok := sameFileHelpers[calleeName(call.Fun)]; ok && helper != fn {
			collectRoles(helper.Body, roleValues, roles)
		}
		return true
	})

	var out []string
	for role := range roles {
		if export, ok := plugintype.GovernanceRoleExport(role); ok {
			out = append(out, export)
		}
	}
	sort.Strings(out)
	return dedupe(out)
}

// collectRoles records every attachment role the body names by its declared
// identifier.
func collectRoles(body *ast.BlockStmt, roleValues map[string]plugintype.Role, into map[plugintype.Role]bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if role, ok := roleValues[sel.Sel.Name]; ok {
			into[role] = true
		}
		return true
	})
}

// governanceRoleIdents reads the attachment-role constants' declared identifiers
// and values out of the plugin-type vocabulary's own source, so this guard holds
// no list of them. A hand-kept list would agree on the day it was written and
// silently stop covering a role added afterwards, which is the moment coverage
// matters.
func governanceRoleIdents(t *testing.T, root string) map[string]plugintype.Role {
	t.Helper()
	path := filepath.Join(root, "pkg", "plugintype", "plugintype.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse the plugin-type vocabulary at %s: %v", path, err)
	}

	known := map[plugintype.Role]bool{}
	for _, role := range plugintype.GovernanceRoles() {
		known[role] = true
	}

	out := map[string]plugintype.Role{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value := plugintype.Role(strings.Trim(lit.Value, "`\""))
			if known[value] {
				out[name.Name] = value
			}
		}
		return true
	})

	if len(out) != len(known) {
		t.Fatalf("read %d attachment-role identifiers out of %s but the vocabulary declares %d roles; "+
			"the census missed one, and a missed role is a call site this guard cannot resolve",
			len(out), path, len(known))
	}
	return out
}

// calleeName reduces a call's function expression to the identifier being
// called, dropping any package or receiver qualifier.
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func dedupe(in []string) []string {
	var out []string
	for i, v := range in {
		if i > 0 && v == in[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}
