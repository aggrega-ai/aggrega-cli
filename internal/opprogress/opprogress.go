// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package opprogress carries an operation's progress, as events, from the point
// where the operation waits to the surface that shows it.
//
// An operation that waits — on a registry, on a download, on compiling a plugin
// — says so here, naming the step in the words its reader would use, and says
// nothing about how it is shown. A surface with a person watching installs an
// Observer on the context the operation runs under and decides how the events
// are drawn; every other surface installs none, and every call here is then a
// no-op. So an operation reports unconditionally, with nothing to test for at
// the call site, and a surface that asked for nothing gets nothing.
//
// The events travel on the context rather than as a parameter because the
// waits sit at every depth of an operation — a trust-policy read inside a
// registration, a plugin compiled inside a lint — and a context already reaches
// all of them. It carries an observer and nothing else: no deadline, no value an
// operation decides anything on, so installing one changes what is shown and
// never what is done.
package opprogress

import (
	"context"
	"io"
	"sync"
	"time"
)

// End reports that a step is over: err is nil where the step did what it waited
// for, and the failure that ended it otherwise, so a surface never shows a step
// that failed as one that finished.
type End func(err error)

// Observer receives an operation's progress events.
//
// Begin is called as a step the operation waits on begins, with the step's name
// — a short lowercase phrase saying what is being waited on, such as "reading
// the trust policy hub.aggrega.ai advertises". It returns the function the
// operation calls once that step is over, whether it succeeded or not.
//
// Yield is called before the operation hands the terminal to a person — a
// question it asks, a sign-in it waits on — so whatever the observer draws is
// out of the way of what that person reads and types.
//
// Beside returns a writer for lines the operation writes while a step may be in
// flight — a status line per item of a batch, say — so they land clear of
// whatever the observer draws rather than under it. Its writes reach w.
//
// An observer may be called from several goroutines at once, and from a timer.
type Observer interface {
	Begin(step string) End
	Yield()
	Beside(w io.Writer) io.Writer
}

// WaitObserver is an Observer that tells a wait on someone else — a registry
// that asked the operation to slow down — from a step of the operation's own work,
// and shows waits its own way. A surface that draws the steps of its command
// itself, and shows an operation only what it waits on, is one.
//
// Wait is called as such a wait begins, with what is being waited on, and returns
// the function the operation calls once the wait is over.
type WaitObserver interface {
	Observer
	Wait(step string) End
}

// observerKey keys the installed observer on a context.
type observerKey struct{}

// With returns a context under which every step an operation reports reaches o.
// A nil o installs nothing, so the context returned reports nowhere.
func With(ctx context.Context, o Observer) context.Context {
	return context.WithValue(ctx, observerKey{}, o)
}

// Without returns a context under which nothing reaches the observer ctx carries.
// An operation that runs several parts of its work at once reports them as one
// step it begins itself, and runs each part under this, so the parts' own steps
// are not drawn over one another.
func Without(ctx context.Context) context.Context {
	return context.WithValue(ctx, observerKey{}, Observer(nil))
}

func from(ctx context.Context) Observer {
	o, _ := ctx.Value(observerKey{}).(Observer)
	return o
}

// Begin reports that the operation has begun to wait on step, and returns the
// function to call once that step is over. With no observer installed it
// returns a function that does nothing. The returned function reports over the
// first call only, so a step ended twice — by a deferred call and an explicit
// one, say — is reported over once.
func Begin(ctx context.Context, step string) End {
	o := from(ctx)
	if o == nil {
		return func(error) {}
	}
	return once(o.Begin(step))
}

// Wait reports that the operation has begun to wait on someone else rather than
// on a step of its own work — a registry that asked it to slow down — and returns
// the function to call once the wait is over. An observer that tells the two apart
// (WaitObserver) receives it as a wait; any other receives it as a step, so a
// surface that shows every step shows the wait on the same terms as the rest.
// With no observer installed it returns a function that does nothing, and the
// returned function reports over the first call only.
func Wait(ctx context.Context, step string) End {
	o := from(ctx)
	if o == nil {
		return func(error) {}
	}
	if w, ok := o.(WaitObserver); ok {
		return once(w.Wait(step))
	}
	return once(o.Begin(step))
}

// BeginAfter reports step as Begin does, but only once it has lasted delay: a
// step that is usually over at once — a plugin whose compiled form is already
// on disk, say — is shown only on the run where it is not. The returned
// function ends the step where it was shown, and stops it being shown where it
// was not yet.
func BeginAfter(ctx context.Context, delay time.Duration, step string) End {
	o := from(ctx)
	if o == nil {
		return func(error) {}
	}
	var (
		mu    sync.Mutex
		ended bool
		stop  End
	)
	timer := time.AfterFunc(delay, func() {
		mu.Lock()
		defer mu.Unlock()
		if ended {
			return
		}
		stop = o.Begin(step)
	})
	return func(err error) {
		timer.Stop()
		mu.Lock()
		defer mu.Unlock()
		if ended {
			return
		}
		ended = true
		if stop != nil {
			stop(err)
		}
	}
}

// Yield reports that the operation is about to hand the terminal to a person.
// With no observer installed it does nothing.
func Yield(ctx context.Context) {
	if o := from(ctx); o != nil {
		o.Yield()
	}
}

// Beside returns w, made to land clear of whatever the installed observer draws
// where one is installed, and w itself where none is. A nil w is returned as it
// is.
func Beside(ctx context.Context, w io.Writer) io.Writer {
	o := from(ctx)
	if o == nil || w == nil {
		return w
	}
	return o.Beside(w)
}

// once wraps end so that only its first call reports.
func once(end End) End {
	if end == nil {
		return func(error) {}
	}
	var o sync.Once
	return func(err error) { o.Do(func() { end(err) }) }
}
