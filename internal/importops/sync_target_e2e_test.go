// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the target a sync drafts for, through the real Import over
// the draft-composition workspace: the record names the target an import was
// given, a sync given none drafts for that one, and a sync given one drafts for
// it and records it in place of the old.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// syncedImport syncs source over rw for target into outputDir, under the delivery
// given, and returns what the import answered.
func syncedImport(rw *workspaceconfig.ResolvedWorkspace, source, target, outputDir string,
	delivery authoredcontent.Delivery) (Result, error) {
	return Import(context.Background(), rw, Options{
		SourcePath:    source,
		SourceName:    source,
		Format:        importopstest.DraftSetFormat,
		Target:        target,
		OutputDir:     outputDir,
		Sync:          true,
		Delivery:      delivery,
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
}

// recordedTarget reads the target the record beneath outputDir names.
func recordedTarget(t *testing.T, outputDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(outputDir, RecordFileName))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	rec, err := parseRecord(data)
	if err != nil {
		t.Fatalf("parse the record: %v", err)
	}
	return rec.sole().Target
}

// TestASyncWithNoTargetDraftsForTheRecordedOne drafts for the compose target,
// then syncs the same source with no target: the record names compose, so the
// sync drafts what the import drafted, finds the drafts in step under a dry run
// and as a write, and leaves the drafted Manifest where it is. An import given no
// target records none.
//
// 🔴 PLANTED DEFECT: have Import (internal/importops/import.go) hand
// composeDraftSet opts.Target rather than the target the record settles: the dry
// run then drifts — the Release changed, the Manifest removed — and this test
// reddens.
func TestASyncWithNoTargetDraftsForTheRecordedOne(t *testing.T) {
	t.Parallel()
	rw, source := draftComposerWorkspace(t, importopstest.DraftComposerSource())
	out := filepath.Join(t.TempDir(), "drafts")
	composedImport(t, rw, source, "compose", out, authoredcontent.DeliveryWrite)
	if got := recordedTarget(t, out); got != "compose" {
		t.Fatalf("the record names the target %q; want compose", got)
	}
	manifest, err := os.ReadFile(filepath.Join(out, ManifestFileName))
	if err != nil {
		t.Fatalf("read the drafted Manifest: %v", err)
	}

	for _, delivery := range []authoredcontent.Delivery{authoredcontent.DeliveryReport, authoredcontent.DeliveryWrite} {
		res, err := syncedImport(rw, source, "", out, delivery)
		if err != nil {
			t.Fatalf("a sync with no target: %v", err)
		}
		if len(res.Drift) != 0 || len(res.Files) != 0 || len(res.Removed) != 0 {
			t.Errorf("a sync with no target over a compose draft drifts %+v, writes %v and removes %v; want it in step",
				res.Drift, draftPaths(res), res.Removed)
		}
	}
	if after, err := os.ReadFile(filepath.Join(out, ManifestFileName)); err != nil || string(after) != string(manifest) {
		t.Errorf("the drafted Manifest after the sync is %q (%v); want it as drafted", after, err)
	}
	if got := recordedTarget(t, out); got != "compose" {
		t.Errorf("the record names the target %q after the sync; want compose", got)
	}

	plain := filepath.Join(t.TempDir(), "plain")
	composedImport(t, rw, source, "", plain, authoredcontent.DeliveryWrite)
	if data, err := os.ReadFile(filepath.Join(plain, RecordFileName)); err != nil || strings.Contains(string(data), `"target"`) {
		t.Errorf("the record of an import given no target is %q (%v); want no target in it", data, err)
	}
}

// TestASyncGivenATargetDraftsForItAndRecordsIt syncs a compose draft for the helm
// target: the drafted Manifest is the one helm maps — a changed file, the only
// part that drifted, the Release naming the same Manifest — and the record then
// names helm, so a later sync with no target is in step.
//
// 🔴 PLANTED DEFECT: have syncImport (internal/importops/sync.go) write the record
// for the target the record already named: the record row reddens on compose.
func TestASyncGivenATargetDraftsForItAndRecordsIt(t *testing.T) {
	t.Parallel()
	rw, source := draftComposerWorkspace(t, importopstest.DraftComposerSource())
	out := filepath.Join(t.TempDir(), "drafts")
	composedImport(t, rw, source, "compose", out, authoredcontent.DeliveryWrite)

	res, err := syncedImport(rw, source, "helm", out, authoredcontent.DeliveryWrite)
	if err != nil {
		t.Fatalf("a sync for the helm target: %v", err)
	}
	if want := []Drift{{Change: DriftChanged, File: ManifestFileName}}; !reflect.DeepEqual(res.Drift, want) {
		t.Errorf("a sync for helm over a compose draft drifts %+v; want %+v", res.Drift, want)
	}
	manifest, err := os.ReadFile(filepath.Join(out, ManifestFileName))
	if err != nil {
		t.Fatalf("read the synced Manifest: %v", err)
	}
	if !strings.Contains(string(manifest), "example/z:1.0.0: helm/z-chart") ||
		strings.Contains(string(manifest), "compose/") {
		t.Errorf("the synced Manifest is\n%s\nwant the helm mapping alone", manifest)
	}
	if got := recordedTarget(t, out); got != "helm" {
		t.Errorf("the record names the target %q after a sync for helm; want helm", got)
	}
	again, err := syncedImport(rw, source, "", out, authoredcontent.DeliveryReport)
	if err != nil {
		t.Fatalf("a sync with no target after the helm sync: %v", err)
	}
	if len(again.Drift) != 0 {
		t.Errorf("a sync with no target after the helm sync drifts %+v; want it in step", again.Drift)
	}
}

// TestARecordedTargetNoRecipeBindsToIsRefusedAsRecorded holds a sync with no
// target to the target refusal when the target the record names is one no Recipe
// of the catalog binds to: it is the registered condition, it says the target is
// the recorded one, and it does not offer leaving the target out, which would
// leave the recorded one in force. Nothing is written.
func TestARecordedTargetNoRecipeBindsToIsRefusedAsRecorded(t *testing.T) {
	t.Parallel()
	rw, source := draftComposerWorkspace(t, importopstest.DraftComposerSource())
	out := filepath.Join(t.TempDir(), "drafts")
	composedImport(t, rw, source, "compose", out, authoredcontent.DeliveryWrite)
	recordPath := filepath.Join(out, RecordFileName)
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), `"target": "compose"`, `"target": "helmm"`, 1)
	if edited == string(data) {
		t.Fatalf("the record names no compose target to edit:\n%s", data)
	}
	if err := os.WriteFile(recordPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = syncedImport(rw, source, "", out, authoredcontent.DeliveryWrite)
	if !errors.Is(err, ErrImportTargetUnbound()) {
		t.Fatalf("a sync over a record naming the target helmm returned %v; want the unbound-target refusal", err)
	}
	msg := err.Error()
	for _, want := range []string{`target "helmm"`, "recorded by the earlier import in " + RecordFileName} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal %q does not name %s", msg, want)
		}
	}
	if strings.Contains(msg, "leave the target out") {
		t.Errorf("the refusal %q offers leaving the target out, which leaves the recorded one in force", msg)
	}
	if after, _ := os.ReadFile(recordPath); string(after) != edited {
		t.Errorf("the record changed under a refused sync")
	}
}
