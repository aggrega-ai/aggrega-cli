// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullverify_test

import (
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
)

// TestEarlierSignatureRanksByLogTimeThenBundleDigest proves the one ordering
// every reader of an artifact's signatures ranks by: a signature the log timed
// before one it did not, the earlier log time first, and a tie on the lower bundle
// digest. Each row is asked in both directions, so a rule that answered from the
// argument order rather than from the signatures fails it; the first row is the
// control.
func TestEarlierSignatureRanksByLogTimeThenBundleDigest(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 7, 17, 31, 0, 0, time.UTC)
	const lowDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const highDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"

	cases := []struct {
		name   string
		defect string
		// first must rank before second, and second must not rank before first.
		first, second pullverify.Result
	}{
		{
			name:   "control: the earlier log time ranks first",
			defect: "a later log time outranks an earlier one, so signing again moves the date",
			first:  pullverify.Result{LoggedAt: at, BundleDigest: highDigest},
			second: pullverify.Result{LoggedAt: at.Add(time.Minute), BundleDigest: lowDigest},
		},
		{
			name:   "a timed signature ranks before an untimed one",
			defect: "a signature attesting no time is preferred, so the attested date is set aside",
			first:  pullverify.Result{LoggedAt: at.Add(time.Hour), BundleDigest: highDigest},
			second: pullverify.Result{BundleDigest: lowDigest},
		},
		{
			name:   "equal log times break on the lower bundle digest",
			defect: "a tie is broken by something other than the bundle, such as the order the registry lists them in",
			first:  pullverify.Result{LoggedAt: at, BundleDigest: lowDigest},
			second: pullverify.Result{LoggedAt: at, BundleDigest: highDigest},
		},
		{
			name:   "two untimed signatures break on the lower bundle digest",
			defect: "two keyed signatures have no defined order, so which bundle is recorded follows the listing",
			first:  pullverify.Result{Mode: "keyed", BundleDigest: lowDigest},
			second: pullverify.Result{Mode: "keyed", BundleDigest: highDigest},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			first, second := tc.first, tc.second
			if !pullverify.EarlierSignature(&first, &second) {
				t.Errorf("the first does not rank before the second (defect: %s)", tc.defect)
			}
			if pullverify.EarlierSignature(&second, &first) {
				t.Errorf("the second ranks before the first (defect: %s)", tc.defect)
			}
		})
	}

	t.Run("a signature does not rank before itself", func(t *testing.T) {
		t.Parallel()
		same := pullverify.Result{LoggedAt: at, BundleDigest: lowDigest}
		if pullverify.EarlierSignature(&same, &same) {
			t.Error("a signature ranks before itself, so the order is not strict and two copies of one signature each outrank the other")
		}
	})
}
