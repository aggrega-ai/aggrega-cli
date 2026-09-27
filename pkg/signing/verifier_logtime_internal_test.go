// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// TestVerifiedLogTimeReadsOnlyTheLogsEarliestTime pins which of a verification's
// timestamps becomes the signature's logged time. Each row names the defect it
// catches.
func TestVerifiedLogTimeReadsOnlyTheLogsEarliestTime(t *testing.T) {
	t.Parallel()
	logged := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC)
	later := logged.Add(time.Hour)
	stamped := logged.Add(-time.Minute)
	rome := time.FixedZone("CEST", 2*60*60)

	cases := []struct {
		name   string
		defect string
		in     []verify.TimestampVerificationResult
		want   time.Time
	}{
		{
			name:   "a log entry's time is the logged time",
			defect: "the log time is discarded",
			in:     []verify.TimestampVerificationResult{{Type: logTimestampType, Timestamp: logged}},
			want:   logged,
		},
		{
			name:   "a timestamp authority's time alone gives none",
			defect: "a signed timestamp is taken for the log's time",
			in:     []verify.TimestampVerificationResult{{Type: "TimestampAuthority", Timestamp: stamped}},
			want:   time.Time{},
		},
		{
			name:   "both kinds, the authority listed first: the log's",
			defect: "the first timestamp listed is taken whatever its kind",
			in: []verify.TimestampVerificationResult{
				{Type: "TimestampAuthority", Timestamp: stamped},
				{Type: logTimestampType, Timestamp: logged},
			},
			want: logged,
		},
		{
			name:   "two log entries: the earliest",
			defect: "the answer depends on the order entries are listed in",
			in: []verify.TimestampVerificationResult{
				{Type: logTimestampType, Timestamp: later},
				{Type: logTimestampType, Timestamp: logged},
			},
			want: logged,
		},
		{
			name:   "a time in another zone is returned in UTC",
			defect: "a zone other than UTC leaks into the recorded time",
			in:     []verify.TimestampVerificationResult{{Type: logTimestampType, Timestamp: logged.In(rome)}},
			want:   logged,
		},
		{
			name:   "a zero log time gives none",
			defect: "an empty entry is recorded as the epoch",
			in:     []verify.TimestampVerificationResult{{Type: logTimestampType}},
			want:   time.Time{},
		},
		{
			name:   "no timestamps gives none",
			defect: "a time is invented",
			in:     nil,
			want:   time.Time{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := verifiedLogTime(tc.in)
			if !got.Equal(tc.want) || (!got.IsZero() && got.Location() != time.UTC) {
				t.Fatalf("verifiedLogTime = %v; want %v (defect: %s)", got, tc.want, tc.defect)
			}
		})
	}
}

// TestVerifyKeylessKeepsTheLogTime proves the keyless verification returns the
// time the transparency log recorded, read from the verified entry, and not the
// timestamp authority's time the same signature also carries: the in-memory
// Sigstore stamps its signed timestamp now, while the log is told to record the
// signature minutes later, so the two cannot be mistaken for each other.
func TestVerifyKeylessKeepsTheLogTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		defect string
		offset time.Duration
	}{
		{name: "logged a minute after signing", defect: "the verified result keeps no time", offset: time.Minute},
		{name: "logged five minutes after signing", defect: "the timestamp authority's time is returned", offset: 5 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vs, err := ca.NewVirtualSigstore()
			if err != nil {
				t.Fatalf("NewVirtualSigstore: %v", err)
			}
			logged := time.Now().Add(tc.offset).UTC().Truncate(time.Second)
			entity, err := vs.SignAtTime(maintainerIdentity, googleIssuer, []byte(keylessDigest), logged)
			if err != nil {
				t.Fatalf("SignAtTime: %v", err)
			}
			ids := []TrustedIdentity{{Identity: maintainerIdentity, Issuer: googleIssuer}}
			matched, err := verifyKeylessEntity(entity, vs, ids, keylessDigest)
			if err != nil {
				t.Fatalf("verifyKeylessEntity: %v", err)
			}
			if !matched.LoggedAt.Equal(logged) {
				t.Fatalf("LoggedAt = %v; want the log's %v (defect: %s)", matched.LoggedAt, logged, tc.defect)
			}
			if matched.Identity != maintainerIdentity || matched.Issuer != googleIssuer {
				t.Errorf("matched identity = %q/%q; the log time must not displace the signer", matched.Identity, matched.Issuer)
			}
		})
	}
}
