// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// decodeEntryPoint is the socket-output decode entry point every socket's call
// site reads its guest's output through. Naming it at a call site is what makes a
// socket's decode visible to this guard.
const decodeEntryPoint = "DecodeGuestOutputCBOR"

// decodeSite is one socket-output decode call: where it is, and which exports the
// enclosing function dispatches.
type decodeSite struct {
	where   string
	exports []string
}

// TestWireRegistryAgreesWithSocketDecodeEntryPoints is the pin between the two
// halves of one fact.
//
// The registry records which exports a socket calls with a payload in the binary
// encoding. The call site records the same fact a second time, by decoding that
// export's output through the decode entry point. Nothing makes the two agree —
// the registry is a declaration and the call site is code — so a socket present
// on one side and not the other produces a build that compiles, a suite that
// passes, and a direct invocation that is broken.
//
// The direction that matters most is the second one below. A socket whose
// registry entry is missing leaves the direct-invocation command handing the
// guest text it cannot read, which is a failure with no red anywhere: the
// compiler's own path works, because it does not consult the registry. It holds
// for every socket, whether its export is a governance role's default or fixed by
// its plugin type: a call site decoding an export through the entry point fails
// this guard unless the registry lists that export, so the table cannot fall
// behind a socket that is added.
//
// How a call site's exports are read, and what that cannot see. A decode call's
// exports are every export its enclosing function — and any same-file helper it
// calls — names in one of three forms: an attachment role, read through the
// role-to-export rule; an export-name identifier the wasm package declares, read
// from that declaration; or a literal export name passed to the decode call
// itself. The reading is syntactic: an export reaching the call through a
// parameter, a variable assigned in another file, or a helper in another file is
// not seen. A decode site from which none of the three resolves anything is
// reported as a hole rather than passed, so what the reading cannot see surfaces
// as a failure, not as a green.
//
// COVERAGE BOUND, stated because an unstated bound reads as full coverage: this
// guard reaches a socket whose output decode routes through the entry point
// above. A socket that decoded through its generated codec directly would not
// name it, so its wire fact would be pinned by nothing here. That is why the
// registry admits an entry only for a socket this guard can see: an entry it
// cannot see would be exactly the unpinned hand-written fact the registry exists
// to avoid rather than to relocate.
//
// It also reads every payload-carrying invocation of a guest export, which is
// the backstop behind the invocation's own type. The host calls an export it
// chooses only as a value the plugin-type registry's host-export lists produce,
// so a call naming any other export does not compile; what compiles and is still
// wrong is a value no list produced spelled at the call — the zero value, say —
// and a name a socket chose itself handed to one of the two entry points that
// exist for an export authored content names. Either is reported here. The
// export identifiers the decode sites name are resolved from the lists too: the
// wasm package declares them only as generated values of those lists, and an
// identifier there spelling a listed name as a literal is a second spelling this
// guard refuses.
func TestWireRegistryAgreesWithSocketDecodeEntryPoints(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	sites := socketDecodeSites(t, root)
	if len(sites) == 0 {
		t.Fatalf("found no socket-output decode call site under %s; the census is looking in the "+
			"wrong place, and an empty census would pass this guard while proving nothing", root)
	}

	for _, d := range wireDisagreements(listedExports(), sites) {
		t.Error(d)
	}

	invocations := payloadInvocationSites(t, root)
	if countKind(invocations, invocationListed) == 0 || countKind(invocations, invocationAuthored) == 0 {
		t.Fatalf("found %d payload invocation(s) under %s, with no listed or no authored one among them; the "+
			"census is looking in the wrong place, and an empty census would pass while proving nothing",
			len(invocations), root)
	}
	for _, d := range invocationComplaints(invocations) {
		t.Error(d)
	}
	for _, d := range hostExportConstructionComplaints(t, root) {
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
		listed  map[string]bool
		sites   []decodeSite
		wantSub string
	}{
		{
			name:    "the registry lists an export no call site decodes",
			listed:  map[string]bool{"mutate": true},
			sites:   []decodeSite{{where: "a.go:1", exports: []string{"verdict_ir"}}},
			wantSub: "no socket-output decode call site",
		},
		{
			name:    "a call site decoding an export the registry does not list",
			listed:  map[string]bool{"mutate": true},
			sites:   []decodeSite{{where: "a.go:1", exports: []string{"import"}}},
			wantSub: "does not list",
		},
		{
			name:    "a call site whose export nothing resolves",
			listed:  map[string]bool{"mutate": true},
			sites:   []decodeSite{{where: "a.go:1", exports: nil}},
			wantSub: "names no export this guard can resolve",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := wireDisagreements(tc.listed, tc.sites)
			if len(got) == 0 {
				t.Fatalf("desynchronised input produced no complaint; the guard cannot fail, "+
					"so its green says nothing (listed=%v sites=%v)", tc.listed, tc.sites)
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, tc.wantSub) {
				t.Fatalf("complaint does not name the disagreement: got\n%s\nwant a mention of %q",
					joined, tc.wantSub)
			}
		})
	}
}

