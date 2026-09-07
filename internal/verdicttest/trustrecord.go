// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package verdicttest

import (
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
)

// TrustRecordAt loads the trust record beside releasePath, failing the test when
// it is absent or unreadable.
//
// It reads through the loader every consumer of the record uses, so a record a
// command wrote and this reads back cannot disagree about what the file means —
// and a record that stopped satisfying its schema fails here rather than being
// silently read as empty.
func TrustRecordAt(t *testing.T, releasePath string) *pluginstrust.Trust {
	t.Helper()
	trust, err := pluginstrust.Load(pluginstrust.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load trust record beside %s: %v", releasePath, err)
	}
	return trust
}

// RecordedCoordinates returns the plugin identity coordinates a trust record
// holds, sorted, for a diagnostic that names what a record actually contains
// when an expected entry is missing.
func RecordedCoordinates(trust *pluginstrust.Trust) []string {
	var out []string
	for _, p := range trust.Plugins {
		out = append(out, p.Plugin)
	}
	sort.Strings(out)
	return out
}
