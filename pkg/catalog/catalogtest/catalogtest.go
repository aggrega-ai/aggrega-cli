// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package catalogtest provides fixtures for tests that hold a catalog view to a
// lockfile's or a closure's pins. It is a test helper, not a way to build pins:
// every pin set it returns comes from catalog.PinsFromLockfile or
// catalog.PinsFromClosure, and a refusal of either stops the test at the line
// that asked, rather than handing back an empty pin set a later assertion would
// misread as a view held to nothing.
//
// It depends on the catalog, closure and lockfile packages only, so a test
// anywhere above them can use it.
package catalogtest

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// MustPinsFromLockfile returns the pins catalog.PinsFromLockfile builds from lf,
// failing t where it refuses: a fixture lockfile records a declared kind on every
// entity row, as a pull does.
func MustPinsFromLockfile(t testing.TB, lf *lockfile.Lockfile) catalog.Pins {
	t.Helper()
	pins, err := catalog.PinsFromLockfile(lf)
	if err != nil {
		t.Fatalf("pin the fixture view to its lockfile: %v", err)
	}
	return pins
}

// MustPinsFromClosure returns the pins catalog.PinsFromClosure builds from cl,
// failing t where it refuses.
func MustPinsFromClosure(t testing.TB, cl closure.Closure) catalog.Pins {
	t.Helper()
	pins, err := catalog.PinsFromClosure(cl)
	if err != nil {
		t.Fatalf("pin the fixture view to its closure: %v", err)
	}
	return pins
}
