// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package buildops

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// noPullNote is the substring identifying the resolution-detail no-pull note.
// Held in one place so the two sinks are matched against the same text: the
// assertions below are only meaningful if "present on the status stream" and
// "absent from the diagnostic stream" test for exactly the same line.
const noPullNote = "resolution detail is a pull-time surface"

// writeFixture writes content to path, creating parent directories as needed.
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestEnsureLockfile_LockfilePresent_NoPullNote pins the resolution-detail
// no-pull path: when no pull step was ASKED for, there is no resolution to
// report. The detail knob says so in one line (rather than silence or a
// fabricated tier) and reports the lockfile pre-existed; without it the path is
// silent.
//
// The cause stated above is the requested pull and not the present lockfile, and
// the distinction is the whole point of the sibling test below: a pull step runs
// whether or not a lockfile is present, so "a lockfile exists, therefore no pull"
// describes this fixture correctly and the mechanism wrongly. The lockfile here
// is present in order to reach this branch at all, not because its presence is
// what silences the fill.
//
// Both sinks are asserted on every row, because the note's stream is the
// contract: the corresponding flag's help promises the status stream, so landing
// the note on the diagnostic sink would be a defect that a single-sink assertion
// — "the text appears somewhere" — cannot distinguish from correct behavior.
func TestEnsureLockfile_LockfilePresent_NoPullNote(t *testing.T) {
	tests := []struct {
		name     string
		detail   bool
		wantNote bool
	}{
		{name: "detail on: the note is written to the status stream", detail: true, wantNote: true},
		{name: "detail off: neither sink is written", detail: false, wantNote: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			rel := filepath.Join(root, "release.yaml")
			writeFixture(t, rel, "kind: Release\n")
			// A present lockfile makes ensureLockfile take the no-pull branch; its
			// content is irrelevant (only os.Stat is consulted there).
			writeFixture(t, lockfile.PathFor(rel), "{}\n")

			var diag, out bytes.Buffer
			pre, filled, err := ensureLockfile(
				context.Background(),
				Config{ReleasePath: rel, CatalogDirs: []string{root}},
				workspaceconfig.Config{},
				RunOptions{Detail: tc.detail},
				pipeline.Options{},
				&diag, &out)
			if err != nil {
				t.Fatalf("ensureLockfile: %v", err)
			}
			if !pre {
				t.Fatal("expected lockfilePreexisted=true when the lockfile is present")
			}
			// No fill ran, so there is no render to present and the build must render
			// for itself. A value here would send it to the presentation path with a
			// render nothing performed.
			if filled != nil {
				t.Error("a run that did not fill reported a conserved render")
			}

			if got := strings.Contains(out.String(), noPullNote); got != tc.wantNote {
				t.Errorf("no-pull note on the status sink: got present=%v, want %v (status=%q)",
					got, tc.wantNote, out.String())
			}
			// The stream is the contract, so the negative half is asserted
			// explicitly: the note never reaches the diagnostic sink, which in
			// production is os.Stderr.
			if strings.Contains(diag.String(), noPullNote) {
				t.Errorf("no-pull note leaked onto the diagnostic sink: %q", diag.String())
			}
			if strings.TrimSpace(diag.String()) != "" {
				t.Errorf("expected no diagnostic output on this path, got %q", diag.String())
			}
		})
	}
}

// TestEnsureLockfile_FillRequestedWithALockfileAlreadyPresent covers the one
// cell of this decision's grid that nothing else reaches, and it is the cell the
// flag's own documentation had been getting wrong.
//
// The decision has two binary axes — is a lockfile present, and was a pull step
// asked for — so it has four cells rather than two behaviours. Three were
// already covered: present-and-not-asked prints the note, absent-and-not-asked
// halts with the pull-first remediation, absent-and-asked fills. This is
// present-AND-asked, and it is the only cell that can tell "the fill happens
// when a lockfile is missing" apart from "the fill happens whenever it is asked
// for". The fill runs here, so the second reading is the true one.
//
// Both routes into the fill are exercised on the same fixture, because they are
// independent: the caller's own knob, and the workspace setting that no flag
// refusal reaches. A build that honoured one and not the other would leave a
// workspace configured to fill silently not filling, which is the failure a
// single-route assertion cannot see.
//
// The note is asserted ABSENT here. Its presence would mean the detail surface
// reported "no pull ran" on a run that pulled, which is the reader-visible form
// of exactly the falsehood this cell pins.
func TestEnsureLockfile_FillRequestedWithALockfileAlreadyPresent(t *testing.T) {
	tests := []struct {
		name  string
		run   RunOptions
		wsCfg workspaceconfig.Config
	}{
		{
			name: "asked for through the caller's own knob",
			run:  RunOptions{AutoPull: true, Detail: true},
		},
		{
			name:  "asked for through the workspace setting, which no flag refusal reaches",
			run:   RunOptions{Detail: true},
			wsCfg: workspaceconfig.Config{AutoPull: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			rel := filepath.Join(root, "release.yaml")
			writeFixture(t, rel, "kind: Release\n")
			// The lockfile IS present — that is the axis under test. Its content is
			// irrelevant: only os.Stat is consulted on this path.
			writeFixture(t, lockfile.PathFor(rel), "{}\n")

			var called bool
			var diag, out bytes.Buffer
			pre, _, err := ensureLockfile(
				context.Background(),
				Config{ReleasePath: rel, Pull: func(_ context.Context, _, _ bool, _ pipeline.Options, _, _ io.Writer) (*compilerops.PreviewResult, error) {
					called = true
					return nil, nil
				}},
				tc.wsCfg,
				tc.run,
				pipeline.Options{},
				&diag, &out)
			if err != nil {
				t.Fatalf("ensureLockfile: %v", err)
			}
			if !called {
				t.Fatal("a requested pull step must run even though a lockfile is already present; " +
					"the fill is gated on being asked for, not on the lockfile being absent")
			}
			if pre {
				t.Error("a run that filled must report the lockfile as not pre-existing, so drift is not " +
					"verified against a set the fill has just rewritten")
			}
			if strings.Contains(out.String(), noPullNote) {
				t.Errorf("the no-pull note must not be written on a run that pulled: %q", out.String())
			}
		})
	}
}

