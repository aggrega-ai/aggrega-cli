// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"bytes"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// baselineCfg assembles the pre-publish single-entity Config the predecessor-
// selection tests share, over an in-memory registry target.
func baselineCfg(t *testing.T, target *memoryRegistryTarget, proposedPath, baseline string, diag *bytes.Buffer) Config {
	t.Helper()
	return Config{
		EntityPath:     proposedPath,
		PrePublish:     true,
		Baseline:       baseline,
		PluginPath:     wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm"),
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		FetchTarget:    target,
		Workspace:      resolveTempWorkspace(t),
		DiagOut:        diag,
	}
}

// seedRemovedExport pushes the removed-export previous fixture under each of the
// given tags (all pointing at the same predecessor body, so whichever tag is
// selected the surface diff is the same removed export → MAJOR) and returns a
// proposed entity at candidateVer.
func seedRemovedExport(t *testing.T, candidateVer string, tags ...string) (*memoryRegistryTarget, string) {
	t.Helper()
	target := &memoryRegistryTarget{Store: memory.New()}
	previousYAML := pluginFixtureBytes(t, "removed-export", "previous.yaml")
	for _, tag := range tags {
		// Rewrite the predecessor body's version to match its tag so each tag
		// carries distinct content (the content-addressed store rejects a
		// duplicate blob) — the describe-surface the classifier diffs is
		// unchanged, so every tag stays a removed-export → MAJOR predecessor.
		body := []byte(strings.Replace(string(previousYAML), "version: 1.0.0", "version: "+tag, 1))
		pushFixtureToTarget(t, target, "plugin", "partner/custom-renderer", tag, body)
	}
	proposedYAML := pluginFixtureBytes(t, "removed-export", "proposed.yaml")
	return target, writeEntityFile(t, proposedYAML, candidateVer)
}

// TestLintPrePublishBackPatchSelectsHighestBelow pins the core case: when a
// back-patch (1.4.2) is published while a far-future stable (2.0.0) already sits
// in the registry, the predecessor diffed must be the version just below the
// candidate (1.4.1), not the latest (2.0.0). The removed export demands MAJOR;
// 1.4.1 -> 1.4.2 is a PATCH, so the run halts naming 1.4.1 as the predecessor.
func TestLintPrePublishBackPatchSelectsHighestBelow(t *testing.T) {
	target, proposedPath := seedRemovedExport(t, "1.4.2", "1.4.1", "2.0.0")
	var diag bytes.Buffer
	err := RunEntity(t.Context(), baselineCfg(t, target, proposedPath, "", &diag))
	if err == nil {
		t.Fatalf("back-patch removing an export should HALT; got PASS (diag: %s)", diag.String())
	}
	if !strings.Contains(err.Error(), "1.4.1 -> 1.4.2") {
		t.Errorf("predecessor should be the highest-below 1.4.1; got: %v", err)
	}
	if strings.Contains(err.Error(), "2.0.0 -> ") {
		t.Errorf("predecessor must NOT be the latest 2.0.0 (the back-patch bug); got: %v", err)
	}
}

// TestLintPrePublishForwardSelectsBelowLatest confirms a normal forward release
// is unchanged: for candidate 2.1.0 over [1.0.0, 2.0.0] the highest version
// below the candidate is 2.0.0 — which coincides with the latest on a forward
// release. The removed export demands MAJOR; 2.0.0 -> 2.1.0 is MINOR, so it halts
// naming 2.0.0.
func TestLintPrePublishForwardSelectsBelowLatest(t *testing.T) {
	target, proposedPath := seedRemovedExport(t, "2.1.0", "1.0.0", "2.0.0")
	var diag bytes.Buffer
	err := RunEntity(t.Context(), baselineCfg(t, target, proposedPath, "", &diag))
	if err == nil {
		t.Fatalf("forward release removing an export should HALT; got PASS (diag: %s)", diag.String())
	}
	if !strings.Contains(err.Error(), "2.0.0 -> 2.1.0") {
		t.Errorf("forward predecessor should be 2.0.0; got: %v", err)
	}
}

