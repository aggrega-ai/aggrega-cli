// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// TestEveryOperationIsDisposedOnTheDaemon is the daemon's half of the
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
func TestEveryOperationIsDisposedOnTheDaemon(t *testing.T) {
	bound := opsurface.BoundStatement(opsurface.SurfaceServe)
	t.Log(bound)

	if findings := opsurface.CheckDispositions(opsurface.SurfaceServe, Dispositions()); len(findings) != 0 {
		t.Fatalf("%d of %d operations are not accounted for on the daemon:\n  %s\n%s",
			len(findings), len(opsurface.Operations()), strings.Join(findings, "\n  "), bound)
	}
}

// TestEveryDeclinedDaemonOperationCarriesItsGround pins that a declined
// operation states a reason a reader can check rather than a placeholder, and
// that the reason is a clause the finding embeds rather than a sentence of its
// own. The other two surfaces hold their grounds to the same shape; without it
// here a placeholder could land in this table and nothing would notice.
func TestEveryDeclinedDaemonOperationCarriesItsGround(t *testing.T) {
	for id, d := range Dispositions() {
		if d.IsExposed() {
			continue
		}
		if len(d.Reason) < 40 {
			t.Errorf("the %q operation is declined with too little to check: %q", id, d.Reason)
		}
		if strings.HasSuffix(strings.TrimSpace(d.Reason), ".") {
			t.Errorf("the %q operation's ground reads as a sentence rather than a clause the finding embeds: %q",
				id, d.Reason)
		}
	}
}

// TestEveryRoutedMethodBelongsToAnOperation is the direction the disposition
// walk structurally cannot see. A method added to the routing table without a
// matching operation leaves nothing missing from any disposition table, so
// nothing above fails; asked from the wire surface's end, the new method is
// visible.
//
// It also catches the inverse: a disposition claiming a method the routing
// table no longer carries.
func TestEveryRoutedMethodBelongsToAnOperation(t *testing.T) {
	if findings := opsurface.CheckMembers(opsurface.SurfaceServe, Dispositions(), Methods()); len(findings) != 0 {
		t.Fatalf("the daemon's wire surface and the operation set disagree:\n  %s",
			strings.Join(findings, "\n  "))
	}
}

// TestTheRoutingTableAndItsLookupAgree pins that the enumerable table is the
// same set the dispatcher routes on. The lookup is built from the table, so this
// asserts the construction rather than a second copy — and it is the assertion
// that makes Methods() evidence about what the daemon answers rather than a
// list beside it.
func TestTheRoutingTableAndItsLookupAgree(t *testing.T) {
	if len(methodIndex) != len(methodRoutes) {
		t.Fatalf("the routing table has %d rows and the lookup %d entries; a method name is declared twice",
			len(methodRoutes), len(methodIndex))
	}
	for _, r := range methodRoutes {
		if methodIndex[r.method] == nil {
			t.Errorf("the routing table carries %q but the lookup does not resolve it", r.method)
		}
		if r.handler == nil {
			t.Errorf("the routing table carries %q with no handler", r.method)
		}
	}
	if got, want := len(Methods()), len(methodRoutes); got != want {
		t.Errorf("Methods() reports %d methods for %d routing-table rows", got, want)
	}
}

// TestNoTwoMethodsShareAHandler pins the mis-wiring the table's own shape
// admits: a row copied to add a method, with the handler left pointing at the
// method it was copied from. Both rows stay enumerable and both route, so
// nothing else here would notice.
//
// Handlers are compared by code pointer, which is what distinguishes two
// distinct method expressions.
func TestNoTwoMethodsShareAHandler(t *testing.T) {
	byHandler := map[uintptr]string{}
	for _, r := range methodRoutes {
		ptr := reflect.ValueOf(r.handler).Pointer()
		if first, ok := byHandler[ptr]; ok {
			t.Errorf("%q and %q route to the same handler", first, r.method)
			continue
		}
		byHandler[ptr] = r.method
	}
}

// TestAnUnroutedMethodIsRefused is the negative control for the routing path:
// the refusal still fires for a method the table does not carry.
//
// The positive direction needs no sample. dispatch resolves the method through
// the lookup built from the routing table above, so a method in the table is
// routed by construction, and which handler each row names is what every
// per-method test in this package already exercises. What is not provable from
// the table's shape is that the refusal branch still works, which is this.
func TestAnUnroutedMethodIsRefused(t *testing.T) {
	sink := &requestSink{}
	ctx := withRequestSink(context.Background(), sink)
	req := &jsonRPCRequest{JSONRPC: "2.0", ID: []byte("1"), Method: "aggrega/notAMethod"}

	s := &Server{}
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
