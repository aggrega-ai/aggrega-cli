// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Writer captures per-pass intermediate state for one node. Implementations:
// the no-op writer when --trace is off, and the asynchronous filesystem
// writer when --trace is on.
type Writer interface {
	// Write captures the snapshot for (pass, nodeID). For passes whose
	// layout is per-file (PassPhaseB3 artifacts, PassPhaseB4 synthesis),
	// pass the per-file name as fileSuffix (e.g., "deployment.yaml");
	// otherwise pass empty string.
	Write(pass Pass, nodeID, boundaryPath, fileSuffix string, data interface{}) error

	// Close drains any in-flight async writes. Safe to call repeatedly;
	// subsequent calls are no-ops. Returns the first error encountered
	// during draining (or nil).
	Close() error
}

// New returns a Writer for the given root directory. When root is empty,
// returns a no-op writer (trace disabled). Otherwise returns an async
// filesystem writer rooted at the given path.
func New(root string) Writer {
	if root == "" {
		return noopWriter{}
	}
	w := &fsWriter{
		root: root,
		ch:   make(chan event, 1024),
		done: make(chan struct{}),
	}
	w.wg.Add(1)
	go w.consume()
	return w
}

type noopWriter struct{}

func (noopWriter) Write(_ Pass, _, _, _ string, _ interface{}) error { return nil }
func (noopWriter) Close() error                                      { return nil }

type event struct {
	pass         Pass
	nodeID       string
	boundaryPath string
	fileSuffix   string
	body         []byte
}

type fsWriter struct {
	root     string
	ch       chan event
	done     chan struct{}
	wg       sync.WaitGroup
	closed   bool
	closeMu  sync.Mutex
	firstErr error
	errMu    sync.Mutex
}

func (w *fsWriter) Write(pass Pass, nodeID, boundaryPath, fileSuffix string, data interface{}) error {
	body, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("trace: marshal %s/%s: %w", boundaryPath, nodeID, err)
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
		return fmt.Errorf("trace: write buffer saturated; pass=%s node=%s", pass, nodeID)
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
	dir := filepath.Join(w.root, "boundaries", ev.boundaryPath, ev.nodeID)
	name := string(ev.pass)
	addExt := true
	if ev.fileSuffix != "" {
		dir = filepath.Join(dir, name)
		name = ev.fileSuffix
		// The fileSuffix is a per-artifact filename supplied by the caller
		// and already carries its own extension (e.g. "deployment.yaml").
		// The pass-level default extension does not apply.
		addExt = false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("trace: mkdir %q: %w", dir, err)
	}
	if addExt {
		ext := ".yaml"
		switch ev.pass {
		case PassEmission, PassLabels:
			ext = ".log"
		}
		name += ext
	}
	path := filepath.Join(dir, name)
	return os.WriteFile(path, ev.body, 0o644)
}
