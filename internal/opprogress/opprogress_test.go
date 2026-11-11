// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package opprogress

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"
)

// recorder is an observer that records the events it receives, in order.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) note(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, s)
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *recorder) Begin(step string) End {
	r.note("begin " + step)
	return func(err error) {
		if err != nil {
			r.note("failed " + step)
			return
		}
		r.note("end " + step)
	}
}

func (r *recorder) Yield() { r.note("hold") }

func (r *recorder) Beside(w io.Writer) io.Writer {
	r.note("beside")
	return w
}

// TestEventsReachTheInstalledObserver drives every call an operation makes and
// reads what the installed observer received, and that nothing reaches anywhere
// where none is installed.
func TestEventsReachTheInstalledObserver(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		drive func(r *recorder) []string
	}{
		{
			name: "a step begun and ended is reported once, even when ended twice",
			drive: func(r *recorder) []string {
				ctx := With(t.Context(), r)
				end := Begin(ctx, "reading")
				end(nil)
				end(errors.New("ended again"))
				return []string{"begin reading", "end reading"}
			},
		},
		{
			name: "a step that failed is reported failed",
			drive: func(r *recorder) []string {
				Begin(With(t.Context(), r), "reading")(errors.New("no answer"))
				return []string{"begin reading", "failed reading"}
			},
		},
		{
			name: "a hold and a writer beside the drawing reach the observer",
			drive: func(r *recorder) []string {
				ctx := With(t.Context(), r)
				Yield(ctx)
				_ = Beside(ctx, &bytes.Buffer{})
				return []string{"hold", "beside"}
			},
		},
		{
			name: "nothing reported under Without reaches the observer the context carries",
			drive: func(r *recorder) []string {
				ctx := Without(With(t.Context(), r))
				Begin(ctx, "reading")(nil)
				Yield(ctx)
				_ = Beside(ctx, &bytes.Buffer{})
				return nil
			},
		},
		{
			name: "a step over sooner than its delay is never shown",
			drive: func(r *recorder) []string {
				end := BeginAfter(With(t.Context(), r), time.Hour, "compiling")
				end(nil)
				return nil
			},
		},
		{
			name: "a step outlasting its delay is shown, and its end reported",
			drive: func(r *recorder) []string {
				end := BeginAfter(With(t.Context(), r), time.Millisecond, "compiling")
				deadline := time.Now().Add(10 * time.Second) // liveness ceiling, not a budget
				for len(r.seen()) == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				end(nil)
				return []string{"begin compiling", "end compiling"}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &recorder{}
			want := tc.drive(r)
			if got := r.seen(); !slices.Equal(got, want) {
				t.Errorf("observer received %q, want %q", got, want)
			}
		})
	}
}

// TestWithNoObserverEveryCallIsANoOp pins the shape an operation relies on to
// report unconditionally: with nothing installed, every call returns at once and
// a writer is handed back as it was.
func TestWithNoObserverEveryCallIsANoOp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	Begin(ctx, "reading")(nil)
	BeginAfter(ctx, time.Millisecond, "compiling")(nil)
	Yield(ctx)
	var w bytes.Buffer
	if got := Beside(ctx, &w); got != &w {
		t.Errorf("Beside with no observer must return the writer it was given")
	}
	if got := Beside(With(ctx, &recorder{}), nil); got != nil {
		t.Errorf("Beside must return a nil writer as it is")
	}
}