// TestLintPrePublishPreReleaseOnlyPreserved proves the pre-release-only warning
// and first-publication branch survive the switch to highest-below: a registry
// holding only a pre-release tag yields no qualifying predecessor, so the run
// warns and treats the publish as a first publication (passes).
func TestLintPrePublishPreReleaseOnlyPreserved(t *testing.T) {
	target := &memoryRegistryTarget{Store: memory.New()}
	previousYAML := pluginFixtureBytes(t, "removed-export", "previous.yaml")
	rcBody := []byte(strings.Replace(string(previousYAML), "version: 1.0.0", "version: 1.0.0-rc.1", 1))
	pushFixtureToTarget(t, target, "plugin", "partner/custom-renderer", "1.0.0-rc.1", rcBody)
	proposedYAML := pluginFixtureBytes(t, "removed-export", "proposed.yaml")
	proposedPath := writeEntityFile(t, proposedYAML, "1.0.0")

	var diag bytes.Buffer
	err := RunEntity(t.Context(), baselineCfg(t, target, proposedPath, "", &diag))
	if err != nil {
		t.Fatalf("pre-release-only registry should treat publish as first publication (PASS); got HALT: %v (diag: %s)", err, diag.String())
	}
	if !strings.Contains(diag.String(), "only pre-release tags") {
		t.Errorf("expected the pre-release-only warning; got: %s", diag.String())
	}
	if !strings.Contains(diag.String(), "first publication of") {
		t.Errorf("expected the first-publication diagnostic; got: %s", diag.String())
	}
}

// TestLintPrePublishBaselineOverride proves --baseline reroutes the predecessor
// through the same selection surface: for candidate 2.0.1 over [1.4.1, 2.0.0] the
// default predecessor is 2.0.0 (removed export demands MAJOR; 2.0.0 -> 2.0.1 is
// PATCH → halt naming 2.0.0). Forcing --baseline 1.4.1 makes the bump 1.4.1 ->
// 2.0.1 a MAJOR, which satisfies the requirement and passes — the only variable
// changed is the baseline, so the pass is causally the override.
func TestLintPrePublishBaselineOverride(t *testing.T) {
	t.Run("default halts naming latest-below", func(t *testing.T) {
		target, proposedPath := seedRemovedExport(t, "2.0.1", "1.4.1", "2.0.0")
		var diag bytes.Buffer
		err := RunEntity(t.Context(), baselineCfg(t, target, proposedPath, "", &diag))
		if err == nil {
			t.Fatalf("default predecessor 2.0.0 with a PATCH bump should HALT; got PASS (diag: %s)", diag.String())
		}
		if !strings.Contains(err.Error(), "2.0.0 -> 2.0.1") {
			t.Errorf("default predecessor should be 2.0.0; got: %v", err)
		}
	})
	t.Run("baseline forces a different predecessor", func(t *testing.T) {
		target, proposedPath := seedRemovedExport(t, "2.0.1", "1.4.1", "2.0.0")
		var diag bytes.Buffer
		err := RunEntity(t.Context(), baselineCfg(t, target, proposedPath, "1.4.1", &diag))
		if err != nil {
			t.Fatalf("--baseline 1.4.1 makes the bump MAJOR (satisfies) and should PASS; got HALT: %v (diag: %s)", err, diag.String())
		}
	})
}

// TestLintPrePublishBaselineInvalid proves an out-of-repo or non-SemVer
// --baseline value is a loud validation error, not a silent fallback.
func TestLintPrePublishBaselineInvalid(t *testing.T) {
	cases := []struct {
		name     string
		baseline string
	}{
		{"absent from registry", "9.9.9"},
		{"not strict semver", "not-a-version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, proposedPath := seedRemovedExport(t, "1.4.2", "1.4.1", "2.0.0")
			var diag bytes.Buffer
			err := RunEntity(t.Context(), baselineCfg(t, target, proposedPath, tc.baseline, &diag))
			if err == nil {
				t.Fatalf("invalid --baseline %q should error; got PASS (diag: %s)", tc.baseline, diag.String())
			}
			if !strings.Contains(err.Error(), "--baseline") || !strings.Contains(err.Error(), tc.baseline) {
				t.Errorf("error should name --baseline and the offending value %q; got: %v", tc.baseline, err)
			}
		})
	}
}
