// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package externalinputs

import (
	"context"
	stderrors "errors"
	"sort"
	"sync"
)

// Fetcher is the render-time external-input seam a surface hands to the
// preview: the single method a provider guest's external-input request arrives
// on. It is declared here rather than taken from the render host so that
// wrapping a service in a recorder does not couple this package to the host's
// own interface; a value satisfying one satisfies the other structurally.
type Fetcher interface {
	Fetch(ctx context.Context, url string, requiredBy string) ([]byte, error)
}

// DeclineRecorder wraps a Fetcher and remembers which references were declined
// for want of a pin. It is what lets a surface rendering a frozen closure
// report where its output diverges from a build's without comparing anything to
// anything: the host knows a reference was declined because it is the code that
// declined it, and it keeps the list.
//
// Recording here rather than reading it back off the render's failure is not a
// preference. A guest asks for the input, so the refusal travels back out
// through the guest, which reports a failure of its own — the registered
// condition does not survive that crossing, and only its rendered text does. A
// caller matching on the returned error would therefore be matching on message
// text that a guest happens to have copied through, and would go silently blind
// the moment a guest worded its own failure differently.
//
// Every method is safe for concurrent use: nodes render in parallel, so the
// callbacks arrive from several goroutines.
type DeclineRecorder struct {
	inner Fetcher

	mu       sync.Mutex
	declined map[string]bool
}

// NewDeclineRecorder wraps inner so that every reference declined for want of a
// pin is remembered. The recorder delegates every fetch; it changes no bytes and
// serves nothing itself.
func NewDeclineRecorder(inner Fetcher) *DeclineRecorder {
	return &DeclineRecorder{inner: inner}
}

// Fetch delegates, recording the URL when the refusal is the frozen-closure one.
// Other failures — an unreachable host, a corrupt store blob, a rejected scheme
// — are passed through unrecorded: they are not a divergence between a frozen
// render and an acquiring one, because an acquiring render fails on them
// identically.
func (f *DeclineRecorder) Fetch(ctx context.Context, url string, requiredBy string) ([]byte, error) {
	b, err := f.inner.Fetch(ctx, url, requiredBy)
	if err != nil && stderrors.Is(err, ErrAcquisitionDisabled()) {
		f.mu.Lock()
		if f.declined == nil {
			f.declined = map[string]bool{}
		}
		f.declined[url] = true
		f.mu.Unlock()
	}
	return b, err
}

// Declined returns the declined references, sorted, so the report a surface
// prints is the same for the same run rather than ordered by whichever node
// happened to render first.
func (f *DeclineRecorder) Declined() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.declined))
	for url := range f.declined {
		out = append(out, url)
	}
	sort.Strings(out)
	return out
}
