// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The terraform worked examples render through the full build path against the
// sibling catalog. These tests exercise both Terraform emission shapes end to
// end and assert the Deterministic Output invariant on the rendered dist/ tree:
// two independent builds of the same inputs produce byte-identical output.
//
// The examples resolve against the sibling aggrega-hub catalog, so the tests
// skip when that sibling checkout is absent (a standalone aggrega-cli clone).
// They need no OCI registry or other integration-only dependency, so they run
// in the default suite rather than under -tags=integration.

// siblingCatalogRootForTF returns repo-root/aggrega-hub/aggrega-catalog from
// cmd/aggrega, skipping when the sibling checkout is absent.
//
// It also skips while the sibling catalog's Plugin entities still declare a
// standalone plugin-artifact field: the entity schema now derives a plugin's
// .wasm location from its identity, so an entity carrying that legacy field is
// schema-rejected on load and its provider never resolves. The catalog is
// regenerated to the identity-derived shape out of band; the skip lifts
// automatically once that regeneration lands, so no re-coupling edit is needed.
func siblingCatalogRootForTF(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Clean(filepath.Join(wd, "..", "..", "..", "aggrega-hub", "aggrega-catalog"))
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("sibling catalog not present at %s: %v", dir, err)
	}
	if hubPluginEntitiesCarryLegacyArtifactField(dir) {
		t.Skipf("sibling catalog Plugin entities under %s still declare a standalone plugin-artifact field; skipping until the catalog is regenerated to the identity-derived shape", dir)
	}
	return dir
}

// terraformExampleCatalogStack returns the catalog search stack an example build
// runs against: a seeded provider overlay ahead of the sibling catalog. The
// sibling catalog declares the terraform provider under its own published
// version, while capability resolution selects the version the binary embeds, so
// without the overlay the lockfile walk would find no local entity at the
// selected version and reach for a registry.
//
// The stack is built once per test and reused across runs that must be compared
// byte for byte, so both runs read the same inputs.
func terraformExampleCatalogStack(t *testing.T, catalogRoot string) []string {
	t.Helper()
	return []string{seedEmbeddedProviderOverlay(t, "provider/terraform"), catalogRoot}
}

// hubPluginEntitiesCarryLegacyArtifactField reports whether any Plugin entity in
// the sibling catalog still declares a top-level spec plugin-artifact field —
// the legacy shape the entity schema no longer accepts. It is a best-effort
// scan: an unreadable tree reports false so the caller proceeds and any real
// breakage surfaces rather than being masked.
func hubPluginEntitiesCarryLegacyArtifactField(catalogRoot string) bool {
	matches, err := filepath.Glob(filepath.Join(catalogRoot, "plugin", "*", "*", "*", "plugin.yaml"))
	if err != nil {
		return false
	}
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "  plugin:") {
				return true
			}
		}
	}
	return false
}

// terraformExampleDir returns the worked-example directory for name, skipping
// when the sibling docs checkout is absent.
func terraformExampleDir(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Clean(filepath.Join(wd, "..", "..", "..", "aggrega-docs", "language-reference", "examples", name))
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("example %q not present at %s: %v", name, dir, err)
	}
	return dir
}

// copyExampleInputs copies an example's committed inputs into dst, skipping the
// regenerated build artifacts (dist/, *.lock, .aggrega/) and the example's
// workspace config (its relative catalog_dirs do not resolve from a temp copy;
// the catalog is supplied to the build explicitly instead).
func copyExampleInputs(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		top := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		switch top {
		case "dist", ".aggrega", ".trace":
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if strings.HasSuffix(rel, ".lock") || rel == ".aggrega-config.yaml" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy example inputs %s -> %s: %v", src, dst, err)
	}
}

// readTFDistTree reads every file under root/dist into a path->content map keyed
// by dist-relative slash paths.
func readTFDistTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	distRoot := filepath.Join(root, "dist")
	out := map[string][]byte{}
	err := filepath.WalkDir(distRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(distRoot, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("read dist tree under %s: %v", distRoot, err)
	}
	if len(out) == 0 {
		t.Fatalf("no files emitted under %s", distRoot)
	}
	return out
}

// buildExampleToDist copies the example (an absolute source directory) into a
// fresh temp workspace, builds it against the sibling catalog, and returns the
// rendered dist tree. src and catalogRoot must be absolute so the function is
// safe to call repeatedly across the working-directory changes each build makes.
func buildExampleToDist(t *testing.T, src string, catalogStack []string) map[string][]byte {
	t.Helper()
	ws := t.TempDir()
	copyExampleInputs(t, src, ws)
	t.Chdir(ws)

	prevRelease, prevCatalog, prevTrace := releasePath, catalogDirs, traceEnabled
	t.Cleanup(func() {
		releasePath, catalogDirs, traceEnabled = prevRelease, prevCatalog, prevTrace
	})
	releasePath = "release.yaml"
	catalogDirs = catalogStack
	traceEnabled = false

	// Build is read-only: prime the lockfile with an explicit pull (output discarded),
	// then build reads it. Each call runs in its own fresh workspace, so the two
	// determinism runs pull independently and stay byte-identical.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catalogDirs, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("prime lockfile via pull for example %q: %v", src, err)
	}

	if err := dispatchBuild(nil, releasePath, catalogDirs, traceEnabled, io.Discard, io.Discard); err != nil {
		t.Fatalf("build example %q: %v", src, err)
	}
	return readTFDistTree(t, ws)
}

