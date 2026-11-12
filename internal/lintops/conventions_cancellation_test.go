// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cancellationProbeCorpus is how many entities the two legs below run over. It is
// small enough to lint quickly and large enough that "stopped at the first" and
// "ran to the end" are different numbers rather than a rounding difference.
const cancellationProbeCorpus = 5

// cancellationProbeComponent renders a Component that breaks the naming shape, so
// every entity in the corpus produces at least one finding.
//
// A finding is not incidental here: it is the synchronisation. The pass writes one
// header line per entity that produced one, so a stream watching those writes
// knows when an entity has been dispatched without the test needing a clock.
func cancellationProbeComponent(n int) string {
	return fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: BadName%d
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`, n)
}

// cancelOnFirstReport is a diagnostic stream that fires cancel the first time the
// conventions pass reports on a file.
//
// It is the whole synchronisation the proof needs, and it is built from a
// parameter the operation already takes rather than from a hook planted in
// production code. The pass writes one "<path>:" header per entity that produced
// a finding, so that first write is positive evidence that one entity has been
// read, parsed and dispatched — which makes the cancel land at a known point in
// the loop instead of at whatever point a timer happened to reach.
type cancelOnFirstReport struct {
	cancel context.CancelFunc
	fired  bool
	buf    bytes.Buffer
}

func (w *cancelOnFirstReport) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if !w.fired && strings.HasSuffix(string(p), ".yaml:\n") {
		w.fired = true
		w.cancel()
	}
	return len(p), nil
}

// entitiesDispatched counts the distinct entity files a run actually reached, read
// off the collector the operation fills as it goes.
//
// This is the observable the cancellation proof turns on. A withheld answer looks
// the same whether the walk ended early or ran to completion, so the proof cannot
// rest on the return value; the number of entities the rules were actually run
// over is work done, and its shortfall is work NOT done.
func entitiesDispatched(collect *FindingCollector) []string {
	seen := map[string]struct{}{}
	for _, f := range collect.Report().Findings {
		if f.Location != nil {
			seen[filepath.Base(f.Location.File)] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// cancellationProbeWorkspace stands a workspace holding cancellationProbeCorpus
// convention-breaking Components under deterministic names, and returns the root
// alongside the workspace resolved over it. The walk sorts by path, so the
// fixtures' numbered names fix the order the entities are visited in.
func cancellationProbeWorkspace(t *testing.T) (string, *workspaceconfig.ResolvedWorkspace) {
	t.Helper()
	ws := seedWorkspace(t)
	for i := 0; i < cancellationProbeCorpus; i++ {
		mustWriteFile(t,
			filepath.Join(ws, fmt.Sprintf("entity-%d.yaml", i)),
			cancellationProbeComponent(i))
	}
	rw, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve the constructed workspace: %v", err)
	}
	return ws, rw
}

// TestCatalogWideStopsWalkingWhenTheCallerStops is the proof that a stop reaches
// the work rather than only the answer.
//
// The pass is given a context the diagnostic stream cancels as soon as the FIRST
// entity has been reported on. The assertion is on how many entities the rules
// were then run over: one of five. The other four were never read, never parsed
// and never dispatched to a linter guest, which is an observable effect and not a
// statement about the return value — and it is the distinction the whole change
// exists to make, because a caller that withdrew gets an identical silence either
// way.
//
// There is no timing bound anywhere in this test. The cancel is fired from inside
// the pass's own output, so the point it lands at is fixed by the loop rather than
// by how fast the machine is.
func TestCatalogWideStopsWalkingWhenTheCallerStops(t *testing.T) {
	ws, rw := cancellationProbeWorkspace(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	diag := &cancelOnFirstReport{cancel: cancel}
	collect := &FindingCollector{}

	err := RunCatalogWide(ctx, rw, passesOf(t, rw, nil, []string{ws}), nil, io.Discard, diag, collect)

	if !diag.fired {
		t.Fatalf("the pass reported on no entity, so nothing cancelled it and the count below measures nothing; the diagnostic stream held:\n%s", diag.buf.String())
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the pass returned %v; want an error carrying context.Canceled, so a caller can tell a withdrawal from a verdict", err)
	}
	dispatched := entitiesDispatched(collect)
	if len(dispatched) != 1 {
		t.Errorf("the rules ran over %d entities (%v); want exactly the 1 the cancel landed after. Anything more means the stop did not reach the loop and the remaining entities were dispatched anyway",
			len(dispatched), dispatched)
	}
	// The summary line claims a corpus, so a stopped pass must not write one: a
	// partial walk reported in the words of a complete one is the reading a caller
	// cannot recover from.
	if strings.Contains(diag.buf.String(), "conventions checked:") {
		t.Errorf("a stopped pass wrote its corpus summary:\n%s", diag.buf.String())
	}
}

// TestCatalogWideRunsToTheEndWhenNobodyStopsIt is the positive control, and it is
// what keeps the test above from being satisfied by a pass that stops on
// everything.
//
// Same workspace, same corpus, same streams — the only difference is that nothing
// cancels. Every entity is dispatched and the pass reports the corpus it covered.
func TestCatalogWideRunsToTheEndWhenNobodyStopsIt(t *testing.T) {
	ws, rw := cancellationProbeWorkspace(t)

	var out, diag bytes.Buffer
	collect := &FindingCollector{}

	if err := RunCatalogWide(t.Context(), rw, passesOf(t, rw, nil, []string{ws}), nil, &out, &diag, collect); err != nil {
		t.Fatalf("an uncancelled whole-catalog pass: %v", err)
	}

	dispatched := entitiesDispatched(collect)
	if len(dispatched) != cancellationProbeCorpus {
		t.Errorf("the rules ran over %d entities (%v); want all %d", len(dispatched), dispatched, cancellationProbeCorpus)
	}
	if got := collect.Report().Summary.EntitiesChecked; got != cancellationProbeCorpus {
		t.Errorf("the run reports %d entities checked; want %d", got, cancellationProbeCorpus)
	}
	if want := fmt.Sprintf("conventions checked: %d entities", cancellationProbeCorpus); !strings.Contains(out.String(), want) {
		t.Errorf("the summary does not name the corpus it covered (%q); it said:\n%s", want, out.String())
	}
}

// TestRunEntityStopsBeforeTheGuestsRunWhenTheCallerStops is the single-entity
// counterpart. The context is already cancelled when the operation is called, so
// the rules must not run at all — which the collector reports by never having been
// reached, the same signal every caller already uses to tell a refusal from a run
// that never happened.
func TestRunEntityStopsBeforeTheGuestsRunWhenTheCallerStops(t *testing.T) {
	ws := seedWorkspace(t)
	entity := filepath.Join(ws, "entity-0.yaml")
	mustWriteFile(t, entity, cancellationProbeComponent(0))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	collect := &FindingCollector{}
	err := RunEntity(ctx, Config{
		EntityPath: entity,
		Workspace:  resolveTempWorkspace(t),
		Out:        io.Discard,
		DiagOut:    io.Discard,
		Collect:    collect,
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("the run returned %v; want an error carrying context.Canceled", err)
	}
	if collect.Reached() {
		t.Errorf("the rules ran over a withdrawn request; the collector gathered %+v", collect.Report().Findings)
	}
}

// TestRunEntityReachesTheGuestsWhenNobodyStopsIt is that test's positive control:
// the same entity under a live context is linted and reported on, so the
// assertion above is about the stop and not about the fixture.
func TestRunEntityReachesTheGuestsWhenNobodyStopsIt(t *testing.T) {
	ws := seedWorkspace(t)
	entity := filepath.Join(ws, "entity-0.yaml")
	mustWriteFile(t, entity, cancellationProbeComponent(0))

	collect := &FindingCollector{}
	// The verdict is dropped: in this mode every finding is advisory, and what this
	// control asserts is that the rules ran.
	_ = RunEntity(t.Context(), Config{
		EntityPath: entity,
		Workspace:  resolveTempWorkspace(t),
		Out:        io.Discard,
		DiagOut:    io.Discard,
		Collect:    collect,
	})

	if !collect.Reached() {
		t.Fatal("the rules did not run over a live request, so the stopped case above proves nothing")
	}
	if n := len(collect.Report().Findings); n == 0 {
		t.Error("the fixture produced no finding, so the diagnostic-stream synchronisation the cancellation proof relies on would never fire")
	}
}
