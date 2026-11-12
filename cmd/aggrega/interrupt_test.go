// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// A process that installs signal handling and a process that does not look
// identical from the outside until a signal actually arrives: both run, both
// produce output, and the difference is only what happens next. So these tests
// deliver a real signal to a real process and read the result from the process
// itself rather than from anything in this one.
//
// Every one of them runs the work in a CHILD process, re-executing this test
// binary. That is not indirection for its own sake: the behaviour under test
// includes ending the process, and a test that exercised it in-process would
// end the run that was observing it.
//
// Two different questions are asked, because neither answers the other:
//
//   - Does a stop request end the WORK, or only the process? That is the frame
//     comparison: with one operation stopped and one not, a single stack dump
//     taken in the child shows the stopped one's frame gone and the control's
//     still there.
//   - Is a reader whose run does not observe the first request trapped? That is
//     the second-request test, which drives an operation that deliberately
//     ignores its context and asserts both directions — the first request does
//     not kill it, and the second one does.

const (
	// stopProbeEnv selects a child role. Empty means this process is the parent
	// running the test.
	stopProbeEnv = "AGGREGA_STOP_PROBE"

	// readyMarker is written to stdout once the child is parked and a signal
	// can do something meaningful. Signalling before it appears would race the
	// child's startup and prove nothing about a run in progress.
	readyMarker = "STOP-PROBE-READY"

	// framesMarker opens the goroutine dump the child writes after its stopped
	// operation has returned.
	framesMarker = "STOP-PROBE-FRAMES"
)

// parkedControlReach and parkedStoppedReach are the same function written
// twice, deliberately. The comparison needs ONE stack dump to distinguish two
// goroutines, and a stack frame is identified by the function it is in, so the
// two operations need two names. This is the one duplicate in this file that
// must not be unified: sharing an implementation would erase the distinction
// the proof rests on.
//
// Each stands in for an operation that has left the process and is waiting on
// something outside it — a registry fetch in flight. It returns when its
// context ends, and otherwise not at all, which is the behaviour that makes
// "did the stop reach the work" answerable from the stack.
func parkedControlReach(ctx context.Context, entered chan<- struct{}) error {
	close(entered)
	<-ctx.Done()
	return ctx.Err()
}

func parkedStoppedReach(ctx context.Context, entered chan<- struct{}) error {
	close(entered)
	<-ctx.Done()
	return ctx.Err()
}

// parkedDeafReach stands in for an operation that does not consult its context
// at all — the case the second stop request exists for. It never returns.
func parkedDeafReach(entered chan<- struct{}) error {
	close(entered)
	select {}
}

// goroutineFrames renders every goroutine's stack, growing the buffer until the
// dump fits. A truncated dump would make an absent frame unreadable from a
// frame that simply did not fit, which is the one way this proof could quietly
// stop proving anything.
func goroutineFrames() string {
	for size := 1 << 20; ; size *= 2 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < size {
			return string(buf[:n])
		}
	}
}

// stopProbeRoot builds the real command tree and attaches one extra command
// that parks. Going through the real root is the point: it exercises the root's
// own PersistentPreRunE, which rebuilds the context before any command body
// sees it, so a wrapping that dropped the stop would fail here rather than in
// a simplified stand-in that never ran it.
func stopProbeRoot(run func(ctx context.Context) error) *cobra.Command {
	root := newRootCmd()
	root.AddCommand(&cobra.Command{
		Use:           "stop-probe",
		Short:         "Park until the run is asked to stop",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context())
		},
	})
	return root
}

