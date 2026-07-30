// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	"testing"
)

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything written to it. Diagnostics that reach a user's terminal are written
// there, so a proof that an advisory "prints" or that a warn "flags" reads the
// captured text rather than an internal buffer.
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
