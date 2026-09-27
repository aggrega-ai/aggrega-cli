// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestPublishedAtPayloadIsCanonical pins the payload's one encoding: UTC, to the
// second, RFC 3339, in a one-key object with no trailing newline. Each row names
// the defect it catches.
func TestPublishedAtPayloadIsCanonical(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 27, 18, 3, 52, 0, time.UTC)
	cases := []struct {
		name   string
		defect string
		in     time.Time
	}{
		{name: "a UTC time", defect: "the encoding moves between runs", in: at},
		{name: "a time with sub-seconds", defect: "sub-seconds reach the record", in: at.Add(987 * time.Millisecond)},
		{name: "a time in another zone", defect: "a local zone reaches the record", in: at.In(time.FixedZone("CEST", 2*60*60))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := oci.MarshalPublishedAt(tc.in)
			if err != nil {
				t.Fatalf("MarshalPublishedAt: %v", err)
			}
			if want := `{"publishedAt":"2026-09-27T18:03:52Z"}`; string(got) != want {
				t.Fatalf("payload = %s; want %s (defect: %s)", got, want, tc.defect)
			}
			back, err := oci.UnmarshalPublishedAt(got)
			if err != nil {
				t.Fatalf("UnmarshalPublishedAt: %v", err)
			}
			if !back.Equal(at) || back.Location() != time.UTC {
				t.Fatalf("round trip = %v; want %v in UTC", back, at)
			}
		})
	}
	if _, err := oci.MarshalPublishedAt(time.Time{}); err == nil {
		t.Error("a zero time was encoded; a publication date needs a time")
	}
}

