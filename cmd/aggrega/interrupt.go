// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// stopSignals are the two requests that mean "stop this run": the interrupt a
// reader sends from the keyboard, and the termination request a supervisor or
// container runtime sends while shutting the process down. A command cannot act
// on the difference — both say the work is no longer wanted — so both end the
// run's context the same way.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// stoppingNotice is written once, to stderr, when the first stop request
// arrives.
//
// It goes to stderr rather than stdout so that asking a run to stop cannot
// perturb the bytes a command writes to stdout. It names the second request
// because a reader whose first request has not visibly ended the run needs to
// know, at that moment, that asking again always works.
const stoppingNotice = "stopping; interrupt again to stop immediately"

// withStopSignals returns a context that ends when the process is first asked
// to stop, together with a function that removes the wiring and releases the
// context.
//
// The first request ends the context, which is what gives work already under
// way the chance to unwind: a fetch in flight is abandoned, a deferred close
// runs, and the command reports what it did rather than vanishing mid-step. The
// second request ends the process outright, so a run that does not observe the
// first one still leaves the reader a way out.
func withStopSignals(parent context.Context) (context.Context, context.CancelFunc) {
	return notifyStop(parent, os.Stderr, os.Exit, stopSignals...)
}

// notifyStop is withStopSignals with its three process-wide effects supplied by
// the caller — where the notice goes, how the process ends, and which signals
// count — so the behaviour can be driven without ending the test process that
// drives it.
func notifyStop(parent context.Context, notices io.Writer, exit func(int), signals ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)

	// Buffered past the number of signals wired, so a second request arriving
	// while the first is still being handled is queued rather than dropped:
	// Notify discards a signal it cannot deliver without blocking, and the
	// second request is the one that must never be lost.
	ch := make(chan os.Signal, len(signals)+1)
	signal.Notify(ch, signals...)

	go func() {
		first := true
		for s := range ch {
			if first {
				first = false
				cancel()
				fmt.Fprintln(notices, stoppingNotice)
				continue
			}
			exit(terminationStatus(s))
			return
		}
	}()

	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			// Stop before close: Notify guarantees no further send on ch once
			// Stop returns, which is what makes closing it safe. Closing first
			// would race a signal arriving against a send on a closed channel.
			signal.Stop(ch)
			close(ch)
		})
		cancel()
	}
}

// terminationStatus reports the exit status conventionally used for a process
// ended by the given signal: 128 plus the signal's number.
//
// The process exits with that status directly rather than restoring the
// signal's default disposition and raising it again. Raising it again would
// report the same status in the ordinary case, but it inherits whatever
// disposition the process started with — and a process launched with the
// interrupt already ignored keeps ignoring it, which would leave the second
// request doing nothing and the reader with no way out of a run that is not
// observing the first. Exiting directly cannot be silenced that way.
func terminationStatus(s os.Signal) int {
	if sig, ok := s.(syscall.Signal); ok && sig > 0 {
		return 128 + int(sig)
	}
	return 1
}
