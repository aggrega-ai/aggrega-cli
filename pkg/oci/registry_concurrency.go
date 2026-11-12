// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"context"
	"sync"
	"sync/atomic"
)

// RegistryRequestsAtOnce is how many artifacts a pull asks a registry about at once
// — in its signature pass and its deprecation-marker pass. A pull's cost there is
// round-trip latency rather than data volume, each artifact costing a handful of
// small sequential requests, so asking about several at once shortens it roughly in
// proportion while keeping the load one pull puts on a registry modest. The number
// of artifacts a pass asks about at once — this, or fewer where the pass has fewer —
// is also the ceiling the registry's allowance recovers to after it asked to slow
// down (WithConcurrencyCeiling), so every request still goes through that handling.
//
// A registry index walk reads at a width of its own, and the two are deliberately
// not one number. The index walk lists every repository a registry serves and
// reads each of them, so its width is sized for a whole registry. A pull's passes
// ask about the artifacts one Release depends on — a few dozen — after its
// dependency walk has already reached the same registry, so a smaller width
// already removes most of the waiting while keeping a pull, which an author runs
// often and many machines run at once, the lighter of the two on the registry.
const RegistryRequestsAtOnce = 8

// EachAtOnce calls work once for every index from 0 to n-1, with at most limit
// calls running at once, and returns when every call has returned. Every index is
// called, whatever ctx says: a call made under a context that has ended fails on
// its own first request, as it would one at a time, so a caller never reads an
// index that was not called as one that answered.
//
// A caller collects each call's answer by its index and reads the answers in index
// order after EachAtOnce returns, so what it writes, prints or refuses with is what
// a run of one call after another would produce — the order calls finish in
// reaches nothing. The context each call is handed states the number of calls run
// at once — limit, or n where n is smaller — as the ceiling the registry's
// allowance recovers to (WithConcurrencyCeiling).
//
// A limit of one, or a single index, calls work one index after another on the
// caller's own goroutine.
//
// A call that panics does not take the process down from a goroutine no caller
// can reach: the panic is recovered where it happened, the other calls run to the
// end, and once every call has returned the panic of the lowest index that panicked
// is raised again on the caller's goroutine, with the value it was raised with — so
// a caller's own recover sees it, as it would have seen it from a run of one call
// after another. The goroutine's stack at the original panic is not carried.
func EachAtOnce(ctx context.Context, n, limit int, work func(ctx context.Context, i int)) {
	if n <= 0 {
		return
	}
	// Two other bounded fan-outs in this module run work at once over an ordered
	// list: the registry index walk's pool of repository readers and the signing
	// batch's pool of signing slots. They stay separate from this one because they
	// behave differently: each streams a finished item's output as soon as every
	// item before it has finished, and each stops taking new items once its context
	// ends. This one reports nothing until the whole batch has returned, calls every
	// index whatever its context says, and raises a call's panic again on the
	// caller's goroutine. Folding the three into one helper means giving it both
	// behaviours as choices, not picking one of them.
	limit = max(1, min(limit, n))
	ctx = WithConcurrencyCeiling(ctx, limit)
	if limit == 1 {
		for i := range n {
			work(ctx, i)
		}
		return
	}
	var (
		next     atomic.Int64
		wg       sync.WaitGroup
		panicked = make([]bool, n)
		raised   = make([]any, n)
	)
	call := func(i int) {
		defer func() {
			if r := recover(); r != nil {
				panicked[i], raised[i] = true, r
			}
		}()
		work(ctx, i)
	}
	for range limit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				call(i)
			}
		}()
	}
	wg.Wait()
	for i := range n {
		if panicked[i] {
			panic(raised[i])
		}
	}
}
