// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the target an import drafts for when it names none, through
// the real Import over a workspace holding the default-target importer, which
// declares compose as the target its drafts default to and reports the target it
// was handed in its first diagnostic.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// defaultTargetWorkspace lays the catalog whose Recipes bind to compose and helm
// with the default-target importer beside it, and writes the draft-composition
// source.
func defaultTargetWorkspace(t *testing.T) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	importopstest.WriteConfiguration(t, wsDir)
	importopstest.LayDefaultTargetCatalog(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir))
	rw := resolveWorkspaceAt(t, wsDir)
	return rw, importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"),
		importopstest.DraftComposerSource())
}

// defaultTargetImport imports source over rw for target into outputDir, as a sync
// where sync is set.
func defaultTargetImport(rw *workspaceconfig.ResolvedWorkspace, source, target, outputDir string, sync bool,
	delivery authoredcontent.Delivery) (Result, error) {
	return Import(context.Background(), rw, Options{
		SourcePath:    source,
		SourceName:    source,
		Format:        importopstest.DefaultTargetFormat,
		Target:        target,
		OutputDir:     outputDir,
		Sync:          sync,
		Delivery:      delivery,
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
}

// handedTarget reads the target the importer reported it was handed.
func handedTarget(t *testing.T, res Result) string {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Severity == abi.SeverityInfo && (d.Message == "no target" || strings.HasPrefix(d.Message, "target ")) {
			return d.Message
		}
	}
	t.Fatalf("the importer reported no target line among %+v", res.Diagnostics)
	return ""
}

// hasInfo reports whether an information diagnostic holds every one of parts.
func hasInfo(res Result, parts ...string) bool {
	for _, d := range res.Diagnostics {
		if d.Severity != abi.SeverityInfo {
			continue
		}
		all := true
		for _, p := range parts {
			all = all && strings.Contains(d.Message, p)
		}
		if all {
			return true
		}
	}
	return false
}

// TestDefaultTargetApplied imports with no target where the importer declares
// compose and the catalog holds compose Recipes: the Manifest is composed, the
// importer is handed compose, an information diagnostic says the drafts are for
// compose, the importer's declared default, and that an import naming a target drafts for that one, and
// the record names compose.
func TestDefaultTargetApplied(t *testing.T) {
	t.Parallel()
	rw, source := defaultTargetWorkspace(t)
	out := filepath.Join(t.TempDir(), "drafts")
	res, err := defaultTargetImport(rw, source, "", out, false, authoredcontent.DeliveryWrite)
	if err != nil {
		t.Fatalf("an import with no target: %v", err)
	}
	if got := fileNamed(t, res, ManifestFileName); !strings.Contains(got, "compose/") {
		t.Errorf("the drafted Manifest is\n%s\nwant the compose mapping", got)
	}
	if got := handedTarget(t, res); got != "target compose" {
		t.Errorf("the importer reported %q; want it handed compose", got)
	}
	if !hasInfo(res, `"compose"`, "the importer's declared default", "an import naming a target drafts for that one") {
		t.Errorf("no information diagnostic names compose as the importer's declared default among %+v",
			res.Diagnostics)
	}
	if got := recordedTarget(t, out); got != "compose" {
		t.Errorf("the record names the target %q; want compose", got)
	}
}

// TestDefaultTargetUnboundDraftsNoManifest imports with no target where no Recipe
// binds to the declared compose: no Manifest, no target handed to the importer,
// an information diagnostic naming compose and why, and the import succeeds.
func TestDefaultTargetUnboundDraftsNoManifest(t *testing.T) {
	t.Parallel()
	wsDir := aWorkspaceDir(t)
	importopstest.WriteConfiguration(t, wsDir)
	importopstest.LayDefaultTargetImporter(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir))
	rw := resolveWorkspaceAt(t, wsDir)
	source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"),
		importopstest.DraftSetBody(abi.ScaffoldFile{Path: "aggregate.yaml", Content: importopstest.DraftedAggregate}))

	res, err := defaultTargetImport(rw, source, "", filepath.Join(t.TempDir(), "drafts"), false,
		authoredcontent.DeliveryReport)
	if err != nil {
		t.Fatalf("an import with no target over a catalog binding no compose Recipe: %v", err)
	}
	for _, f := range res.Files {
		if f.Path == ManifestFileName {
			t.Errorf("the import drafted a %s with no Recipe binding to the default target", ManifestFileName)
		}
	}
	if got := handedTarget(t, res); got != "no target" {
		t.Errorf("the importer reported %q; want it handed no target", got)
	}
	if !hasInfo(res, `"compose"`, "no Recipe of the catalog binds to it", "no Manifest is drafted") {
		t.Errorf("no information diagnostic names compose and why no Manifest is drafted among %+v", res.Diagnostics)
	}
	if res.Halted() {
		t.Errorf("the import halted: %+v", res.Diagnostics)
	}
}

