// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package livetrust_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust/livetrusttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// verifiersAtOnce is how many verifications each round starts together: the
// bound a registry walk reads with.
const verifiersAtOnce = 16

// conflictText is what a TUF client says when another client replaced the
// metadata it had just written to the cache directory they share.
const conflictText = "failed to persist metadata"

// raceFetches starts n calls of fetch together and returns what each obtained.
func raceFetches(n int, fetch func() (root.TrustedMaterial, error)) ([]root.TrustedMaterial, []error) {
	got := make([]root.TrustedMaterial, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got[i], errs[i] = fetch()
		}()
	}
	close(start)
	wg.Wait()
	return got, errs
}

// TestVerificationsSharingARootFetchItOnceAndNeverConflict starts sixteen
// verifications at once, round after round, each round on a machine that has
// never fetched the trust root, against a repository whose root has been rotated
// the way the public one has.
//
// The control comes first: fetches made the way a single verification makes one,
// each building its own TUF client, are started together until two of them
// conflict over the cache directory they share — the failure a walk verifying
// many signatures at once met. A fixture that never produced the conflict would
// make the shared rounds' zero below prove nothing, so the control fails then.
//
// Then the same verifications share one root: in every round, every one of them
// is handed the one root fetched for the round, no fetch fails, and the
// repository is read once.
//
// Three edges of the shared root follow. A configuration that is offline is
// refused by the offline gate even where a root is held; a fetch that failed is
// not shared, so the next call fetches again; and a context carrying no shared
// root fetches on every call, as a single verification does.
func TestVerificationsSharingARootFetchItOnceAndNeverConflict(t *testing.T) {
	// Not parallel: the repository every live fetch reads is the test binary's.
	repo := livetrusttest.Serve(t, trustedRootJSON(t))
	cfg := workspaceconfig.Config{}

	const controlRounds = 200
	conflicted := 0
	for round := 0; round < controlRounds && conflicted == 0; round++ {
		repo.FreshCache(t)
		_, errs := raceFetches(verifiersAtOnce, func() (root.TrustedMaterial, error) { return livetrust.TrustedRoot(cfg) })
		for _, err := range errs {
			if err != nil && strings.Contains(err.Error(), conflictText) {
				conflicted++
				t.Logf("control, round %d: %v", round, err)
			}
		}
	}
	if conflicted == 0 {
		t.Fatalf("in %d rounds of %d fetches each building its own client, no two conflicted, so this fixture cannot show "+
			"that sharing one root removes the conflict", controlRounds, verifiersAtOnce)
	}

	const sharedRounds = 40
	for round := range sharedRounds {
		repo.FreshCache(t)
		before := repo.Fetches()
		ctx := livetrust.WithSharedRoot(t.Context())
		got, errs := raceFetches(verifiersAtOnce, func() (root.TrustedMaterial, error) { return livetrust.TrustedRootWithin(ctx, cfg) })
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d: a verification sharing the root could not obtain it: %v", round, err)
			}
			if got[i] != got[0] {
				t.Fatalf("round %d: verifications sharing one root were handed different roots", round)
			}
		}
		if fetched := repo.Fetches() - before; fetched != 1 {
			t.Fatalf("round %d: %d verifications sharing one root fetched it %d times; want once", round, verifiersAtOnce, fetched)
		}

		if round == 0 {
			if _, err := livetrust.TrustedRootWithin(ctx, workspaceconfig.Config{Offline: true}); !errors.Is(err, egress.ErrOfflineEgress()) {
				t.Errorf("offline, under a context holding a fetched root, the call was not refused as offline: %v", err)
			}
		}
	}

	// A fetch that failed is not shared: the next call fetches again, and the root
	// it obtains is the one shared from then on.
	ctx := livetrust.WithSharedRoot(t.Context())
	livetrust.UseTestRepository(t, []byte("not a repository root"), "http://127.0.0.1:1", t.TempDir())
	if _, err := livetrust.TrustedRootWithin(ctx, cfg); err == nil {
		t.Fatal("a fetch from a repository that cannot be read obtained a root")
	}
	repo.FreshCache(t)
	before := repo.Fetches()
	first, err := livetrust.TrustedRootWithin(ctx, cfg)
	if err != nil {
		t.Fatalf("after a failed fetch, the next call under the same context did not fetch again: %v", err)
	}
	again, err := livetrust.TrustedRootWithin(ctx, cfg)
	if err != nil || again != first || repo.Fetches()-before != 1 {
		t.Errorf("after a failed fetch and one that succeeded, a third call got (%v, same root %v) with %d fetches; want the held root and no fetch",
			err, again == first, repo.Fetches()-before)
	}

	before = repo.Fetches()
	for range 2 {
		if _, err := livetrust.TrustedRootWithin(context.Background(), cfg); err != nil {
			t.Fatalf("a call under a context carrying no shared root: %v", err)
		}
	}
	if fetched := repo.Fetches() - before; fetched != 2 {
		t.Errorf("two calls under a context carrying no shared root fetched %d times; want a fetch each", fetched)
	}
}
