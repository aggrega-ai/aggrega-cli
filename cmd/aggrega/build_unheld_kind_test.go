// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestBuildAutoPull_NamesAFileOfAKindNoCatalogHolds drives the ordinary first
// build of a workspace — one with no lockfile, which fills it before rendering —
// over a catalog whose only document declaring the Component the Aggregate
// places misspells its kind. The fill's resolution fails on the reference, and
// what is asserted is what that failure tells the reader.
//
// The fill reads the lazy catalog view rather than the load a build with a
// lockfile reads, so this is the path that would fall silent if only one of the
// two walks recorded the file. Where the misspelled file declares the name the
// reference asks for, the failure names it, the kind it declares and its short
// code, keeps its own condition, and carries no advice to move a file the
// catalog already reads. Where it declares another name, it is not mentioned
// and the advice stands — the positive control that keeps the first row from
// passing on a failure that never mentions anything.
//
// The workspace is offline, so the fill reaches no registry for a name no
// directory holds, and the failure is the same on every machine.
func TestBuildAutoPull_NamesAFileOfAKindNoCatalogHolds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declares string
		named    bool
	}{
		{name: "the misspelled file declares the referenced name", declares: "local/probe-server", named: true},
		{name: "the misspelled file declares another name", declares: "local/other-server", named: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			releasePath, catalogRoot := writeValidWorkspace(t, root)
			writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\noffline: true\n")
			writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: iso-smoke
  version: "1.0.0"
spec:
  components:
    only:
      type: local/probe-server
      values:
        port: 8080
`)
			misspelled := filepath.Join(catalogRoot, "base", "components", "probe.yaml")
			writeFixture(t, misspelled, `apiVersion: aggrega.ai/v1alpha1
kind: Componnent
metadata:
  name: `+tc.declares+`
  version: "1.0.0"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
			if _, err := os.Stat(filepath.Join(root, "release.lock")); !os.IsNotExist(err) {
				t.Fatalf("precondition: the fill must be the step that runs, so there must be no lockfile yet; stat err = %v", err)
			}
			rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
			account := strings.TrimSpace(rw.LocalCatalogAdviceNote(releasePath))
			if account == "" {
				t.Fatal("precondition: this workspace composes no account of where local entities are read from, " +
					"so the assertions about it below would hold on a failure that carried nothing")
			}

			var diagBuf, outBuf bytes.Buffer
			err := dispatchBuildWithDiag(context.Background(), rw, releasePath, []string{catalogRoot}, false,
				buildDiagOptions{}, buildops.RunOptions{AutoPull: true}, &diagBuf, &outBuf)
			if err == nil {
				t.Fatal("the fill resolved a Component no document of a held kind declares")
			}
			if !errors.Is(err, closure.ErrUnresolvableURI()) {
				t.Fatalf("the failure lost the unresolvable-reference condition: %v", err)
			}
			rendered := formatCompilerError(err)
			if !strings.HasPrefix(rendered, "auto-pull: ") {
				t.Errorf("the failure is not the fill's; rendered:\n%s", rendered)
			}
			names := strings.Contains(rendered, misspelled) && strings.Contains(rendered, `kind "Componnent"`) &&
				strings.Contains(rendered, "E0371") && strings.Contains(rendered, "a kind a catalog does not hold")
			if names != tc.named {
				t.Errorf("the failure names the misspelled file = %v, want %v; rendered:\n%s", names, tc.named, rendered)
			}
			if advises := strings.Contains(rendered, account); advises == tc.named {
				t.Errorf("the failure carries the account of where local entities are read from = %v, want %v; rendered:\n%s",
					advises, !tc.named, rendered)
			}
		})
	}
}
