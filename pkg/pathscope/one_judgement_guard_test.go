// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope_test

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

// moduleRoot is this module's root, relative to this package's directory, where
// the test runs.
const moduleRoot = "../.."

// devToolingDirs are the directories, relative to the module root, holding tools
// that build, check or document the product rather than being part of it. They
// resolve links for their own bookkeeping — a census of the files a test read, say
// — and decide nothing about a path the product reads or writes.
var devToolingDirs = []string{"internal/buildtools", "internal/tools"}

// sanctionedLinkResolvers are the functions of the product that may resolve a
// link themselves, keyed by the package directory and the function — its receiver
// type first for a method — each valued by why that is not a second judgement of
// where a path leads.
//
// 🔴 This is the one HAND-WRITTEN set this guard reads, and it is hand-written
// because it IS the decision rather than a fact about the source: an entry states
// that the function resolves a link for a reason other than deciding whether a
// path is inside. Deriving it from the calls would make the guard agree with
// whatever the code does.
var sanctionedLinkResolvers = map[string]string{
	"pkg/pathscope:resolveExisting": "the one definition of inside resolving the part of a path it judges",
	"pkg/writescope:landingOf": "follows a link that leads nowhere, at the last part of a path about to be " +
		"written, to the place the write would create its file; the one definition of inside then judges that place",
	"pkg/workspaceconfig:canonicalReleasePath": "the one spelling a configured Release file is keyed by — a key " +
		"the configuration is looked up under, not a verdict on where a path leads",
	"internal/serve:resolvedPath": "spells a path the configuration names resolved, so the configuration's " +
		"exception can compare it with the place a link leads; the place itself was judged by the one definition",
}

// decidersOfInside are the functions that decide whether a path read or written
// leads out of the root it is held to, keyed as sanctionedLinkResolvers is, each
// valued by what it judges. Each must be FOUND asking the one definition —
// pathscope.ResolvesInside, or a judgement built on it — so a decider that stops
// asking and decides for itself, or is renamed so nothing here exercises it, fails
// rather than passing over an empty walk.
var decidersOfInside = map[string]string{
	"pkg/writescope:check":                        "a write or a delete the product composes",
	"pkg/writescope:CheckWrite":                   "where a write through a link leading nowhere lands",
	"pkg/writescope:RootFor":                      "which side of the workspace a named directory lies on",
	"pkg/readscope:CheckRead":                     "a read beneath a directory read as a whole",
	"pkg/catalog:catalogWalk.walk":                "every entry of a catalog directory, and the directory itself",
	"pkg/resolver:scanReleaseDir":                 "the documents beside a Release that its names resolve against",
	"internal/sourceref:AnchorContent":            "every component of a content path an entity declares",
	"internal/wasm:Plugin.resolveUnderSourceRoot": "every component of a selection a plugin asks to read",
	"internal/serve:resolveLink":                  "a link the language server's walks meet, and a Release a request names",
	"internal/serve:gitignoreWalker.matcherFor":   "the ignore file the language server's entity walk reads",

	// Every path a request or a tool call names, on every transport, reaches the one
	// judgement through these, read or written alike: one request judgement in the
	// language server, which the assistant tools ask too, and the request resolvers
	// built on it. A resolver that judges a path for itself — lexically, or with a
	// second resolution — stops asking, and fails here.
	"internal/serve:linkScope.leadsOut":           "every path a request names, read or written, and a document the entity walk meets",
	"internal/serve:leadsOutOfWorkspace":          "every path a request names, with the configuration's Release exception",
	"internal/serve:LeadsOutOfWorkspace":          "a path an assistant-tool call names",
	"internal/serve:ReleaseLeadsOutOfWorkspace":   "a Release an assistant-tool call names",
	"internal/serve:resolveWorkspacePathParam":    "every path a language-server request names for the daemon to read",
	"internal/serve:Server.workspacePath":         "every path a language-server request names for the daemon to write",
	"internal/serve:Server.pickedOrWorkspacePath": "a relative key file or trust root a registration names",
	"internal/serve:insertionPointIn":             "the document an insertion point is located in",
	"internal/mcp:toolWorkspace.workspacePath":    "every path an assistant-tool call names over the listener",
	"internal/editops:resolveInside":              "the document an edit reads and writes back, on every surface",
}

// judgementsOfInside are the calls a decider is found asking.
var judgementsOfInside = map[string][]string{
	"pathscope":  {"ResolvesInside"},
	"readscope":  {"CheckRead"},
	"writescope": {"CheckWrite", "CheckDelete", "RootFor"},
	// The language server's request judgement, which the assistant tools ask.
	"serve": {"LeadsOutOfWorkspace", "ReleaseLeadsOutOfWorkspace"},
}

