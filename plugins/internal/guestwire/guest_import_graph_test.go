// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package guestwire_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// modulePath is this module's import path, the prefix every package of it
// carries in a listing.
const modulePath = "github.com/aggrega-ai/aggrega-cli"

// hostOnlyPackages are the import paths no provider guest may link, each matched
// as itself or as any package beneath it: the host's plugin loader and the
// WebAssembly runtime and plugin SDK it is built on.
var hostOnlyPackages = []string{
	modulePath + "/internal/wasm",
	"github.com/tetratelabs/wazero",
	"github.com/extism/go-sdk",
}

// hostOnlyPackage reports which of hostOnlyPackages importPath is, or is beneath.
func hostOnlyPackage(importPath string) (string, bool) {
	for _, p := range hostOnlyPackages {
		if importPath == p || strings.HasPrefix(importPath, p+"/") {
			return p, true
		}
	}
	return "", false
}

// TestNoProviderGuestLinksTheHostRuntime asserts that the import graph every
// provider guest is built from — its main package, compiled for the WebAssembly
// target the guest builder uses — reaches none of hostOnlyPackages.
//
// The guests are read from the tree rather than listed here: every directory
// plugins/<name>-provider holding a Go package is one, so a provider added
// tomorrow is held to the property by being added, with no edit to this test.
//
// The graph is the build's own, from `go list -deps` under the guest target, so
// an import reached through any intermediate package counts exactly as a direct
// one does. A failure names the guest and the shortest import path from its main
// package to the host package it reached.
//
// The same computation is first run over a host package that does link the
// loader, and is required to find it there. Without that control a listing that
// came back empty or malformed would agree with a clean tree for the wrong reason.
func TestNoProviderGuestLinksTheHostRuntime(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	root := moduleRoot(t)

	const control = "internal/compiler/featurerecipe_pipeline/stepwalk"
	if reached := hostPackagesReached(t, goBin, root, control); len(reached) == 0 {
		t.Fatalf("the listing of %s, a host package that links the plugin loader, reached no host-only "+
			"package, so the same listing's silence over the guests would say nothing about them", control)
	}

	guests := providerGuests(t, root)
	if len(guests) == 0 {
		t.Fatalf("no provider guest found under %s; the assertion would pass over nothing",
			filepath.Join(root, "plugins"))
	}
	for _, guest := range guests {
		for _, path := range hostPackagesReached(t, goBin, root, guest) {
			t.Errorf("provider guest %s links a host-only package: %s\n"+
				"A guest never runs the host's plugin loader, so linking it only adds the runtime's bytes and "+
				"memory to the guest and moves its digest whenever the runtime's pin moves. Declare what the "+
				"guest needs in a package that imports no runtime, and keep host-only code out of the "+
				"packages a guest links.", guest, path)
		}
	}
}

// providerGuests returns every plugins/<name>-provider directory holding a Go
// package, relative to the module root, in sorted order.
func providerGuests(t *testing.T, root string) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(root, "plugins", "*-provider"))
	if err != nil {
		t.Fatalf("glob the provider guests: %v", err)
	}
	var out []string
	for _, dir := range dirs {
		gos, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		if len(gos) == 0 {
			continue
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatalf("relativize %s: %v", dir, err)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

// hostPackagesReached lists pkg's import graph under the guest build target and
// returns, for each host-only package the graph reaches, the shortest import path
// to it from pkg, written as the chain of import paths. It returns nothing when
// the graph reaches none.
func hostPackagesReached(t *testing.T, goBin, root, pkg string) []string {
	t.Helper()
	cmd := exec.Command(goBin, "list", "-deps", "-f", "{{.ImportPath}}|{{join .Imports \" \"}}", "./"+pkg)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list -deps ./%s for the guest target: %v\n%s", pkg, err, stderr)
	}

	imports := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path, deps, ok := strings.Cut(line, "|")
		if !ok {
			t.Fatalf("go list -deps ./%s: unreadable line %q", pkg, line)
		}
		imports[path] = strings.Fields(deps)
	}
	start := modulePath + "/" + pkg
	if _, ok := imports[start]; !ok {
		t.Fatalf("go list -deps ./%s did not list %s itself, so the graph read is not that package's", pkg, start)
	}

	// Breadth-first from the package, in the listing's own import order, so the
	// chain a failure names is a shortest one and the same one on every run.
	prev := map[string]string{start: ""}
	queue := []string{start}
	found := map[string]string{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if host, ok := hostOnlyPackage(cur); ok {
			if _, seen := found[host]; !seen {
				found[host] = cur
			}
			continue
		}
		for _, next := range imports[cur] {
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = cur
			queue = append(queue, next)
		}
	}

	var chains []string
	for _, host := range hostOnlyPackages {
		reached, ok := found[host]
		if !ok {
			continue
		}
		var chain []string
		for p := reached; p != ""; p = prev[p] {
			chain = append(chain, p)
		}
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
		chains = append(chains, strings.Join(chain, " -> "))
	}
	return chains
}
