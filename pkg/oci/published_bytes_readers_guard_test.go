// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// This guard exists because of how the bundle-blind readers arose: one site was
// written correctly and four beside it were written the same careless way,
// independently, with nothing able to notice they disagreed. The unwrapper is
// unexported, so the two-line shape test can no longer be re-rolled anywhere —
// but the compiler cannot notice a reader that never asks the question at all,
// and that is the shape every one of the four had.
//
// So the enforcement is an enumeration. A function that obtains bytes from a
// registry coordinate or from the content cache is a READER of published bytes,
// and every reader is named below with what it does with them. A new one fails
// this test until it is named, which is the moment somebody has to decide
// whether it parses and therefore owes oci.EntityYAML.
//
// # What it catches, and what it structurally cannot
//
// It matches CALL NAMES syntactically, so it needs no type information and
// cannot be defeated by an alias, a variable rename, or an interface. It cannot
// see a reader that obtains its bytes some other way — handed them by a caller,
// read from a file, decoded from a payload — and it does not judge what a listed
// function does with them. Its whole job is to make the set of readers a thing
// somebody has to edit rather than a thing nobody can see.

// publishedByteSources are the calls that yield bytes as a registry served them.
// A function calling one of these holds an artifact, in whatever shape its kind
// publishes as.
var publishedByteSources = map[string]bool{
	"Pull":                    true, // a registry fetch by reference + tag
	"PullPublishedEntity":     true, // the fetch of whatever descriptor carries a coordinate's entity
	"PullEntity":              true, // the kind-bound entity fetch
	"PullLibrary":             true, // the kind-bound library fetch
	"PullAggregate":           true, // the bare-name Aggregate fetch
	"ReadCached":              true, // the content blob of a cache slot
	"tier2ReadVerified":       true, // the pin-verified cache read
	"tier2ReadSlotUnverified": true, // the unverified cache read beneath it

	// Pass-throughs. These do not fetch, they HAND ON what a fetch returned, so a
	// caller of one holds an artifact exactly as a caller of a fetch does. Listing
	// them is what stops the enumeration stopping at the first hop and declaring
	// the tier that forwards the bytes to be the only reader of them.
	// PullAndCacheAggregate is deliberately NOT here: it hands back the entity
	// document rather than the artifact, so its callers hold a document and are
	// not readers of published bytes at all.
	"tier2Verified":          true, // the lazy lookup's verified Tier-2 read
	"tier3PullCatalogEntity": true, // the entity Tier-3 pull primitive
}

