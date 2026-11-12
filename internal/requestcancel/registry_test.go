// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package requestcancel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestCancelRegistryTrackUntrackLifecycle is the unit-level proof of the
// per-request cancel registry: Track records an id, Untrack removes it,
// Cancel(id) fires the stored func, an unknown id is a safe no-op, and a
// notification (empty id) is never tracked. This asserts registry BEHAVIOR; it
// is not a live per-request cancel-by-id round-trip — no frontend's dispatch or
// notification routing is exercised here. Each frontend proves its own wiring in
// its own package.
func TestCancelRegistryTrackUntrackLifecycle(t *testing.T) {
	t.Parallel()
	r := New()

	id := json.RawMessage(`"req-1"`)
	ctx, cancel := context.WithCancel(context.Background())

	if r.Len() != 0 {
		t.Fatalf("fresh registry Len = %d; want 0", r.Len())
	}

	r.Track(id, cancel)
	if r.Len() != 1 {
		t.Errorf("after Track Len = %d; want 1", r.Len())
	}

	// Cancel(id) must fire the stored func, cancelling the derived ctx.
	r.Cancel(id)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Cancel(id) did not fire the registered cancel func")
	}

	r.Untrack(id)
	if r.Len() != 0 {
		t.Errorf("after Untrack Len = %d; want 0", r.Len())
	}

	// Cancel on an absent id is a safe no-op.
	r.Cancel(json.RawMessage(`"absent"`))

	// A notification carries no id and is never tracked.
	r.Track(nil, func() {})
	r.Track(json.RawMessage{}, func() {})
	if r.Len() != 0 {
		t.Errorf("notification (empty id) was tracked; Len = %d; want 0", r.Len())
	}
	// Untrack / Cancel on an empty id are no-ops too.
	r.Untrack(nil)
	r.Cancel(nil)
}

// TestCancelRegistryNormalizesIdWhitespace is the unit-level proof of the key
// canonicalization: an id tracked with one JSON spelling is cancelled by another
// spelling of the same logical id, differing only in insignificant whitespace. A
// verbatim-byte map key would miss.
//
// The canonicalization is unexported, so this reaches it the only way a caller
// can — through the exported surface — which is what makes the assertion a
// statement about the registry's contract rather than about a private helper.
func TestCancelRegistryNormalizesIdWhitespace(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		tracked       string
		cancelledWith string
	}{
		{name: "number with leading space", tracked: `7`, cancelledWith: ` 7`},
		{name: "number with trailing space", tracked: `7`, cancelledWith: "7\n"},
		{name: "string with surrounding space", tracked: `"a"`, cancelledWith: ` "a" `},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := New()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.Track(json.RawMessage(tc.tracked), cancel)
			r.Cancel(json.RawMessage(tc.cancelledWith))
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatalf("id tracked as %q was not cancelled by %q; the key canonicalization "+
					"did not resolve the two spellings to one key", tc.tracked, tc.cancelledWith)
			}
		})
	}
}

// TestCancelRegistryConcurrentTrackUntrackRaceClean fans out many goroutines
// tracking and untracking distinct ids (and reading Len / firing Cancel) on one
// registry. Under -race this fails if Track / Untrack / Cancel / Len touch the
// map without the lock. It also proves the Track / Untrack pairing is balanced:
// the registry returns to empty.
func TestCancelRegistryConcurrentTrackUntrackRaceClean(t *testing.T) {
	t.Parallel()
	r := New()

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		w := w
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := json.RawMessage(fmt.Sprintf(`"w%d-%d"`, w, i))
				_, cancel := context.WithCancel(context.Background())
				r.Track(id, cancel)
				r.Len()
				r.Cancel(id)
				r.Untrack(id)
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("registry fan-out did not complete")
	}
	if r.Len() != 0 {
		t.Errorf("registry not balanced after fan-out; Len = %d; want 0", r.Len())
	}
}