// TestWireRegistryGuardReachesEveryListedExport takes every export the generated
// registry lists, drops it, and requires the comparison over the module's real
// decode call sites to complain, naming that export. A synthetic pair proves the
// comparison's logic; this proves it reaches every socket that ships, so a
// socket whose registration declares no export — which no generation-time check
// can see, since such a registration serves no plugin type — is reported here
// from its decode site, and a socket added later is covered without this test
// being edited. It refuses an empty registry, because a loop over nothing passes
// while proving nothing.
func TestWireRegistryGuardReachesEveryListedExport(t *testing.T) {
	t.Parallel()

	listed := listedExports()
	if len(listed) == 0 {
		t.Fatal("the generated registry lists no export, so there is nothing to drop and this test proves nothing")
	}
	sites := socketDecodeSites(t, moduleRoot(t))

	for _, dropped := range sortedExports(listed) {
		t.Run(dropped, func(t *testing.T) {
			t.Parallel()
			weakened := make(map[string]bool, len(listed))
			for export := range listed {
				if export != dropped {
					weakened[export] = true
				}
			}
			got := wireDisagreements(weakened, sites)
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, strconv.Quote(dropped)) {
				t.Fatalf("with %q dropped from the registry the comparison over the real call sites did not name it, "+
					"so a socket decoding that export with no registration would pass; got\n%s", dropped, joined)
			}
		})
	}
}

