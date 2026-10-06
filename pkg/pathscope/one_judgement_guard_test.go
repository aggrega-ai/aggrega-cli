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
	"pkg/readscope:checkRead":                     "a read of workspace content, whichever remedy its refusal names",
	"pkg/readscope:CheckRead":                     "a read beneath a directory read as a whole",
	"pkg/readscope:CheckConfigurationRead":        "a workspace configuration file the configuration is merged from",
	"pkg/readscope:CheckLockfileRead":             "the lockfile beside a Release, read for its pins",
	"pkg/readscope:CheckBesideEntityRead":         "a file read beside an entity's document",
	"internal/entitypin:RefuseSharedDirectory":    "the documents beside an entity, read to tell whether another entity shares its directory",
	"pkg/pluginpath:Resolver.readColocated":       "the .wasm beside a catalog Plugin entity, read by the plugin cascade",
	"pkg/lockfile:CheckRead":                      "the lockfile beside a Release, held to the root its write is held to",
	"pkg/workspaceconfig:resolve":                 "every configuration file the workspace's configuration cascade merges",
	"pkg/catalog:catalogWalk.walk":                "every entry of a catalog directory, and the directory itself",
	"pkg/resolver:scanReleaseDir":                 "the documents beside a Release that its names resolve against",
	"internal/sourceref:AnchorContent":            "every component of a content path an entity declares",
	"internal/wasm:Plugin.resolveUnderSourceRoot": "every component of a selection a plugin asks to read",
	"internal/serve:resolveLink":                  "a link the language server's walks meet, and a Release a request names",
	"internal/serve:gitignoreWalker.matcherFor":   "the ignore file the language server's entity walk reads",
	"pkg/workspaceconfig:Reaches":                 "whether a place is reached by a directory or a Release file somebody named",

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

	// Every reference a document makes to another file by a path reaches the one
	// judgement through readscope.CheckReference, at the point the file is about to
	// be opened, and every surface that resolves a Release's references resolves
	// them through these. A resolver of a surface's own — reading a name as a file
	// name and judging no path — stops asking, and fails here.
	"pkg/readscope:CheckReference":              "a reference a document makes to another file",
	"pkg/resolver:ResolveAggregate":             "the Aggregate a Release names by a path",
	"pkg/resolver:resolveManifest":              "the Manifest a Release names by a path, and a Manifest's extends",
	"pkg/resolver:resolveReleaseExtendsRef":     "the Release a Release's extends names",
	"pkg/resolver:LoadParametersImport":         "the parameters file a Release imports, chain link or not",
	"pkg/resolver:ManifestExtendsChain":         "each link of a Manifest's extends chain",
	"pkg/resolver:ReleaseExtendsChain":          "each link of a Release's extends chain",
	"pkg/resolver:releaseParameterContribution": "the parameters file each link of a Release's chain imports",
	"pkg/resolver:resolveFromInputs":            "the parameters file a Release heading no chain imports",
	"cmd/aggrega:renderReleaseFile":             "the Aggregate and the Manifest the inspect command draws a Release with",
}

// judgementsOfInside are the calls a decider is found asking.
var judgementsOfInside = map[string][]string{
	"pathscope":  {"ResolvesInside"},
	"readscope":  {"CheckRead", "CheckConfigurationRead", "CheckLockfileRead", "CheckBesideEntityRead", "CheckReference"},
	"writescope": {"CheckWrite", "CheckDelete", "RootFor"},
	// The language server's request judgement, which the assistant tools ask.
	"serve": {"LeadsOutOfWorkspace", "ReleaseLeadsOutOfWorkspace"},
	// The resolvers of a Release's aggregate and manifest references, which a
	// surface resolving a Release asks rather than reading the files itself.
	"resolver": {"ResolveAggregate", "ResolveManifest"},
}

// judgementsWithinAPackage are the judgements built on the one definition that a
// decider in the same package asks by name — as a function, or as a method of a
// value it holds — keyed by the package directory. Each is itself a decider above,
// so the chain from every request resolver to the definition is held link by link.
var judgementsWithinAPackage = map[string][]string{
	"internal/serve": {"leadsOutOfWorkspace", "leadsOut", "workspacePath"},
	"pkg/readscope":  {"checkRead"},
	"pkg/resolver":   {"resolveManifest", "resolveReleaseExtendsRef", "LoadParametersImport"},
}

// linkResolvers are the calls that resolve a link.
var linkResolvers = map[string][]string{
	"filepath": {"EvalSymlinks"},
	"os":       {"Readlink"},
}

