// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// The one home for redirecting the process diagnostic stream in this test
// package.
//
// It carries no build tag deliberately. Two tests need it — an advisory proof
// that runs untagged and the socket proofs that run only under the integration
// tag — and a copy in each is a copy the untagged build cannot see the other half
// of: under the integration tag BOTH files compile, so a second declaration is
// not a drift risk to be watched but a duplicate-symbol build failure. That
// failure is invisible to every untagged battery, which compiles only one of the
// two, so it surfaces first at the integration battery and takes the whole
// package's tagged tests down with it.

package pipeline_test

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
)

// displayBuffer is a display writer a run can be handed: it keeps everything
// written to it, and is safe for the concurrent writes a run may make.
type displayBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *displayBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *displayBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureDisplay runs fn with a fresh display writer for the run fn drives and
// returns everything the run displayed on it. A proof that an advisory "prints"
// or that a warn "flags" reads what the run showed the reader it was handed,
// which is where a terminal reader sees it.
func captureDisplay(t *testing.T, fn func(display io.Writer)) string {
	t.Helper()
	var display displayBuffer
	fn(&display)
	return display.String()
}

// captureDisplayOnly is captureDisplay that also holds the run to its display:
// the process's standard error is redirected for the duration and must stay
// empty, because a run shows what it shows on the writer its caller handed it
// and on no stream of its own choosing. It is for a run whose every line is the
// run's own — no guest is loaded that might write to the process stream itself.
//
// It mutates a process global, so its callers do not run in parallel.
func captureDisplayOnly(t *testing.T, fn func(display io.Writer)) string {
	t.Helper()
	var display displayBuffer
	stray := captureStderr(t, func() { fn(&display) })
	if stray != "" {
		t.Errorf("the run wrote to the process's standard error rather than to the display it was handed:\n%s", stray)
	}
	return display.String()
}

// processStderr is a display writer that writes to the process's standard error
// as it stands at the moment of each write. A fixture builder hands it to the run
// so a test observing the run's display through captureStderr — the socket
// proofs, whose guests may write to that stream themselves — reads the display
// and the guest's own lines in one place. Which writer a run displays on is
// proved elsewhere, against a writer that is not the process stream.
type processStderr struct{}

func (processStderr) Write(p []byte) (int, error) { return os.Stderr.Write(p) }

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it.
//
// It mutates a process global, so its callers do not run in parallel.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stderr = saved
	// The write end closes first so the copying goroutine sees EOF and the
	// receive below cannot block; the read end closes after, once its buffer has
	// been handed over.
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
