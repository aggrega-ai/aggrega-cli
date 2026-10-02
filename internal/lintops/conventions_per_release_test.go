// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// perReleaseComponent is a Component the publishing rules pass; plantedComponent
// is the same identity carrying one convention finding, a reserved annotation
// prefix. The two are the side-by-side variants of one entity a workspace keeps
// when each Release reads a catalog of its own.
const (
	perReleaseComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/category: compute
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`
	plantedComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/api-server
  version: 1.0.0
  annotations:
    aggrega.ai/planted: "x"
  labels:
    component.aggrega.ai/category: compute
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`
)

// perReleaseWorkspace authors a workspace root carrying config verbatim, a
// Release document at each named file, and each given entity at its path, and
// resolves it.
func perReleaseWorkspace(t *testing.T, config string, releases []string, entities map[string]string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		mustWriteFile(t, p, body)
	}
	put(workspaceconfig.ConfigFilename, config)
	for _, r := range releases {
		put(r, "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: probe/"+strings.TrimSuffix(r, ".yaml")+"\n  version: 1.0.0\nspec: {}\n")
	}
	for rel, body := range entities {
		put(rel, body)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the constructed workspace: %v", err)
	}
	return rw
}

const perReleaseConfig = `workspace:
  releases:
    release-a.yaml:
      catalog_dirs:
        - ./variant-a
    release-b.yaml:
      catalog_dirs:
        - ./variant-b
    release-b2.yaml:
      catalog_dirs:
        - ./variant-b
    release-off.yaml:
      catalog_dirs: []
`

// TestCatalogWideRunsOncePerRelease drives the real whole-catalog pass over a
// workspace keeping two variants of one entity side by side, a third Release
// sharing the second variant, and a fourth reading nothing, and reads both the
// human report and the collected data.
//
// What it holds: one pass for each set of directories, each under the line naming
// its Releases and what it read, each with its own summary after it; the finding
// attributed to the pass whose variant carries it and to no other; the Releases
// sharing a variant named on one pass, so the finding is reported once; the pass
// reading nothing reported rather than failing the others; and the totals covering
// every pass together. The clean variant's pass is the positive control: without
// it, a pass that reported every finding everywhere would satisfy the attribution
// assertion.
func TestCatalogWideRunsOncePerRelease(t *testing.T) {
	t.Parallel()
	rw := perReleaseWorkspace(t, perReleaseConfig,
		[]string{"release-a.yaml", "release-b.yaml", "release-b2.yaml", "release-off.yaml"},
		map[string]string{
			"variant-a/component/api-server.yaml": perReleaseComponent,
			"variant-b/component/api-server.yaml": plantedComponent,
		})
	passes := rw.WholeCatalogPasses(func() []string {
		return []string{"release-a.yaml", "release-b.yaml", "release-b2.yaml", "release-off.yaml"}
	}, nil)

	var out, diag bytes.Buffer
	collect := &FindingCollector{}
	if err := RunCatalogWide(t.Context(), rw, passes, &out, &diag, collect); err != nil {
		t.Fatalf("a per-Release whole-catalog run with a directory to read: %v\nout:\n%s\ndiag:\n%s", err, out.String(), diag.String())
	}

	wantOut := "Release release-a.yaml reads variant-a\n" +
		"conventions checked: 1 entity, 0 advisory finding(s)\n" +
		"\n" +
		"Releases release-b.yaml, release-b2.yaml read variant-b\n" +
		"conventions checked: 1 entity, 1 advisory finding(s)\n" +
		"\n" +
		"Release release-off.yaml reads no catalog directory, so there is nothing to check for it\n"
	if out.String() != wantOut {
		t.Errorf("the report is not one pass per set of directories, each under its line:\ngot:\n%s\nwant:\n%s", out.String(), wantOut)
	}
	planted := filepath.Join(rw.Root(), "variant-b", "component", "api-server.yaml")
	if strings.Count(diag.String(), planted+":") != 1 {
		t.Errorf("the planted finding's file is reported %d time(s) on the diagnostic stream; want once, under the shared pass:\n%s",
			strings.Count(diag.String(), planted+":"), diag.String())
	}
	if strings.Contains(diag.String(), filepath.Join("variant-a", "component")) {
		t.Errorf("the clean variant was reported on the diagnostic stream:\n%s", diag.String())
	}

	report := collect.Report()
	if len(report.Passes) != 3 {
		t.Fatalf("the collected run carries %d pass(es); want 3: %+v", len(report.Passes), report.Passes)
	}
	type wantPass struct {
		releases []string
		dirs     []string
		checked  int
		findings int
	}
	want := []wantPass{
		{[]string{"release-a.yaml"}, []string{filepath.Join(rw.Root(), "variant-a")}, 1, 0},
		{[]string{"release-b.yaml", "release-b2.yaml"}, []string{filepath.Join(rw.Root(), "variant-b")}, 1, 1},
		{[]string{"release-off.yaml"}, []string{}, 0, 0},
	}
	for i, w := range want {
		got := report.Passes[i]
		if strings.Join(got.Releases, ",") != strings.Join(w.releases, ",") {
			t.Errorf("pass %d names %v; want %v", i, got.Releases, w.releases)
		}
		if got.CatalogDirs == nil || strings.Join(got.CatalogDirs, ",") != strings.Join(w.dirs, ",") {
			t.Errorf("pass %d reads %#v; want %#v (never nil)", i, got.CatalogDirs, w.dirs)
		}
		if got.Summary.EntitiesChecked != w.checked || got.Summary.Findings != w.findings || len(got.Findings) != w.findings {
			t.Errorf("pass %d checked %d and found %d (%d listed); want %d and %d",
				i, got.Summary.EntitiesChecked, got.Summary.Findings, len(got.Findings), w.checked, w.findings)
		}
		for _, f := range got.Findings {
			if f.Location == nil || f.Location.File != planted {
				t.Errorf("pass %d carries a finding on %+v; the only finding is on %s", i, f.Location, planted)
			}
		}
	}
	if report.Summary.EntitiesChecked != 2 || report.Summary.Findings != 1 || len(report.Findings) != 1 {
		t.Errorf("the run's totals are %d checked, %d found (%d listed); want 2, 1 and 1 — every pass together",
			report.Summary.EntitiesChecked, report.Summary.Findings, len(report.Findings))
	}
}