// exceptionPredicates are the exceptions to the workspace boundary, each the one
// function that lets its class of path through, keyed as decidersOfInside is and
// valued by the class it answers for. Each must be FOUND declared, so a predicate
// renamed or folded away leaves no entry describing a class nothing answers.
//
// 🔴 Hand-written for the reason sanctionedLinkResolvers is: an entry is the
// decision that a class of path is let through and by which function, not a fact
// about the source.
//
// Two exceptions have no predicate, because nothing judges them: a path the owner
// types on the command line is used as typed, and the product's machine-wide
// stores lie where no workspace root holds them. What the product works out
// beneath either is still held — beneath a typed directory, by the anchor rule
// below.
var exceptionPredicates = map[string]string{
	"pkg/writescope:RootFor": "a directory the owner typed, or the configuration named, that lies outside the " +
		"workspace is a root of its own for what is composed or read beneath it",
	"pkg/writescope:RootForCache": "the artifact cache directory the configuration spells climbing out of the " +
		"workspace is a root of its own",
	"pkg/workspaceconfig:CatalogDirRoot": "a catalog directory somebody named, typed or configured, has the root " +
		"the anchor rule picks, and the conventional one nobody named is the workspace's",
	"pkg/workspaceconfig:Reaches": "what the configuration names — a catalog directory and what lies beneath it, " +
		"a Release file — or the owner typed, compared with a path by the place each leads to: the one comparison " +
		"of the configuration's exception, on every surface",
	"internal/serve:Server.selectedPath": "a file picked in a dialog, read where it names",
	"internal/serve:Server.canonicalFile": "a parameter naming a document the client holds open, which is " +
		"answered from what the client sent",
	"internal/mcp:toolWorkspace.workspacePath": "a path an assistant names over the process's own streams, " +
		"honoured as the owner typing it on the command line",
}

// exceptionPrimitives are calls that let a path through by their nature, each
// valued by the one predicate that may make it. pathscope.ResolveSelected resolves
// a path with no root to stay inside, which is right for a file a person picked
// and for nothing else, so a site reaching it elsewhere has admitted a path by a
// reasoning of its own. The path-scope package itself is exempt: it spells a path
// with it and admits nothing.
var exceptionPrimitives = map[string]string{
	"pathscope.ResolveSelected": "internal/serve:Server.selectedPath",
}

// theConfigurationsReach is the configuration exception's one comparison,
// workspaceconfig.Reaches: whether the place a path leads to is reached by a
// directory or a Release file somebody named — listed in the configuration, or a
// directory typed on the command line. It is the exception's one predicate, named
// in exceptionPredicates; this is how a call of it is recognised, as
// workspaceconfig.Reaches outside its package and as Reaches inside it.
const (
	theConfigurationsReachPackage = "workspaceconfig"
	theConfigurationsReachDir     = "pkg/workspaceconfig"
	theConfigurationsReach        = "Reaches"
)

// consultantsOfTheConfigurationsReach are the functions that let a path through,
// or decide where it is held, because somebody named the place it leads to, keyed
// as decidersOfInside is and valued by what each decides with the answer. Each must
// be FOUND asking theConfigurationsReach, and every function asking it must be one
// of them.
//
// 🔴 Hand-written for the reason sanctionedLinkResolvers is. Both directions are
// held because the exception had three comparisons — one by place, two by spelling,
// one of those counting a directory beneath a named one and the other not — which
// agreed on every layout a test was likely to build and disagreed on a `catalog`
// link to a listed directory: the language server named nothing unread while every
// catalog walk refused the link. A consultant that compares a path with what was
// named for itself, by spelling or by a resolution of its own, stops asking and
// fails here; a new site that asks and then admits a path for a reason of its own
// is not named here, and fails too.
var consultantsOfTheConfigurationsReach = map[string]string{
	"pkg/workspaceconfig:CatalogDirRoot": "whether the conventional catalog directory was named, and so which " +
		"root it is held to",
	"pkg/workspaceconfig:ResolvedWorkspace.ResolveCatalogDirs": "whether a stack already reads the place the " +
		"conventional catalog directory leads to, and so whether to append it",
	"internal/serve:linkScope.configurationReaches": "whether a link the language server's walks meet, or a " +
		"Release a request names, leads to a place the configuration names",
}

// predicatesThatReadNothing are the exception predicates whose class is defined by
// reading nothing at the path, each valued by why. Neither the predicate nor any
// function of its own package it calls may reach the filesystem or a judgement of
// inside: one that did would be reading the path the class exists not to read.
var predicatesThatReadNothing = map[string]string{
	"internal/serve:Server.canonicalFile": "a document the client holds open is answered from what the client " +
		"sent, so the key it is kept under is spelled without reading anything at its path",
}

