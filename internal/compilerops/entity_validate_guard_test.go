// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
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
)

// validateopsImportPath is the package holding the single-entity check.
const validateopsImportPath = "github.com/aggrega-ai/aggrega-cli/internal/validateops"

// singleEntityCheck is the check's two entry points, which hand the catalog
// load's refusals back raw.
var singleEntityCheck = map[string]bool{"ValidateEntity": true, "ValidateEntityContent": true}

// TestEverySurfaceChecksOneEntityThroughTheAnnotatingEntry holds every surface
// that checks one entity to ValidateSingleEntity or ValidateSingleEntityContent:
// no production file outside the check's own package and the one file holding
// that entry names validateops.ValidateEntity or validateops.ValidateEntityContent,
// called or as a value — the rest of this package included. The check hands back the refusals raw and the failure they explain
// carries only its reference, so a surface calling it directly would report that
// failure without naming the refused file — a difference from the other
// surfaces that nothing else would notice.
//
// It matches the selector through each file's own imports, so an aliased import
// is caught as surely as a plain one, and it walks the whole module rather than a
// list of the surfaces known today, so a surface added later is covered the day
// it is added.
func TestEverySurfaceChecksOneEntityThroughTheAnnotatingEntry(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module root is not two directories up from this package: %v", err)
	}

	// Calibration, both halves: the scan must fire on this package's own entry,
	// which names both, or it matched nothing and would agree with any source.
	own := scanSingleEntityCheckRefs(t, ".")
	if len(own) < 2 {
		t.Fatalf("the scan finds %d reference(s) to the check in this package, which names both entry points; "+
			"it would pass having checked nothing:\n  %s", len(own), strings.Join(own, "\n  "))
	}

	// The check's own package, and the one file holding the annotating entry. The
	// rest of this package is walked like any other: only the entry may call the
	// check. The entry file must be among the files the walk meets, or the
	// exemption names a file that no longer exists and exempts nothing.
	exemptDir := filepath.Join("internal", "validateops")
	entryFile := filepath.Join("internal", "compilerops", "entity_validate.go")
	entryFileMet := false
	// The front doors known today must be among the files the walk read, or the
	// walk did not reach where the surfaces live.
	frontDoors := map[string]bool{
		filepath.Join("cmd", "aggrega", "validate.go"):             false,
		filepath.Join("internal", "mcp", "tool_validate.go"):       false,
		filepath.Join("internal", "serve", "entity_validation.go"): false,
	}
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			if rel == exemptDir {
				return filepath.SkipDir
			}
			// A nested module is another module's source, not this one's.
			if rel != "." {
				if _, serr := os.Stat(filepath.Join(path, "go.mod")); serr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if _, known := frontDoors[rel]; known {
			frontDoors[rel] = true
		}
		if rel == entryFile {
			entryFileMet = true
			return nil
		}
		found = append(found, singleEntityCheckRefsIn(t, path, rel)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	for door, read := range frontDoors {
		if !read {
			t.Fatalf("the walk never read %s; it did not reach where the surfaces live", door)
		}
	}
	if !entryFileMet {
		t.Fatalf("the walk never met %s, the one file exempted; the exemption is stale", entryFile)
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Errorf("production code outside the check calls it directly; a surface that checks one entity calls "+
			"compilerops.ValidateSingleEntity or ValidateSingleEntityContent, which name the refused catalog file "+
			"the check hands back raw:\n  %s", strings.Join(found, "\n  "))
	}
}

// scanSingleEntityCheckRefs returns the references to the check's entry points
// in the production files of the package in dir.
func scanSingleEntityCheckRefs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		found = append(found, singleEntityCheckRefsIn(t, filepath.Join(dir, name), name)...)
	}
	return found
}

// singleEntityCheckRefsIn returns, as `<rel>:<line>: validateops.<name>`, every
// selector in the file at path naming one of the check's entry points through
// the name that file imports the check's package under.
func singleEntityCheckRefsIn(t *testing.T, path, rel string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	local := validateopsImportName(file)
	if local == "" {
		return nil
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == local && singleEntityCheck[sel.Sel.Name] {
			found = append(found, rel+":"+strconv.Itoa(fset.Position(sel.Pos()).Line)+": validateops."+sel.Sel.Name)
		}
		return true
	})
	return found
}

// validateopsImportName is the name file uses for the check's package, or ""
// when it does not import it (or imports it blank or dot, which names nothing).
func validateopsImportName(file *ast.File) string {
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != validateopsImportPath {
			continue
		}
		if spec.Name == nil {
			return "validateops"
		}
		if spec.Name.Name == "_" || spec.Name.Name == "." {
			return ""
		}
		return spec.Name.Name
	}
	return ""
}