// judgementsWithinAPackage are the judgements built on the one definition that a
// decider in the same package asks by name — as a function, or as a method of a
// value it holds — keyed by the package directory. Each is itself a decider above,
// so the chain from every request resolver to the definition is held link by link.
var judgementsWithinAPackage = map[string][]string{
	"internal/serve": {"leadsOutOfWorkspace", "leadsOut", "workspacePath"},
}

// linkResolvers are the calls that resolve a link.
var linkResolvers = map[string][]string{
	"filepath": {"EvalSymlinks"},
	"os":       {"Readlink"},
}

// TestTheOneJudgementOfInsideHasNoSecondHome keeps the judgement of where a path
// leads on disk in the one function that makes it.
//
// The failure it guards is not a wrong rule written on purpose. It is a reader or
// a writer of workspace content resolving a link itself, because that was the
// nearer thing to write, and then comparing the result with a root: a second
// definition of inside, which agrees with the first on every input a test is
// likely to send and is the copy that stops agreeing first.
//
// It checks both directions. A function resolving a link must be one this file
// sanctions, with the reason it is not a judgement; and every function this file
// names as a decider must be found asking the one definition, so a decider that
// stops asking it fails here as well as in its own tests.
//
// It FAILS rather than reports, because a Go package that passes prints nothing,
// so a guard that only reported would be inert in every routine run.
//
// 🔴 What it cannot see, because a guard's reach is part of its contract: a
// second judgement written without resolving a link at all — a lexical prefix
// test of a path against a root, say — and a link resolved through a call other
// than the two named in linkResolvers. The first is the lexical question the
// package's other entry points answer, and a reader asking it where the on-disk
// question was owed is a defect its own tests, not this guard, have to catch.
func TestTheOneJudgementOfInsideHasNoSecondHome(t *testing.T) {
	fset := token.NewFileSet()
	resolving := map[string]bool{}
	asking := map[string]bool{}
	visited := 0
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, moduleRoot+string(filepath.Separator)))
		if d.IsDir() {
			if d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") && path != moduleRoot {
				return filepath.SkipDir
			}
			for _, dir := range devToolingDirs {
				if rel == dir {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		node, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		visited++
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := pkgDir + ":" + funcName(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, isIdent := call.Fun.(*ast.Ident); isIdent {
					if listed(judgementsWithinAPackage[pkgDir], ident.Name) {
						asking[key] = true
					}
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if listed(judgementsWithinAPackage[pkgDir], sel.Sel.Name) {
					asking[key] = true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if names(linkResolvers, pkg.Name, sel.Sel.Name) {
					if _, sanctioned := sanctionedLinkResolvers[key]; !sanctioned {
						t.Errorf("%s: %s.%s resolves a link in %s, outside the one definition of inside — ask "+
							"pathscope.ResolvesInside, which resolves the path and judges it against the root in one "+
							"place. If this resolution decides nothing about a path being inside, add the function to "+
							"sanctionedLinkResolvers in this file with the reason, in the same change.",
							fset.Position(call.Pos()), pkg.Name, sel.Sel.Name, key)
					}
					resolving[key] = true
				}
				if names(judgementsOfInside, pkg.Name, sel.Sel.Name) {
					asking[key] = true
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module's production source: %v", err)
	}
	// The walk's own reach is asserted rather than assumed: a guard that is negative
	// by default passes over an empty walk exactly as it passes over a clean tree.
	if visited == 0 {
		t.Fatal("the walk visited no production Go file — the guard is inert, not the tree clean")
	}

	// A set difference rather than a count, because both sides short by one still
	// agree on a count.
	if missing := absentFrom(sanctionedLinkResolvers, resolving); len(missing) > 0 {
		t.Errorf("sanctioned link resolvers the walk did not find resolving a link: %v — a function renamed, or "+
			"one that no longer resolves a link; drop or rename its entry, because a permission nothing exercises "+
			"reads as one something does", missing)
	}
	if missing := absentFrom(decidersOfInside, asking); len(missing) > 0 {
		t.Errorf("deciders the walk did not find asking the one definition of inside: %v — a decider that "+
			"stopped asking it decides for itself, and a renamed one leaves its entry judging nothing; restore the "+
			"call, or rename the entry in the same change", missing)
	}
}

// funcName spells a declaration as its key does: the receiver's type and a dot
// first for a method, pointer or not.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// names reports whether calls lists pkg.name.
func names(calls map[string][]string, pkg, name string) bool {
	return listed(calls[pkg], name)
}

// listed reports whether name is one of names.
func listed(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// absentFrom returns the keys of want missing from found, sorted.
func absentFrom(want map[string]string, found map[string]bool) []string {
	var missing []string
	for key := range want {
		if !found[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

// The module root this guard walks must hold the module, or every check above
// passes over nothing.
func TestTheGuardWalksThisModule(t *testing.T) {
	if _, err := os.Stat(filepath.Join(moduleRoot, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: %v — the guard would walk something other than this module", moduleRoot, err)
	}
}
