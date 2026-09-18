// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"sync"
	"time"
)

// secondsPerMinute converts the cap a deployment writes — requests per minute —
// into the rate the allowance below refills at. It is named rather than spelled
// as a number where it is used, so the unit the configured value carries is
// stated once.
const secondsPerMinute = 60.0

// principalRateLimiter bounds how fast one caller may send, counted against
// that caller and never against the deployment.
//
// Per caller is the whole point: a cap on the deployment would let one caller
// spend everyone's allowance, which is a denial of service one authorized
// caller could inflict on the others by accident. What a caller is called is
// not interpreted here any more than anywhere else — it is a map key and
// nothing more.
//
// One allowance is held per caller for as long as the process runs, and that is
// bounded rather than unbounded: a key is a subject the configured
// authorization server issued a token for, or an identity the trusted proxy
// asserted, so the number of them is the deployment's own population of callers
// rather than a function of how much traffic arrives. A caller that cannot get
// past the credential check never reaches here and so can add nothing.
type principalRateLimiter struct {
	// perMinute is the cap as the deployment wrote it, and it is two things at
	// once: how many requests a caller may send in a burst, and — divided by
	// the seconds in a minute — how fast the allowance comes back afterwards.
	perMinute float64

	// mu guards allowances. Requests arrive on the goroutines net/http gives
	// them, so every read and write of the map and of an allowance inside it
	// happens under it.
	mu         sync.Mutex
	allowances map[string]*rateAllowance
}

// rateAllowance is one caller's remaining requests, and when that remainder was
// last brought up to date.
//
// The remainder is fractional because it refills continuously: a caller capped
// at 120 a minute gets one more every half second, and rounding that to whole
// requests would either give away a request that has not been earned or lose
// one that has.
type rateAllowance struct {
	remaining float64
	at        time.Time
}

// newPrincipalRateLimiter builds the cap for a deployment allowing perMinute
// requests from one caller in a minute.
func newPrincipalRateLimiter(perMinute int) *principalRateLimiter {
	return &principalRateLimiter{
		perMinute:  float64(perMinute),
		allowances: map[string]*rateAllowance{},
	}
}

// allow reports whether this caller has a request left, and spends one where it
// does.
//
// A caller met for the first time starts with the full allowance, so the cap
// bounds a caller that keeps asking rather than delaying one that has just
// arrived. A caller with none left is reported refused and its allowance is
// still brought up to date, because the time that passed while it was being
// refused is time the allowance was refilling.
//
// A request that reaches here carrying no caller at all is counted as one
// anonymous caller rather than being exempt, so no path through the handler is
// uncapped. Both credentials this server accepts attach a caller to every
// request they admit, so that key is reached only by a test-only authenticator
// inside this package.
func (l *principalRateLimiter) allow(p *principal) bool {
	key := ""
	if p != nil {
		key = p.Subject
	}
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	held, known := l.allowances[key]
	if !known {
		held = &rateAllowance{remaining: l.perMinute, at: now}
		l.allowances[key] = held
	} else {
		held.remaining += now.Sub(held.at).Seconds() * (l.perMinute / secondsPerMinute)
		if held.remaining > l.perMinute {
			held.remaining = l.perMinute
		}
		held.at = now
	}

	if held.remaining < 1 {
		return false
	}
	held.remaining--
	return true
}