// assertByteIdenticalTrees fails when two dist trees differ in file set or in
// any file's bytes — the Deterministic Output invariant across independent runs.
func assertByteIdenticalTrees(t *testing.T, a, b map[string][]byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("dist file count differs across runs: %d vs %d\nrun1: %v\nrun2: %v",
			len(a), len(b), sortedKeys(a), sortedKeys(b))
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			t.Errorf("file %q present in run1 but absent in run2", k)
			continue
		}
		if !bytes.Equal(va, vb) {
			t.Errorf("file %q not byte-identical across runs\n--- run1 ---\n%s\n--- run2 ---\n%s", k, va, vb)
		}
	}
}

func sortedKeys(m map[string][]byte) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// distFile fetches the single dist file whose key ends in suffix.
func distFile(t *testing.T, tree map[string][]byte, suffix string) []byte {
	t.Helper()
	var hit []byte
	var matched string
	for k, v := range tree {
		if strings.HasSuffix(k, suffix) {
			if matched != "" {
				t.Fatalf("two dist files end in %q: %q and %q", suffix, matched, k)
			}
			hit, matched = v, k
		}
	}
	if matched == "" {
		t.Fatalf("no dist file ends in %q; keys: %v", suffix, sortedKeys(tree))
	}
	return hit
}

// TestTerraformArtifactsExampleRenderDeterminism drives the terraform-rds
// example (inline HCL artifact body) through two independent builds and asserts
// the rendered main.tf is byte-identical across runs, opens with the provenance
// comment block, and carries the resolved HCL (operator sizing substituted,
// RDS-managed credentials).
func TestTerraformArtifactsExampleRenderDeterminism(t *testing.T) {
	catalogRoot := siblingCatalogRootForTF(t)
	src := terraformExampleDir(t, "terraform-rds")

	// One overlay for both runs, so the two builds see byte-identical inputs.
	stack := terraformExampleCatalogStack(t, catalogRoot)

	run1 := buildExampleToDist(t, src, stack)
	run2 := buildExampleToDist(t, src, stack)
	assertByteIdenticalTrees(t, run1, run2)

	mainTF := distFile(t, run1, "main.tf")
	if !bytes.HasPrefix(mainTF, []byte("# Aggrega build provenance\n")) {
		t.Errorf("main.tf must open with the provenance comment block; got:\n%s", mainTF)
	}
	for _, want := range []string{
		"# aggrega.ai/provider: terraform",
		`resource "aws_db_instance" "db"`,
		"port           = 3306",
		"allocated_storage = 100",
		"manage_master_user_password = true",
	} {
		if !bytes.Contains(mainTF, []byte(want)) {
			t.Errorf("main.tf missing expected substring %q; got:\n%s", want, mainTF)
		}
	}
}

// TestTerraformSourcedExampleRenderDeterminism drives the terraform-rds-module
// example (vendored local module + overlay) through two independent builds and
// asserts the rendered tree is byte-identical across runs, the module files
// vendor verbatim, the aggrega.auto.tfvars.json overlay carries the resolved
// variables with a Terraform ${...} pointer passing through untouched, and the
// standalone provenance comment file is emitted.
func TestTerraformSourcedExampleRenderDeterminism(t *testing.T) {
	catalogRoot := siblingCatalogRootForTF(t)
	src := terraformExampleDir(t, "terraform-rds-module")

	// One overlay for both runs, so the two builds see byte-identical inputs.
	stack := terraformExampleCatalogStack(t, catalogRoot)

	run1 := buildExampleToDist(t, src, stack)
	run2 := buildExampleToDist(t, src, stack)
	assertByteIdenticalTrees(t, run1, run2)

	// Module files vendor byte-for-byte from the recipe's local module.
	moduleRoot := filepath.Join(catalogRoot, "recipe", "terraform", "mariadb-rds-module", "1.0.0", "module")
	for _, name := range []string{"main.tf", "variables.tf"} {
		want, err := os.ReadFile(filepath.Join(moduleRoot, name))
		if err != nil {
			t.Fatalf("read source module file %q: %v", name, err)
		}
		got := distFile(t, run1, "/"+name)
		if !bytes.Equal(got, want) {
			t.Errorf("module file %q not vendored verbatim:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}

	// The overlay carries the resolved variables with keys in deterministic
	// (sorted) order and the ${...} interpolation pointer passing through as a
	// string.
	wantOverlay := `{
  "allocated_storage": 100,
  "db_name": "appdb",
  "engine_version": "11.4",
  "multi_az": true,
  "subnet_id": "${data.aws_db_subnet_group.default.name}"
}
`
	gotOverlay := distFile(t, run1, "aggrega.auto.tfvars.json")
	if string(gotOverlay) != wantOverlay {
		t.Errorf("tfvars overlay mismatch:\n--- got ---\n%s\n--- want ---\n%s", gotOverlay, wantOverlay)
	}

	// Build provenance rides in its own comment-only .tf file (the JSON overlay
	// cannot carry a comment).
	prov := distFile(t, run1, "aggrega.provenance.tf")
	if !bytes.HasPrefix(prov, []byte("# Aggrega build provenance\n")) {
		t.Errorf("aggrega.provenance.tf must open with the provenance comment block; got:\n%s", prov)
	}
	if !bytes.Contains(prov, []byte("# aggrega.ai/provider: terraform")) {
		t.Errorf("provenance file missing terraform provider identity; got:\n%s", prov)
	}
}
