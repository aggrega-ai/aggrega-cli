// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// This is the question the whole cancellation effort started from, asked at the one
// place a person actually asks it: a real signal delivered to a real `aggrega pull`
// that is waiting on a registry. Nothing here stands in for the signal, the process,
// the command tree or the resolution — only the registry at the far end is a
// stand-in, and it is a stand-in for a registry that has not answered yet.
//
// parkedControlPullFetch and parkedStoppedPullFetch are the same function written
// twice, deliberately. The comparison needs ONE stack dump to distinguish two
// goroutines, and a stack frame is identified by the function it is in, so the two
// need two names. Sharing an implementation would erase the distinction the proof
// rests on.
func parkedControlPullFetch(ctx context.Context, entered chan<- struct{}) error {
	close(entered)
	<-ctx.Done()
	return ctx.Err()
}

func parkedStoppedPullFetch(ctx context.Context, entered chan<- struct{}) error {
	close(entered)
	<-ctx.Done()
	return ctx.Err()
}

// interruptParkingPuller is the Tier-3 registry client the resolution reaches when a
// referenced library is not in the local catalog. It stays inside the fetch until the
// context it was handed ends, which is how a registry that has not answered behaves.
type interruptParkingPuller struct{ entered chan struct{} }

func (p *interruptParkingPuller) Pull(ctx context.Context, _, _ string) (*oci.PullResult, error) {
	return nil, parkedStoppedPullFetch(ctx, p.entered)
}

func (p *interruptParkingPuller) PullLibrary(ctx context.Context, _, _, _ string) (*oci.PullResult, string, error) {
	return nil, "", parkedStoppedPullFetch(ctx, p.entered)
}

// pullProbeCommand runs the real pull command body — pullRun under the command's own
// context, over a real Release whose library only the registry tier can resolve — and
// parks in the fetch. Nothing between the signal and the fetch is stood in for: the
// workspace resolves the way every command's does, and the resolution is the shared
// one every surface runs.
func pullProbeCommand(t *testing.T, entered chan struct{}) *cobra.Command {
	t.Helper()
	releasePath, catalogDir, ws := remoteOnlyLibraryWorkspace(t)
	return &cobra.Command{
		Use:           "pull-probe",
		Short:         "Run a pull that parks in its registry fetch",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return pullRun(cmd.Context(), pullConfig{
				ws:          ws,
				ReleasePath: releasePath,
				CatalogDirs: []string{catalogDir},
				puller:      &interruptParkingPuller{entered: entered},
				DiagOut:     io.Discard,
				Out:         io.Discard,
				diag:        buildDiagOptions{quiet: true},
			})
		},
	}
}

// TestAStopRequestEndsThePullsWork proves that an interrupt delivered to a running
// `aggrega pull` reaches the resolution in progress.
//
// A pull is driven through the real command tree to the point where it waits on a
// registry, and a control is parked at the same depth on a context nothing ends. The
// process is then asked to stop. A single stack dump, taken in the child while both
// goroutines exist, must show the pull's fetch GONE and the control's frame STILL
// PRESENT.
//
// A planted defect makes this fail: hand pullRunWithResult a background context
// instead of the one the command body inherited, and the pull's fetch parks exactly
// as the control does, leaving parkedStoppedPullFetch on the stack.
func TestAStopRequestEndsThePullsWork(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	if role := os.Getenv(stopProbeEnv); role != "" {
		runPullStopProbeChild(t, role)
		return
	}
	t.Parallel()

	cmd, lines := stopProbeChild(t, "pull")
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

				// This is the comparison's needle. It runs the same matcher over the
				// same dump and must find the control's parked frame, so a failure to
				// find the pull's below is a fact about the stack rather than about
				// the instrument reading it.
				if !strings.Contains(frames, "parkedControlPullFetch") {
					t.Fatalf("the control's frame is ABSENT from the dump, so the comparison below "+
						"would pass for the wrong reason; the control must still be parked\ndump:\n%s", frames)
				}
				if strings.Contains(frames, "parkedStoppedPullFetch") {
					t.Errorf("the pull's registry fetch is STILL on the stack while the control's frame "+
						"is there too: the stop reached the process but not the pull's work\ndump:\n%s", frames)
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

// runPullStopProbeChild is the child half. It mirrors the build probe's shape: a
// control parked on a context nothing ends, the real command tree dispatching the
// pull, and a dumping goroutine that owns the exit so the dump is written whether or
// not the command ever returns.
//
// The child leaves its temporary workspace behind, because the dumping goroutine
// exits the process and cleanup registered with the harness never runs. That is the
// same trade every probe in this package makes: the directory is under the system
// temporary root and the alternative is a dump that is never written.
func runPullStopProbeChild(t *testing.T, role string) {
	t.Helper()
	if role != "pull" {
		fmt.Fprintln(os.Stderr, "pull stop probe: unknown role", role)
		os.Exit(2)
	}

	ctx, stop := withStopSignals(context.Background())
	defer stop()

	// The control is started on a context this test never ends, so its frame must
	// still be on the stack when the dump is taken. Without it, a dump missing both
	// frames would read as success.
	controlEntered := make(chan struct{})
	go func() { _ = parkedControlPullFetch(context.Background(), controlEntered) }()
	<-controlEntered

	fetchEntered := make(chan struct{})
	go func() {
		<-fetchEntered
		fmt.Println(readyMarker)
		os.Stdout.Sync() //nolint:errcheck // best-effort flush of a readiness marker
	}()

	// The dump is taken on the stop wiring's own context rather than after the
	// command returns, and that placement is what makes the comparison
	// discriminating. A command that never observes the stop never returns, so a dump
	// taken after it would simply never be written — reporting only that nothing
	// happened. Taken here, the dump is written either way, and the two frames say
	// which of the two occurred.
	go func() {
		<-ctx.Done()
		// A settle window, not a budget: it gives a pull that IS going to unwind the
		// time to do so, so the absence of its frame below means it stopped rather
		// than that it had not stopped yet.
		time.Sleep(2 * time.Second)
		fmt.Println(framesMarker)
		fmt.Println(goroutineFrames())
		fmt.Println(framesMarker)
		os.Exit(0)
	}()

	probe := pullProbeCommand(t, fetchEntered)
	root := newRootCmd()
	root.AddCommand(probe)
	root.SetArgs([]string{"pull-probe"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	_ = executeRoot(ctx, root)

	// The dumping goroutine owns the exit, so the process stays alive long enough for
	// it to run whether or not the pull returned.
	select {}
}