// runStopProbeChild is the child half of every test below. It is dispatched
// from the test functions rather than from TestMain so the whole mechanism
// stays readable in one file.
func runStopProbeChild(role string) {
	workspace, err := os.MkdirTemp("", "stop-probe-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "stop probe: temp workspace:", err)
		os.Exit(2)
	}
	defer os.RemoveAll(workspace)

	ctx, stop := withStopSignals(context.Background())
	defer stop()

	switch role {
	case "frames":
		// The control is started on a context this test never ends, so its
		// frame must still be on the stack when the dump is taken. Without it,
		// a dump missing both frames would read as success.
		controlEntered := make(chan struct{})
		go func() { _ = parkedControlReach(context.Background(), controlEntered) }()
		<-controlEntered

		stoppedEntered := make(chan struct{})
		go func() {
			<-stoppedEntered
			fmt.Println(readyMarker)
			os.Stdout.Sync() //nolint:errcheck // best-effort flush of a readiness marker
		}()

		// The dump is taken on the stop wiring's own context rather than after
		// the command returns, and that placement is what makes the comparison
		// discriminating. A command that never observes the stop never returns,
		// so a dump taken after it would simply never be written — reporting
		// only that nothing happened. Taken here, the dump is written either
		// way, and the two frames say which of the two occurred.
		go func() {
			<-ctx.Done()
			// A settle window, not a budget: it gives a command that IS going
			// to unwind the time to do so, so the absence of its frame below
			// means it stopped rather than that it had not stopped yet.
			time.Sleep(2 * time.Second)
			fmt.Println(framesMarker)
			fmt.Println(goroutineFrames())
			fmt.Println(framesMarker)
			os.Exit(0)
		}()

		root := stopProbeRoot(func(cctx context.Context) error {
			return parkedStoppedReach(cctx, stoppedEntered)
		})
		root.SetArgs([]string{"--workspace-root", workspace, "stop-probe"})
		_ = executeRoot(ctx, root)

		// The dumping goroutine owns the exit, so the process stays alive long
		// enough for it to run whether or not the command returned.
		select {}

	case "build":
		// Same shape as the frames role, driving a real build instead of a bare
		// park: the control is started on a context this test never ends, so its
		// frame must still be on the stack when the dump is taken.
		controlEntered := make(chan struct{})
		go func() { _ = parkedControlBuildFill(context.Background(), controlEntered) }()
		<-controlEntered

		buildEntered := make(chan struct{})
		go func() {
			<-buildEntered
			fmt.Println(readyMarker)
			os.Stdout.Sync() //nolint:errcheck // best-effort flush of a readiness marker
		}()

		go func() {
			<-ctx.Done()
			time.Sleep(2 * time.Second)
			fmt.Println(framesMarker)
			fmt.Println(goroutineFrames())
			fmt.Println(framesMarker)
			os.Exit(0)
		}()

		root := newRootCmd()
		root.AddCommand(buildProbeCommand(buildEntered))
		root.SetArgs([]string{"--workspace-root", workspace, "build-probe"})
		_ = executeRoot(ctx, root)

		// The dumping goroutine owns the exit, so the process stays alive long
		// enough for it to run whether or not the build returned.
		select {}

	case "deaf":
		entered := make(chan struct{})
		go func() {
			<-entered
			fmt.Println(readyMarker)
			os.Stdout.Sync() //nolint:errcheck // best-effort flush of a readiness marker
		}()

		root := stopProbeRoot(func(context.Context) error {
			return parkedDeafReach(entered)
		})
		root.SetArgs([]string{"--workspace-root", workspace, "stop-probe"})
		_ = executeRoot(ctx, root)
		os.Exit(0)

	default:
		fmt.Fprintln(os.Stderr, "stop probe: unknown role", role)
		os.Exit(2)
	}
}

// stopProbeChild launches this test binary again in the given role and returns
// the running command together with a channel that yields each line the child
// writes.
func stopProbeChild(t *testing.T, role string) (*exec.Cmd, <-chan string) {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary to re-execute: %v", err)
	}
	cmd := exec.Command(exe, "-test.run="+t.Name(), "-test.timeout=0")
	cmd.Env = append(os.Environ(), stopProbeEnv+"="+role)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("child stdout: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the child: %v", err)
	}

	lines := make(chan string, 4096)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
		for sc.Scan() {
			lines <- sc.Text()
		}
		_ = io.Discard
	}()
	return cmd, lines
}

// probeCeiling bounds "happens" against "never happens" and is NOT a
// performance budget: nothing here measures how quickly a stop takes effect,
// only that it takes effect at all. A machine slow enough to exceed this is a
// machine on which the whole suite has already failed.
const probeCeiling = 60 * time.Second

