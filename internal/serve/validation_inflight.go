// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"sync"
	"time"
)

// defaultValidationWait bounds how long a diagnostics request waits for a
// validation of its own file that is already running, before refusing rather
// than waiting on.
//
// It is a stuck-detector rather than a latency budget, so it sits far above the
// slowest legitimate validation: crossing it means the validation stopped making
// progress, not that it merely took a while. The slowest legitimate one is a full
// semantic run, whose resolve phase is not interruptible mid-phase (see
// handleValidate) and takes seconds on a large document, so a budget measured in
// hundreds of milliseconds would refuse work that was going to succeed.
//
// It is deliberately not the shutdown drain's budget. That one bounds how long a
// session takes to leave after it has been told to; this one bounds how long one
// reader waits for one file. Sharing a constant between them would tie a reader's
// patience to a teardown deadline, and moving either for its own reasons would
// silently move the other.
const defaultValidationWait = 10 * time.Second

// validationWait is the budget a reader waits under: the injected one when a test
// harness set it, otherwise the package default. Production never sets the field,
// so a real daemon always waits the default.
func (s *session) validationWait() time.Duration {
	if s.validationWaitBudget > 0 {
		return s.validationWaitBudget
	}
	return defaultValidationWait
}

// validationRun is one running validation of one file. Its channel closes when
// that validation's handler returns, and that close is the only thing anything
// here waits on — so what a waiter observes is a handler that has finished
// writing, never a handler that has reached some particular write.
type validationRun struct {
	done chan struct{}
}

// validationTracker records which of a workspace's files are being validated
// right now, so a request asking about one of them can wait for the validation
// instead of being answered from state that validation is halfway through
// writing.
//
// # Why a reader has to wait at all
//
// Validation seeds two different stores — the parse cache and the diagnostic
// layers — and it seeds them at two different moments inside one handler. A
// diagnostics request finds the buffer in the first and answers out of the
// second, so a reader landing between those two moments would read a file the
// validation is about to report on as one with nothing to report, or as one not
// validated at all. Waiting for the handler's return, and not for a moment
// inside it, answers that reader with what the validation found. What keeps a
// validation that never reaches an answer — one cancelled partway — from reading
// as clean is the other half: the reader answers only where the answer standing
// for the file is a validation's answer for the text of the parse the reader then
// holds. A cancelled validation writes nothing, so it never makes one, and an
// earlier validation's answer for a different text is not one.
//
// The other file readers — hover, completion, definition resolution, references
// and implementations — consult the parse cache and answer out of the parse
// cache. One store, complete when it is written, no window, nothing to wait for.
// The asymmetry is why this is not a general reader gate.
//
// # It is not a lock over validation
//
// A lock held across validation would serialize the whole daemon behind one
// file's semantic run and undo the worker pool. Nothing here is held for the
// duration of anything: registering and releasing are O(1) map work under this
// tracker's own mutex, and a waiter holds no lock at all while it waits — it
// takes its snapshot, releases the mutex, and blocks on channels.
//
// # Why it cannot deadlock against the worker pool
//
// A validation registers itself from inside its own handler, which means it is
// already executing on a worker by the time any reader can see it. So a reader
// never waits on work that is itself waiting for a worker: whatever it waits for
// is guaranteed to be making progress, however many readers are blocked and
// however small the pool. Readers beyond the pool's width queue behind the ones
// that are waiting, which is back-pressure, and they are served as the waiters
// finish. The per-request deadline is a backstop against a validation that wedges
// for its own reasons, not the thing that makes this safe.
//
// # Zero value
//
// Usable. The map is created on first registration, and a workspace nothing has
// validated has no entry to wait on, so a reader arriving first passes straight
// through to the ordinary not-yet-validated refusal.
type validationTracker struct {
	mu sync.Mutex
	// inFlight holds the running validations per file, keyed by the document's
	// canonical path — the same key the parse cache and the diagnostic layers
	// use, because a reader that resolved its own spelling to that key must find
	// the validation a differently-spelled request registered.
	//
	// It is a list rather than a count because a waiter needs to name the
	// validations it is waiting for. Several validations of one file can run at
	// once (two requests naming two Releases, say), and a count cannot tell the
	// waiter which of them were the ones running when it arrived.
	inFlight map[string][]*validationRun

	// waiters counts, per file, the readers currently blocked on a validation of
	// it. Nothing in the request path reads it: it is instrumentation, and it is
	// here because a test proving the wait happens has to be able to SEE a
	// blocked reader before it lets the validation finish. Without that, such a
	// test releases the validation on a guess, and it then passes just as
	// happily on a design that never waits at all — which is the one outcome the
	// test exists to rule out.
	waiters map[string]int
}

