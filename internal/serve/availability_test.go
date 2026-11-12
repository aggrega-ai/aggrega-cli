// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// TestEveryOperationIsDeclaredOnTheDaemon is the daemon's half of the
// operation-completeness guard: every operation the compiler exposes is either
// reachable at a named request method or declined with a reason, and no entry
// names an operation that no longer exists.
//
// The count it walks is derived from the operation set at run time rather than
// written down, so the guard cannot rot into agreement with a stale number.
// The bound's reach is reported alongside the verdict, because a check that
// goes quiet about what it did not assert is indistinguishable from one that
// asserted everything. It travels on two channels and neither substitutes for
// the other: it is folded into the failure text, which is what the reader of a
// red sees, and it is logged, which the framework shows under -v and whenever
// anything in this package fails. A package that passes without -v prints
// nothing on any channel, so the logged copy is the weaker of the two.
func TestEveryOperationIsDeclaredOnTheDaemon(t *testing.T) {
	bound := opsurface.BoundStatement(opsurface.SurfaceServe)
	t.Log(bound)

	if findings := opsurface.CheckAvailability(opsurface.SurfaceServe, OperationAvailability()); len(findings) != 0 {
		t.Fatalf("%d of %d operations are not accounted for on the daemon:\n  %s\n%s",
			len(findings), len(opsurface.Operations()), strings.Join(findings, "\n  "), bound)
	}
}

// TestEveryUnavailableDaemonOperationCarriesItsReason pins that a declined
// operation states a reason a reader can check rather than a placeholder, and
// that the reason is a clause the finding embeds rather than a sentence of its
// own. The other two surfaces hold their reasons to the same shape; without it
// here a placeholder could land in this table and nothing would notice.
func TestEveryUnavailableDaemonOperationCarriesItsReason(t *testing.T) {
	for id, a := range OperationAvailability() {
		if a.IsExposed() {
			continue
		}
		if len(a.UnavailabilityReason) < 40 {
			t.Errorf("the %q operation is declined with too little to check: %q", id, a.UnavailabilityReason)
		}
		if strings.HasSuffix(strings.TrimSpace(a.UnavailabilityReason), ".") {
			t.Errorf("the %q operation's reason reads as a sentence rather than a clause the finding embeds: %q",
				id, a.UnavailabilityReason)
		}
	}
}

// TestEveryRoutedMethodBelongsToAnOperation is the direction the availability
// walk structurally cannot see. A method added without a matching operation
// leaves nothing missing from any availability table, so nothing above fails;
// asked from the wire surface's end, the new method is visible.
//
// It also catches the inverse: a row claiming a method the daemon no longer
// routes.
func TestEveryRoutedMethodBelongsToAnOperation(t *testing.T) {
	if findings := opsurface.CheckMembers(opsurface.SurfaceServe, OperationAvailability(), Methods()); len(findings) != 0 {
		t.Fatalf("the daemon's wire surface and the operation set disagree:\n  %s",
			strings.Join(findings, "\n  "))
	}
}

// TestTheAvailabilityTableIsTheWholeRoutingSurface pins that the table is not a
// description of the routing but its source: every method the dispatcher can
// resolve comes from a row, every method an exposed row names is resolvable, and
// a row carrying an unavailability reason contributes none.
//
// The third clause is the one that makes a reason operative. Without it a reason
// could sit beside a method that routes anyway, which is the shape this table
// replaced.
func TestTheAvailabilityTableIsTheWholeRoutingSurface(t *testing.T) {
	fromTable := map[string]string{}
	for id, a := range operationAvailability {
		if a.unavailabilityReason != "" {
			if len(a.methods) != 0 {
				t.Errorf("the %q operation carries an unavailability reason and %d method(s); a declined "+
					"operation contributes no route", id, len(a.methods))
			}
			continue
		}
		for _, b := range a.methods {
			fromTable[b.method] = id
		}
	}

	for method := range methodIndex {
		if _, ok := fromTable[method]; !ok {
			t.Errorf("the dispatcher resolves %q, which no row of the availability table names", method)
		}
	}
	for method, id := range fromTable {
		if _, routed := methodIndex[method]; !routed {
			t.Errorf("the availability table exposes the %q operation at %q, which the dispatcher does not "+
				"resolve", id, method)
		}
	}
	if got, want := len(Methods()), len(fromTable); got != want {
		t.Errorf("Methods() reports %d methods for %d bindings in the table", got, want)
	}
}

