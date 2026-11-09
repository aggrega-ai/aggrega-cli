// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refusedSiblingRecipe is a Recipe the catalog walk refuses to place — a helm
// Recipe with no output-bearing block — so a view reading its directory writes
// the one-line notice that it skipped it.
const refusedSiblingRecipe = "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: refused\n" +
	"  version: \"1.0.0\"\nspec:\n  capability: helm\n  implements: refused\n"

// TestThePinStepsCatalogNoticesGoToTheWriterItIsGiven holds the catalog view the
// pin step reads an entity through to the writer its caller hands it: the
// notice that the view skipped a document beside the entity reaches that writer,
// and nothing reaches the process's own standard error. A surface decides where
// its reader looks — a command its error stream, a long-running process the
// stream it was launched with — and the step writes nowhere of its own.
//
// The step is driven through Hold, which reads the entity exactly as Pin does
// and writes nothing, so the drive leaves the workspace as it found it.
//
// 🔴 PLANTED DEFECT: drop view.WriteNoticesTo(opts.Notices) from planNamed
// (internal/entitypin/entity.go), or write the view's notices to the process
// stream in pkg/catalog — and this fails.
func TestThePinStepsCatalogNoticesGoToTheWriterItIsGiven(t *testing.T) {
	ws := t.TempDir()
	rw := resolvedWorkspace(t, ws)
	dir := filepath.Join(ws, "catalog", "server")
	recipe := filepath.Join(dir, "recipe.yaml")
	writeFile(t, recipe, recipeYAML("server", "", true))
	writeFile(t, filepath.Join(dir, "extra.yaml"), "kind: ConfigMap\n")
	refused := filepath.Join(dir, "refused.yaml")
	writeFile(t, refused, refusedSiblingRecipe)

	var notices bytes.Buffer
	stray := captureEntityPinStderr(t, func() {
		// The hold's own verdict is not the subject: no lock was written, so it
		// refuses, and that is beside the point here.
		_ = Hold(context.Background(), Options{Workspace: rw, EntityPath: recipe, Notices: &notices}, testRemedy)
	})

	var skipped []string
	for _, line := range strings.Split(notices.String(), "\n") {
		if strings.Contains(line, "refused.yaml") && strings.Contains(line, "skipped:") {
			skipped = append(skipped, line)
		}
	}
	if len(skipped) != 1 {
		t.Fatalf("want one notice naming the skipped sibling on the writer the step was given, got %d:\n%s",
			len(skipped), notices.String())
	}
	if stray != "" {
		t.Errorf("the pin step's catalog view wrote to the process's standard error:\n%s", stray)
	}
}

// captureEntityPinStderr runs fn with the process's standard error redirected and
// returns what was written to it.
func captureEntityPinStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	func() {
		defer func() { os.Stderr = saved }()
		fn()
	}()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