// TestEnsureLockfile_FillRequestedWithNoPullSupplied refuses rather than
// silently skipping. A build reached from a surface that offers no pull cannot
// perform the fill itself, and a run that quietly proceeded would leave the
// caller believing a fill happened.
//
// The negative control is the row above it: the same absent pull with the fill
// NOT requested reaches the ordinary no-lockfile remediation, so this row cannot
// pass because every path refuses.
func TestEnsureLockfile_FillRequestedWithNoPullSupplied(t *testing.T) {
	root := t.TempDir()
	rel := filepath.Join(root, "release.yaml")
	writeFixture(t, rel, "kind: Release\n")

	var diag, out bytes.Buffer
	_, _, err := ensureLockfile(
		context.Background(),
		Config{ReleasePath: rel},
		workspaceconfig.Config{},
		RunOptions{AutoPull: false},
		pipeline.Options{},
		&diag, &out)
	if err == nil || !strings.Contains(err.Error(), "run `aggrega pull") {
		t.Fatalf("no fill requested and no lockfile: want the pull-first remediation, got %v", err)
	}

	_, _, err = ensureLockfile(
		context.Background(),
		Config{ReleasePath: rel},
		workspaceconfig.Config{},
		RunOptions{AutoPull: true},
		pipeline.Options{},
		&diag, &out)
	if err == nil || !strings.Contains(err.Error(), "no dependency fill supplied") {
		t.Fatalf("fill requested with no pull supplied: want the wiring refusal, got %v", err)
	}
}

// TestEnsureLockfile_FillRunsThroughTheSuppliedPull proves the fill is invoked
// through the caller's pull rather than reimplemented here, and that everything
// the fill's render needs reaches it verbatim: the two resolution knobs, the
// caller's context, and the render option set. The call is through a function
// value, so no structural derivation can see this edge; only an invocation can.
//
// The option set is asserted by VALUE rather than by field, and that is the
// point of asserting it at all. What travels is the whole struct the build
// assembled, so a knob added to the pipeline reaches the fill's render with
// nothing here to widen — and a comparison against the struct the caller handed
// in is the only assertion that keeps saying so as the struct grows. A per-field
// check would go on passing over a field it was never taught about.
//
// The context is asserted through a value carried on it, because that is how the
// two render knobs that do not live on the option struct reach a render: a fill
// handed a context this layer invented would run with both of them off while
// every field assertion here still passed.
func TestEnsureLockfile_FillRunsThroughTheSuppliedPull(t *testing.T) {
	root := t.TempDir()
	rel := filepath.Join(root, "release.yaml")
	writeFixture(t, rel, "kind: Release\n")

	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "the caller's own")

	// Distinct from the zero value on every axis a caller can set, so a fill
	// handed a freshly-built option set rather than the caller's fails here.
	want := pipeline.Options{
		IncludeTrace:                     true,
		TraceUnredacted:                  true,
		SuppressUnsupportedFieldWarnings: true,
	}

	var gotUpgrade, gotDetail, called, sawCallerCtx bool
	var gotRender pipeline.Options
	var diag, out bytes.Buffer
	pre, filled, err := ensureLockfile(
		ctx,
		Config{ReleasePath: rel, Pull: func(pullCtx context.Context, upgrade, detail bool, render pipeline.Options, _, _ io.Writer) (*compilerops.PreviewResult, error) {
			called, gotUpgrade, gotDetail, gotRender = true, upgrade, detail, render
			sawCallerCtx = pullCtx.Value(ctxKey{}) == "the caller's own"
			return &compilerops.PreviewResult{}, nil
		}},
		workspaceconfig.Config{},
		RunOptions{AutoPull: true, Upgrade: true, Detail: true},
		want,
		&diag, &out)
	if err != nil {
		t.Fatalf("ensureLockfile with a supplied pull: %v", err)
	}
	if !called {
		t.Fatal("the fill did not reach the supplied pull")
	}
	if !gotUpgrade || !gotDetail {
		t.Errorf("pull received upgrade=%v detail=%v; want both true", gotUpgrade, gotDetail)
	}
	if !sawCallerCtx {
		t.Error("the fill ran under a context this layer invented, so the render knobs that ride " +
			"on a context would not have reached the render it performs")
	}
	// Comparing the two option sets needs a comparison the language does not give
	// a struct holding functions, so the fields that are values are compared and
	// the callbacks are asserted present. A callback left unset is not cosmetic:
	// an unset compile-begin changes WHEN guests compile.
	if gotRender.IncludeTrace != want.IncludeTrace ||
		gotRender.TraceUnredacted != want.TraceUnredacted ||
		gotRender.SuppressUnsupportedFieldWarnings != want.SuppressUnsupportedFieldWarnings {
		t.Errorf("the fill's render ran under %+v, want the option set the caller assembled", gotRender)
	}
	if pre {
		t.Error("a filled lockfile is reported as not pre-existing, so drift is not verified against it")
	}
	if filled == nil {
		t.Error("the render the fill handed back was dropped, so the build would render the Release a second time")
	}
}
