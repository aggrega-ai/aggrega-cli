// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"net/http"
	"sync"
	"testing"
)

// Every proof here drives a real listener and counts real requests. Nothing
// manipulates a clock and nothing measures a duration: a proof that issues its
// requests as fast as it can issue them is inside one minute by construction,
// and what the cap is asserted on is how many requests it admitted rather than
// how long the admitting took.
//
// The allowance each proof configures is deliberately tiny, so the burst it has
// to send to exhaust one is a handful of requests rather than a load test.

// testSecondCaller is the other caller in every proof that has two. It differs
// from testSubject in every character that matters, so a build keying the cap on
// something other than the caller cannot pass by accident.
const testSecondCaller = "another-caller@example.test"

// The allowance every proof here configures, and the cap it exercises: a caller
// may send this many and the next one is refused.
const testAllowance = 3

// spend issues one well-formed request for the given caller and returns the
// answer, so a row reads as the count it is asserting.
func spendOne(t *testing.T, f *tokenFixture, token string) answer {
	t.Helper()
	return f.call(t, bearer(token))
}

// One more request than the allowance, from one caller, inside a minute, is
// refused — and a second caller in the same window is untouched.
//
// The second caller is what makes this a proof about the caller rather than
// about the deployment: a cap counted across everyone would refuse that request
// too, and every other assertion here would still pass.
func TestTheRateCapRefusesOneMoreThanTheAllowanceAndCountsPerCaller(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	transport := transportFor(issuer.authorizationConfig(), issuer.egress())
	transport.RequestsPerMinute = testAllowance
	f := newTokenFixture(t, transport)

	first := issuer.mint(t, tokenClaimSet{})
	second := issuer.mint(t, tokenClaimSet{subject: testSecondCaller})

	for spent := 1; spent <= testAllowance; spent++ {
		got := spendOne(t, f, first)
		if got.status != http.StatusOK {
			t.Fatalf("request %d of an allowance of %d was answered %d, want 200: the refusal below "+
				"would then be a fact about something other than the cap (body %q)",
				spent, testAllowance, got.status, got.raw)
		}
	}

	over := spendOne(t, f, first)
	wantRefusal(t, over, http.StatusTooManyRequests, codeInternalError,
		"one request past the allowance")

	// The second caller has spent nothing, and the first caller's exhausted
	// allowance is not the second caller's.
	if got := spendOne(t, f, second); got.status != http.StatusOK {
		t.Errorf("a second caller was answered %d in the same window, want 200: the cap is being counted "+
			"across callers rather than against each one (body %q)", got.status, got.raw)
	}

	// The first caller is still refused, so the row above is the second caller
	// being untouched rather than the cap having been lifted for everyone.
	if got := spendOne(t, f, first); got.status != http.StatusTooManyRequests {
		t.Errorf("the exhausted caller was answered %d, want 429: serving the second caller released the "+
			"first caller's cap (body %q)", got.status, got.raw)
	}
}

// The cap counts a caller a trusted proxy asserted exactly as it counts one a
// token named, because a proxy-asserted caller is a caller.
//
// Without this row the proxy path would be uncapped, and nothing would say so:
// every assertion in the proof above passes against a build that caps only the
// callers a token named.
func TestTheRateCapCountsACallerAProxyAsserted(t *testing.T) {
	transport := proxyTransport(testTrustedProxy(), nil)
	transport.RequestsPerMinute = testAllowance
	f := newProxyFixture(t, transport)

	asserting := func(caller string) map[string]string {
		return map[string]string{testPrincipalHeader: caller}
	}

	for spent := 1; spent <= testAllowance; spent++ {
		got := f.callWith(t, asserting(testAssertedPrincipal))
		if got.status != http.StatusOK {
			t.Fatalf("request %d of an allowance of %d was answered %d, want 200 (body %q)",
				spent, testAllowance, got.status, got.raw)
		}
	}

	over := f.callWith(t, asserting(testAssertedPrincipal))
	wantRefusal(t, over, http.StatusTooManyRequests, codeInternalError,
		"one request past the allowance, from a proxy-asserted caller")

	if got := f.callWith(t, asserting(testSecondCaller)); got.status != http.StatusOK {
		t.Errorf("a second proxy-asserted caller was answered %d in the same window, want 200 (body %q)",
			got.status, got.raw)
	}
}

// The cap is claimed before the concurrency slot, which is the handler's
// documented order and is asserted rather than left to the comment.
//
// The construction makes the two orders answer differently rather than
// answering at different speeds. The allowance and the slot count are both one,
// and the first request is held inside the tool: it has spent the allowance AND
// it holds the only slot. A second request from the same caller therefore meets
// both gates at once, and which of them answers is readable from the status —
// 429 where the cap ran first, 503 where the slot did. No timing assumption is
// made anywhere, because the tool reports that it has been entered.
//
// The caller here is the anonymous one, which is the other thing this proof
// pins: the accept-all reader this fixture installs attaches no caller, and a
// request that reaches the cap without one is counted rather than exempted, so
// no path through the handler is uncapped.
func TestTheRateCapIsClaimedBeforeTheConcurrencySlot(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseHeld := sync.OnceFunc(func() { close(release) })
	defer releaseHeld()

	transport := defaultTransportConfig()
	transport.MaxConcurrentRequests = 1
	transport.RequestsPerMinute = 1
	f := newHTTPFixture(t, transport, heldCall(entered, release))

	held := f.issue(callHeldToolBody(1))
	awaitEntered(t, entered, "the held request")

	excess := awaitExchange(t, f.issue(callHeldToolBody(2)), "the excess request")
	if excess.status == http.StatusServiceUnavailable {
		t.Fatalf("the excess request was answered 503, so it took the concurrency gate before the rate "+
			"cap: a request about to be refused for sending too fast first displaced a caller whose "+
			"request was going to be answered (body %q)", excess.raw)
	}
	wantRefusal(t, excess, http.StatusTooManyRequests, codeInternalError, "the excess request")

	releaseHeld()
	// Control: the slot the excess request never needed was held by a request
	// that was itself served, so the assertion above is about the order of the
	// two gates rather than about a listener refusing everything.
	if got := awaitExchange(t, held, "the held request"); got.status != http.StatusOK {
		t.Errorf("the held request was answered %d, want 200 (body %q)", got.status, got.raw)
	}
}
