// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceresolver

import (
	"fmt"
	"sort"
	"sync"
)

// Dispatcher routes SourceSpec inputs to registered per-kind
// adapters. The package-level Register and Resolve functions
// delegate to a singleton default Dispatcher used by production
// code; tests that need state isolation construct their own
// Dispatcher via NewDispatcher.
//
// Concurrency: the routing table is protected by a read-write
// mutex. Resolve takes the read lock; Register takes the write
// lock. Multiple concurrent Resolve invocations against the same
// Dispatcher are safe.
type Dispatcher struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewDispatcher returns an empty Dispatcher with no adapters
// registered. Callers register adapters via the Register method
// (per-instance, returns an error on duplicate kind) or via the
// package-level Register function (against the default dispatcher,
// panics on duplicate kind).
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		adapters: map[string]Adapter{},
	}
}

// Register installs adapter for adapter.Kind() in d's routing
// table. Re-registering the same kind returns an error rather than
// panicking — per-instance registration is for tests and other
// callers that want to react to collisions; the package-level
// Register panics on the same condition.
func (d *Dispatcher) Register(adapter Adapter) error {
	if adapter == nil {
		return fmt.Errorf("sourceresolver: Register called with nil adapter")
	}
	kind := adapter.Kind()
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.adapters[kind]; exists {
		return fmt.Errorf("sourceresolver: kind %q already registered", kind)
	}
	d.adapters[kind] = adapter
	return nil
}

// Resolve routes spec to the registered adapter for spec.Kind and
// returns the adapter's ResolvedSource (or its error). Returns a
// wrapped ErrUnknownKind when no adapter is registered for the
// requested kind.
func (d *Dispatcher) Resolve(spec SourceSpec) (*ResolvedSource, error) {
	d.mu.RLock()
	adapter, ok := d.adapters[spec.Kind]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("sourceresolver: kind=%s: %w", spec.Kind, ErrUnknownKind)
	}
	return adapter.Resolve(spec)
}

// Kinds returns the registered kind names in lexicographic order.
// Useful for diagnostic surfaces that enumerate the supported set
// (e.g., CLI usage strings, debug logs) and for tests that need
// deterministic ordering.
func (d *Dispatcher) Kinds() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.adapters))
	for k := range d.adapters {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// defaultDispatcher backs the package-level Register and Resolve
// functions. Production code uses the package-level entry points;
// tests that need state isolation construct their own Dispatcher
// via NewDispatcher.
var defaultDispatcher = NewDispatcher()

// Register installs adapter against the package-level default
// dispatcher. Adapters typically call Register from an init()
// function in their sub-package. Re-registering the same kind
// panics — one adapter per kind across the program's lifetime is
// the convention. The panic surfaces at process startup, before
// any source resolution attempt, so misconfigured workspaces fail
// loudly.
func Register(adapter Adapter) {
	if err := defaultDispatcher.Register(adapter); err != nil {
		panic(err.Error())
	}
}

// Resolve routes spec through the package-level default
// dispatcher. See Dispatcher.Resolve for the contract.
func Resolve(spec SourceSpec) (*ResolvedSource, error) {
	return defaultDispatcher.Resolve(spec)
}

// Kinds returns the kind names registered against the package-level
// default dispatcher, in lexicographic order. See Dispatcher.Kinds
// for the contract.
func Kinds() []string {
	return defaultDispatcher.Kinds()
}
