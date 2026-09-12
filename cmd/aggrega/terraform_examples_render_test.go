// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
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

// siblingCatalogRootForExamples returns repo-root/aggrega-hub/aggrega-catalog
// from cmd/aggrega, skipping when the sibling checkout is absent.
//
// It also skips while the sibling catalog's Plugin entities still declare a
// standalone plugin-artifact field: the entity schema now derives a plugin's
// .wasm location from its identity, so an entity carrying that legacy field is
// schema-rejected on load and its provider never resolves. The catalog is
// regenerated to the identity-derived shape out of band; the skip lifts
// automatically once that regeneration lands, so no re-coupling edit is needed.
func siblingCatalogRootForExamples(t *testing.T) string {
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

// exampleCatalogStack returns the catalog search stack an example build runs
// against: a seeded overlay for the provider identity the example renders
// through, ahead of the sibling catalog. The sibling catalog declares each core
// provider under its own published version, while capability resolution selects
// the version the binary embeds, so without the overlay the lockfile walk would
// find no local entity at the selected version and reach for a registry.
//
// The stack is built once per test and reused across runs that must be compared
// byte for byte, so both runs read the same inputs.
func exampleCatalogStack(t *testing.T, catalogRoot, providerURI string) []string {
	t.Helper()
	return []string{seedEmbeddedProviderOverlay(t, providerURI), catalogRoot}
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

// workedExampleDir returns the worked-example directory for name, skipping
// when the sibling docs checkout is absent.
func workedExampleDir(t *testing.T, name string) string {
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

// readExampleDistTree reads every file under root/dist into a path->content map
// keyed by dist-relative slash paths.
func readExampleDistTree(t *testing.T, root string) map[string][]byte {
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

	releasePath := "release.yaml"
	catalogDirs := catalogStack
	traceEnabled := false

	// Build is read-only: prime the lockfile with an explicit pull (output discarded),
	// then build reads it. Each call runs in its own fresh workspace, so the two
	// determinism runs pull independently and stay byte-identical.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: releasePath, CatalogDirs: catalogDirs, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("prime lockfile via pull for example %q: %v", src, err)
	}

	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath, catalogDirs, traceEnabled, io.Discard, io.Discard); err != nil {
		t.Fatalf("build example %q: %v", src, err)
	}
	return readExampleDistTree(t, ws)
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

// sortedKeys returns m's keys in a stable order, so a failure message naming a
// rendered tree reads the same way on every run. It is generic over the value type
// because a rendered tree is collected as bytes in some fixtures and as strings in
// others, and one ordering helper is better than one per collection shape.
func sortedKeys[V any](m map[string]V) []string {
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
//
// It also asserts the boundary's root configuration, which is where this example
// composes: the Recipe authors the configuration body itself and resolves the
// developer's values into it, so the emitted body declares no input variables and
// the root's module block must pass no arguments. An argument here would name an
// input the emitted configuration does not declare, which an engine refuses —
// asserting main.tf alone cannot see that, because the two files are emitted
// separately and only the root names the other.
func TestTerraformArtifactsExampleRenderDeterminism(t *testing.T) {
	catalogRoot := siblingCatalogRootForExamples(t)
	src := workedExampleDir(t, "terraform-rds")

	// One overlay for both runs, so the two builds see byte-identical inputs.
	stack := exampleCatalogStack(t, catalogRoot, "provider/terraform")

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

	// The authored body declares no input variables — the premise the module
	// block's empty argument list rests on. Asserted rather than assumed, so a
	// Recipe rewritten to declare variables fails here and names the reason
	// instead of silently weakening the check below.
	if bytes.Contains(mainTF, []byte("variable \"")) {
		t.Errorf("main.tf declares an input variable; the emitted body this example composes resolves its values inline and declares none:\n%s", mainTF)
	}

	// The Aggregate's own values, which the authored body consumed. None of them
	// may reappear as a module-block argument.
	wantBlock := `module "db" {
  source = "./modules/db"
}
`
	gotRoot := distFile(t, run1, "aggrega.modules.tf")
	if !bytes.Contains(gotRoot, []byte(wantBlock)) {
		t.Errorf("root configuration module block mismatch:\n--- got ---\n%s\n--- want ---\n%s", gotRoot, wantBlock)
	}
	for _, key := range []string{"database", "user", "charset", "collation"} {
		if bytes.Contains(gotRoot, []byte(key)) {
			t.Errorf("the root configuration names %q; an argument by that name is an input the emitted configuration never declares, and the engine rejects it:\n%s", key, gotRoot)
		}
	}
}

// TestTerraformSourcedExampleRenderDeterminism drives the terraform-rds-module
// example (a vendored local module composed from the boundary root) through two
// independent builds and asserts the rendered tree is byte-identical across runs,
// the module files vendor verbatim, the root configuration's module block carries
// the resolved variables as its arguments — including the DB subnet group name the
// Release supplies under infra.overrides — no variables file is written beside the
// module's own files, and the standalone provenance comment file is emitted.
func TestTerraformSourcedExampleRenderDeterminism(t *testing.T) {
	catalogRoot := siblingCatalogRootForExamples(t)
	src := workedExampleDir(t, "terraform-rds-module")

	// One overlay for both runs, so the two builds see byte-identical inputs.
	stack := exampleCatalogStack(t, catalogRoot, "provider/terraform")

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

	// The root configuration's module block carries the resolved variables as its
	// arguments, with keys in deterministic (sorted) order. An argument is an
	// expression the engine evaluates, which is what a value written as a pointer
	// needs; a variables file beside the module's own files would be read as data
	// instead. subnet_id is the DB subnet group name the Release supplies under
	// infra.overrides: the recipe reads it from the operator's pillar rather than
	// naming a Terraform data source, so what lands here is a plain string with
	// nothing left to resolve. instance_class comes from the same pillar and falls
	// back to the value the recipe declares, which is why it appears without the
	// Release stating one.
	wantBlock := `module "db" {
  source = "./modules/db"

  allocated_storage = 100
  db_name           = "appdb"
  engine_version    = "11.4"
  instance_class    = "db.t3.small"
  multi_az          = true
  subnet_id         = "production-private-db-subnets"
}
`
	gotRoot := distFile(t, run1, "aggrega.modules.tf")
	if !strings.Contains(string(gotRoot), wantBlock) {
		t.Errorf("root configuration module block mismatch:\n--- got ---\n%s\n--- want ---\n%s", gotRoot, wantBlock)
	}

	// And the values are not written a second time beside the module's own files.
	// Two carriers for one value would answer differently for a pointer depending
	// on which directory the engine is run from.
	for k := range run1 {
		if strings.HasSuffix(k, ".tfvars.json") || strings.HasSuffix(k, ".tfvars") {
			t.Errorf("a variables file %q was rendered; the resolved values belong to the root configuration's module block and nowhere else", k)
		}
	}

	// Build provenance rides in its own comment-only .tf file, because the
	// vendored module's own files stay byte-for-byte verbatim.
	prov := distFile(t, run1, "aggrega.provenance.tf")
	if !bytes.HasPrefix(prov, []byte("# Aggrega build provenance\n")) {
		t.Errorf("aggrega.provenance.tf must open with the provenance comment block; got:\n%s", prov)
	}
	if !bytes.Contains(prov, []byte("# aggrega.ai/provider: terraform")) {
		t.Errorf("provenance file missing terraform provider identity; got:\n%s", prov)
	}
}

// TestTerraformDeclaredSensitivityExampleRenderDeterminism drives the
// terraform-declared-sensitivity example through two independent builds and
// asserts the rendered tree is byte-identical across runs and that a value the
// Component declares sensitive arrives as Terraform's own sensitivity attribute:
// an override file lands beside the module's own files and marks exactly the one
// module input the declaration names.
//
// Both directions are asserted, and the second is the one a presence check
// cannot see. Marking the declared input is what the example demonstrates;
// leaving the two public coordinates unmarked is what keeps the marking a
// consequence of the declaration rather than of a variable's name or its value.
// A build that marked every input satisfies a presence check while hiding values
// that were never secret, and a consumer that has started reading redacted plan
// output is expensive to walk back — so the marked set is compared for equality
// rather than searched for the name that ought to be in it.
//
// The expectations here are the ones the example's own page states, written out
// as literals. They are not recomputed from the Component's declaration or from
// the module the Recipe vendors, because those are what the build reads: an
// expectation derived from them would move with a defect planted in them and
// assert nothing.
func TestTerraformDeclaredSensitivityExampleRenderDeterminism(t *testing.T) {
	catalogRoot := siblingCatalogRootForExamples(t)
	src := workedExampleDir(t, "terraform-declared-sensitivity")

	// One overlay for both runs, so the two builds see byte-identical inputs.
	stack := exampleCatalogStack(t, catalogRoot, "provider/terraform")

	run1 := buildExampleToDist(t, src, stack)
	run2 := buildExampleToDist(t, src, stack)
	assertByteIdenticalTrees(t, run1, run2)

	// All three module inputs are rendered as arguments of the component's module
	// block. That is what makes the marking below a choice rather than an artifact
	// of two inputs being absent: every unmarked input is present and could have
	// been marked.
	wantBlock := `module "store" {
  source = "./modules/store"

  access_key_id     = "AKIAEXAMPLE0001"
  bucket            = "app-artifacts"
  secret_access_key = "example-placeholder-not-a-real-credential"
}
`
	gotRoot := distFile(t, run1, "aggrega.modules.tf")
	if !strings.Contains(string(gotRoot), wantBlock) {
		t.Errorf("root configuration module block mismatch:\n--- got ---\n%s\n--- want ---\n%s", gotRoot, wantBlock)
	}

	// The vendored module declares none of its own inputs sensitive, so the
	// attribute asserted below can only have come from the appended override. The
	// check reads attribute declarations instead of searching for the word: the
	// module file explains in a comment why it carries no such attribute, and a
	// substring search matches that sentence and keeps passing on a module that
	// has since grown one.
	vars := distFile(t, run1, "/variables.tf")
	for i, line := range strings.Split(string(vars), "\n") {
		stmt := strings.TrimSpace(line)
		if strings.HasPrefix(stmt, "#") {
			continue
		}
		if strings.HasPrefix(stmt, "sensitive") {
			t.Errorf("the vendored variables.tf declares a sensitivity attribute of its own at line %d (%q), so the rendered marking is no longer distinguishable from the module's own text:\n%s", i+1, stmt, vars)
		}
	}

	// Terraform merges an override into the configuration in its own directory and
	// ignores one placed anywhere else without complaint, so where the file lands
	// is part of what is asserted. Its absence is a legal build that renders the
	// credential unmarked, which is the failure this case exists to catch and the
	// one an exit code cannot see.
	const overridePath = "infra/terraform/modules/store/aggrega_override.tf.json"
	override, emitted := run1[overridePath]
	if !emitted {
		t.Fatalf("no sensitivity override at %q: the declared value did not reach Terraform's own sensitivity attribute; rendered files: %v", overridePath, sortedKeys(run1))
	}

	// Decoded rather than searched, so what follows is a statement about the
	// document's structure. Unknown fields are refused because the override
	// carries `sensitive` and nothing else: an attribute in this syntax that
	// Terraform does not evaluate is accepted in silence and reads as correct in
	// every diff.
	var doc struct {
		Variable map[string]struct {
			Sensitive *bool `json:"sensitive"`
		} `json:"variable"`
	}
	dec := json.NewDecoder(bytes.NewReader(override))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode %s: %v\n%s", overridePath, err, override)
	}

	// Equality on the whole marked set. A lookup for the declared input passes on
	// a build that marks every input, which is the over-collecting direction.
	wantMarked := []string{"secret_access_key"}
	gotMarked := sortedKeys(doc.Variable)
	if !slices.Equal(gotMarked, wantMarked) {
		t.Errorf("the override marks %v; the declaration names %v, and the marking follows the declaration alone:\n%s", gotMarked, wantMarked, override)
	}

	// Named one by one as well, so a failure says which direction broke. Both
	// carry public identifiers, neither is declared sensitive, and access_key_id
	// sits beside the credential with a name that reads like one.
	for _, public := range []string{"access_key_id", "bucket"} {
		if _, marked := doc.Variable[public]; marked {
			t.Errorf("the override marks %q, which no declaration names sensitive; marking a public input withholds a value the reader needs in order to read a plan:\n%s", public, override)
		}
	}
	if marking, marked := doc.Variable["secret_access_key"]; !marked || marking.Sensitive == nil || !*marking.Sensitive {
		t.Errorf("the declared-sensitive input carries no `sensitive` marking in the override:\n%s", override)
	}
}
