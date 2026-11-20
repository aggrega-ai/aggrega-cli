// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// A Feature instance compiles at the highest version every constraint declared on
// it admits, as a component instance does: the Manifest's features key and the
// spec.implements of the FeatureRecipe that key names.
//
// The workspace is a copy of the quickstart a published documentation example
// carries, over a catalog that also holds core/secret-value at 1.1.0. The Manifest
// maps the Feature with a key naming no version, to the FeatureRecipe implementing
// 1.0.0, while the Aggregate names it with no version, so the FeatureRecipe alone
// holds the attachments to 1.0.0.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// secretValueFeature is the Feature the quickstart's attachments name.
const secretValueFeature = "core/secret-value"

// featureVersionsWorkspace lays the quickstart down with core/secret-value held at
// 1.0.0 and 1.1.0 and the features key naming no version.
func featureVersionsWorkspace(t *testing.T) string {
	t.Helper()
	root := instanceVersionsWorkspace(t, "quickstart-web")
	feature := filepath.Join(root, "catalog", "feature", "core", "secret-value")
	body, err := os.ReadFile(filepath.Join(feature, "1.0.0", "feature.yaml"))
	if err != nil {
		t.Fatalf("read the Feature: %v", err)
	}
	newer := strings.Replace(string(body), "version: 1.0.0", "version: 1.1.0", 1)
	if newer == string(body) {
		t.Fatal("the fixture's Feature carries no version line to move")
	}
	if err := os.MkdirAll(filepath.Join(feature, "1.1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(feature, "1.1.0", "feature.yaml"), newer)
	replaceInFixture(t, filepath.Join(root, "manifest.yaml"),
		"core/secret-value:1.0.0: compose/dotenv-secret:1.0.0", "core/secret-value: compose/dotenv-secret:1.0.0")
	return root
}

// TestValidateHoldsFeatureAtRecipeVersion validates the Release over both held
// versions of the Feature: every attachment compiles at 1.0.0, the version the
// FeatureRecipe implements.
func TestValidateHoldsFeatureAtRecipeVersion(t *testing.T) {
	t.Parallel()
	root := featureVersionsWorkspace(t)

	resolved, vr := instanceVersionsCoreValidate(t, root)
	if findings := coreFindings(resolved, vr); len(findings) > 0 {
		t.Fatalf("the Release does not validate:\n%v", findings)
	}
	if vr.Tier4 == nil || vr.Tier4.IRGraph == nil {
		t.Fatal("the validation built no graph")
	}
	for node, attachment := range map[string]string{"web": "db_password", "db": "password"} {
		entries, ok := vr.Tier4.IRGraph.Spec.FeatureValuesMapping.Get(node)
		if !ok {
			t.Fatalf("the graph records no attachments on node %q", node)
		}
		entry, ok := entries.Get(attachment)
		if !ok {
			t.Fatalf("node %q records no attachment %q", node, attachment)
		}
		if entry.Type != secretValueFeature || entry.ResolvedVersion != "1.0.0" {
			t.Errorf("%s.%s compiled as %s at %q, want %s at 1.0.0", node, attachment, entry.Type, entry.ResolvedVersion, secretValueFeature)
		}
	}
	if verdict, diag := validateReleaseAt(t, root); verdict != nil {
		t.Errorf("aggrega validate --release refused the Release: %v\n%s", verdict, diag)
	}
}

// TestPullRecordsFeatureInstanceVersion records the Feature's row at the version
// the FeatureRecipe implements.
func TestPullRecordsFeatureInstanceVersion(t *testing.T) {
	t.Parallel()
	root := featureVersionsWorkspace(t)
	releasePath := filepath.Join(root, "release.yaml")
	if text, err := pullInstanceVersions(t, root, releasePath, nil); err != nil {
		t.Fatalf("pull: %v\n%s", err, text)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	var rows []string
	for _, e := range lf.Entities {
		if e.Kind == closure.KindFeature && e.URI == secretValueFeature {
			rows = append(rows, e.ResolvedVersion)
		}
	}
	if len(rows) != 1 || rows[0] != "1.0.0" {
		t.Errorf("the lockfile records %s at %v, want one row at 1.0.0", secretValueFeature, rows)
	}
	assertPullHoldsItsLockfile(t, root, releasePath, nil)
}