// waiting reports how many readers are currently blocked on a validation of
// file. It is the instrumentation described on waiters and has no production
// caller.
func (t *validationTracker) waiting(file string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.waiters[file]
}

// begin registers a validation of file as running and returns the release its
// caller defers.
//
// The release belongs on the validating handler's RETURN rather than beside any
// one of its writes. That handler leaves by several paths — each of the
// diagnostic-writing exits, the early returns above them, and a panic — and a
// release placed at one of them leaks the signal on all the others, leaving every
// later reader of that file waiting out the full budget for a validation that
// finished long ago.
func (t *validationTracker) begin(file string) func() {
	run := &validationRun{done: make(chan struct{})}
	t.mu.Lock()
	if t.inFlight == nil {
		t.inFlight = map[string][]*validationRun{}
	}
	t.inFlight[file] = append(t.inFlight[file], run)
	t.mu.Unlock()
	return func() { t.release(file, run) }
}

// release drops run from file's in-flight list and closes its channel, waking
// every waiter that captured it. The two happen under one lock so a waiter taking
// a snapshot never sees a run that has been forgotten but not yet signalled.
func (t *validationTracker) release(file string, run *validationRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	runs := t.inFlight[file]
	for i, r := range runs {
		if r == run {
			t.inFlight[file] = append(runs[:i], runs[i+1:]...)
			break
		}
	}
	if len(t.inFlight[file]) == 0 {
		delete(t.inFlight, file)
	}
	close(run.done)
}

// await blocks until every validation of file that was running when await was
// called has returned, or until ctx ends. It reports ctx's error in the second
// case and nil in the first, including when nothing was running.
//
// It waits for the validations in flight AT ARRIVAL and no later ones, which is
// why the snapshot is taken once rather than re-read in the loop. A reader has no
// interest in a validation that started after it asked — its own answer is
// already at least as current as that one's input — and re-reading would let a
// stream of arriving validations extend one reader's wait without bound, turning
// a busy file into a permanent refusal at the deadline.
//
// That holds for a validation's answer, not for what it writes on the way: one
// that started after the snapshot can seed the parse cache before the reader
// consults it. So a reader that looks the buffer up in the parse cache calls
// await again after the lookup, as handleDiagnostics does — a validation
// registers before it seeds the cache, so the second snapshot holds every
// validation whose entry the lookup saw, or that validation has already returned.
// One that seeds the cache after the second snapshot can leave the reader holding
// a text no finished validation has answered for; the reader answers only where
// the answer standing for the file is a validation's answer for the text it
// holds, and is refused as not validated otherwise.
func (t *validationTracker) await(ctx context.Context, file string) error {
	t.mu.Lock()
	runs := append([]*validationRun(nil), t.inFlight[file]...)
	if len(runs) > 0 {
		// Counted under the same lock the snapshot was taken under, so a reader
		// the count includes is one that already holds a non-empty snapshot and
		// is about to block on it. Counting outside the lock would let an
		// observer see a waiter that had not yet decided to wait.
		if t.waiters == nil {
			t.waiters = map[string]int{}
		}
		t.waiters[file]++
		defer t.stopWaiting(file)
	}
	t.mu.Unlock()
	for _, run := range runs {
		select {
		case <-run.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// stopWaiting drops one waiter from file's count, deleting the entry at zero so
// the map does not accumulate one key per file ever read.
func (t *validationTracker) stopWaiting(file string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.waiters[file]--
	if t.waiters[file] <= 0 {
		delete(t.waiters, file)
	}
}
