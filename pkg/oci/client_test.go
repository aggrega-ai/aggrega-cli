// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

func TestPullPushBareYAMLRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	source := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/compute/api-server\n")

	store := memory.New()
	digest, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, source)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if digest == "" {
		t.Errorf("Push returned empty digest")
	}

	got, err := oci.Pull(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if string(got.Bytes) != string(source) {
		t.Errorf("round-trip bytes mismatch.\n  got:  %q\n  want: %q", got.Bytes, source)
	}
	if got.MediaType != oci.MediaTypeComponent {
		t.Errorf("round-trip mediatype: got %q; want %q", got.MediaType, oci.MediaTypeComponent)
	}
}

func TestPullDigestMismatchSurfacesErrFetchIntegrity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Push an artifact through Push, then directly corrupt the
	// stored bytes via a low-level memory-store push so the
	// manifest's claimed digest no longer matches the fetched
	// content. The Pull path's digest verification step then
	// surfaces ErrFetchIntegrity.
	store := memory.New()
	source := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\n")
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, source); err != nil {
		t.Fatalf("initial Push: %v", err)
	}

	// Construct a parallel store with tampered bytes claiming the
	// SAME manifest tag. The tag resolves via the new store, the
	// content digest differs from what was claimed at Push time,
	// and Pull catches the mismatch.
	tamperedStore := memory.New()
	tampered := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: ComponentDIFFERENT\n")
	if _, err := oci.Push(ctx, tamperedStore, "1.0.0", oci.MediaTypeComponent, tampered); err != nil {
		t.Fatalf("tampered Push: %v", err)
	}

	got, err := oci.Pull(ctx, tamperedStore, "1.0.0")
	if err != nil {
		t.Fatalf("tampered Pull: %v", err)
	}
	// Sanity: tampered bytes round-trip cleanly with their own
	// digest. To exercise ErrFetchIntegrity proper, the test needs
	// a way to inject a mismatched manifest. Memory store doesn't
	// expose that surface; we exercise the equivalent path through
	// a custom mock target below.
	if string(got.Bytes) != string(tampered) {
		t.Errorf("tampered round-trip mismatch")
	}

	// The corruption-path verification: a stub target that returns
	// content whose SHA does not match the manifest's claimed
	// digest. The pull integrity check surfaces ErrFetchIntegrity.
	corrupt := &corruptingTarget{store: memory.New(), tampered: []byte("THIS IS NOT THE ORIGINAL CONTENT")}
	if _, err := oci.Push(ctx, corrupt.store, "1.0.0", oci.MediaTypeComponent, source); err != nil {
		t.Fatalf("Push into corrupting store: %v", err)
	}
	corrupt.corrupt = true
	_, err = oci.Pull(ctx, corrupt, "1.0.0")
	if err == nil {
		t.Fatal("expected ErrFetchIntegrity; got nil")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Errorf("error should wrap ErrFetchIntegrity; got %v", err)
	}
}

// corruptingTarget wraps a memory.Store and, when `corrupt` is true,
// returns tampered bytes for content fetches whose mediatype matches
// a registered Aggrega catalog mediatype. The manifest fetch still
// delegates to the real store so the manifest's claimed content
// digest stays intact — the mismatch surfaces when Pull verifies
// the fetched content's digest against the manifest's claim.
type corruptingTarget struct {
	store    *memory.Store
	tampered []byte
	corrupt  bool
}

func (c *corruptingTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	return c.store.Resolve(ctx, ref)
}

func (c *corruptingTarget) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	if c.corrupt && isAggregaContentMediaType(desc.MediaType) {
		return io.NopCloser(bytes.NewReader(c.tampered)), nil
	}
	return c.store.Fetch(ctx, desc)
}

func (c *corruptingTarget) Exists(ctx context.Context, desc ocispec.Descriptor) (bool, error) {
	return c.store.Exists(ctx, desc)
}

// isAggregaContentMediaType returns true when the mediatype names
// an Aggrega catalog artifact (as opposed to the OCI manifest
// mediatype). Used by the corruptingTarget to tamper with content
// fetches while leaving manifest fetches alone.
func isAggregaContentMediaType(mt string) bool {
	return strings.Contains(mt, "vnd.aggrega.")
}
