// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proofs that an attachment a module declares on one of its own
// subsystems reaches the module's components beneath that subsystem, and is held
// there to the rules an attachment the Aggregate declares is held to — the
// applicability its Feature declares first among them — with any finding
// positioned at the module's own document, at the subsystem that declared it.

package pipeline_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// moduleCascadeWorkspace writes one row's workspace: the committed catalog with
// the authoring-site entities beside it and a module "site-module" whose spec is
// the given body, an Aggregate importing that module as "mod", and a Release
// routing the whole module into the boundary "cluster".
func moduleCascadeWorkspace(t *testing.T, moduleSpec string) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	catalog := filepath.Join(dir, "catalog")
	if err := os.CopyFS(catalog, os.DirFS(connectionAttachmentDir+"catalog/base")); err != nil {
		t.Fatalf("copy the catalog: %v", err)
	}
	entities := map[string]string{
		"aggregates/site-module.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n" +
			"metadata:\n  name: site-module\n  version: \"1.0.0\"\nspec:\n" + moduleSpec,
	}
	for name, body := range authoringSiteEntities {
		entities[name] = body
	}
	if err := os.MkdirAll(filepath.Join(catalog, "aggregates"), 0o700); err != nil {
		t.Fatalf("create the module directory: %v", err)
	}
	for name, body := range entities {
		path := filepath.Join(catalog, name)
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("the committed catalog already holds %s; this row would overwrite an entity other proofs read", name)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	documents := map[string]string{
		filepath.Join(dir, "manifest.yaml"): authoringSiteManifest,
		filepath.Join(dir, "aggregate.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n" +
			"metadata:\n  name: module-cascade\n  version: \"1.0.0\"\nspec:\n" +
			"  aggregates:\n    mod:\n      type: site-module\n",
		filepath.Join(dir, "release.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Release\n" +
			"metadata:\n  name: module-cascade-release\n  environment: local\n  version: \"1.0.0\"\n" +
			"spec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n  boundaries:\n" +
			"    cluster:\n      directory: ./dist/cluster\n      aggregates:\n        mod: {}\n",
	}
	for path, body := range documents {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{catalog},
	}
}

// moduleSubsystemAttaching is a module spec holding the component "app" inside
// the subsystem "tier", which carries the given subsystem-wide attachments.
func moduleSubsystemAttaching(attachments string) string {
	return "  subsystems:\n    tier:\n      features:\n" + attachments +
		"      components:\n        app:\n          type: site/app\n"
}

// TestAModuleSubsystemAttachmentIsHeldToTheFeaturesApplicability proves an
// attachment a module declares on its own subsystem reaches the module's
// component and meets the applicability its Feature declares there: a Feature the
// component's labels do not satisfy halts under the same condition it halts
// under in the Aggregate, positioned at the module's own document and at the
// subsystem that declared it, and filed under the developer. Without the cascade
// the attachment reaches no component and nothing is reported.
func TestAModuleSubsystemAttachmentIsHeldToTheFeaturesApplicability(t *testing.T) {
	t.Parallel()
	got := socketSlotFindings(t,
		moduleCascadeWorkspace(t, moduleSubsystemAttaching(
			attachment(inAggregateSubsystem, "metrics", "feature.site/workers-only"))),
		analyzer.ErrSelectorViolation())
	if len(got) != 1 {
		t.Fatalf("the run carries %d applicability finding(s) %+v; want exactly one", len(got), got)
	}
	want := socketSlotFinding{file: "site-module.yaml", field: ".spec.subsystems.tier.features.metrics"}
	if got[0].file != want.file || got[0].field != want.field {
		t.Errorf("the finding is positioned at %s %s; want %s %s", got[0].file, got[0].field, want.file, want.field)
	}
	if wantCategory := filedUnder(want.file); got[0].category != wantCategory {
		t.Errorf("the finding is filed under %q; want %q", got[0].category, wantCategory)
	}
}

// TestAModuleSubsystemAttachmentReachesTheModulesComponent proves the cascade
// itself through the validation entry point: an attachment the module's
// subsystem declares, with a Feature the component satisfies, is held by the
// module's component in the graph with the values the module wrote, and draws no
// applicability finding.
func TestAModuleSubsystemAttachmentReachesTheModulesComponent(t *testing.T) {
	t.Parallel()
	in := moduleCascadeWorkspace(t, moduleSubsystemAttaching(
		attachment(inAggregateSubsystem, "note", "feature.site/needs-level", "values: { level: from-module }")))
	if got := socketSlotFindings(t, in, analyzer.ErrSelectorViolation()); len(got) != 0 {
		t.Fatalf("an attachment the component satisfies drew applicability findings %+v", got)
	}
	t4, err := pipelinetest.Tier4E(t, in)
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	held, ok := t4.IRGraph.Spec.FeatureValuesMapping.Get("mod.app")
	if !ok {
		t.Fatal("the module's component holds no attachment; want the one its subsystem declares")
	}
	entry, ok := held.Get("note")
	if !ok {
		t.Fatalf("the module's component holds %v; want \"note\" among them", held.Keys())
	}
	if got, _ := entry.Values.Get("level"); got != "from-module" {
		t.Errorf("the attachment's value is %v; want the module's (from-module)", got)
	}
}