// TestExplicitTargetWinsOverDefault names helm over an importer declaring
// compose: the importer is handed helm, no default is announced, and a named
// target no Recipe binds to is refused whatever the default.
func TestExplicitTargetWinsOverDefault(t *testing.T) {
	t.Parallel()
	rw, source := defaultTargetWorkspace(t)
	out := filepath.Join(t.TempDir(), "drafts")
	res, err := defaultTargetImport(rw, source, "helm", out, false, authoredcontent.DeliveryWrite)
	if err != nil {
		t.Fatalf("an import for helm: %v", err)
	}
	if got := handedTarget(t, res); got != "target helm" {
		t.Errorf("the importer reported %q; want it handed helm", got)
	}
	if hasInfo(res, "the importer's declared default") {
		t.Errorf("an import naming a target announced the default among %+v", res.Diagnostics)
	}
	if got := recordedTarget(t, out); got != "helm" {
		t.Errorf("the record names the target %q; want helm", got)
	}

	_, err = defaultTargetImport(rw, source, "helmm", filepath.Join(t.TempDir(), "unbound"), false,
		authoredcontent.DeliveryReport)
	if !errors.Is(err, ErrImportTargetUnbound()) {
		t.Errorf("an import for the unbound target helmm returned %v; want the unbound-target refusal", err)
	}
}

// TestRecordedTargetWinsOverDefault syncs with no target over a record naming
// helm, for an importer declaring compose: the sync drafts for helm.
func TestRecordedTargetWinsOverDefault(t *testing.T) {
	t.Parallel()
	rw, source := defaultTargetWorkspace(t)
	out := filepath.Join(t.TempDir(), "drafts")
	if _, err := defaultTargetImport(rw, source, "helm", out, false, authoredcontent.DeliveryWrite); err != nil {
		t.Fatalf("an import for helm: %v", err)
	}
	res, err := defaultTargetImport(rw, source, "", out, true, authoredcontent.DeliveryReport)
	if err != nil {
		t.Fatalf("a sync with no target: %v", err)
	}
	if got := handedTarget(t, res); got != "target helm" {
		t.Errorf("the importer reported %q; want it handed the recorded helm", got)
	}
	if hasInfo(res, "the importer's declared default") {
		t.Errorf("a sync over a recorded target announced the default among %+v", res.Diagnostics)
	}
	if len(res.Drift) != 0 {
		t.Errorf("the sync drifts %+v; want it in step", res.Drift)
	}
}

// TestSyncReusesTheDefaultTargetRecorded syncs with no target over the record of an
// import that took the importer's default: the sync drafts for compose again and
// finds the drafts in step.
func TestSyncReusesTheDefaultTargetRecorded(t *testing.T) {
	t.Parallel()
	rw, source := defaultTargetWorkspace(t)
	out := filepath.Join(t.TempDir(), "drafts")
	if _, err := defaultTargetImport(rw, source, "", out, false, authoredcontent.DeliveryWrite); err != nil {
		t.Fatalf("an import with no target: %v", err)
	}
	res, err := defaultTargetImport(rw, source, "", out, true, authoredcontent.DeliveryReport)
	if err != nil {
		t.Fatalf("a sync with no target: %v", err)
	}
	if got := handedTarget(t, res); got != "target compose" {
		t.Errorf("the importer reported %q; want it handed the recorded compose", got)
	}
	if len(res.Drift) != 0 || len(res.Files) != 0 {
		t.Errorf("the sync drifts %+v and writes %v; want it in step", res.Drift, draftPaths(res))
	}
}
