// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
)

// The three tests here drive the verdict-trust record through the pull COMMAND
// rather than through the shared resolution: what each one is really asserting is
// what a person invoking `aggrega pull` sees on disk and on their two streams.
// The rest of the record's behaviour is asserted against the shared layer, beside
// the code that implements it.

// TestVerdictTrustNoVerdictConfigurationWritesNoFile proves the negative
// criterion: a pull over a workspace that attaches no governance plugin and no
// linter plugin produces NO trust record — not an empty one — and says nothing
// about it on either sink.
//
// An empty file would be the wrong outcome and the easy one to ship by accident:
// it reads as "these attachments were checked and there were none", which is a
// claim, where absence is the honest first-run state.
func TestVerdictTrustNoVerdictConfigurationWritesNoFile(t *testing.T) {
	root := t.TempDir()
	releasePath := authoringFixture(t, root)

	var stdout, stderr bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		DiagOut:     &stderr,
		Out:         &stdout,
	}); err != nil {
		t.Fatalf("pullRun: %v (stderr=%q)", err, stderr.String())
	}

	recordPath := pluginstrust.PathFor(releasePath)
	if _, err := os.Stat(recordPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a pull with no verdict-returning attachment must write no record at %s; stat err = %v", recordPath, err)
	}
	if strings.Contains(stderr.String(), pluginstrust.FileName) {
		t.Errorf("nothing was recorded, so nothing should be said about the record; stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Pull complete") {
		t.Fatalf("expected the pull summary on stdout; got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "trusted_plugins") {
		t.Errorf("the completion line must carry no count when nothing was recorded; got %q", stdout.String())
	}
}

// TestTrustedPluginsSummary pins the completion-line field: absent at zero,
// present with the count otherwise. Absent rather than `=0` because a workspace
// attaching no verdict-returning plugin was never asked the question.
func TestTrustedPluginsSummary(t *testing.T) {
	if got := trustedPluginsSummary(0); got != "" {
		t.Errorf("trustedPluginsSummary(0) = %q; want the empty string", got)
	}
	if got := trustedPluginsSummary(3); got != ", trusted_plugins=3" {
		t.Errorf("trustedPluginsSummary(3) = %q; want %q", got, ", trusted_plugins=3")
	}
}

// TestPullRefusesADamagedRecordEvenWithNothingAttached locks the reading a pull
// now takes of a record that exists and does not satisfy its schema: it halts,
// whether or not the workspace attaches anything.
//
// It is the same fail-closed reading every other consumer of the file takes, and
// the asymmetry it removes is the one that mattered: a build and a release-scoped
// lint already halted on a damaged record unconditionally, so a pull that passed
// over it left the two commands disagreeing about whether the committed file was
// usable.
//
// The halt is not attributable to ONE read, and that is worth stating rather than
// guessing at: a pull now reads the record at two points — once to decide which
// upgrade targets it accepts, and once at the write — and EITHER one is sufficient
// on its own. So a change that made one of them lenient would leave this test
// green, and only making both lenient loses the halt.
//
// The A/B is the second half: the identical workspace with the file removed pulls
// clean, so the halt is attributable to the record's bytes rather than to the
// fixture.
func TestPullRefusesADamagedRecordEvenWithNothingAttached(t *testing.T) {
	root := t.TempDir()
	releasePath := authoringFixture(t, root)
	recordPath := pluginstrust.PathFor(releasePath)
	// Schema-invalid: an entry missing the required resolved_version and digest.
	const damaged = "version: 1\nplugins:\n  - plugin: linter/x\n"
	if err := os.WriteFile(recordPath, []byte(damaged), 0o644); err != nil {
		t.Fatalf("write damaged record: %v", err)
	}

	pull := func() error {
		var stdout, stderr bytes.Buffer
		return pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: releasePath,
			CatalogDirs: []string{filepath.Join(root, "catalog")},
			DiagOut:     &stderr,
			Out:         &stdout,
		})
	}

	if err := pull(); err == nil {
		t.Fatal("a record that does not satisfy its schema must halt the pull rather than be passed over")
	}
	onDisk, rerr := os.ReadFile(recordPath)
	if rerr != nil {
		t.Fatalf("read the record back: %v", rerr)
	}
	if string(onDisk) != damaged {
		t.Errorf("a refused pull must leave the record exactly as it was; got %q", string(onDisk))
	}

	if err := os.Remove(recordPath); err != nil {
		t.Fatalf("remove the damaged record: %v", err)
	}
	if err := pull(); err != nil {
		t.Fatalf("with no record the identical workspace must pull clean, or the halt above proves nothing; got %v", err)
	}
}
