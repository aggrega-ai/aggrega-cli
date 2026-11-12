// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// A build that accepts a context and a build that observes one look identical
// from the outside: both run, both write the same artifacts, and the difference
// appears only when the reader asks the run to stop. So the two tests here ask
// the question the two ways that between them cover the whole path from the
// keyboard to the work.
//
//   - Does a signal delivered to the running binary end the BUILD's work, or
//     only the process? That is the frame comparison, driven in a child process
//     through the real command tree, because ending a process is part of the
//     behaviour and a test that ran it here would end the run observing it.
//   - Does the build command hand its own run's context down, rather than
//     starting a fresh one? That is asked in process, of the real `build`
//     command, by withdrawing the run before it begins.

// parkedControlBuildFill and parkedStoppedBuildFill are the same function
// written twice, deliberately. The comparison needs ONE stack dump to
// distinguish two goroutines, and a stack frame is identified by the function it
// is in, so the two operations need two names. Sharing an implementation would
// erase the distinction the proof rests on.
//
// Each stands in for the dependency fill as it really behaves: a step that has
// left the process and is waiting on a registry to answer. It returns when its
// context ends, and otherwise not at all.
func parkedControlBuildFill(ctx context.Context, entered chan<- struct{}) error {
	close(entered)
	<-ctx.Done()
	return ctx.Err()
}

func parkedStoppedBuildFill(ctx context.Context, entered chan<- struct{}) error {
	close(entered)
	<-ctx.Done()
	return ctx.Err()
}

// buildProbeCommand is a command that runs the real build orchestration and
// parks in its dependency fill, which is the step a build waits on a registry
// in. It resolves its workspace the way every command does, and takes its
// context from the command, so nothing between the signal and the fill is
// stood in for.
func buildProbeCommand(entered chan<- struct{}) *cobra.Command {
	return &cobra.Command{
		Use:           "build-probe",
		Short:         "Run a build that parks in its dependency fill",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			_, runErr := buildops.Run(cmd.Context(), buildops.Config{
				Workspace:   rw,
				ReleasePath: filepath.Join(rw.Root(), "release.yaml"),
				CatalogDirs: []string{rw.Root()},
				// The pull step is what routes the lockfile phase through the fill.
				Options: buildops.RunOptions{AutoPull: true},
				Pull: func(pullCtx context.Context, _, _ bool, _ pipeline.Options, _, _ io.Writer) (*compilerops.PreviewResult, error) {
					return nil, parkedStoppedBuildFill(pullCtx, entered)
				},
				DiagOut: io.Discard,
				Out:     io.Discard,
			})
			return runErr
		},
	}
}

// TestAStopRequestEndsTheBuildsWork proves that an interrupt delivered to the
// running binary reaches a build in progress.
//
// A build is driven through the real command tree to the point where it waits
// on a registry, and a control is parked at the same depth on a context nothing
// ends. The process is then asked to stop. A single stack dump, taken in the
// child while both goroutines exist, must show the build's fill GONE and the
// control's frame STILL PRESENT.
//
// A planted defect makes this fail: mint a fresh background context inside the
// build orchestration instead of deriving one from the caller's, and the
// build's fill parks exactly as the control does, leaving
// parkedStoppedBuildFill on the stack.
func TestAStopRequestEndsTheBuildsWork(t *testing.T) {
	if role := os.Getenv(stopProbeEnv); role != "" {
		runStopProbeChild(role)
		return
	}
	t.Parallel()

	cmd, lines := stopProbeChild(t, "build")
	defer func() { _ = cmd.Process.Kill() }()

	awaitLine(t, lines, func(s string) bool { return s == readyMarker }, "the child's readiness marker")

	// Signalled by process identifier, on a process this test started itself.
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering the stop request: %v", err)
	}

	awaitLine(t, lines, func(s string) bool { return s == framesMarker }, "the goroutine dump")

	var dump strings.Builder
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("the child's output ended mid-dump")
			}
			if line == framesMarker {
				frames := dump.String()

				// This is the comparison's needle. It runs the same matcher over
				// the same dump and must find the control's parked frame, so a
				// failure to find the build's below is a fact about the stack
				// rather than about the instrument reading it.
				if !strings.Contains(frames, "parkedControlBuildFill") {
					t.Fatalf("the control's frame is ABSENT from the dump, so the comparison below "+
						"would pass for the wrong reason; the control must still be parked\ndump:\n%s", frames)
				}
				if strings.Contains(frames, "parkedStoppedBuildFill") {
					t.Errorf("the build's fill is STILL on the stack while the control's is there too: "+
						"the stop reached the process but not the build's work\ndump:\n%s", frames)
				}
				return
			}
			dump.WriteString(line)
			dump.WriteByte('\n')
		case <-time.After(probeCeiling):
			t.Fatal("the goroutine dump never closed within the liveness ceiling")
		}
	}
}

// TestTheBuildCommandRunsUnderTheRunsOwnContext closes the one link the frame
// comparison above does not drive: that the `build` command hands its own run's
// context to the build orchestration rather than starting a fresh one.
//
// It is asked by withdrawing the run before it begins and reading which failure
// comes back. The control is the same invocation under a live context, which
// must fail for its own reason — it has no lockfile — so the assertion
// distinguishes a withdrawn run from any other failure rather than matching
// whatever error happens to arrive.
//
// A planted defect makes this fail: replace the command's context with a
// background one anywhere between the command and the build, and the withdrawn
// invocation reports the missing lockfile exactly as the control does.
func TestTheBuildCommandRunsUnderTheRunsOwnContext(t *testing.T) {
	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(releasePath, []byte("kind: Release\n"), 0o644); err != nil {
		t.Fatalf("write the probe Release: %v", err)
	}
	// The workspace is declared rather than discovered, so resolution never
	// climbs above the temporary tree and the run is hermetic against whatever
	// configuration sits above the checkout.
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace:\n  catalog_dirs: []\n"), 0o644); err != nil {
		t.Fatalf("write the probe workspace config: %v", err)
	}

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	endedErr := runBuildThroughTheCommandTree(ended, root, releasePath)
	if endedErr == nil {
		t.Fatal("a build whose run had already been withdrawn reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("the withdrawn build ended with %v; want a failure naming the withdrawal, which "+
			"is what a build reports when the command's own context reaches it", endedErr)
	}

	liveErr := runBuildThroughTheCommandTree(context.Background(), root, releasePath)
	if liveErr == nil {
		t.Fatal("the control build reported success; it has no lockfile and must halt")
	}
	if strings.Contains(liveErr.Error(), context.Canceled.Error()) {
		t.Errorf("the control build reported the withdrawal too, so the assertion above cannot "+
			"distinguish a withdrawn run from any other failure: %v", liveErr)
	}
}

// runBuildThroughTheCommandTree dispatches the real `build` command against the
// declared workspace and returns what it reported, with both of cobra's own
// streams discarded so the test's output carries only its own findings.
func runBuildThroughTheCommandTree(ctx context.Context, wsRoot, releasePath string) error {
	root := newRootCmd()
	root.SetArgs([]string{"--workspace-root", wsRoot, "build", "--release", releasePath})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	_, err := root.ExecuteContextC(ctx)
	return err
}
