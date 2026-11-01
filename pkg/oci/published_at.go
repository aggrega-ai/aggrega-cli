// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

// A published-at referrer records when a publication landed, beside the
// artifact rather than on it. A manifest's creation annotation is pinned to the
// epoch so its digest is a function of what is published, which leaves the
// manifest with no room for a real time; a referrer is a sibling manifest whose
// subject is the published one, so attaching it moves no digest and changes no
// tag.
//
// The time it carries is the publishing machine's own clock, asserted by the
// publisher. It is not signed and nothing attests it, so a reader states it as
// the publisher's claim. A publication whose signature carries a transparency
// log's attested time needs none, and a publication writes one only where its
// signature does not.
//
// This file is the format's one home: its artifact type, its payload, and the
// write and the read, so the publishing side and the reading side cannot come
// to disagree about any of them.

// MediaTypePublishedAt is the OCI artifactType of a published-at referrer and
// the media type of its payload.
const MediaTypePublishedAt = "application/vnd.aggrega.published-at.v1+json"

// publishedAtPayload is the payload of a published-at referrer: one RFC 3339
// UTC timestamp, to the second.
type publishedAtPayload struct {
	PublishedAt string `json:"publishedAt"`
}

// MarshalPublishedAt returns the canonical payload recording at: the time in
// UTC, to the second, as RFC 3339, in a fixed one-key JSON object with no
// trailing newline, so one time always encodes to the same bytes.
func MarshalPublishedAt(at time.Time) ([]byte, error) {
	if at.IsZero() {
		return nil, fmt.Errorf("a publication date needs a time")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(publishedAtPayload{PublishedAt: FormatPublishedAt(at)}); err != nil {
		return nil, fmt.Errorf("marshal publication date: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// UnmarshalPublishedAt decodes a published-at payload and returns its time in
// UTC. A payload that is not the one-key object, or whose value is not an
// RFC 3339 timestamp, is refused rather than read as some time.
func UnmarshalPublishedAt(payload []byte) (time.Time, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	var p publishedAtPayload
	if err := dec.Decode(&p); err != nil {
		return time.Time{}, fmt.Errorf("decode publication date: %w", err)
	}
	if p.PublishedAt == "" {
		return time.Time{}, fmt.Errorf("publication date payload carries no publishedAt")
	}
	at, err := time.Parse(time.RFC3339, p.PublishedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("publication date %q is not an RFC 3339 timestamp: %w", p.PublishedAt, err)
	}
	return at.UTC(), nil
}

// FormatPublishedAt spells a publication time the way every record of one
// spells it: UTC, to the second, RFC 3339.
func FormatPublishedAt(at time.Time) string {
	return at.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// AttachPublishedAt attaches a published-at referrer recording at to the
// artifact subjectRef resolves to. The artifact itself is not re-pushed,
// re-tagged or changed: the referrer is a sibling manifest pointing back at it,
// through the one attach path signatures and deprecation markers take, so its
// writes retry a transient registry failure as that path's do. The payload is
// encoded once, before the first attempt, so every attempt sends the same date.
func AttachPublishedAt(ctx context.Context, target oras.Target, subjectRef string, at time.Time) (ocispec.Descriptor, error) {
	payload, err := MarshalPublishedAt(at)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return AttachReferrer(ctx, target, subjectRef, MediaTypePublishedAt, payload)
}

// ReadPublishedAt returns the time the published-at referrers of subjectRef
// record, and whether any recorded one. Where several are attached it returns
// the earliest, so the answer does not depend on the order a registry lists
// referrers in, and a later attachment cannot move a publication's date
// forward. A referrer whose payload cannot be fetched or does not decode is
// passed over. A listing that could not be completed is returned as the error,
// so a caller can tell "no date recorded" from "no answer".
func ReadPublishedAt(ctx context.Context, target oras.ReadOnlyTarget, subjectRef string) (time.Time, bool, error) {
	earliest, found, _, err := readPublishedAt(ctx, target, subjectRef)
	return earliest, found, err
}

// ReadPublishedAtFully is ReadPublishedAt for a caller that keeps the answer
// until the referrers change, and so must not keep one a passing failure shaped.
// It returns the same date, and an error as well where any published-at
// referrer's payload could not be fetched: the earliest of the ones that were
// read may then not be the earliest attached. A payload that was fetched and does
// not decode is still passed over without an error, since reading it again
// decodes the same bytes the same way, and so is a referrer the registry answers
// it does not hold, which a later read answers the same way.
func ReadPublishedAtFully(ctx context.Context, target oras.ReadOnlyTarget, subjectRef string) (time.Time, bool, error) {
	earliest, found, unfetched, err := readPublishedAt(ctx, target, subjectRef)
	if err != nil {
		return time.Time{}, false, err
	}
	if unfetched != nil {
		return earliest, found, unfetched
	}
	return earliest, found, nil
}

// readPublishedAt is the one walk over a version's published-at referrers both
// readings share. unfetched is the last payload fetch that failed without an
// answer, if any.
func readPublishedAt(ctx context.Context, target oras.ReadOnlyTarget, subjectRef string) (earliest time.Time, found bool, unfetched, err error) {
	refs, err := ListReferrers(ctx, target, subjectRef, MediaTypePublishedAt)
	if err != nil {
		return time.Time{}, false, nil, err
	}
	for _, ref := range refs {
		payload, ferr := FetchReferrerPayload(ctx, target, ref)
		if ferr != nil {
			if !isNotFoundResponse(ferr) {
				unfetched = ferr
			}
			continue
		}
		at, perr := UnmarshalPublishedAt(payload)
		if perr != nil {
			continue
		}
		if !found || at.Before(earliest) {
			earliest, found = at, true
		}
	}
	return earliest, found, unfetched, nil
}
