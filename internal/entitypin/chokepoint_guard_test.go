// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The pin step is one implementation with several callers, and this file is what
// holds it to that structurally rather than by reading the call graph once.
//
// These facts together are the single path:
//
//   - The lock is written, and previewed, from the pin step's own plan and from
//     nowhere else in the module. A second function calling the lock's writer
//     would be a second pin step, free to route a pin to another entity or order
//     the rows another way.
//   - The plan is computed from a named entity document in one place, which both
//     the author's own step and a publication's hold on the lock read, and
//     otherwise only by the dependency resolution's run of it.
//   - The author's own step is reached by exactly the surfaces that carry it: the
//     command line's pin command, a publication that pins first, the daemon's pin
//     method and the tool protocol's pin tool. The hold is reached by a
//     publication that does not pin, and by nothing else.
//   - Whether an entity has its directory to itself is decided by one predicate,
//     reached by the pin step and by a publication, and composed into one
//     refusal — so the two moments cannot come to disagree about a directory.
//   - A local source's pins are digested over the one reading of which files the
//     source contributes, and compared in one place — so the files pinned, the
//     files a build compares and the files a publication packs are one set.
//
// A legitimate new caller changes one of the sets below, and the failure is the
// point: whoever adds one has to say so here, next to the others, rather than
// land a second route beside the first.

// moduleRoot is the aggrega-cli module root, from this package's directory.
const moduleRoot = "../.."

// callersOf parses every non-test Go file in the module and returns, for each
// package-qualified callee, the set of package-qualified functions whose body
// calls it. A method is named by its receiver's type as well, so a caller on a
// type cannot be mistaken for a free function of the same name.
//
// A qualified callee resolves through the calling file's own imports, to the
// last segment of the imported path, so an aliased import resolves to the same
// key as an unaliased one; an unqualified callee resolves to the calling file's
// own package. A walk that parsed nothing fails loudly rather than returning an
// empty set, because an empty set reads exactly like a call site that was
// removed.
func callersOf(t *testing.T) map[string]map[string]bool {
	t.Helper()
	callers := map[string]map[string]bool{}
	parsed := 0
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		parsed++
		pkg := file.Name.Name
		imports := map[string]string{}
		for _, spec := range file.Imports {
			ip := strings.Trim(spec.Path.Value, `"`)
			canonical := ip[strings.LastIndex(ip, "/")+1:]
			local := canonical
			if spec.Name != nil {
				if spec.Name.Name == "_" || spec.Name.Name == "." {
					continue
				}
				local = spec.Name.Name
			}
			imports[local] = canonical
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			enclosing := pkg + "." + fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				enclosing = pkg + "." + receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var callee string
				switch f := call.Fun.(type) {
				case *ast.Ident:
					callee = pkg + "." + f.Name
				case *ast.SelectorExpr:
					if x, ok := f.X.(*ast.Ident); ok {
						if prefix, ok := imports[x.Name]; ok {
							callee = prefix + "." + f.Sel.Name
						}
					}
				}
				if callee == "" {
					return true
				}
				if callers[callee] == nil {
					callers[callee] = map[string]bool{}
				}
				callers[callee][enclosing] = true
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module source: %v", err)
	}
	if parsed == 0 {
		t.Fatalf("no Go source parsed under %s; the module moved — point moduleRoot at it so this guard keeps seeing every call site", moduleRoot)
	}
	return callers
}

// receiverName renders a method receiver's type as the bare type name.
func receiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return receiverName(e.X)
	}
	return "?"
}