// TestNoTwoMethodsShareAHandler pins the mis-wiring the table's own shape
// admits: a binding copied to add a method, with the handler left pointing at
// the method it was copied from. Both bindings stay enumerable and both route,
// so nothing else here would notice.
//
// Handlers are compared by code pointer, which is what distinguishes two
// distinct method expressions.
func TestNoTwoMethodsShareAHandler(t *testing.T) {
	byHandler := map[uintptr]string{}
	for _, id := range sortedOperationIDs(operationAvailability) {
		for _, b := range operationAvailability[id].methods {
			ptr := reflect.ValueOf(b.handler).Pointer()
			if first, ok := byHandler[ptr]; ok {
				t.Errorf("%q and %q route to the same handler", first, b.method)
				continue
			}
			byHandler[ptr] = b.method
		}
	}
}

// TestTheRoutingIndexRefusesATableItCannotRoute drives every refusal the index
// builder makes, and is the standing proof that a method cannot reach the wire
// without an operation naming it.
//
// The first row is the positive control and it is not decoration: three of the
// refusals below are panics, and a probe that only asserts a panic cannot tell a
// refusal apart from a builder that is broken for some unrelated reason. The
// control is a well-formed table that must still build.
func TestTheRoutingIndexRefusesATableItCannotRoute(t *testing.T) {
	handler := func(*Server, context.Context, *jsonRPCRequest) error { return nil }

	for _, tc := range []struct {
		name    string
		table   map[string]serveAvailability
		refuses string
	}{
		{
			name:    "a well-formed table builds",
			table:   map[string]serveAvailability{"validate": servedAt(at("aggrega/validate", handler))},
			refuses: "",
		},
		{
			name:    "an identifier that is not an operation",
			table:   map[string]serveAvailability{"not-an-operation": servedAt(at("aggrega/x", handler))},
			refuses: "which is not an operation",
		},
		{
			name:    "an exposed row with neither a method nor a reason",
			table:   map[string]serveAvailability{"validate": servedAt()},
			refuses: "under no method and gives no",
		},
		{
			name:    "an empty method name",
			table:   map[string]serveAvailability{"validate": servedAt(at("", handler))},
			refuses: "under an empty method name",
		},
		{
			name:    "a method with no handler",
			table:   map[string]serveAvailability{"validate": servedAt(at("aggrega/validate", nil))},
			refuses: "with no handler",
		},
		{
			name: "one method claimed by two operations",
			table: map[string]serveAvailability{
				"validate": servedAt(at("aggrega/same", handler)),
				"ir-graph": servedAt(at("aggrega/same", handler)),
			},
			refuses: "one wire method answers one capability",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refusal string
			func() {
				defer func() {
					if r := recover(); r != nil {
						refusal, _ = r.(string)
					}
				}()
				buildMethodIndex(tc.table)
			}()

			switch {
			case tc.refuses == "" && refusal != "":
				t.Fatalf("a table the daemon should route was refused: %s", refusal)
			case tc.refuses != "" && refusal == "":
				t.Fatalf("the index was built from a table it should have refused for %q", tc.refuses)
			case tc.refuses != "" && !strings.Contains(refusal, tc.refuses):
				t.Fatalf("the refusal does not name what was wrong: want %q in %q", tc.refuses, refusal)
			}
		})
	}
}

// TestAnUnroutedMethodIsRefused is the negative control for the routing path:
// the refusal still fires for a method the table does not carry.
//
// The positive direction needs no sample. dispatch resolves the method through
// the index built from the availability table, so a method in the table is
// routed by construction, and which handler each row names is what every
// per-method test in this package already exercises. What is not provable from
// the table's shape is that the refusal branch still works, which is this.
func TestAnUnroutedMethodIsRefused(t *testing.T) {
	sink := &requestSink{}
	ctx := withRequestSink(context.Background(), sink)
	req := &jsonRPCRequest{JSONRPC: "2.0", ID: []byte("1"), Method: "aggrega/notAMethod"}

	// A bare session: an unrouted method is refused before any workspace is
	// resolved, so nothing here needs one.
	s := &session{}
	if err := s.dispatch(ctx, req); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	body := string(sink.response)
	if !strings.Contains(body, "method not found") {
		t.Fatalf("an unrouted method was not refused; reply was %s", body)
	}
	if !strings.Contains(body, "aggrega/notAMethod") {
		t.Errorf("the refusal does not name the method it refused: %s", body)
	}
}
