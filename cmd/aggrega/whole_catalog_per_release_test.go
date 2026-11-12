// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// perReleaseFeature is a Feature whole-catalog correctness accepts; with extends
// set to an identity no catalog holds it is one the extends gate refuses. The two
// sit in two catalog variants of one workspace, each read by its own Release.
func perReleaseFeature(extends string) string {
	doc := `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: core/secret-value
  version: 1.0.0
  description: A single secret value delivered to the attached component.
  labels:
    feature.aggrega.ai/kind: secret
spec:
`
	if extends != "" {
		doc += "  extends: " + extends + "\n"
	}
	return doc + `  schema:
    values:
      key:
        type: string
        required: true
        description: Environment variable name the credential is delivered as.
`
}

// perReleaseCatalogWorkspace authors a workspace whose configuration body is
// config, a Release document at each named file and each given document at its
// path, and resolves it. HOME and the XDG roots are not touched here; a test
// driving the command tree isolates them itself.
func perReleaseCatalogWorkspace(t *testing.T, config string, releases []string, docs map[string]string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	put(workspaceconfig.ConfigFilename, config)
	for _, r := range releases {
		put(r, "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: probe/"+strings.TrimSuffix(r, ".yaml")+"\n  version: 1.0.0\nspec: {}\n")
	}
	for rel, body := range docs {
		put(rel, body)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rw
}

const twoVariantsConfig = `workspace:
  releases:
    release-a.yaml:
      catalog_dirs:
        - ./variant-a
    release-b.yaml:
      catalog_dirs:
        - ./variant-b
    release-off.yaml:
      catalog_dirs: []
`

// TestWholeCatalogValidationRunsOncePerRelease drives whole-catalog correctness
// over a workspace keeping two variants side by side, the second broken, and a
// third Release reading nothing: every pass is reported under its line, the
// broken variant fails its own pass and no other, the pass reading nothing is
// reported rather than failing, and the verdict names the Release whose
// directories failed. The clean variant's pass is the positive control.
//
// Each variant is valid on its own and both declare the same Feature, so a union
// of their directories would be one catalog holding that identity twice: the
// passes are what keep one variant from being read beside the other.
func TestWholeCatalogValidationRunsOncePerRelease(t *testing.T) {
	t.Parallel()
	rw := perReleaseCatalogWorkspace(t, twoVariantsConfig,
		[]string{"release-a.yaml", "release-b.yaml", "release-off.yaml"},
		map[string]string{
			"variant-a/feature/secret-value.yaml": perReleaseFeature(""),
			"variant-b/feature/secret-value.yaml": perReleaseFeature("core/no-such-parent"),
		})
	passes := passesFor(t, rw, nil)

	var out, diag bytes.Buffer
	err := reportCatalogPasses(t.Context(), rw, passes, &diag, &out)

	if err == nil {
		t.Fatalf("a pass over a broken variant was not a failing verdict; out:\n%s\ndiag:\n%s", out.String(), diag.String())
	}
	if got, want := err.Error(), "catalog validation failed in the catalog directories of release-b.yaml"; got != want {
		t.Errorf("the verdict reads %q; want %q", got, want)
	}
	wantOut := "Release release-a.yaml reads variant-a\n" +
		"catalog validation passed (schema-pillar + library + extends + oci-location)\n" +
		"\n" +
		"Release release-b.yaml reads variant-b\n" +
		"\n" +
		"Release release-off.yaml reads no catalog directory, so there is nothing to check for it\n"
	if out.String() != wantOut {
		t.Errorf("the report is not every pass under its line:\ngot:\n%s\nwant:\n%s", out.String(), wantOut)
	}
	if !strings.Contains(diag.String(), "core/no-such-parent") || !strings.Contains(diag.String(), "1 extends resolution error(s)") {
		t.Errorf("the broken variant's reason is not on the diagnostic stream:\n%s", diag.String())
	}
}

// TestWholeCatalogPerReleasePassesHaltOnAnAggregaDocumentTheyCannotLoad plants
// an Aggrega document that does not parse in one of two catalog variants and
// holds both whole-catalog scopes, run once per Release, to the halt over that
// variant: lint stops with the document-load condition naming the file, and
// validate reports the file under the failing Release's pass and names that
// Release in its verdict, while the clean variant's pass still passes.
func TestWholeCatalogPerReleasePassesHaltOnAnAggregaDocumentTheyCannotLoad(t *testing.T) {
	t.Parallel()
	rw := perReleaseCatalogWorkspace(t, twoVariantsConfig,
		[]string{"release-a.yaml", "release-b.yaml", "release-off.yaml"},
		map[string]string{
			"variant-a/feature/secret-value.yaml": perReleaseFeature(""),
			"variant-b/feature/secret-value.yaml": perReleaseFeature(""),
			"variant-b/component/broken.yaml":     unloadableSyntaxComponent,
		})
	passes := passesFor(t, rw, nil)
	broken := filepath.Join("variant-b", "component", "broken.yaml")

	var lintOut, lintDiag bytes.Buffer
	lintErr := lintops.RunCatalogWide(context.Background(), rw, passes, nil, &lintOut, &lintDiag, nil)
	if !errors.Is(lintErr, resolver.ErrYAMLLoad()) {
		t.Errorf("whole-catalog lint over the broken variant did not halt on the document-load condition: %v", lintErr)
	} else if chain := cerrors.CauseChain(lintErr); !strings.Contains(chain, broken) {
		t.Errorf("whole-catalog lint halted without naming %s:\n%s", broken, chain)
	}

	var out, diag bytes.Buffer
	err := reportCatalogPasses(t.Context(), rw, passes, &diag, &out)
	if err == nil {
		t.Fatalf("whole-catalog validation passed a variant holding an Aggrega document it cannot load\nout:\n%s\ndiag:\n%s", out.String(), diag.String())
	}
	if got, want := err.Error(), "catalog validation failed in the catalog directories of release-b.yaml"; got != want {
		t.Errorf("the verdict reads %q; want %q", got, want)
	}
	if !strings.Contains(diag.String(), broken) {
		t.Errorf("the failing pass's reason does not name %s:\n%s", broken, diag.String())
	}
	if !strings.Contains(out.String(), "Release release-a.yaml reads variant-a\ncatalog validation passed") {
		t.Errorf("the clean variant's pass did not pass:\n%s", out.String())
	}
}

// TestWholeCatalogScopesRefuseWhenNoReleaseReadsADirectory holds both
// whole-catalog refusals in a workspace naming Releases to the case they are
// for, and to a remedy that reaches such a workspace: a Release's own list.
func TestWholeCatalogScopesRefuseWhenNoReleaseReadsADirectory(t *testing.T) {
	t.Parallel()
	const allOff = "workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs: []\n    release-b.yaml:\n      catalog_dirs: []\n"
	rw := perReleaseCatalogWorkspace(t, allOff, []string{"release-a.yaml", "release-b.yaml"}, nil)
	remedy := "or a Release's own `catalog_dirs` under `" + workspaceconfig.ReleaseCatalogsSetting + "`"
	scopes := []struct {
		name string
		run  func() error
	}{
		{"aggrega validate (whole catalog)", func() error { return validateCatalogWideFromWorkspace(t.Context(), rw, nil) }},
		{"aggrega lint (whole catalog)", func() error {
			var out, diag bytes.Buffer
			return lintops.RunCatalogWide(context.Background(), rw, passesFor(t, rw, nil), nil, &out, &diag, nil)
		}},
	}
	for _, s := range scopes {
		err := s.run()
		if err == nil {
			t.Errorf("%s: no Release reads a directory and the run was not refused", s.name)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "needs catalog directories to scan") || !strings.Contains(msg, remedy) {
			t.Errorf("%s refused with %q; want the whole-catalog refusal naming %s", s.name, msg, remedy)
		}
	}
}

// TestWholeCatalogLintThroughTheCommandTreeRunsOncePerRelease drives `aggrega
// lint` with no argument, as text and as JSON, through the real command tree over
// a per-Release workspace — the shape it refused before the passes existed — and
// reads both renderings and the process status.
func TestWholeCatalogLintThroughTheCommandTreeRunsOncePerRelease(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rw := perReleaseCatalogWorkspace(t, twoVariantsConfig,
		[]string{"release-a.yaml", "release-b.yaml", "release-off.yaml"},
		map[string]string{
			"variant-a/feature/secret-value.yaml": perReleaseFeature(""),
			"variant-b/feature/secret-value.yaml": perReleaseFeature(""),
		})

	run := func(args ...string) (int, string, string) {
		t.Helper()
		root := newRootCmd()
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetIn(strings.NewReader(""))
		root.SetArgs(append([]string{"--workspace-root", rw.Root()}, args...))
		return executeRoot(context.Background(), root), out.String(), errOut.String()
	}

	code, out, errOut := run("lint")
	if code != 0 {
		t.Fatalf("`aggrega lint` over a per-Release workspace exited %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	for _, line := range []string{
		"Release release-a.yaml reads variant-a\nconventions checked: 1 entity,",
		"Release release-b.yaml reads variant-b\nconventions checked: 1 entity,",
		"Release release-off.yaml reads no catalog directory, so there is nothing to check for it",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("stdout does not carry %q:\n%s", line, out)
		}
	}

	code, out, errOut = run("lint", "-o", "json")
	if code != 0 {
		t.Fatalf("`aggrega lint -o json` exited %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	var report lintops.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	if len(report.Passes) != 3 || report.Summary.EntitiesChecked != 2 {
		t.Errorf("the structured report carries %d pass(es) over %d checked; want 3 and 2: %s",
			len(report.Passes), report.Summary.EntitiesChecked, out)
	}
	if strings.Contains(out, "conventions checked:") || strings.Contains(out, "reads variant-a") {
		t.Errorf("the human report leaked onto the structured stream:\n%s", out)
	}
}