// TestUnmarshalPublishedAtRefusesWhatIsNotADate proves a payload that is not the
// one-key object with an RFC 3339 value is refused rather than read as some time.
func TestUnmarshalPublishedAtRefusesWhatIsNotADate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload string
	}{
		{name: "not JSON", payload: `published yesterday`},
		{name: "no value", payload: `{}`},
		{name: "an empty value", payload: `{"publishedAt":""}`},
		{name: "not RFC 3339", payload: `{"publishedAt":"27/09/2026"}`},
		{name: "an unknown key beside it", payload: `{"publishedAt":"2026-09-27T18:03:52Z","by":"someone"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if at, err := oci.UnmarshalPublishedAt([]byte(tc.payload)); err == nil {
				t.Fatalf("UnmarshalPublishedAt(%s) = %v; want a refusal", tc.payload, at)
			}
		})
	}
}

// TestAttachPublishedAtMovesNoDigest proves the date travels beside the manifest,
// never on it: after the attachment the tag still resolves to the digest it named,
// the manifest's bytes are unchanged, and the referrer is a sibling whose subject is
// that manifest, pinned to the epoch like every manifest this package packs. A
// defect writing the date onto the manifest moves the tag's digest and fails here.
func TestAttachPublishedAtMovesNoDigest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	digest, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	before, err := store.Resolve(ctx, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve before: %v", err)
	}
	beforeBytes, err := content.FetchAll(ctx, store, before)
	if err != nil {
		t.Fatalf("fetch manifest before: %v", err)
	}

	ref, err := oci.AttachPublishedAt(ctx, store, "1.0.0", time.Date(2026, 9, 27, 18, 3, 52, 0, time.UTC))
	if err != nil {
		t.Fatalf("AttachPublishedAt: %v", err)
	}

	after, err := store.Resolve(ctx, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve after: %v", err)
	}
	if after.Digest.String() != digest || after.Digest != before.Digest {
		t.Fatalf("the tag moved from %s to %s; the date must not touch the manifest", before.Digest, after.Digest)
	}
	afterBytes, err := content.FetchAll(ctx, store, after)
	if err != nil {
		t.Fatalf("fetch manifest after: %v", err)
	}
	if string(afterBytes) != string(beforeBytes) {
		t.Fatal("the manifest's bytes changed")
	}

	raw, err := content.FetchAll(ctx, store, ref)
	if err != nil {
		t.Fatalf("fetch referrer: %v", err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode referrer: %v", err)
	}
	if m.ArtifactType != oci.MediaTypePublishedAt {
		t.Errorf("referrer artifactType = %q; want %q", m.ArtifactType, oci.MediaTypePublishedAt)
	}
	if m.Subject == nil || m.Subject.Digest != before.Digest {
		t.Errorf("referrer subject = %v; want the published manifest %s", m.Subject, before.Digest)
	}
	if got := m.Annotations[ocispec.AnnotationCreated]; got != "1970-01-01T00:00:00Z" {
		t.Errorf("referrer created = %q; want the epoch", got)
	}
}

// TestReadPublishedAtTakesTheEarliestValidStatement proves the read is independent
// of the order a registry lists referrers in, passes over a payload that does not
// decode, and tells "no date recorded" from "no answer". Each row names the defect
// it catches.
func TestReadPublishedAtTakesTheEarliestValidStatement(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	cases := []struct {
		name      string
		defect    string
		statement []time.Time
		garbage   bool
		wantFound bool
		want      time.Time
	}{
		{name: "none attached", defect: "a date is invented", wantFound: false},
		{name: "one attached", defect: "the statement is not read", statement: []time.Time{late}, wantFound: true, want: late},
		{name: "two, the later attached first", defect: "the first listed wins, so the answer follows listing order", statement: []time.Time{late, early}, wantFound: true, want: early},
		{name: "one, beside a payload that does not decode", defect: "an unreadable payload fails the read or is taken as a date", statement: []time.Time{late}, garbage: true, wantFound: true, want: late},
		{name: "only a payload that does not decode", defect: "an unreadable payload is taken as a date", garbage: true, wantFound: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := memory.New()
			if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil); err != nil {
				t.Fatalf("Push: %v", err)
			}
			for _, at := range tc.statement {
				if _, err := oci.AttachPublishedAt(ctx, store, "1.0.0", at); err != nil {
					t.Fatalf("AttachPublishedAt: %v", err)
				}
			}
			if tc.garbage {
				if _, err := oci.AttachReferrer(ctx, store, "1.0.0", oci.MediaTypePublishedAt, []byte("not a date")); err != nil {
					t.Fatalf("AttachReferrer: %v", err)
				}
			}
			got, found, err := oci.ReadPublishedAt(ctx, store, "1.0.0")
			if err != nil {
				t.Fatalf("ReadPublishedAt: %v", err)
			}
			if found != tc.wantFound || !got.Equal(tc.want) {
				t.Fatalf("ReadPublishedAt = (%v, %v); want (%v, %v) (defect: %s)", got, found, tc.want, tc.wantFound, tc.defect)
			}
		})
	}
}

// TestReadPublishedAtReportsAListingItCouldNotComplete proves "no answer" is not
// reported as "no date": a target that cannot list referrers is an error wrapping
// the discovery-unavailable condition.
func TestReadPublishedAtReportsAListingItCouldNotComplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	_, found, err := oci.ReadPublishedAt(ctx, resolveOnly{store}, "1.0.0")
	if err == nil || found {
		t.Fatalf("ReadPublishedAt over a target that cannot list referrers = (found %v, err %v); want an error", found, err)
	}
	if !errors.Is(err, oci.ErrReferrerDiscoveryUnavailable()) {
		t.Errorf("error %v does not carry the discovery-unavailable condition", err)
	}
}

// resolveOnly is a read-only target that resolves and fetches but cannot serve a
// referrer listing.
type resolveOnly struct{ s *memory.Store }

func (r resolveOnly) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	return r.s.Resolve(ctx, ref)
}

func (r resolveOnly) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	return r.s.Fetch(ctx, desc)
}

func (r resolveOnly) Exists(ctx context.Context, desc ocispec.Descriptor) (bool, error) {
	return r.s.Exists(ctx, desc)
}