// TestDecodeSiteCensusResolvesEveryNamingForm proves the census reads a decode
// site's export in each form a socket call site names it, beyond the governance
// roles alone: a role, an export-name identifier the wasm package declares, a
// literal export name on the decode call, and either of the first two named in a
// same-file helper. A site naming none of them resolves nothing, which the guard
// above reports as a hole.
//
// The identifiers are read from the real vocabulary and the real wasm package, so
// a census that stopped reading one of them fails here rather than silently
// resolving nothing at a real call site.
func TestDecodeSiteCensusResolvesEveryNamingForm(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	roles := governanceRoleIdents(t, root)
	naming := exportNaming{roles: roles, exports: exportNameIdents(t, root, roles, pluginTypeIdents(t, root))}

	mutate, ok := plugintype.GovernanceRoleExport(plugintype.RoleLabelTransform)
	if !ok {
		t.Fatal("the label-transform role has no default export")
	}
	importer, ok := plugintype.Lookup(plugintype.Importer)
	if !ok || len(importer.HostExports) != 1 {
		t.Fatal("the importer type lists no single host export")
	}
	if got := naming.exports["ImportExport"]; got != importer.HostExports[0] {
		t.Fatalf("the census resolves the wasm package's ImportExport to %q; want the importer type's host "+
			"export %q", got, importer.HostExports[0])
	}
	linter, ok := plugintype.Lookup(plugintype.Linter)
	if !ok || len(linter.HostExports) != 1 {
		t.Fatal("the linter type lists no single host export")
	}
	if got := naming.exports["LintRulesExport"]; got != linter.HostExports[0] {
		t.Fatalf("the census resolves the wasm package's LintRulesExport to %q; want the linter type's host "+
			"export %q, read from its list", got, linter.HostExports[0])
	}

	const src = `package synthetic

func viaRole() { _ = plugintype.RoleLabelTransform; wasm.DecodeGuestOutputCBOR(export, out, &v) }

func viaExportName() { wasm.DecodeGuestOutputCBOR(wasm.ImportExport, out, &v) }

func viaLiteral() { wasm.DecodeGuestOutputCBOR("a_literal_export", out, &v) }

func viaHelper() { name := helper(); wasm.DecodeGuestOutputCBOR(name, out, &v) }

func helper() string { return wasm.MutateExport }

func viaNothing() { wasm.DecodeGuestOutputCBOR(export, out, &v) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse the synthetic source: %v", err)
	}
	sites := fileDecodeSites(fset, file, "synthetic.go", naming)

	want := map[string][]string{
		"synthetic.go:3":  {mutate},
		"synthetic.go:5":  {importer.HostExports[0]},
		"synthetic.go:7":  {"a_literal_export"},
		"synthetic.go:9":  {mutate},
		"synthetic.go:13": nil,
	}
	if len(sites) != len(want) {
		t.Fatalf("found %d decode sites in the synthetic source; want %d: %+v", len(sites), len(want), sites)
	}
	for _, s := range sites {
		w, ok := want[s.where]
		if !ok {
			t.Errorf("unexpected decode site %s", s.where)
			continue
		}
		if strings.Join(s.exports, ",") != strings.Join(w, ",") {
			t.Errorf("site %s resolves exports %v; want %v", s.where, s.exports, w)
		}
	}
}

// wireDisagreements compares the exports the registry lists against the exports
// each socket-output decode call site decodes, and returns one complaint per
// disagreement. It is a pure function so a planted disagreement can prove it
// fires; the two censuses feeding it are gathered by the callers above.
//
// It reads in both directions on purpose. Only the second catches a socket whose
// registry entry is missing, which is the failure the direct-invocation command
// shows and no other gate does.
func wireDisagreements(listed map[string]bool, sites []decodeSite) []string {
	var out []string

	// Direction one: every export the registry lists is decoded through the entry
	// point at some call site.
	for _, export := range sortedExports(listed) {
		decoded := false
		for _, s := range sites {
			if containsExport(s.exports, export) {
				decoded = true
				break
			}
		}
		if !decoded {
			out = append(out, fmt.Sprintf(
				"the plugin-wire registry lists export %q, but no socket-output decode call site decodes it "+
					"through %s: either no socket calls that export and the registry is ahead of the code, or "+
					"the call site is not visible to this guard",
				export, decodeEntryPoint))
		}
	}

	// Direction two: every call site naming the entry point dispatches exports the
	// registry lists — and resolves at least one, since a site whose export nothing
	// resolves is a hole rather than a pass.
	for _, s := range sites {
		if len(s.exports) == 0 {
			out = append(out, fmt.Sprintf(
				"the socket-output decode call site at %s decodes through %s but names no export this "+
					"guard can resolve — no attachment role, no export-name identifier the wasm package "+
					"declares, and no literal export name — so it cannot tell which export the site serves "+
					"and cannot check it against the plugin-wire registry",
				s.where, decodeEntryPoint))
			continue
		}
		for _, export := range s.exports {
			if !listed[export] {
				out = append(out, fmt.Sprintf(
					"the socket-output decode call site at %s decodes export %q through %s, but the "+
						"plugin-wire registry does not list that export: a socket without its registry entry "+
						"hands an authored payload to a guest in the wrong encoding, and nothing else reports it",
					s.where, export, decodeEntryPoint))
			}
		}
	}

	sort.Strings(out)
	return out
}

// listedExports reduces the generated registry to the set of exports it lists,
// which is the fact this guard checks. An export appearing more than once — one
// export serving two payload shapes — contributes one entry.
func listedExports() map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		out[e.export] = true
	}
	return out
}

// sortedExports returns the set's exports in sorted order, so complaints and
// subtests read the same on every run.
func sortedExports(set map[string]bool) []string {
	var out []string
	for export := range set {
		out = append(out, export)
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

// exportNaming is what a decode site's exports are read from: the
// attachment-role identifiers the plugin-type vocabulary declares, each mapped to
// its role, and the export-name identifiers the wasm package declares, each
// mapped to the export it names.
type exportNaming struct {
	roles   map[string]plugintype.Role
	exports map[string]string
}

// socketDecodeSites walks every non-test Go file in the module and returns one
// entry per socket-output decode call, with the exports the enclosing function
// dispatches.
//
// Test files are excluded: a test drives a decode entry point directly with a
// literal export name and no attachment role, so including them would report a
// hole at every such test rather than at a real call site.
//
// This package is excluded too. Its one decode call is the registry reading back
// an answer for whichever export its caller names, through the rows of the table
// itself, so it is not a socket call site: counting it would either report a hole
// — its export arrives as a parameter — or, were it made legible here, satisfy
// direction one for every listed export at once and leave that direction proving
// nothing about the sockets.
func socketDecodeSites(t *testing.T, root string) []decodeSite {
	t.Helper()
	roleValues := governanceRoleIdents(t, root)
	naming := exportNaming{
		roles:   roleValues,
		exports: exportNameIdents(t, root, roleValues, pluginTypeIdents(t, root)),
	}
	registryDir := filepath.Join(root, "internal", "pluginwire")

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
			if path == registryDir {
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
		sites = append(sites, fileDecodeSites(fset, file, rel, naming)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk module for socket-output decode call sites: %v", err)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].where < sites[j].where })
	return sites
}

// fileDecodeSites returns one entry per socket-output decode call in one parsed
// file, each carrying the exports its enclosing function dispatches plus any
// literal export name the call itself passes. It reads the file it is handed and
// nothing else, so a synthetic source can show which forms it resolves.
func fileDecodeSites(fset *token.FileSet, file *ast.File, rel string, naming exportNaming) []decodeSite {
	var sites []decodeSite
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var found []decodeSite
		var literals [][]string
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if calleeName(call.Fun) != decodeEntryPoint {
				return true
			}
			found = append(found, decodeSite{
				where: fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line),
			})
			literals = append(literals, literalExport(call))
			return true
		})
		if len(found) == 0 {
			continue
		}
		exports := dispatchedExports(fn, file, naming)
		for i := range found {
			found[i].exports = dedupe(sortedUnion(exports, literals[i]))
		}
		sites = append(sites, found...)
	}
	return sites
}

// literalExport returns the export a decode call names as a string literal in
// its export argument, the first one, or nothing when it names it any other way.
func literalExport(call *ast.CallExpr) []string {
	if len(call.Args) == 0 {
		return nil
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil
	}
	export, err := strconv.Unquote(lit.Value)
	if err != nil || export == "" {
		return nil
	}
	return []string{export}
}

// dispatchedExports returns the export names the function dispatches, read from
// the attachment roles and the export-name identifiers it names.
//
// The route is role or identifier to export rather than a search for the export
// name itself, and that is what keeps the name single-sourced: a socket function
// names the role it serves or the identifier the wasm package declares for its
// export, and the canonical rule behind each turns that into an export. Nothing
// here spells an export name, so nothing here can drift from the rule the host's
// own dispatch reads.
//
// It looks one hop through a same-file helper, because a socket function that
// shares its export default with a sibling reads it from a small function beside
// them rather than inline.
func dispatchedExports(fn *ast.FuncDecl, file *ast.File, naming exportNaming) []string {
	found := map[string]bool{}
	collectExports(fn.Body, naming, found)

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
			collectExports(helper.Body, naming, found)
		}
		return true
	})

	var out []string
	for export := range found {
		out = append(out, export)
	}
	sort.Strings(out)
	return out
}

// collectExports records the export behind every attachment role and every
// export-name identifier the body names by its declared identifier.
func collectExports(body *ast.BlockStmt, naming exportNaming, into map[string]bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if role, ok := naming.roles[sel.Sel.Name]; ok {
			if export, ok := plugintype.GovernanceRoleExport(role); ok {
				into[export] = true
			}
		}
		if export, ok := naming.exports[sel.Sel.Name]; ok {
			into[export] = true
		}
		return true
	})
}

// sortedUnion returns the sorted union of two lists of names, duplicates kept for
// dedupe to drop.
func sortedUnion(a, b []string) []string {
	out := append(append([]string(nil), a...), b...)
	sort.Strings(out)
	return out
}

// exportNameIdents reads the export-name identifiers the wasm package declares,
// and the export each names, out of that package's own source, so this guard
// holds no list of them. Each is resolved through the declaration's own
// initializer, never by its spelling: a generated host-export identifier names
// the export its plugin type's host-export list holds — read from that list, and
// unresolved if the list does not hold it — and a role's default is read through
// the canonical role-to-export rule. A literal resolves to itself, but only for a
// name no list holds — the describe export, which carries no payload; a literal
// spelling a listed export is a second spelling of a name the list already
// holds, and is refused.
//
// It refuses rather than skipping a declaration it cannot resolve whose
// identifier reads as an export name, because a missed identifier is a call site
// this guard reads as naming nothing, and a census that quietly misses one is the
// way a socket's export stops being checked.
func exportNameIdents(t *testing.T, root string, roles map[string]plugintype.Role, types map[string]plugintype.Type) map[string]string {
	t.Helper()
	dir := filepath.Join(root, "internal", "wasm")
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the wasm package at %s: %v", dir, err)
	}

	listed := map[string]bool{}
	for _, e := range plugintype.Entries() {
		for _, name := range e.HostExports {
			listed[name] = true
		}
	}

	out := map[string]string{}
	var unresolved, secondSpellings []string
	for _, e := range files {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.VAR && gen.Tok != token.CONST) {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if export, err := strconv.Unquote(lit.Value); err == nil && listed[export] {
							secondSpellings = append(secondSpellings, fmt.Sprintf("%s = %s in %s", ident.Name, lit.Value, name))
							continue
						}
					}
					if export, ok := resolveExportName(vs.Values[i], roles, types); ok {
						out[ident.Name] = export
						continue
					}
					if strings.HasSuffix(ident.Name, "Export") || strings.HasSuffix(ident.Name, "ExportName") {
						unresolved = append(unresolved, ident.Name+" in "+name)
					}
				}
			}
		}
	}

	if len(secondSpellings) > 0 {
		sort.Strings(secondSpellings)
		t.Fatalf("the wasm package spells a name a host-export list already holds as a literal (%s); the list is "+
			"the one home of that name, and the identifier for it is generated from the list",
			strings.Join(secondSpellings, ", "))
	}
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		t.Fatalf("the wasm package declares export-name identifiers this census cannot resolve (%s); a "+
			"missed identifier is a call site this guard reads as naming no export", strings.Join(unresolved, ", "))
	}
	if len(out) == 0 {
		t.Fatalf("found no export-name identifier in %s; the census is looking in the wrong place", dir)
	}
	return out
}

// resolveExportName resolves one declaration's initializer to the export it
// names: a string literal, the role-to-export rule applied to a role named by its
// declared identifier, or — the form every generated host-export identifier
// takes — a plugin type named by its declared identifier together with a name
// that type's host-export list holds. A name the list does not hold resolves to
// nothing, so a generated identifier outliving its list entry is reported as
// unresolved rather than read as naming an export.
func resolveExportName(value ast.Expr, roles map[string]plugintype.Role, types map[string]plugintype.Type) (string, bool) {
	switch v := value.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		export, err := strconv.Unquote(v.Value)
		return export, err == nil && export != ""
	case *ast.CallExpr:
		if len(v.Args) == 0 {
			return "", false
		}
		arg, ok := v.Args[0].(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		if role, ok := roles[arg.Sel.Name]; ok && len(v.Args) == 1 {
			return plugintype.GovernanceRoleExport(role)
		}
		typ, ok := types[arg.Sel.Name]
		if !ok || len(v.Args) != 2 {
			return "", false
		}
		lit, ok := v.Args[1].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil {
			return "", false
		}
		entry, ok := plugintype.Lookup(typ)
		if !ok {
			return "", false
		}
		for _, listed := range entry.HostExports {
			if listed == name {
				return listed, true
			}
		}
	}
	return "", false
}

// pluginTypeIdents reads the plugin-type constants' declared identifiers and
// values out of the plugin-type vocabulary's own source, the same way
// governanceRoleIdents reads the roles, and for the same reason.
func pluginTypeIdents(t *testing.T, root string) map[string]plugintype.Type {
	t.Helper()
	path := filepath.Join(root, "pkg", "plugintype", "plugintype.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse the plugin-type vocabulary at %s: %v", path, err)
	}

	known := map[plugintype.Type]bool{}
	for _, e := range plugintype.Entries() {
		known[e.Type] = true
	}

	out := map[string]plugintype.Type{}
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
			value := plugintype.Type(strings.Trim(lit.Value, "`\""))
			if known[value] {
				out[name.Name] = value
			}
		}
		return true
	})

	if len(out) != len(known) {
		t.Fatalf("read %d plugin-type identifiers out of %s but the vocabulary declares %d types; the census "+
			"missed one, and a missed type is an export this guard cannot resolve", len(out), path, len(known))
	}
	return out
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
