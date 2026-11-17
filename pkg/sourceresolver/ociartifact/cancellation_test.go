// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package ociartifact

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

// endedTarget answers every call with the error of the context it was handed, as a
// registry transport does when the caller has stopped waiting.
type endedTarget struct{}

func (endedTarget) Resolve(ctx context.Context, _ string) (ocispec.Descriptor, error) {
	<-ctx.Done()
	return ocispec.Descriptor{}, ctx.Err()
}

func (endedTarget) Fetch(ctx context.Context, _ ocispec.Descriptor) (io.ReadCloser, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (endedTarget) Exists(context.Context, ocispec.Descriptor) (bool, error) { return false, nil }

var _ oras.ReadOnlyTarget = endedTarget{}

func TestResolveWithdrawnReportsTheWithdrawal(t *testing.T) {
	t.Parallel()
	for _, kind := range sourcekind.OCIModuleKinds() {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			row, _ := sourcekind.OCIArtifactOf(kind)
			a := &adapter{kind: kind, row: row, newTarget: func(string, *http.Client) (oras.ReadOnlyTarget, error) {
				return endedTarget{}, nil
			}}
			ctx, stop := context.WithCancel(context.Background())
			stop()
			_, err := a.Resolve(ctx, specOf(kind, root, tag, ""))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error does not wrap context.Canceled: %v", err)
			}
			if errors.Is(err, sourceresolver.ErrFetchNetworkUnreachable()) || strings.Contains(err.Error(), "unreachable") {
				t.Errorf("a withdrawal reads as an unreachable network: %v", err)
			}
			assertNothingCached(t, root)
		})
	}
}
