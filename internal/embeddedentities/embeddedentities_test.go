// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package embeddedentities

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// recordingTB is a testing.TB recording whether the guard skipped or failed it,
// and ending the goroutine it runs on as testing.T does. internal/wasmtest's
// tests carry the same small type for its own helpers; a test type cannot be
// shared across packages without an exported non-test one.
type recordingTB struct {
	testing.TB
	skipped, failed string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Skipf(format string, args ...any) {
	r.skipped = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failed = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// TestRequireSkipsOnlyWithoutAggregaHub lays out a module holding no
// generated embedded plugin entity and checks require in each
// state of the aggrega-hub checkout beside it: absent skips naming aggrega-hub,
// present fails naming the generator, and a generated entity lets the test run.
//
// 🔴 PLANTED DEFECT: in require (embeddedentities.go), skip whenever no
// entity is generated: the present-checkout case skips instead of failing, and
// this test fails on it.
func TestRequireSkipsOnlyWithoutAggregaHub(t *testing.T) {
	t.Parallel()
	run := func(moduleRoot string) *recordingTB {
		rec := &recordingTB{TB: t}
		done := make(chan struct{})
		go func() {
			defer close(done)
			require(rec, moduleRoot)
		}()
		<-done
		return rec
	}
	parent := t.TempDir()
	moduleRoot := filepath.Join(parent, "cli")
	entityDir := filepath.Join(moduleRoot, filepath.FromSlash(entityDir))
	if err := os.MkdirAll(entityDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", entityDir, err)
	}

	rec := run(moduleRoot)
	if !strings.Contains(rec.skipped, "aggrega-hub, whose plugin overlays they are generated from, is not checked out") || rec.failed != "" {
		t.Errorf("no entity and no aggrega-hub: skipped %q, failed %q; want a skip naming aggrega-hub", rec.skipped, rec.failed)
	}

	hub := filepath.Join(parent, "aggrega-hub")
	if err := os.MkdirAll(filepath.Join(hub, "plugin-overlays"), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", hub, err)
	}
	rec = run(moduleRoot)
	if rec.skipped != "" || !strings.Contains(rec.failed, "generate them with scripts/gen-embedded-plugin-entities.sh") {
		t.Errorf("no entity, aggrega-hub present: skipped %q, failed %q; want a failure naming the generator", rec.skipped, rec.failed)
	}

	if err := os.WriteFile(filepath.Join(entityDir, "helm.plugin.yaml"), []byte("kind: Plugin\n"), 0o644); err != nil {
		t.Fatalf("write entity: %v", err)
	}
	rec = run(moduleRoot)
	if rec.skipped != "" || rec.failed != "" {
		t.Errorf("a generated entity: skipped %q, failed %q; want the test to run", rec.skipped, rec.failed)
	}
}
