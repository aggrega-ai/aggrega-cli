// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestPullRefusesARetiredMediaType publishes one document into an in-memory
// store under three media types and pulls it back.
//
// Under the Component media type an earlier release wrote, the pull is refused by
// name — the condition's identity and short code, the media type the artifact
// carries, the one the compiler reads, and what to do — before any content is
// read, and a read of the manifest's facets is refused the same way. Under the
// current Component media type it is read. Under a third party's
// media type that happens to carry the same retired version segment it is read
// too, so the refusal is Aggrega's media types only and not any string carrying
// the version.
func TestPullRefusesARetiredMediaType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: probe\n")
	retired := strings.Replace(oci.MediaTypeComponent, ".v1beta1", ".v1alpha1", 1)
	if retired == oci.MediaTypeComponent {
		t.Fatalf("the Component media type %q carries no v1beta1 segment to retire", oci.MediaTypeComponent)
	}

	cases := []struct {
		name      string
		mediaType string
		refused   bool
	}{
		{name: "the retired Component media type is refused", mediaType: retired, refused: true},
		{name: "the current Component media type is read", mediaType: oci.MediaTypeComponent},
		{name: "a third party's media type with the same segment is read", mediaType: "application/vnd.example.thing.v1alpha1+yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := memory.New()
			if _, err := oci.Push(ctx, store, "1.0.0", tc.mediaType, source, nil); err != nil {
				t.Fatalf("Push: %v", err)
			}
			got, err := oci.Pull(ctx, store, "1.0.0")
			if !tc.refused {
				if err != nil {
					t.Fatalf("Pull under %q: %v", tc.mediaType, err)
				}
				if string(got.Bytes) != string(source) {
					t.Errorf("Pull under %q returned other bytes", tc.mediaType)
				}
				return
			}
			if !errors.Is(err, oci.ErrRetiredMediaType()) {
				t.Fatalf("Pull under %q: want ErrRetiredMediaType, got %v", tc.mediaType, err)
			}
			if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0375" {
				t.Errorf("the short code must be E0375; got %q (found %v)", code, ok)
			}
			for _, want := range []string{`"` + retired + `"`, `"` + oci.MediaTypeComponent + `"`, "publish the artifact again"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q; got %v", want, err)
				}
			}
			// An index reads the same manifest for its facets, through the same read,
			// so it is refused the same way rather than listing what a pull refuses.
			if _, _, err := oci.FetchManifestAnnotations(ctx, store, "1.0.0"); !errors.Is(err, oci.ErrRetiredMediaType()) {
				t.Errorf("FetchManifestAnnotations under %q: want ErrRetiredMediaType, got %v", tc.mediaType, err)
			}
		})
	}
}
