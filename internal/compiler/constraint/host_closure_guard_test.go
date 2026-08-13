// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The host-closure guard.
//
// A provider is a WebAssembly guest. The format-specific translation that turns
// a node's render input into Helm, compose, Terraform or CloudFormation content
// lives inside that guest and is compiled for wasip1; the host binary drives the
// render without linking any of it. That is what lets a provider be dropped in
// without rebuilding the host, and it is a claim about the dependency graph
// rather than about how any file is written.
//
// The language holds the host to part of it and not all of it, and the
// difference is what this guard covers. Each provider's translation core sits
// under the plugin tree's internal segment, so a host package importing one does
// not compile — that half is enforced by the compiler and needs no test. The
// shared cores that sit outside that segment are ordinary Go in the same module:
// a host package may import one and the result compiles, links and passes every
// test. The binary silently grows code that belongs to the guests, the drop-in
// property quietly stops holding, and nothing else says so. This guard reads the
// host binary's own dependency closure and refuses that edge.
//
// # Two edges, because there are two ways back in
//
// A package under plugins/ reached directly is the obvious one: plugins/ is
// where every guest, every shared core and every provider's translation core
// lives, and the guard refuses the whole tree rather than the part the compiler
// does not already reject. Refusing all of it costs nothing and keeps the guard
// correct if a core is ever moved back out of the internal segment.
//
// A per-format package re-created inside the render orchestration tree is the
// other, and it is the one that looks innocent. The render tree is host code by
// construction, so a package added under it is in the host closure by default;
// only its name says it is format-specific. Both edges are refused.
//
// # The format set is derived, never listed here
//
// The formats are the guests the tree actually carries — the *-provider
// directories under plugins/. Listing them here instead would produce a second
// copy of that set, and the copy is what goes stale: a guest added later would
// be absent from the list, and a host package named for it would pass a guard
// written to catch exactly that.
//
// # Scope, and one thing this guard deliberately does not claim
//
// The refusal is about the RENDER path. Naming a format is not by itself the
// violation: the host resolves a Recipe's declared source artifact, and that
// fetch is genuinely format-shaped — a chart is pulled differently from a Git
// module. Those adapters are host code on purpose, they sit outside the render
// orchestration tree, and this guard leaves them alone. What it refuses is
// format-specific *translation* reaching the host, which is the property the
// drop-in provider model rests on.
func TestHostClosureCarriesNoProviderGuestCode(t *testing.T) {
	t.Parallel()

	modulePath := hostModulePath(t)
	closure := hostDependencyClosure(t)

	// Anti-vacuity control. Every assertion below is a search for something
	// absent, so an instrument that returned nothing would report a clean
	// result while measuring nothing at all. The closure must at minimum
	// contain the binary it was taken for.
	if len(closure) == 0 {
		t.Fatalf("the dependency closure came back empty, so a clean result certifies nothing")
	}
	if !contains(closure, modulePath+"/cmd/aggrega") {
		t.Fatalf("the dependency closure does not contain the binary it was taken for (%s/cmd/aggrega), "+
			"so it is not the closure this guard means to read and its verdict means nothing",
			modulePath)
	}

	formats := providerFormats(t)
	if len(formats) == 0 {
		t.Fatalf("no provider guest directory was found under plugins/, so the format set is empty and " +
			"the per-format half of this guard would pass without checking anything")
	}

	renderTree := modulePath + "/internal/compiler/renderer"
	if countWithPrefix(closure, renderTree) == 0 {
		t.Fatalf("the closure contains no package under %s, so the per-format half of this guard has "+
			"nothing to range over and would pass vacuously", renderTree)
	}

	guests, perFormat := closureViolations(closure, modulePath, formats)

	if len(guests) > 0 {
		t.Errorf("the host binary links %d package(s) under plugins/, so the format-specific translation "+
			"a provider guest carries is now compiled into the host and the drop-in property no longer "+
			"holds: %s\nREMEDY: move the shared code into a package both sides import — the render-core "+
			"and render-context packages exist for exactly this — or keep it in the guest.",
			len(guests), strings.Join(guests, ", "))
	}

	if len(perFormat) > 0 {
		t.Errorf("the host binary links %d format-specific package(s) inside the render orchestration "+
			"tree, so the render path is no longer format-blind: %s\nREMEDY: a provider's translation "+
			"belongs in the plugin tree's internal segment, which the host cannot import, not under "+
			"the render orchestration tree.",
			len(perFormat), strings.Join(perFormat, ", "))
	}
}

