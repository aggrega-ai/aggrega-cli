// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// selectorViolatingAggregate attaches two Features to the worker component,
// whose labels neither Feature's appliesTo admits, so a Release over it carries
// two selector violations, each positioned at the attachment that wrote it.
const selectorViolatingAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: selector-surface-attach
  version: "1.0.0"
spec:
  components:
    web:
      type: core/web
    worker:
      type: core/worker
      features:
        eq:
          type: core/feat-equality
        zone-in:
          type: core/feat-in
`

// selectorViolatingWorkspace copies the selector-surface fixture into a fresh
// workspace and replaces its Aggregate with selectorViolatingAggregate. It
// returns the workspace root and the Release that reaches the Aggregate.
func selectorViolatingWorkspace(t *testing.T) (string, string) {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	fixture := filepath.Join(filepath.Dir(filepath.Dir(cwd)), "internal", "compiler", "testdata", "selector-surface")
	root := filepath.Join(t.TempDir(), "ws")
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy the selector-surface fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "aggregate-attach.yaml"), []byte(selectorViolatingAggregate), 0o644); err != nil {
		t.Fatalf("write the violating Aggregate: %v", err)
	}
	return root, filepath.Join(root, "release-attach.yaml")
}

// TestValidateReleasePrintsEachFindingWithItsHeadAndShortCode validates a Release
// carrying two selector violations and reads what the command prints for them.
//
// Each finding opens with the bracketed head a build or a pull prints for the
// same failure — its category and the short code `aggrega explain` resolves —
// followed by the file and the key it is positioned at, and then the sentence
// naming the Feature and the component. The concise form's single summary line
// carries the first finding with the same head.
//
// 🔴 PLANTED DEFECT: render each finding with cerrors.CauseChain instead of
// releaseFinding, and both forms fail — every finding prints as its sentence
// alone, with no head, no short code and no position.
func TestValidateReleasePrintsEachFindingWithItsHeadAndShortCode(t *testing.T) {
	code, ok := cerrors.ShortCodeOf(analyzer.ErrSelectorViolation())
	if !ok {
		t.Fatal("the selector-violation condition carries no short code, so no head can name it")
	}
	head := "[" + string(cerrors.DeveloperError) + " " + code + "]"

	root, release := selectorViolatingWorkspace(t)
	rw, err := workspaceconfig.Resolve(root, root, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}

	t.Run("detailed", func(t *testing.T) {
		var diag, out bytes.Buffer
		err := validateRelease(t.Context(), rw, release, nil, compilerops.VerbosityDetailed, false, false, &diag, &out)
		if err == nil {
			t.Fatalf("a Release carrying two selector violations validated clean\n%s", diag.String())
		}
		got := diag.String()
		for _, want := range []string{
			"  " + head + " aggregate-attach.yaml at .spec.components.worker.features.eq\n",
			"  " + head + " aggregate-attach.yaml at .spec.components.worker.features.zone-in\n",
			`declared by Feature "core/feat-equality", not satisfied by the labels of "worker"`,
			`declared by Feature "core/feat-in", not satisfied by the labels of "worker"`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the report does not carry %q\nfull report:\n%s", want, got)
			}
		}
		if n := strings.Count(got, head); n != 2 {
			t.Errorf("the report opens %d finding(s) with %s, want 2\nfull report:\n%s", n, head, got)
		}
	})

	t.Run("concise", func(t *testing.T) {
		var diag, out bytes.Buffer
		err := validateRelease(t.Context(), rw, release, nil, compilerops.VerbosityConcise, false, false, &diag, &out)
		if err == nil {
			t.Fatalf("a Release carrying two selector violations validated clean\n%s", diag.String())
		}
		want := "release dependency validation: 2 diagnostic(s); first: " + head +
			" aggregate-attach.yaml at .spec.components.worker.features.eq selector violation ("
		if got := diag.String(); !strings.Contains(got, want) {
			t.Errorf("the concise summary does not open its first finding with the head\nwant it to carry: %q\nfull report:\n%s", want, got)
		}
	})
}