// TestCatalogWideRefusesOnlyWhenNoPassReads holds the refusal to the one case it
// is for — no pass has a directory to read — and its wording to the workspace in
// front of the reader: a workspace naming no Release under `workspace.releases` is
// told exactly what it has always been told, and one that names Releases is also
// told where a Release's own list is set.
func TestCatalogWideRefusesOnlyWhenNoPassReads(t *testing.T) {
	t.Parallel()
	const singleRefusal = "whole-catalog conventions check needs catalog directories to scan; put the entities under " +
		"`catalog/` at the workspace root, pass --catalog, or set `workspace.catalog_dirs` in .aggrega-config.yaml " +
		"(or check a specific Release with --release)"
	const perReleaseRefusal = "whole-catalog conventions check needs catalog directories to scan; put the entities under " +
		"`catalog/` at the workspace root, pass --catalog, or set `workspace.catalog_dirs`, or a Release's own " +
		"`catalog_dirs` under `workspace.releases`, in .aggrega-config.yaml (or check a specific Release with --release)"
	cases := []struct {
		name     string
		config   string
		releases []string
		want     string
	}{
		{
			name:   "a workspace naming no Release, with nothing to scan, gets today's refusal",
			config: "workspace: {}\n",
			want:   singleRefusal,
		},
		{
			name:     "every Release switched off is refused, naming a Release's own list",
			config:   "workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs: []\n    release-b.yaml:\n      catalog_dirs: []\n",
			releases: []string{"release-a.yaml", "release-b.yaml"},
			want:     perReleaseRefusal,
		},
		{
			name:     "one Release switched off beside one that reads a directory is a run, not a refusal",
			config:   "workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs: []\n    release-b.yaml:\n      catalog_dirs:\n        - ./variant-b\n",
			releases: []string{"release-a.yaml", "release-b.yaml"},
			want:     "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw := perReleaseWorkspace(t, tc.config, tc.releases,
				map[string]string{"variant-b/component/api-server.yaml": perReleaseComponent})
			passes := rw.WholeCatalogPasses(func() []string { return tc.releases }, nil)
			var out, diag bytes.Buffer
			err := RunCatalogWide(t.Context(), rw, passes, &out, &diag, nil)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("a run with one pass to read was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("no pass reads a directory and the run was not refused; it reported:\n%s", out.String())
			}
			if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Errorf("the refusal reads %q; want it to carry exactly %q", got, tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("a refused run wrote a report:\n%s", out.String())
			}
		})
	}
}
