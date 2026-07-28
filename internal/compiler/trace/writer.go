// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// Mode carries the one choice this surface offers an operator: whether a capture
// withholds the data an author declared sensitive, or shows all of it verbatim.
//
// It is a single value rather than two independent switches because the two
// consequences of that choice are inseparable. A capture that shows a withheld
// datum must announce that it does, or a file found later in a bug report or a
// shared directory reads as an ordinary capture; and a file carrying that
// announcement must actually be unredacted, or the announcement is false. Both
// consequences are read from this one value — Keys decides what a capture
// withholds and the writers stamp the header — so neither can be turned on
// without the other.
//
// The zero value withholds, so a caller that constructs a writer without
// considering this gets the safe direction.
type Mode struct {
	// Unredacted shows every captured datum verbatim, including data an author
	// declared sensitive, and stamps a header on every file the capture writes
	// saying so. It is an invocation-time choice and is deliberately not
	// readable from workspace configuration: a setting left on after one
	// debugging session would silently widen what every later build writes.
	Unredacted bool
}

// Keys returns the set of keys a capture under this mode withholds. Withholding
// — the default — returns the caller's set unchanged. Unredacted returns the
// empty set, which every applier treats as "substitute nothing", so the
// projection is byte-for-byte the one a build predating the sensitivity axis
// produced, under the header.
func (m Mode) Keys(k redact.Keys) redact.Keys {
	if m.Unredacted {
		return redact.Keys{}
	}
	return k
}

// unredactedHeader prefixes every file an unredacted capture writes. It is a
// YAML comment, which every slot accepts: each payload this package writes is
// YAML-marshalled whatever extension its slot carries.
//
// It names the flag rather than describing the mode abstractly, because the
// reader who most needs it is someone who did not run the build and has only the
// file.
const unredactedHeader = "# WARNING: written with `aggrega build --trace-unredacted`.\n" +
	"# Values an author declared sensitive appear below in the clear.\n" +
	"# Handle this file as you would the secrets it carries.\n"

// marshalEvent is the ONE serialization of a trace payload in this package, and
// therefore the one place the unredacted-capture header is stamped. Both writers
// route through it. A second marshal site would be free to emit a body with no
// header, and an unannounced unredacted file is exactly the failure whoever
// finds it later cannot detect.
func marshalEvent(mode Mode, boundaryPath, nodeID string, data any) ([]byte, error) {
	body, err := yaml.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("trace: marshal %s/%s: %w", boundaryPath, nodeID, err)
	}
	if !mode.Unredacted {
		return body, nil
	}
	return append([]byte(unredactedHeader), body...), nil
}

// Writer captures per-pass intermediate state for one node. Implementations:
// the no-op writer when --trace is off, and the asynchronous filesystem
// writer when --trace is on.
type Writer interface {
	// Write captures the snapshot for (pass, nodeID). For passes whose
	// layout is per-file (PassPhaseB3 artifacts, PassPhaseB4 synthesis),
	// pass the per-file name as fileSuffix (e.g., "deployment.yaml");
	// otherwise pass empty string.
	Write(pass Pass, nodeID, boundaryPath, fileSuffix string, data any) error

	// Close drains any in-flight async writes. Safe to call repeatedly;
	// subsequent calls are no-ops. Returns the first error encountered
	// during draining (or nil).
	Close() error
}

// New returns a Writer for the given root directory. When root is empty,
// returns a no-op writer (trace disabled). Otherwise returns an async
// filesystem writer rooted at the given path.
//
// mode is required rather than optional so a new capture site cannot construct a
// writer without deciding whether it withholds declared-sensitive data; the zero
// value withholds.
func New(root string, mode Mode) Writer {
	if root == "" {
		return noopWriter{}
	}
	w := &fsWriter{
		root: root,
		mode: mode,
		ch:   make(chan event, 1024),
		done: make(chan struct{}),
	}
	w.wg.Add(1)
	go w.consume()
	return w
}

type noopWriter struct{}

func (noopWriter) Write(_ Pass, _, _, _ string, _ any) error { return nil }
func (noopWriter) Close() error                              { return nil }

// Captures reports whether a Writer records anything a reader will ever see.
//
// It exists because work done solely to feed a capture is waste when nothing is
// captured, and some of that work is no longer cheap: building the substituted
// display of a projection that is not addressable by key means evaluating a
// template a second time. A caller gates that work on this rather than on holding
// a non-nil Writer, because a build with capture off still holds one — the no-op
// writer, so that every capture site can call Write unconditionally.
//
// A nil Writer captures nothing. Every other implementation is assumed to record,
// which is the direction that fails safe: the cost of a projection nobody reads,
// rather than a capture that silently records nothing.
func Captures(w Writer) bool {
	if w == nil {
		return false
	}
	_, noop := w.(noopWriter)
	return !noop
}

