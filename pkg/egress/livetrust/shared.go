// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package livetrust

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// sharedRootKey is the context key a [WithSharedRoot] context carries its holder
// under. It is unexported, so nothing outside this package can place a trust root
// in a context: the only root a holder ever hands out is one [TrustedRoot]
// fetched.
type sharedRootKey struct{}

// sharedRoot holds the live trust root for every verification made under one
// context, once it has been fetched.
type sharedRoot struct {
	mu   sync.Mutex
	live root.TrustedMaterial
}

// WithSharedRoot returns a context under which every [TrustedRootWithin] call
// shares one fetch of the live public trust root: the first call that needs it
// fetches it through [TrustedRoot], and every later call under the context is
// handed that same root rather than fetching again.
//
// It is for a caller about to verify many signatures, several at a time, where
// one fetch per verification would both repeat the same round trip and let the
// fetches' TUF clients rewrite one cache directory at once (see [TrustedRoot]).
// The fetches are made one at a time: a call arriving while another is fetching
// waits for that fetch and is handed its root.
//
// Only a fetch that succeeded is shared. A fetch that failed leaves nothing held,
// so the next call that needs the root fetches again, as it would have without a
// shared root, and a failure in passing does not become the answer for every
// verification after it.
//
// Nothing else changes: a verification under a policy pinning its own trust root
// reads that file and never asks for the live root, and an offline configuration
// is refused by [TrustedRoot]'s own offline gate on every call, whatever is held.
func WithSharedRoot(ctx context.Context) context.Context {
	return context.WithValue(ctx, sharedRootKey{}, &sharedRoot{})
}

// EnsureSharedRoot returns ctx itself where it already carries a shared root
// ([WithSharedRoot]), and [WithSharedRoot] of ctx otherwise. It is for a pass that
// verifies several signatures at once and may be reached both under a caller that
// shares one root across a larger run and under one that shares none: the first
// keeps sharing the caller's one fetch, and the second shares one fetch among the
// pass's own verifications rather than letting them fetch side by side.
func EnsureSharedRoot(ctx context.Context) context.Context {
	if sharedRootOf(ctx) != nil {
		return ctx
	}
	return WithSharedRoot(ctx)
}

// sharedRootOf returns the shared root ctx carries ([WithSharedRoot]), or nil.
func sharedRootOf(ctx context.Context) *sharedRoot {
	shared, _ := ctx.Value(sharedRootKey{}).(*sharedRoot)
	return shared
}

// TrustedRootWithin is [TrustedRoot] for a verification made under ctx. Where ctx
// carries a shared root ([WithSharedRoot]) and cfg is not offline, it is that
// root, fetched by the first call that needed it; otherwise it is a fetch of its
// own, exactly as [TrustedRoot] makes one.
func TrustedRootWithin(ctx context.Context, cfg workspaceconfig.Config) (root.TrustedMaterial, error) {
	shared := sharedRootOf(ctx)
	if shared == nil || cfg.Offline {
		return TrustedRoot(cfg)
	}
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.live == nil {
		live, err := TrustedRoot(cfg)
		if err != nil {
			return nil, err
		}
		shared.live = live
	}
	return shared.live, nil
}

// testRepository, where set, is the TUF repository and cache directory every live
// fetch in this test binary uses in place of the public repository and this
// machine's cache ([UseTestRepository]).
var testRepository atomic.Pointer[tuf.Options]

// UseTestRepository makes every live trust-root fetch this test binary makes, for
// the rest of tb, read the TUF repository served at mirror, starting from the
// repository root rootJSON, and keep what it fetches in cacheDir — so a test can
// drive the live fetch, through every caller that reaches it, without reaching
// the public repository or writing this machine's cache.
//
// It applies to the whole test binary, so a test calling it must not run in
// parallel with another test that fetches the live root. Outside a test binary it
// panics: the live fetch reads the public repository and nothing else.
func UseTestRepository(tb testing.TB, rootJSON []byte, mirror, cacheDir string) {
	tb.Helper()
	if !testing.Testing() {
		panic("UseTestRepository called outside a test binary: the live trust root is fetched from the public repository alone")
	}
	testRepository.Store(&tuf.Options{Root: rootJSON, CachePath: cacheDir, RepositoryBaseURL: mirror})
	tb.Cleanup(func() { testRepository.Store(nil) })
}
