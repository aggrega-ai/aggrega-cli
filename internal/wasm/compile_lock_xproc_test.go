// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build unix || windows

package wasm

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Environment a compile-lock worker process reads. Absent them, the worker test
// is inert.
const (
	lockWorkerModeEnv    = "AGGREGA_COMPILE_LOCK_WORKER_MODE"
	lockWorkerDirEnv     = "AGGREGA_COMPILE_LOCK_WORKER_DIR"
	lockWorkerWitnessEnv = "AGGREGA_COMPILE_LOCK_WORKER_WITNESS"
	lockWorkerMarker     = "AGGREGA_COMPILE_LOCK_WORKER:"
)

// lockWorkerKey is the guest digest both processes take the compile lock for.
// It is synthetic and names no guest: the lock file is all these tests need, and
// the key only has to be the same on both sides.
const lockWorkerKey = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// lockWorkerCeiling is a LIVENESS ceiling, not a performance budget. It
// separates a worker that reports from one that never will — a lock that never
// grants, or a try-lock that waits — and is long enough to cover starting a test
// process on a loaded machine, which is the slow part of every step it bounds.
const lockWorkerCeiling = 2 * time.Minute

// lockWorkerGrace is how long the first process keeps holding the compile lock
// after the second process reports it is about to ask for it. It is not an
// assertion and no outcome is read from it: it widens the window in which a lock
// that let the second process through would be caught. A correct lock passes at
// any grace; a shorter one only makes a broken lock likelier to slip past.
const lockWorkerGrace = 500 * time.Millisecond

// TestCompileLockWorker is the process the cross-process compile-lock tests
// start. In "wait" mode it reports that it is about to take the per-guest
// compile lock, takes it, and reports whether the first process had already
// signalled its release by then. In "try" mode it tries the reclamation lock
// once and reports whether it got it. It is inert unless one of those tests
// started it.
func TestCompileLockWorker(t *testing.T) {
	mode := os.Getenv(lockWorkerModeEnv)
	if mode == "" {
		t.Skip("not a compile-lock worker invocation")
	}
	dir := os.Getenv(lockWorkerDirEnv)
	switch mode {
	case "wait":
		fmt.Fprintln(os.Stdout, lockWorkerMarker+"attempting")
		release := flockGuestCompile(dir, lockWorkerKey)
		_, err := os.Stat(os.Getenv(lockWorkerWitnessEnv))
		fmt.Fprintf(os.Stdout, "%sacquired released=%t\n", lockWorkerMarker, err == nil)
		release()
	case "try":
		release, got := flockCompileCacheReclaim(dir)
		fmt.Fprintf(os.Stdout, "%stried got=%t\n", lockWorkerMarker, got)
		release()
	default:
		t.Fatalf("unknown compile-lock worker mode %q", mode)
	}
}

// lockWorker is one running TestCompileLockWorker process, started by
// startLockWorker. Its marker lines arrive on reports, in the order it printed
// them; reports is closed once the process has exited.
type lockWorker struct {
	cmd     *exec.Cmd
	reports chan string
	exited  chan struct{}
	stdout  strings.Builder
	stderr  bytes.Buffer
	err     error
}

// startLockWorker starts a worker process in mode against the cache directory
// dir. witness is the file the first process creates just before it releases a
// lock the worker waits on; it is empty where the mode reads none. A worker
// still running when the test ends is killed, before the test's temporary
// directories are removed.
func startLockWorker(t *testing.T, mode, dir, witness string) *lockWorker {
	t.Helper()
	w := &lockWorker{reports: make(chan string, 8), exited: make(chan struct{})}
	w.cmd = exec.Command(os.Args[0], "-test.run=^TestCompileLockWorker$", "-test.count=1")
	w.cmd.Env = append(os.Environ(),
		lockWorkerModeEnv+"="+mode,
		lockWorkerDirEnv+"="+dir,
		lockWorkerWitnessEnv+"="+witness,
	)
	w.cmd.Stderr = &w.stderr
	out, err := w.cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("connect to a worker process's output: %v", err)
	}
	if err := w.cmd.Start(); err != nil {
		t.Fatalf("start a worker process: %v", err)
	}
	t.Cleanup(func() { w.stop() })
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			line := sc.Text()
			w.stdout.WriteString(line + "\n")
			if i := strings.Index(line, lockWorkerMarker); i >= 0 {
				w.reports <- line[i+len(lockWorkerMarker):]
			}
		}
		// Wait only once every read from the pipe has returned, as the pipe
		// requires.
		w.err = w.cmd.Wait()
		close(w.reports)
		close(w.exited)
	}()
	return w
}