// readsTheFilesystem are the calls that read, list or resolve something on disk.
// An empty list stands for every function of the package.
var readsTheFilesystem = map[string][]string{
	"os":       {},
	"ioutil":   {},
	"fs":       {"ReadFile", "ReadDir", "Stat", "Glob", "WalkDir", "Sub"},
	"filepath": {"EvalSymlinks", "Glob", "Walk", "WalkDir"},
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
// stops asking it fails here as well as in its own tests. The reverse holds too:
// a function asking the one definition itself must be a decider this file names,
// because a site that asks it and then lets a path through for a reason of its
// own has become a second home of an exception.
//
// The exceptions are held by class. Each class's predicate is named in
// exceptionPredicates and must be found; a call that lets a path through by its
// nature is reached only by the predicate exceptionPrimitives gives it; a
// predicate whose class is reading nothing at the path — a document the client
// holds open — is held to reaching nothing on disk, through any function of its
// own package it calls; and the configuration's exception has one comparison,
// which every consultant named in consultantsOfTheConfigurationsReach must be
// found asking and nothing else may ask.
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
// Likewise a path let through by asking nothing at all, which is how the command
// line's typed paths and the machine-wide stores are let through by design; in the
// read-nothing check, a method called on a value other than the function's own
// receiver; and a consultant of the configuration's exception that asks its one
// comparison and ALSO compares a spelling of its own beside it.
func TestTheOneJudgementOfInsideHasNoSecondHome(t *testing.T) {
	fset := token.NewFileSet()
	resolving := map[string]bool{}
	asking := map[string]bool{}
	declared := map[string]declaredFunc{}
	reachedPrimitive := map[string]bool{}
	consulting := map[string]bool{}
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
			declared[key] = declaredFunc{pkgDir: pkgDir, fn: fn}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, isIdent := call.Fun.(*ast.Ident); isIdent {
					if listed(judgementsWithinAPackage[pkgDir], ident.Name) {
						asking[key] = true
					}
					if pkgDir == theConfigurationsReachDir && ident.Name == theConfigurationsReach {
						consultTheConfigurationsReach(t, fset, call, key, consulting)
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
				if pkg.Name == theConfigurationsReachPackage && sel.Sel.Name == theConfigurationsReach {
					consultTheConfigurationsReach(t, fset, call, key, consulting)
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
				if pkg.Name == "pathscope" && sel.Sel.Name == "ResolvesInside" {
					if _, decider := decidersOfInside[key]; !decider {
						t.Errorf("%s: pathscope.ResolvesInside asked directly in %s, which this file does not name as "+
							"a decider — a site that asks the one definition and then acts on the answer for a reason "+
							"of its own is a second home of a judgement or an exception. Ask the judgement or the "+
							"exception that already answers the question; if this is a new decider, add it to "+
							"decidersOfInside with what it judges, in the same change.",
							fset.Position(call.Pos()), key)
					}
				}
				if predicate, primitive := exceptionPrimitives[pkg.Name+"."+sel.Sel.Name]; primitive && pkgDir != "pkg/pathscope" {
					if key != predicate {
						t.Errorf("%s: %s.%s lets a path through in %s, and only %s may — it is the one place that "+
							"class of path is admitted. Route the path through that predicate, or through the "+
							"judgement its own class is held to.",
							fset.Position(call.Pos()), pkg.Name, sel.Sel.Name, key, predicate)
					} else {
						reachedPrimitive[key] = true
					}
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

	// The exceptions, by class.
	found := map[string]bool{}
	for key := range declared {
		found[key] = true
	}
	if missing := absentFrom(exceptionPredicates, found); len(missing) > 0 {
		t.Errorf("exception predicates the walk did not find declared: %v — a predicate renamed, or folded into "+
			"another function; rename or drop its entry in the same change, because an entry naming nothing leaves "+
			"its class answered by a function that does not exist", missing)
	}
	if missing := absentFrom(consultantsOfTheConfigurationsReach, consulting); len(missing) > 0 {
		t.Errorf("functions the walk did not find asking %s.%s, the configuration exception's one comparison: %v — "+
			"a site that stopped asking compares what was named with a path for itself, which is the second "+
			"comparison this guard exists to refuse; restore the call, or rename the entry in "+
			"consultantsOfTheConfigurationsReach in the same change", theConfigurationsReachPackage, theConfigurationsReach, missing)
	}
	owners := map[string]string{}
	for primitive, predicate := range exceptionPrimitives {
		owners[predicate] = primitive
	}
	if missing := absentFrom(owners, reachedPrimitive); len(missing) > 0 {
		t.Errorf("exception predicates the walk did not find reaching the call that lets their class through: %v — "+
			"a predicate that stopped making the call leaves the call owned by nothing; restore it, or move the "+
			"entry in exceptionPrimitives in the same change", missing)
	}
	for _, key := range sortedKeys(predicatesThatReadNothing) {
		if !found[key] {
			t.Errorf("a predicate held to reading nothing was not found declared: %s — rename or drop its entry "+
				"in predicatesThatReadNothing in the same change", key)
			continue
		}
		if site, call, what := filesystemReachOf(declared, key); call != nil {
			t.Errorf("%s: %s reaches %s in %s, through the functions of its own package it calls — %s; reading "+
				"anything at the path would make it a judgement its class does not make",
				fset.Position(call.Pos()), key, what, site, predicatesThatReadNothing[key])
		}
	}
}

// consultTheConfigurationsReach records that the function key asks the
// configuration exception's one comparison, and fails where key is not a
// consultant named in consultantsOfTheConfigurationsReach — the comparison itself
// excepted, which may delegate to a helper of its own.
func consultTheConfigurationsReach(t *testing.T, fset *token.FileSet, call *ast.CallExpr, key string, consulting map[string]bool) {
	t.Helper()
	if key == theConfigurationsReachDir+":"+theConfigurationsReach {
		return
	}
	if _, named := consultantsOfTheConfigurationsReach[key]; !named {
		t.Errorf("%s: %s.%s asked in %s, which this file does not name as a consultant of the configuration's "+
			"exception — a site that asks whether somebody named a path's place and then lets the path through, or "+
			"holds it, for a reason of its own is a second home of that exception. Ask the consultant that already "+
			"answers the question; if this is a new consultant, add it to consultantsOfTheConfigurationsReach with "+
			"what it decides, in the same change.",
			fset.Position(call.Pos()), theConfigurationsReachPackage, theConfigurationsReach, key)
	}
	consulting[key] = true
}

// declaredFunc is a production function the walk met, with the package directory
// it sits in.
type declaredFunc struct {
	pkgDir string
	fn     *ast.FuncDecl
}

// filesystemReachOf follows the function start names, and every function of its
// own package it calls — by name, or as a method of its own receiver — and
// returns the first call met that reaches the filesystem or a judgement of
// inside: the function it sits in, the call, and the call as written. The call is
// nil where none is met.
func filesystemReachOf(declared map[string]declaredFunc, start string) (string, *ast.CallExpr, string) {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		d := declared[key]
		recvName, recvType := receiverOf(d.fn)
		var hit *ast.CallExpr
		var what string
		ast.Inspect(d.fn.Body, func(n ast.Node) bool {
			if hit != nil {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				callee = d.pkgDir + ":" + fun.Name
			case *ast.SelectorExpr:
				x, isIdent := fun.X.(*ast.Ident)
				if !isIdent {
					return true
				}
				if recvName != "" && x.Name == recvName && recvType != "" {
					callee = d.pkgDir + ":" + recvType + "." + fun.Sel.Name
					break
				}
				if reaches(readsTheFilesystem, x.Name, fun.Sel.Name) || names(linkResolvers, x.Name, fun.Sel.Name) ||
					names(judgementsOfInside, x.Name, fun.Sel.Name) {
					hit, what = call, x.Name+"."+fun.Sel.Name
					return false
				}
			}
			if _, inPackage := declared[callee]; inPackage && !seen[callee] {
				seen[callee] = true
				queue = append(queue, callee)
			}
			return true
		})
		if hit != nil {
			return key, hit, what
		}
	}
	return "", nil, ""
}

// receiverOf returns the name a method's receiver is bound to and the type it
// belongs to, pointer or not; both are empty for a plain function.
func receiverOf(fn *ast.FuncDecl) (string, string) {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "", ""
	}
	field := fn.Recv.List[0]
	name := ""
	if len(field.Names) > 0 {
		name = field.Names[0].Name
	}
	recv := field.Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return name, ident.Name
	}
	return name, ""
}

// reaches reports whether calls names pkg.name, an empty list naming every
// function of pkg.
func reaches(calls map[string][]string, pkg, name string) bool {
	list, ok := calls[pkg]
	return ok && (len(list) == 0 || listed(list, name))
}

// sortedKeys returns the keys of m, sorted, so a failure names the same entry
// first on every run.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
