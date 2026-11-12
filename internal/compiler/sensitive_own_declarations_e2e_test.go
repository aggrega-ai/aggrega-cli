// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that the trace's re-render of a
// workload withholds what the node's own entities declare beyond its own values
// and infra: the leaves the Feature attached to the node itself declares, and the
// provides keys the node's Component declares sensitive.
//
// The workload template reads nothing else that is declared, so the re-render is
// triggered by those two readings alone. Each withheld position sits beside an
// undeclared sibling read the same way, because a display that blanked every
// variable would satisfy the withheld rows on its own.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

const ownDeclarationsArtifactPath = "dist/own/compose/docker-compose.yaml"

const (
	// The own attachment's declared values leaf.
	ownDeclaredPassword = "own-decl-password-plaintext"
	// The own attachment's resolved provides value under a key its Feature
	// declares sensitive.
	ownDeclaredProvided = "s3cr3t-from-feature"
	// The node's resolved provides value under a key its Component declares
	// sensitive, computed from a declared leaf of its own values.
	ownDeclaredSession = "sess-own-session-plaintext"
	// The own attachment's undeclared values leaf, published again as its
	// undeclared provides key.
	ownDeclaredLocator = "own-decl-locator-public"
)

// ownDeclarationsWorkloadSlots are the node's trace slots that project the
// rendered .workload document.
var ownDeclarationsWorkloadSlots = []string{
	"09b-feature-rendering-boundary.yaml",
	"10-engine-synthesis/app.compose-fragment.yaml",
}

func ownDeclarationsBuild(t *testing.T, unredacted bool) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:     "testdata/sensitive-redaction/own-declarations-release.yaml",
		CatalogPaths:    []string{"testdata/sensitive-redaction/catalog/base"},
		IncludeTrace:    true,
		TraceUnredacted: unredacted,
	})
}

// TestSensitiveRedaction_TraceWithholdsTheNodesOwnDeclarationsFromTheWorkload is
// the per-variable table over every slot that shows the workload, with the trace
// withholding and with `--trace-unredacted`. Withholding, each variable reading a
// declared datum shows the replacement text inside the announced re-render;
// unredacted, the same slot shows the emitted document, every value as rendered
// and no announcement. The undeclared siblings read the same either way.
func TestSensitiveRedaction_TraceWithholdsTheNodesOwnDeclarationsFromTheWorkload(t *testing.T) {
	t.Parallel()

	type row struct {
		variable   string
		withheld   bool
		unredacted string
	}
	rows := []row{
		{"OWN_PASSWORD", true, ownDeclaredPassword},
		{"OWN_PROVIDED", true, ownDeclaredProvided},
		{"SESSION", true, ownDeclaredSession},
		{"OWN_LOCATOR", false, ownDeclaredLocator},
		{"OWN_NAME", false, ownDeclaredLocator},
		{"HOST", false, "app"},
		{"APP_NAME", false, "owndemo"},
	}

	modes := []struct {
		name       string
		unredacted bool
	}{
		{"withholding", false},
		{"unredacted", true},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			result := ownDeclarationsBuild(t, mode.unredacted)
			for _, slot := range ownDeclarationsWorkloadSlots {
				body := traceEntry(t, result, "boundaries/own/app/"+slot)
				announced := strings.Contains(body, redact.SubstitutedProjection())
				if announced == mode.unredacted {
					t.Errorf("%s: announcement present=%v, want %v\n--- slot ---\n%s", slot, announced, !mode.unredacted, body)
				}
				leaves := workloadLeaves(t, body)
				for _, r := range rows {
					want := r.unredacted
					if r.withheld && !mode.unredacted {
						want = redact.Placeholder()
					}
					path := "containers.main.variables." + r.variable
					if got := leaves[path]; got != want {
						t.Errorf("%s: %s = %q, want %q", slot, path, got, want)
					}
				}
			}
		})
	}
}

// TestSensitiveRedaction_OwnDeclarationsAppearNowhereInTheWithholdingTrace is the
// whole-trace half: no slot of the withholding build carries a declared value in
// the clear, whichever projection would have shown it. The unredacted build is
// the needle — the same search must find each value there, or a zero here would
// say nothing about the search.
func TestSensitiveRedaction_OwnDeclarationsAppearNowhereInTheWithholdingTrace(t *testing.T) {
	t.Parallel()
	withholding := ownDeclarationsBuild(t, false)
	unredacted := ownDeclarationsBuild(t, true)

	for _, plaintext := range []string{ownDeclaredPassword, ownDeclaredProvided, ownDeclaredSession} {
		if len(withholding.Trace) == 0 {
			t.Fatal("the withholding build produced no trace; the search would be vacuous")
		}
		for slot, body := range withholding.Trace {
			if strings.Contains(string(body), plaintext) {
				t.Errorf("%s carries %q in the clear\n--- slot ---\n%s", slot, plaintext, body)
			}
		}
		found := false
		for _, body := range unredacted.Trace {
			if strings.Contains(string(body), plaintext) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the unredacted trace carries %q nowhere; the search above cannot be trusted to find it", plaintext)
		}
	}
}

// TestSensitiveRedaction_OwnDeclarationsArtifactCarriesPlaintext is the boundary's
// other half: every value the trace withholds reaches the deployable in the clear,
// and the deployable is byte-identical whether the trace withholds or not.
func TestSensitiveRedaction_OwnDeclarationsArtifactCarriesPlaintext(t *testing.T) {
	t.Parallel()
	withholding := ownDeclarationsBuild(t, false)
	unredacted := ownDeclarationsBuild(t, true)

	body, ok := withholding.Artifacts[ownDeclarationsArtifactPath]
	if !ok {
		t.Fatalf("expected the merged compose artifact at %q; got %v", ownDeclarationsArtifactPath, sortedTraceSlots(withholding.Artifacts))
	}
	artifact := string(body)
	for _, want := range []string{ownDeclaredPassword, ownDeclaredProvided, ownDeclaredSession, ownDeclaredLocator} {
		if !strings.Contains(artifact, want) {
			t.Errorf("the rendered artifact must carry %q verbatim\n--- artifact ---\n%s", want, artifact)
		}
	}
	if strings.Contains(artifact, redact.Placeholder()) {
		t.Errorf("the rendered artifact must never contain the redaction placeholder\n--- artifact ---\n%s", artifact)
	}

	if len(unredacted.Artifacts) != len(withholding.Artifacts) {
		t.Fatalf("the trace mode changed the rendered file count: %d unredacted, %d withholding", len(unredacted.Artifacts), len(withholding.Artifacts))
	}
	for path, want := range withholding.Artifacts {
		if got := unredacted.Artifacts[path]; string(got) != string(want) {
			t.Errorf("the trace mode moved rendered bytes at %q\n--- withholding ---\n%s\n--- unredacted ---\n%s", path, want, got)
		}
	}
}