// TestHostClosureGuardDetectsBothEdges calibrates the guard above.
//
// The guard passes today, and a passing guard proves nothing about its own
// instrument: a predicate that never fires and a property that always holds are
// indistinguishable from the result. So the same predicate the guard uses is run
// here over a synthetic closure carrying one violation of each kind, and it must
// report both. The other direction is checked too — a detector that fired on
// ordinary host packages would be switched off, and switching it off takes the
// real signal with it.
func TestHostClosureGuardDetectsBothEdges(t *testing.T) {
	t.Parallel()

	const modulePath = "example.com/m"
	formats := []string{"helm", "terraform"}

	fires := []string{
		modulePath + "/cmd/aggrega",
		modulePath + "/plugins/internal/helmcore",
		modulePath + "/internal/compiler/renderer",
		modulePath + "/internal/compiler/renderer/terraform",
	}

	guests, perFormat := closureViolations(fires, modulePath, formats)
	if len(guests) != 1 {
		t.Fatalf("the detector reported %d guest-package violation(s) over a closure carrying exactly "+
			"one, so it is not a working instrument and the guard's verdict means nothing: %v",
			len(guests), guests)
	}
	if len(perFormat) != 1 {
		t.Fatalf("the detector reported %d per-format violation(s) over a closure carrying exactly one, "+
			"so it is not a working instrument and the guard's verdict means nothing: %v",
			len(perFormat), perFormat)
	}

	// The negative control. Every entry here is a shape the host legitimately
	// carries, including the source-resolver adapters that are format-named on
	// purpose and the render-tree packages that are format-blind.
	clean := []string{
		modulePath + "/cmd/aggrega",
		modulePath + "/internal/compiler/renderer",
		modulePath + "/internal/compiler/renderer/wasmengine",
		modulePath + "/internal/compiler/renderer/enginefactory",
		modulePath + "/pkg/sourceresolver/helm",
		modulePath + "/pkg/sourceresolver/terraform",
		"helm.sh/helm/v3/pkg/chart",
	}

	guests, perFormat = closureViolations(clean, modulePath, formats)
	if len(guests) != 0 || len(perFormat) != 0 {
		t.Fatalf("the detector reported a violation over a closure of packages the host legitimately "+
			"carries, so it would fire on correct code: guests=%v perFormat=%v", guests, perFormat)
	}
}

// closureViolations partitions the refused edges out of a dependency closure:
// packages under plugins/, and packages inside the render orchestration tree
// whose final path segment names a provider format.
//
// It takes the closure, the module path and the format set as arguments rather
// than reading any of them, so the same predicate the guard runs over the real
// binary is the one the calibration runs over synthetic input. A calibration
// exercising a separate copy would prove that copy works.
func closureViolations(closure []string, modulePath string, formats []string) (guests, perFormat []string) {
	guestPrefix := modulePath + "/plugins/"
	renderPrefix := modulePath + "/internal/compiler/renderer/"

	isFormat := make(map[string]bool, len(formats))
	for _, f := range formats {
		isFormat[f] = true
	}

	for _, pkg := range closure {
		switch {
		case strings.HasPrefix(pkg, guestPrefix):
			guests = append(guests, pkg)
		case strings.HasPrefix(pkg, renderPrefix):
			// The segment immediately under the render tree is what names a
			// format; a deeper package inherits its parent's verdict.
			rest := strings.TrimPrefix(pkg, renderPrefix)
			if head, _, _ := strings.Cut(rest, "/"); isFormat[head] {
				perFormat = append(perFormat, pkg)
			}
		}
	}

	sort.Strings(guests)
	sort.Strings(perFormat)
	return guests, perFormat
}

// hostDependencyClosure returns every package the host binary links, read from
// the build tool rather than from any import scan. The tool resolves build
// constraints, which is the whole point: a guest core is excluded from the host
// build by its own tag set, and an import scan cannot see that.
func hostDependencyClosure(t *testing.T) []string {
	t.Helper()
	return goLines(t, "list", "-deps", "./cmd/aggrega")
}

// hostModulePath returns the module's own path, so every prefix this guard
// builds is derived rather than restated.
func hostModulePath(t *testing.T) string {
	t.Helper()

	lines := goLines(t, "list", "-m")
	if len(lines) == 0 {
		t.Fatalf("the build tool reported no module path, so every prefix this guard builds would be wrong")
	}
	return lines[0]
}

// providerFormats returns the format each provider guest under plugins/ serves,
// taken from the guest directory names.
func providerFormats(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(moduleRoot, "plugins"))
	if err != nil {
		t.Fatalf("read the guest directories under plugins/: %v", err)
	}

	var formats []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if format, ok := strings.CutSuffix(e.Name(), "-provider"); ok {
			formats = append(formats, format)
		}
	}
	sort.Strings(formats)
	return formats
}

// goLines runs the build tool in the module root and returns its non-empty
// output lines.
func goLines(t *testing.T, args ...string) []string {
	t.Helper()

	cmd := exec.Command("go", args...)
	cmd.Dir = moduleRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read the dependency graph (go %s in %s): %v\n%s",
			strings.Join(args, " "), moduleRoot, err, stderr.String())
	}

	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func contains(haystack []string, want string) bool {
	for _, h := range haystack {
		if h == want {
			return true
		}
	}
	return false
}

func countWithPrefix(haystack []string, prefix string) int {
	n := 0
	for _, h := range haystack {
		if strings.HasPrefix(h, prefix) {
			n++
		}
	}
	return n
}
