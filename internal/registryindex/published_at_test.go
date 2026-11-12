// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package registryindex

import (
	"context"
	"testing"
	"time"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
)

// TestPublicationDateTakesTheAttestedTimeFirstAndAlwaysNamesItsSource pins the
// date's two sources, their order, and that a date never travels without its
// source. Each row names the defect it catches.
func TestPublicationDateTakesTheAttestedTimeFirstAndAlwaysNamesItsSource(t *testing.T) {
	t.Parallel()
	logged := time.Date(2026, 9, 27, 18, 5, 0, 0, time.UTC)
	stated := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC)
	keyless := &pullverify.Result{Mode: "keyless", Identity: "publisher@example.test", LoggedAt: logged}
	keyed := &pullverify.Result{Mode: "keyed", Identity: "keyed:sha256:00"}

	cases := []struct {
		name       string
		defect     string
		verified   *pullverify.Result
		statement  bool
		wantAt     string
		wantSource string
	}{
		{
			name: "a verified keyless signature and no statement", defect: "the log's time is not recorded",
			verified: keyless, wantAt: "2026-09-27T18:05:00Z", wantSource: PublishedAtFromTransparencyLog,
		},
		{
			name: "a verified keyless signature beside a statement", defect: "the publisher's clock displaces the attested time",
			verified: keyless, statement: true, wantAt: "2026-09-27T18:05:00Z", wantSource: PublishedAtFromTransparencyLog,
		},
		{
			name: "a verified keyed signature beside a statement", defect: "a keyed signature is taken to carry a time",
			verified: keyed, statement: true, wantAt: "2026-09-27T18:00:00Z", wantSource: PublishedAtFromPublisher,
		},
		{
			name: "no verified signature beside a statement", defect: "the statement needs a trust anchor to be read",
			verified: nil, statement: true, wantAt: "2026-09-27T18:00:00Z", wantSource: PublishedAtFromPublisher,
		},
		{
			name: "a verified keyed signature and no statement", defect: "a date is invented",
			verified: keyed, wantAt: "", wantSource: "",
		},
		{
			name: "nothing at all", defect: "a date is invented",
			verified: nil, wantAt: "", wantSource: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := memory.New()
			if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil); err != nil {
				t.Fatalf("Push: %v", err)
			}
			if tc.statement {
				if _, err := oci.AttachPublishedAt(ctx, store, "1.0.0", stated); err != nil {
					t.Fatalf("AttachPublishedAt: %v", err)
				}
			}
			at, source := PublicationDate(ctx, store, "1.0.0", tc.verified)
			if at != tc.wantAt || source != tc.wantSource {
				t.Fatalf("PublicationDate = (%q, %q); want (%q, %q) (defect: %s)", at, source, tc.wantAt, tc.wantSource, tc.defect)
			}
			if (at == "") != (source == "") {
				t.Fatalf("a date and its source travel apart: (%q, %q)", at, source)
			}
		})
	}
}
