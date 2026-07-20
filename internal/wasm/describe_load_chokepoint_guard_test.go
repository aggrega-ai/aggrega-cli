// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// describeLoadModuleRoot is the aggrega-cli module root relative to this
// package directory (internal/wasm).
const describeLoadModuleRoot = "../.."

// scanForCallSites walks every non-test Go source file under the module root and
// returns, keyed by module-root-relative slash path, the files whose body
// contains a call site of the given method spelling — matched as ".<method>("
// so a method call is caught while the method definition and godoc prose (which
// carry no leading dot and trailing paren together) are not.
func scanForCallSites(t *testing.T, method string) map[string]bool {
	t.Helper()
	needle := "." + method + "("
	hits := map[string]bool{}
	walkModuleGoSource(t, func(rel string, src []byte, isTest bool) {
		if isTest {
			return
		}
		if strings.Contains(string(src), needle) {
			hits[rel] = true
		}
	})
	return hits
}

// walkModuleGoSource walks every Go source file under the module root and calls
// visit with the file's module-root-relative slash path, its bytes, and whether
// it is a test file.
func walkModuleGoSource(t *testing.T, visit func(rel string, src []byte, isTest bool)) {
	t.Helper()
	err := filepath.Walk(describeLoadModuleRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(describeLoadModuleRoot, path)
		if err != nil {
			return err
		}
		visit(filepath.ToSlash(rel), src, strings.HasSuffix(path, "_test.go"))
		return nil
	})
	if err != nil {
		t.Fatalf("walk module source: %v", err)
	}
}

// TestDescribeLoadDoorsSingleSourced pins the describe-load doors closed so a
// future introspection site cannot open a second one. It asserts that the exact
// set of production files calling LoadAndDescribe — the real-execution load gate
// that binds real host functions — equals the real-execution allowlist. A
// surface that only reads a guest's describe() self-report must route through
// LoadForDescribe (inert host-function stubs, union import allowlist), never
// LoadAndDescribe; a new LoadAndDescribe caller outside the render, pipeline,
// parser-socket, and publishing-linter execution paths trips this guard.
//
// If a legitimate new real-execution caller is added, update the allowlist
// consciously — the failure is the point: it forces the author to confirm the
// new caller genuinely executes the guest rather than merely introspecting it.
func TestDescribeLoadDoorsSingleSourced(t *testing.T) {
	got := scanForCallSites(t, "LoadAndDescribe")
	want := []string{
		"cmd/aggrega/plugin_run_local.go",
		"internal/compiler/parsersocket/parsersocket.go",
		"internal/compiler/pipeline/wasm_sockets.go",
		"internal/compiler/renderer/pipeline_exec.go",
		"internal/compiler/renderer/wasmengine/engine.go",
		"pkg/validator/publishing_linter_dispatch.go",
	}
	gotList := make([]string, 0, len(got))
	for f := range got {
		gotList = append(gotList, f)
	}
	sort.Strings(gotList)
	sort.Strings(want)
	if strings.Join(gotList, ",") != strings.Join(want, ",") {
		t.Errorf("production callers of LoadAndDescribe = %v; want exactly %v\n"+
			"a describe-only introspection surface must load through LoadForDescribe, not the real-execution LoadAndDescribe; "+
			"if you added a legitimate real-execution caller, update this allowlist consciously", gotList, want)
	}
}

// TestRuntimeCLILoadSingleSourced pins LoadCLI — the runtime CLI load that binds
// real streaming host functions to run a command — to its sole execution site.
// It asserts that the exact set of production files calling LoadCLI equals the
// runtime-execution allowlist: only the CLI runner, which loads the guest and
// invokes its command export. A describe-only surface that reads a CLI guest's
// self-report must route through LoadForDescribe (inert host-function stubs,
// union import allowlist), never LoadCLI; a new LoadCLI caller outside the runner
// trips this guard.
//
// The definition of LoadCLI carries no leading dot before the name, so
// scanForCallSites (which matches ".LoadCLI(") sees call sites only, not the
// method declaration.
func TestRuntimeCLILoadSingleSourced(t *testing.T) {
	got := scanForCallSites(t, "LoadCLI")
	want := []string{
		"internal/cliplugin/runner.go",
	}
	gotList := make([]string, 0, len(got))
	for f := range got {
		gotList = append(gotList, f)
	}
	sort.Strings(gotList)
	sort.Strings(want)
	if strings.Join(gotList, ",") != strings.Join(want, ",") {
		t.Errorf("production callers of LoadCLI = %v; want exactly %v\n"+
			"a describe-only site must load through LoadForDescribe, not the runtime-execution LoadCLI; "+
			"LoadCLI is reserved for the CLI runner that actually executes the guest's command", gotList, want)
	}
}

// TestNoSecondDescribeLoadDoor proves the removed CLI-specific describe-load
// method stays removed: no source file anywhere — production or test —
// references it. The describe-only profile (LoadForDescribe) admits a streaming
// CLI guest through its union import allowlist, so a separate CLI describe-load
// gate is redundant; reintroducing one would fracture the describe-load surface
// back into two doors. The search token is assembled from fragments so this
// guard's own source does not register as a reference.
func TestNoSecondDescribeLoadDoor(t *testing.T) {
	token := "LoadCLI" + "AndDescribe"
	var found []string
	walkModuleGoSource(t, func(rel string, src []byte, _ bool) {
		if strings.Contains(string(src), token) {
			found = append(found, rel)
		}
	})
	if len(found) != 0 {
		sort.Strings(found)
		t.Errorf("the removed CLI-specific describe-load method is referenced in %v; "+
			"it was consolidated into LoadForDescribe and must not return", found)
	}
}