// next returns the worker's next report, failing the test if the worker exits
// without one or reports nothing within the liveness ceiling.
func (w *lockWorker) next(t *testing.T) string {
	t.Helper()
	select {
	case report, ok := <-w.reports:
		if !ok {
			t.Fatalf("worker process %d exited without reporting: %v\n%s", w.cmd.Process.Pid, w.err, w.output())
		}
		return report
	case <-time.After(lockWorkerCeiling):
		// The output is read only once the worker has been stopped, because until
		// then the goroutine draining its pipe is still writing it.
		w.stop()
		t.Fatalf("worker process %d reported nothing within %s, so it was stuck waiting\n%s", w.cmd.Process.Pid, lockWorkerCeiling, w.output())
	}
	return ""
}

// wait waits for the worker to exit and fails the test if it failed or did not
// exit within the liveness ceiling.
func (w *lockWorker) wait(t *testing.T) {
	t.Helper()
	select {
	case <-w.exited:
	case <-time.After(lockWorkerCeiling):
		t.Fatalf("worker process %d did not exit within %s", w.cmd.Process.Pid, lockWorkerCeiling)
	}
	if w.err != nil {
		t.Fatalf("worker process %d failed: %v\n%s", w.cmd.Process.Pid, w.err, w.output())
	}
}

// stop kills the worker if it is still running and waits for it to exit, so a
// failing test leaves no process holding a file in its temporary directories.
func (w *lockWorker) stop() {
	select {
	case <-w.exited:
		return
	default:
	}
	if err := w.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return
	}
	<-w.exited
}

// output is everything the worker wrote, for a failure message. It is read only
// once the worker has exited.
func (w *lockWorker) output() string {
	return "stdout:\n" + w.stdout.String() + "stderr:\n" + w.stderr.String()
}

// TestCompileLockMakesASecondProcessWait proves the per-guest compile lock
// excludes across processes, and that the excluded process WAITS rather than
// proceeding: while this process holds the lock, a second process asks for it,
// and it must get it only after this process has released it.
//
// What is read is an order, not a time. This process creates a witness file
// immediately before it releases, and the second process records whether the
// witness existed at the moment its lock was granted. A lock that let it through
// early reports the witness absent however fast or slow the machine is.
func TestCompileLockMakesASecondProcessWait(t *testing.T) {
	dir := t.TempDir()
	witness := filepath.Join(t.TempDir(), "released")

	release := flockGuestCompile(dir, lockWorkerKey)
	held := true
	t.Cleanup(func() {
		if held {
			release()
		}
	})

	w := startLockWorker(t, "wait", dir, witness)
	if report := w.next(t); report != "attempting" {
		t.Fatalf("want the worker to report it is about to take the lock; it reported %q", report)
	}

	select {
	case report, ok := <-w.reports:
		if ok {
			t.Fatalf("a second process took the compile lock while this process held it: it reported %q", report)
		}
		t.Fatalf("the worker process exited while it should have been waiting for the lock: %v\n%s", w.err, w.output())
	case <-time.After(lockWorkerGrace):
	}

	if err := os.WriteFile(witness, nil, 0o600); err != nil {
		t.Fatalf("create the release witness: %v", err)
	}
	held = false
	release()

	if report := w.next(t); report != "acquired released=true" {
		t.Fatalf("a second process was granted the compile lock before this process released it: it reported %q", report)
	}
	w.wait(t)
}

// TestCompileCacheReclaimLockRefusesASecondProcess proves the reclamation lock
// excludes across processes and that the excluded process is REFUSED rather
// than made to wait: while this process holds the lock, a second process tries
// it and must report that it did not get it, and once this process releases it
// a third process must get it. A try that waited instead would never report,
// which the liveness ceiling turns into a failure.
func TestCompileCacheReclaimLockRefusesASecondProcess(t *testing.T) {
	dir := t.TempDir()

	release, ok := flockCompileCacheReclaim(dir)
	if !ok {
		t.Fatal("this process could not take the reclamation lock in an empty directory")
	}
	held := true
	t.Cleanup(func() {
		if held {
			release()
		}
	})

	refused := startLockWorker(t, "try", dir, "")
	if report := refused.next(t); report != "tried got=false" {
		t.Fatalf("while this process held the reclamation lock, a second process reported %q; it must report that it did not get the lock", report)
	}
	refused.wait(t)

	held = false
	release()

	granted := startLockWorker(t, "try", dir, "")
	if report := granted.next(t); report != "tried got=true" {
		t.Fatalf("once this process released the reclamation lock, another process reported %q; it must get the lock", report)
	}
	granted.wait(t)
}