// MemoryWriter collects trace events into an in-memory map keyed by the
// same relative path the filesystem writer would write to. Used by the
// pipeline package to surface Result.Trace as map[string][]byte without
// touching the filesystem; the CLI wrapper then writes the map to .trace/
// while the long-running daemon discards it.
type MemoryWriter interface {
	Writer
	// Entries returns a snapshot of collected entries keyed by relative path
	// (boundaries/<boundaryPath>/<nodeID>/...). The returned map is owned by
	// the caller; subsequent Write calls on the writer do not mutate it.
	Entries() map[string][]byte
}

type memoryWriter struct {
	mu      sync.Mutex
	mode    Mode
	entries map[string][]byte
	closed  bool
}

// NewMemory returns a Writer that collects per-event YAML payloads into
// an in-memory map. Callers retrieve the map via Entries() after the
// renderer finishes; Close is a no-op because there is no I/O to drain.
//
// mode is required for the same reason New requires it: the writer that
// serializes a payload is the writer that must announce an unredacted one, and
// the zero value withholds.
func NewMemory(mode Mode) MemoryWriter {
	return &memoryWriter{mode: mode, entries: map[string][]byte{}}
}

func (m *memoryWriter) Write(pass Pass, nodeID, boundaryPath, fileSuffix string, data any) error {
	body, err := marshalEvent(m.mode, boundaryPath, nodeID, data)
	if err != nil {
		return err
	}
	rel := eventPath(event{
		pass:         pass,
		nodeID:       nodeID,
		boundaryPath: boundaryPath,
		fileSuffix:   fileSuffix,
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.entries[rel] = body
	return nil
}

func (m *memoryWriter) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}

func (m *memoryWriter) Entries() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(m.entries))
	for k, v := range m.entries {
		out[k] = v
	}
	return out
}

type event struct {
	pass         Pass
	nodeID       string
	boundaryPath string
	fileSuffix   string
	body         []byte
}

type fsWriter struct {
	root     string
	mode     Mode
	ch       chan event
	done     chan struct{}
	wg       sync.WaitGroup
	closed   bool
	closeMu  sync.Mutex
	firstErr error
	errMu    sync.Mutex

	// Test-only instrumentation. The renderer hot path increments these
	// atomically; readers via Stats(Writer) observe race-free snapshots.
	// Production code does NOT branch on these counters; they exist so
	// design-contract tests can assert non-blocking semantics structurally
	// rather than via wall-clock proxies.
	maxWriteLatencyNanos atomic.Int64 // peak observed Write() call duration
	totalWrites          atomic.Int64 // total Write() calls (success + drop)
	droppedWrites        atomic.Int64 // Write() calls hitting the channel-full default branch
}

// WriterStats is a snapshot of the trace writer's instrumentation counters.
// Returned by Stats(Writer) for design-contract test assertions. Counters
// are race-free — atomic loads — but a concurrent Write() between any two
// field reads can observe a slightly inconsistent snapshot (e.g., TotalWrites
// could increment before MaxWriteLatency for the same Write() call). For the
// design-contract tests' purposes this is acceptable: tests inspect Stats
// after RenderAll + Close return, when no further Writes occur.
//
// Zero-valued for noopWriter (no instrumentation; the no-op Writer never
// touches disk so non-blocking semantics are trivially satisfied).
type WriterStats struct {
	// MaxWriteLatency is the largest observed wall-clock duration of any
	// single Write() call. The Write() body is a struct copy plus a
	// non-blocking channel send; values in the microsecond range are
	// expected. Values in the millisecond range indicate a Write() blocked
	// on disk I/O — design contract violation.
	MaxWriteLatency time.Duration

	// TotalWrites is the total number of Write() calls received, including
	// calls that were dropped at channel saturation.
	TotalWrites int64

	// DroppedWrites is the number of Write() calls where the channel was
	// full at send time and the default branch fired. At sanity-gate loads
	// (50 nodes × ~9 events) this should be zero — the 1024-event channel
	// buffer absorbs the workload comfortably. Non-zero indicates either
	// a stalled consumer goroutine or a buffer sized below actual demand.
	DroppedWrites int64
}

// Stats returns a snapshot of the writer's instrumentation counters.
// For noopWriter, returns the zero value (no-op writers never block on
// disk and don't drop events; the counters carry no information). For
// fsWriter, returns the live counter values.
//
// Free-function form (rather than a Stats() method on the Writer interface)
// keeps noopWriter's API surface clean — noopWriter has no meaningful
// instrumentation to expose, and forcing it to implement Stats() would
// pollute the production interface with a test-only concern.
func Stats(w Writer) WriterStats {
	fs, ok := w.(*fsWriter)
	if !ok {
		return WriterStats{}
	}
	return WriterStats{
		MaxWriteLatency: time.Duration(fs.maxWriteLatencyNanos.Load()),
		TotalWrites:     fs.totalWrites.Load(),
		DroppedWrites:   fs.droppedWrites.Load(),
	}
}