// publishedByteReaders is the recorded set. The key is
// `<package-relative-dir>.[<receiver>.]<function>`; the value says what that
// function does with the bytes, which is the fact a reviewer needs and the one
// no pattern can derive.
//
// Adding an entry is the point of the failure, not a way around it: write what
// the function does with the bytes, and if the answer is "parses them", it goes
// through oci.EntityYAML first.
var publishedByteReaders = map[string]string{
	// --- the transport itself: it yields bytes and reads none of them ---
	"pkg/catalog.DefaultOCIPuller.Pull":            "the fetch; returns what the registry served, parses nothing",
	"pkg/catalog.DefaultOCIPuller.PullEntity":      "the kind-bound fetch; returns what the registry served, parses nothing",
	"pkg/catalog.DefaultOCIPuller.PullLibrary":     "the kind-bound fetch; returns what the registry served, parses nothing",
	"pkg/catalog.DefaultOCIPuller.PullAggregate":   "the bare-name fetch; returns what the registry served, parses nothing",
	"pkg/catalog.tier2ReadVerified":                "the pin-verified cache read; verifies bytes against the pin and returns them unread",
	"pkg/catalog.catalogLookup.tier2":              "hands the slot's bytes to the cascade after the reuse-check; parses nothing",
	"pkg/catalog.LazyLookup.tier2Verified":         "hands the slot's bytes to the cascade under the verify posture; parses nothing",
	"pkg/catalog.LazyLookup.VerifyPinnedIntegrity": "verifies every pinned slot and discards the bytes; nothing is parsed",

	// --- entity readers: each takes the document through oci.EntityYAML ---
	"pkg/catalog.tier3PullCatalogEntity":                "pins the entity's own metadata.version, read through oci.EntityYAML",
	"pkg/catalog.catalogLookup.tier3Library":            "pins the library's own metadata.version, read through oci.EntityYAML",
	"pkg/catalog.PullAndCacheAggregate":                 "pins and returns the Aggregate document, both read through oci.EntityYAML",
	"pkg/catalog.catalogLookup.LoadAggregate":           "parses a sub-aggregate from its cache slot, read through oci.EntityYAML",
	"pkg/catalog.LazyLookup.foldPinnedCacheEntities":    "folds pinned slots by kind, each read through oci.EntityYAML",
	"pkg/catalog.LazyLookup.resolveComponentContext":    "parses a Component, read through entityDocumentIn (oci.EntityYAML)",
	"pkg/catalog.LazyLookup.resolveFeatureContext":      "parses a Feature, read through entityDocumentIn (oci.EntityYAML)",
	"pkg/catalog.LazyLookup.resolveComponentForClosure": "parses a Component for the walk, read through entityDocumentIn (oci.EntityYAML)",
	"pkg/catalog.LazyLookup.resolveFeatureForClosure":   "parses a Feature for the walk, read through entityDocumentIn (oci.EntityYAML)",
	"pkg/catalog.catalogLookup.tier3CatalogEntity":      "forwards the entity tier's answer and its resolution; parses nothing",
	"internal/lintops.fetchPreviousRegistryVersion":     "converts the previous version for the evolution comparison, read through oci.EntityYAML",
	"internal/compilerops.ReadPublishedEntity":          "reads the entity a reference publishes for a person to see, read through oci.EntityYAML; caches and pins nothing",

	// --- not entity documents: a plugin binary, its manifest, or another payload ---
	"pkg/catalog.LazyLookup.PluginFromCacheByURI":              "a Plugin entity from the manifest's config blob; a Plugin publishes as a manifest, never through the bundler",
	"pkg/catalog.catalogLookup.readCachedPluginArtifact":       "a plugin .wasm artifact verified against its manifest; never an entity document",
	"pkg/catalog.catalogLookup.resolveVersionedPluginArtifact": "a plugin .wasm artifact addressed by content digest; never an entity document",
	"pkg/pluginpath.Resolver.ResolveContext":                   "a plugin .wasm artifact from its cache slot; never an entity document",
	"internal/pullops.verifyCachedVerdictSocketPlugin":         "a plugin .wasm artifact from its cache slot; never an entity document",
	"internal/pullops.verifyPluginHostFunctions":               "a plugin .wasm artifact re-derived against its stamp; never an entity document",
	"cmd/aggrega.productionInstallPluginFetch":                 "a plugin artifact fetched for installation; never an entity document",
	"pkg/docscache.ResolveAndCache":                            "a documentation bundle, refused unless it carries the docs mediatype; never an entity document",
	"pkg/externalinputs.Service.ociPull":                       "an external input artifact; never an entity document",
	"pkg/sourceresolver/helm.ociAdapter.Resolve":               "a Helm chart artifact cached by its own digest; never an entity document",

	// --- matched by name only: not a registry read at all ---
	"internal/buildops.ensureLockfile": "calls the configured PULL RUN, not a registry fetch; it holds no artifact bytes. Matched because this guard reads call NAMES and cannot tell the two apart",
}

func TestEveryReaderOfPublishedBytesIsAccountedFor(t *testing.T) {
	root := moduleRoot(t)
	found := map[string][]string{}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules") {
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
			return nil // a file this parser cannot read is reported by the compiler, not here
		}
		rel, _ := filepath.Rel(root, path)
		dir := filepath.ToSlash(filepath.Dir(rel))
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := dir + "." + funcIdentity(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if name := calleeName(call.Fun); publishedByteSources[name] {
					found[key] = append(found[key], name)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Calibration: an empty walk would agree with any allowlist at all.
	if len(found) == 0 {
		t.Fatal("no reader of published bytes was found anywhere in the module; the walk matched nothing and would agree with any list")
	}

	var unlisted, stale []string
	for key := range found {
		if _, ok := publishedByteReaders[key]; !ok {
			unlisted = append(unlisted, key+"  (calls "+strings.Join(dedupe(found[key]), ", ")+")")
		}
	}
	for key := range publishedByteReaders {
		if _, ok := found[key]; !ok {
			stale = append(stale, key)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)

	if len(unlisted) > 0 {
		t.Errorf("these functions read bytes as a registry served them and are not recorded.\n"+
			"Bytes from a coordinate arrive in whatever shape their kind publishes as, and two kinds publish as an archive:\n"+
			"a function that PARSES them takes them through oci.EntityYAML first, and one that does not still says so in\n"+
			"publishedByteReaders. Unrecorded:\n  %s", strings.Join(unlisted, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("these entries name no function that reads published bytes any more; a stale entry silently covers "+
			"whatever later takes the name:\n  %s", strings.Join(stale, "\n  "))
	}
}

func funcIdentity(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return receiverTypeName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	default:
		return "?"
	}
}

func calleeName(expr ast.Expr) string {
	switch f := expr.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	default:
		return ""
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, serr := os.Stat(filepath.Join(dir, "go.mod")); serr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory; the walk below would have no root")
		}
		dir = parent
	}
}
