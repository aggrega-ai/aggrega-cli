// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"errors"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestResolveAbsentTag: a tag the target has never seen resolves to
// exists=false with no error — the "safe to publish" signal the push
// guard needs, distinct from a transport failure.
func TestResolveAbsentTag(t *testing.T) {
	ctx := context.Background()
	digest, exists, err := oci.Resolve(ctx, memory.New(), "1.0.0")
	if err != nil {
		t.Fatalf("Resolve on empty target should not error; got %v", err)
	}
	if exists {
		t.Errorf("exists = true for an absent tag")
	}
	if digest != "" {
		t.Errorf("digest = %q for an absent tag; want empty", digest)
	}
}

// TestResolvePresentTag: after a push, Resolve reports exists=true and
// the same manifest digest the push returned.
func TestResolvePresentTag(t *testing.T) {
	ctx := context.Background()
	content := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/api-server\n  version: 1.0.0\n")

	store := memory.New()
	pushed, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, content, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	digest, exists, err := oci.Resolve(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !exists {
		t.Errorf("exists = false for a published tag")
	}
	if digest != pushed {
		t.Errorf("resolved digest %q != pushed digest %q", digest, pushed)
	}
}

// resolveErrTarget wraps a memory store but fails every Resolve with a
// non-not-found error, standing in for a transport/auth failure.
type resolveErrTarget struct {
	*memory.Store
	err error
}

func (r *resolveErrTarget) Resolve(_ context.Context, _ string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, r.err
}

// TestResolveTransportError: a non-not-found error from the target
// surfaces as an error (not a false "absent"), so the guard fails loudly
// instead of silently treating an unreachable registry as a free tag.
func TestResolveTransportError(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection refused")
	target := &resolveErrTarget{Store: memory.New(), err: boom}

	digest, exists, err := oci.Resolve(ctx, target, "1.0.0")
	if err == nil {
		t.Fatal("expected a transport error to surface; got nil")
	}
	if exists {
		t.Errorf("exists = true on a transport error")
	}
	if digest != "" {
		t.Errorf("digest = %q on a transport error; want empty", digest)
	}
}