// TestThePinStepIsOneImplementation pins the caller set of each link in the
// chain, so a second pin step — or a surface that pins without going through the
// author's own step — reddens here.
//
// Planted defects, demonstrated by reasoning over the walk: a function anywhere
// in the module calling recipepin.WriteLock joins the first set; a surface that
// computed a plan of its own joins the second; a surface calling the lock's
// writer through a helper of its own joins the first set under that helper's
// name. Each is a non-empty difference, reported with the name.
func TestThePinStepIsOneImplementation(t *testing.T) {
	callers := callersOf(t)
	cases := []struct {
		callee string
		why    string
		want   []string
	}{
		{
			callee: "recipepin.WriteLock",
			why:    "the lock is written from the pin step's plan and from nowhere else",
			want:   []string{"entitypin.Plan.Write"},
		},
		{
			// The writer is the preview plus one write, so it is a caller too — and it
			// is the one allowed, since it is reached only through the plan above.
			callee: "recipepin.PreviewLock",
			why:    "a report of what the step would write reads the same plan the write reads",
			want:   []string{"entitypin.Plan.Report", "recipepin.WriteLock"},
		},
		{
			callee: "closure.DescendAuthoredSources",
			why:    "the Release-free resolution of an entity's sources is the pin step's own",
			want:   []string{"entitypin.Compute"},
		},
		{
			callee: "entitypin.Compute",
			why:    "the plan is computed from a named document in one place, and by a dependency resolution's run of it",
			want:   []string{"entitypin.planNamed", "pullops.entityPins"},
		},
		{
			callee: "entitypin.planNamed",
			why:    "a named document becomes a plan for the step that writes it and the hold that checks it, and for nothing else",
			want:   []string{"entitypin.Hold", "entitypin.Pin"},
		},
		{
			callee: "entitypin.Pin",
			why:    "the author's own step is reached by exactly the surfaces that carry it",
			want:   []string{"main.pinRun", "mcp.pinInvoke", "publishops.pinBeforePublishing", "serve.Server.dispatchPin"},
		},
		{
			callee: "entitypin.Hold",
			why:    "a publication that does not pin holds the entity's lock to the step, and nothing else holds a named document",
			want:   []string{"publishops.holdBeforeBundling"},
		},
		{
			callee: "publishops.holdBeforeBundling",
			why:    "a publication holds the lock once, immediately before it bundles",
			want:   []string{"publishops.Push"},
		},
		{
			callee: "pullops.entityPins",
			why:    "a dependency resolution runs the step once, from its own run",
			want:   []string{"pullops.Run"},
		},
		{
			callee: "publishops.pinBeforePublishing",
			why:    "a publication runs the step once, before it bundles",
			want:   []string{"publishops.Push"},
		},
		{
			// One predicate decides whether an entity has its directory to itself,
			// and it is reached at the two moments something is produced from that
			// directory and at no other: the pin step's plan for writing, which every
			// caller that pins goes through, and a publication, whether or not it
			// pins. A catalog load, a lint or a validate calling it would join this
			// set, and so would a second implementation calling the listing helpers.
			callee: "entitypin.RefuseSharedDirectory",
			why:    "the directory rule is one predicate, asked by the pin step and by a publication and by nothing else",
			want:   []string{"entitypin.Compute", "publishops.Push"},
		},
		{
			callee: "entitypin.sharedDirectory",
			why:    "the directory rule's refusal is composed in one place, so both moments give one diagnostic",
			want:   []string{"entitypin.RefuseSharedDirectory"},
		},
		{
			// A local source's pins are the digests of the files the entity's declared
			// set says the source contributes — the files a publication packs — so the
			// digesting is reached from that one reading and from nowhere else. A
			// second caller would be a second enumeration of a source's files, free to
			// pin a file a publication does not carry or to miss one it does.
			callee: "recipepin.LocalSourcePins",
			why:    "a local source's files are pinned over the declared set's answer and over no other listing",
			want:   []string{"catalog.LocalSourceOf"},
		},
		{
			callee: "catalog.LocalSourceOf",
			why:    "a local source is read from an entity document in one place",
			want:   []string{"catalog.LocalSourceAt"},
		},
		{
			// The reading carries whether the entity declares a local source at all,
			// which is what decides whether a lock's local source pins are held to
			// it, so the pin step's hold and a verifying pull read it here and scope
			// the lock's pins through its answer.
			callee: "catalog.LocalSourceAt",
			why:    "the pin step, a verifying pull and a build read a local source through the one reading",
			want:   []string{"catalog.LocalSourcePinsAt", "entitypin.Compute", "pullops.verifyEntityLocks"},
		},
		{
			// The build reaches this as a value the catalog hands the render through
			// its lookup, which a call walk does not see; the value is set where the
			// catalog builds that lookup and nowhere else, so nothing calls it.
			callee: "catalog.LocalSourcePinsAt",
			why:    "the render's reading of a local source's pins is the one value its lookup carries",
			want:   []string{},
		},
		{
			callee: "recipepin.CheckLocalSourcePins",
			why:    "a build and a verifying pull refuse a drifted local source through one comparison and one diagnostic",
			want:   []string{"pullops.verifyEntityLocks", "renderer.checkLocalSourcePins"},
		},
	}
	for _, tc := range cases {
		got := make([]string, 0, len(callers[tc.callee]))
		for name := range callers[tc.callee] {
			got = append(got, name)
		}
		sort.Strings(got)
		want := append([]string(nil), tc.want...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("callers of %s = %v; want exactly %v — %s. If you added a legitimate route, name it here "+
				"beside the others rather than landing a second implementation", tc.callee, got, want, tc.why)
		}
	}
}

// TestTheGuardSeesAPlantedSecondWriter is the guard's own positive control: a
// file carrying a second writer of the lock, parsed by the same walk, lands in
// the caller set. Without it, a walk that silently parsed nothing relevant would
// pass the test above for want of seeing anything.
func TestTheGuardSeesAPlantedSecondWriter(t *testing.T) {
	dir := t.TempDir()
	planted := filepath.Join(dir, "planted.go")
	src := "package planted\n\nimport \"github.com/aggrega-ai/aggrega-cli/pkg/recipepin\"\n\n" +
		"func secondWriter() { _, _ = recipepin.WriteLock(\"x\", recipepin.Pins{}) }\n"
	if err := os.WriteFile(planted, []byte(src), 0o644); err != nil {
		t.Fatalf("plant: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, planted, nil, 0)
	if err != nil {
		t.Fatalf("parse the planted file: %v", err)
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "WriteLock" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("the walk's own parse does not see a planted call to the lock's writer, so the guard above proves nothing")
	}
	if got := callersOf(t)["recipepin.WriteLock"]; len(got) == 0 {
		t.Fatal("the module walk found no caller of the lock's writer at all; the writer is called from the plan, so the walk is blind")
	}
}