func (w *fsWriter) Write(pass Pass, nodeID, boundaryPath, fileSuffix string, data any) error {
	t0 := time.Now()
	w.totalWrites.Add(1)
	defer func() {
		// Record peak observed latency via atomic-max (CAS loop). The send
		// path is dominated by a struct copy + channel-send; expected
		// latencies are sub-microsecond on warm caches, microseconds under
		// contention. A value in the millisecond range indicates the
		// renderer goroutine blocked on disk I/O — non-blocking-contract
		// violation. The recordMaxLatency wrap covers all return paths
		// (success, drop, marshal error, closed) so the contract test sees
		// every Write()'s latency.
		w.recordMaxLatency(time.Since(t0).Nanoseconds())
	}()

	body, err := marshalEvent(w.mode, boundaryPath, nodeID, data)
	if err != nil {
		return err
	}
	w.closeMu.Lock()
	closed := w.closed
	w.closeMu.Unlock()
	if closed {
		return nil
	}
	select {
	case w.ch <- event{pass: pass, nodeID: nodeID, boundaryPath: boundaryPath, fileSuffix: fileSuffix, body: body}:
		return nil
	default:
		// Channel full — drop with an error rather than block. The bound
		// (1024) is large enough for typical node counts; saturation
		// indicates a perf-bound issue worth surfacing.
		w.droppedWrites.Add(1)
		return fmt.Errorf("trace: write buffer saturated; pass=%s node=%s", pass, nodeID)
	}
}

// recordMaxLatency atomically updates maxWriteLatencyNanos to the larger of
// the current value and elapsed. CAS loop is the standard atomic-max pattern;
// it handles concurrent Writes correctly without a lock. Termination is
// practically guaranteed because the time-between-Write-calls is many orders
// of magnitude greater than the time-to-execute-one-CAS-iteration.
func (w *fsWriter) recordMaxLatency(elapsed int64) {
	for {
		prev := w.maxWriteLatencyNanos.Load()
		if elapsed <= prev {
			return
		}
		if w.maxWriteLatencyNanos.CompareAndSwap(prev, elapsed) {
			return
		}
	}
}

func (w *fsWriter) Close() error {
	w.closeMu.Lock()
	if w.closed {
		w.closeMu.Unlock()
		return nil
	}
	w.closed = true
	w.closeMu.Unlock()
	close(w.ch)
	<-w.done
	w.errMu.Lock()
	defer w.errMu.Unlock()
	return w.firstErr
}

func (w *fsWriter) consume() {
	defer close(w.done)
	defer w.wg.Done()
	for ev := range w.ch {
		if err := w.persist(ev); err != nil {
			w.errMu.Lock()
			if w.firstErr == nil {
				w.firstErr = err
			}
			w.errMu.Unlock()
		}
	}
}

func (w *fsWriter) persist(ev event) error {
	rel := eventPath(ev)
	full := filepath.Join(w.root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("trace: mkdir %q: %w", filepath.Dir(full), err)
	}
	return os.WriteFile(full, ev.body, 0o644)
}

// eventPath computes the per-event relative path used by both the
// filesystem writer (which prepends its root directory) and the memory
// writer (which uses the relative path as its map key). The two writers
// produce identical paths for the same event — that identity is the
// contract callers rely on when comparing in-memory traces to on-disk
// .trace/ output.
//
// Layout:
//
//   - Base directory: boundaries/<boundaryPath>/<nodeID>
//   - When fileSuffix is empty: <base>/<pass>.<ext>
//     where <ext> is .log for PassEmission and PassLabels, .yaml otherwise.
//   - When fileSuffix is non-empty (per-file pass like PhaseB3 artifacts):
//     <base>/<pass>/<fileSuffix>. The fileSuffix already carries its own
//     extension (e.g. "deployment.yaml") so no default extension is appended.
func eventPath(ev event) string {
	dir := filepath.Join("boundaries", ev.boundaryPath, ev.nodeID)
	name := string(ev.pass)
	if ev.fileSuffix != "" {
		dir = filepath.Join(dir, name)
		return filepath.Join(dir, ev.fileSuffix)
	}
	ext := ".yaml"
	switch ev.pass {
	case PassEmission, PassLabels:
		ext = ".log"
	}
	return filepath.Join(dir, name+ext)
}
