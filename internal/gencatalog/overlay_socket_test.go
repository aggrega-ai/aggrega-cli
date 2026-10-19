// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gencatalog

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestOverlaysOfOneNameInTwoSocketsAreMatchedByTheirSocket lays two overlays
// declaring the name compose, one in the provider socket's directory and one in
// the importer socket's, and requires each guest coordinate matched to the
// overlay of its own socket; an overlay alone under its name to be matched
// wherever it sits; and two overlays of one name in one socket directory to be
// refused.
//
// 🔴 PLANTED DEFECT: in loadOverlaysByPlugin (gencatalog.go), refuse every second
// overlay of a name whatever its socket: the two-socket row fails on the refusal.
func TestOverlaysOfOneNameInTwoSocketsAreMatchedByTheirSocket(t *testing.T) {
	t.Parallel()
	overlay := func(name, timeout string) string {
		return "apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: " + name +
			"\nspec:\n  runtime:\n    timeoutMs: " + timeout + "\n"
	}
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "provider", "compose.yaml"), overlay("compose", "1111"))
	writeFixture(t, filepath.Join(dir, "importer", "compose.yaml"), overlay("compose", "2222"))
	writeFixture(t, filepath.Join(dir, "json.yaml"), overlay("json", "3333"))
	overlays, err := loadOverlaysByPlugin(dir, dir)
	if err != nil {
		t.Fatalf("two overlays of one name in two socket directories were refused: %v", err)
	}
	for uri, want := range map[string]uint64{"provider/compose": 1111, "importer/compose": 2222, "parser/json": 3333} {
		got, ok := overlayFor(overlays, uri)
		if !ok || got.Spec.Runtime == nil || got.Spec.Runtime.TimeoutMs != want {
			t.Errorf("the overlay matched to %s is %+v (found %v), want the one with timeoutMs %d", uri, got.Spec.Runtime, ok, want)
		}
	}
	if _, ok := overlayFor(overlays, "linter/compose"); ok {
		t.Errorf("a guest of a third socket was matched to an overlay of another socket")
	}

	dup := t.TempDir()
	writeFixture(t, filepath.Join(dup, "provider", "compose.yaml"), overlay("compose", "1"))
	writeFixture(t, filepath.Join(dup, "provider", "compose-again.yaml"), overlay("compose", "2"))
	if _, err := loadOverlaysByPlugin(dup, dup); err == nil || !strings.Contains(err.Error(), "identity must be unique") {
		t.Errorf("two overlays of one name in one socket directory were not refused: %v", err)
	}
}