// awaitLine consumes lines until one satisfies match, and fails if the ceiling
// passes first.
func awaitLine(t *testing.T, lines <-chan string, match func(string) bool, what string) {
	t.Helper()
	deadline := time.After(probeCeiling)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("the child's output ended before %s appeared", what)
			}
			if match(line) {
				return
			}
		case <-deadline:
			t.Fatalf("%s never appeared within the liveness ceiling", what)
		}
	}
}

// TestAStopRequestEndsTheWorkRatherThanOnlyTheProcess is the load-bearing proof
// that a signal delivered to the running binary reaches the work.
//
// Two operations are driven to the same point — parked, waiting on something
// outside the process — and the process is then asked to stop. One of them
// holds the run's context and one does not. A single stack dump, taken in the
// child while both goroutines exist, must show the stopped operation's frame
// GONE and the control's frame STILL PRESENT.
//
// A planted defect makes this fail: give the root a fresh background context
// instead of the one the stop wiring produced, and the stopped operation parks
// exactly as the control does, leaving parkedStoppedReach on the stack.
func TestAStopRequestEndsTheWorkRatherThanOnlyTheProcess(t *testing.T) {
	if role := os.Getenv(stopProbeEnv); role != "" {
		runStopProbeChild(role)
		return
	}
	t.Parallel()

	cmd, lines := stopProbeChild(t, "frames")
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
				// failure to find the stopped one below is a fact about the stack
				// rather than about the instrument reading it.
				if !strings.Contains(frames, "parkedControlReach") {
					t.Fatalf("the control's frame is ABSENT from the dump, so the comparison below "+
						"would pass for the wrong reason; the control must still be parked\ndump:\n%s", frames)
				}
				if strings.Contains(frames, "parkedStoppedReach") {
					t.Errorf("the stopped operation's frame is STILL on the stack while the control's "+
						"is there too: the stop reached the process but not the work\ndump:\n%s", frames)
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

// TestASecondStopRequestEndsARunThatIgnoredTheFirst proves both directions of
// the guarantee that makes installing a handler safe at all.
//
// A handler that swallows a stop request and then keeps running is strictly
// worse than no handler, because it removes the reader's only way out. So the
// run here parks in an operation that never consults its context: the first
// request must NOT end it — that is the orderly stop being attempted — and the
// second request must end it at once.
//
// A planted defect fails it in each direction: drop the cancel on the first
// request and the child dies immediately, failing the first assertion; stop
// listening after the first request and the second one is swallowed, failing
// the second.
func TestASecondStopRequestEndsARunThatIgnoredTheFirst(t *testing.T) {
	if role := os.Getenv(stopProbeEnv); role != "" {
		runStopProbeChild(role)
		return
	}
	t.Parallel()

	cmd, lines := stopProbeChild(t, "deaf")
	defer func() { _ = cmd.Process.Kill() }()

	awaitLine(t, lines, func(s string) bool { return s == readyMarker }, "the child's readiness marker")

	var exited atomic.Bool
	waited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		exited.Store(true)
		waited <- err
	}()

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering the first stop request: %v", err)
	}

	// Direction one: the first request begins a stop, it does not perform one.
	// The run is not observing it, so the process must still be here.
	time.Sleep(2 * time.Second)
	if exited.Load() {
		t.Fatal("the first stop request ended the process outright; it is supposed to ask the run " +
			"to stop and leave the second request as the way to end it")
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("delivering the second stop request: %v", err)
	}

	// Direction two: the second request is the reader's way out, and it works
	// even though nothing observed the first.
	select {
	case err := <-waited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("the child ended with %v; want a non-zero exit reporting the stop", err)
		}
		if got, want := exit.ExitCode(), 128+int(syscall.SIGINT); got != want {
			t.Errorf("the child exited %d; want %d — the status a shell reports for a run "+
				"ended by an interrupt", got, want)
		}
	case <-time.After(probeCeiling):
		t.Fatal("the second stop request did not end the process within the liveness ceiling: " +
			"a reader whose run ignores the first request is trapped, which is worse than " +
			"installing no handler at all")
	}
}
