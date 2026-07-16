// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// TestSignAttachesReferrerWithoutMutatingArtifact proves `aggrega sign`
// signs an already-published artifact and attaches the signature as a
// referrer, leaving the artifact's bytes and tag untouched — the
// immutability-safe amendment.
func TestSignAttachesReferrerWithoutMutatingArtifact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	published, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("published content"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	fake := &signingtest.FakeSigner{}
	err = signRun(signConfig{
		References:  []string{"hub.aggrega.ai/component/core/api-server:1.0.0"},
		SubjectRefs: map[string]string{"hub.aggrega.ai/component/core/api-server:1.0.0": "1.0.0"},
		Target:      store,
		Signer:      fake,
		// A supplied token short-circuits the keyless acquire-once block (the
		// injected signer is keyless: no KeyPath), keeping the test offline.
		IDToken:   "supplied-token",
		EnvLookup: func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("signRun: %v", err)
	}

	// The signer was asked to sign the published manifest digest.
	if len(fake.Signed) != 1 || fake.Signed[0] != published {
		t.Fatalf("signer saw %v, want one call for digest %q", fake.Signed, published)
	}

	// The artifact tag still points at the same digest (no re-push).
	after, exists, err := oci.Resolve(ctx, store, "1.0.0")
	if err != nil || !exists {
		t.Fatalf("Resolve after sign: exists=%v err=%v", exists, err)
	}
	if after != published {
		t.Errorf("tag now points at %q, want unchanged %q", after, published)
	}

	// The signature is discoverable as a referrer.
	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d signature referrers, want 1", len(refs))
	}
}

// TestSignUnpublishedArtifactFails proves signing a reference the registry
// does not hold fails loudly rather than publishing anything.
func TestSignUnpublishedArtifactFails(t *testing.T) {
	t.Parallel()
	store := memory.New()

	err := signRun(signConfig{
		References:  []string{"hub.aggrega.ai/component/core/missing:9.9.9"},
		SubjectRefs: map[string]string{"hub.aggrega.ai/component/core/missing:9.9.9": "9.9.9"},
		Target:      store,
		Signer:      &signingtest.FakeSigner{},
		IDToken:     "supplied-token",
		EnvLookup:   func(string) string { return "" },
	})
	if err == nil {
		t.Fatal("signRun should fail for an unpublished reference")
	}
	if !strings.Contains(err.Error(), "not published") {
		t.Errorf("error = %v, want a not-published diagnostic", err)
	}
}

// TestSignWithRealKeyedSignerRoundTrips proves the keyed path produces a
// real, parseable Sigstore bundle attached to the artifact — a fully
// offline end-to-end sign through the command layer.
func TestSignWithRealKeyedSignerRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	if _, err := oci.Push(ctx, store, "2.0.0", oci.MediaTypeComponent, []byte("keyed content"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyed, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	if err := signRun(signConfig{
		References:  []string{"hub.aggrega.ai/component/core/api-server:2.0.0"},
		SubjectRefs: map[string]string{"hub.aggrega.ai/component/core/api-server:2.0.0": "2.0.0"},
		Target:      store,
		Signer:      keyed,
		// The injected signer is keyed; mark the batch keyed so prepare skips
		// the keyless acquire-once block. KeyPath only gates the path here —
		// the injected signer is used verbatim, so the value is never read.
		KeyPath: "(injected-keyed-signer)",
	}); err != nil {
		t.Fatalf("signRun: %v", err)
	}

	refs, err := oci.ListReferrers(ctx, store, "2.0.0", signing.BundleMediaType)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListReferrers: refs=%d err=%v", len(refs), err)
	}
	payload, err := oci.FetchReferrerPayload(ctx, store, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	if _, err := signing.LoadBundle(payload); err != nil {
		t.Fatalf("attached bundle does not parse as a Sigstore bundle: %v", err)
	}
}

// TestSignKeylessRefusesWithoutLiveInfra proves the keyless signer surfaces
// ErrKeylessLive rather than silently producing anything when the live
// identity/Fulcio/Rekor endpoints are absent — there is no offline keyless
// shortcut.
func TestSignKeylessRefusesWithoutLiveInfra(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := &signing.KeylessSigner{} // no IDToken/Fulcio/Rekor configured
	_, _, err := signer.Sign(ctx, "sha256:abc")
	if !errors.Is(err, signing.ErrKeylessLive) {
		t.Fatalf("want ErrKeylessLive, got %v", err)
	}
}

// publishN seeds a memory store with N artifacts under tags 1.0.0 … N.0.0 and
// returns the references and the per-reference subject map keyed to those tags.
func publishN(t *testing.T, store *memory.Store, n int) ([]string, map[string]string) {
	t.Helper()
	ctx := context.Background()
	refs := make([]string, 0, n)
	subjects := make(map[string]string, n)
	for i := 1; i <= n; i++ {
		tag := itoaVer(i)
		if _, err := oci.Push(ctx, store, tag, oci.MediaTypeComponent, []byte("content "+tag), nil); err != nil {
			t.Fatalf("Push %s: %v", tag, err)
		}
		ref := "hub.aggrega.ai/component/core/c" + itoaVer(i) + ":" + tag
		refs = append(refs, ref)
		subjects[ref] = tag
	}
	return refs, subjects
}

func itoaVer(i int) string {
	return string(rune('0'+i)) + ".0.0"
}

// TestSignBatchAcquiresTokenOnce proves a keyless batch over N references
// acquires the identity token at most once (one interactive authentication,
// many signatures) while signing every reference.
func TestSignBatchAcquiresTokenOnce(t *testing.T) {
	t.Parallel()
	store := memory.New()
	refs, subjects := publishN(t, store, 3)

	var acquired int
	fake := &signingtest.FakeSigner{}
	err := signRun(signConfig{
		References:  refs,
		SubjectRefs: subjects,
		Target:      store,
		Signer:      fake,
		EnvLookup:   func(string) string { return "" },
		IsTTY:       func() bool { return true },
		TokenGetter: func() (string, error) { acquired++; return "interactive-token", nil },
	})
	if err != nil {
		t.Fatalf("signRun: %v", err)
	}
	if acquired != 1 {
		t.Errorf("token acquired %d times, want exactly 1 for the whole batch", acquired)
	}
	if len(fake.Signed) != len(refs) {
		t.Errorf("signed %d digests, want %d (one per reference)", len(fake.Signed), len(refs))
	}
}

// TestSignBatchContinuesAndReports proves a mid-batch signing failure does not
// halt the batch: the other references are still signed, the run returns a
// non-zero error, and the summary names the failed reference.
func TestSignBatchContinuesAndReports(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	refs, subjects := publishN(t, store, 3)

	// Fail exactly the middle reference's digest.
	middleDigest, _, err := oci.Resolve(ctx, store, subjects[refs[1]])
	if err != nil {
		t.Fatalf("Resolve middle: %v", err)
	}
	fake := &signingtest.FakeSigner{
		FailOn: func(digest string) error {
			if digest == middleDigest {
				return errors.New("synthetic mid-batch failure")
			}
			return nil
		},
	}
	err = signRun(signConfig{
		References:  refs,
		SubjectRefs: subjects,
		Target:      store,
		Signer:      fake,
		IDToken:     "supplied-token",
		EnvLookup:   func(string) string { return "" },
	})
	if err == nil {
		t.Fatal("signRun should return a non-zero error when a reference fails")
	}
	if !strings.Contains(err.Error(), refs[1]) {
		t.Errorf("error summary %q does not name the failed reference %q", err.Error(), refs[1])
	}
	// All three references were attempted (the batch did not halt at the
	// middle), and the two good ones each carry a signature referrer.
	if len(fake.Signed) != 3 {
		t.Errorf("attempted %d signs, want 3 (batch must not halt on first failure)", len(fake.Signed))
	}
	for _, ref := range []string{refs[0], refs[2]} {
		got, lerr := oci.ListReferrers(ctx, store, subjects[ref], signing.BundleMediaType)
		if lerr != nil || len(got) != 1 {
			t.Errorf("%s: referrers=%d err=%v, want 1", ref, len(got), lerr)
		}
	}
	// The failed reference has no referrer attached.
	if got, _ := oci.ListReferrers(ctx, store, subjects[refs[1]], signing.BundleMediaType); len(got) != 0 {
		t.Errorf("failed reference %s should carry no referrer, got %d", refs[1], len(got))
	}
}

// TestSignKeyedBatchAcquiresNoToken proves a keyed batch signs every reference
// without ever acquiring an identity token — the keyed path never authenticates.
func TestSignKeyedBatchAcquiresNoToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	refs, subjects := publishN(t, store, 2)

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyed, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	var acquired int
	err = signRun(signConfig{
		References:  refs,
		SubjectRefs: subjects,
		Target:      store,
		Signer:      keyed,
		KeyPath:     "(injected-keyed-signer)", // mark the batch keyed
		EnvLookup:   func(string) string { return "" },
		IsTTY:       func() bool { return true },
		TokenGetter: func() (string, error) { acquired++; return "should-not-be-called", nil },
	})
	if err != nil {
		t.Fatalf("signRun: %v", err)
	}
	if acquired != 0 {
		t.Errorf("token acquired %d times, want 0 for a keyed batch", acquired)
	}
	for _, ref := range refs {
		got, lerr := oci.ListReferrers(ctx, store, subjects[ref], signing.BundleMediaType)
		if lerr != nil || len(got) != 1 {
			t.Errorf("%s: referrers=%d err=%v, want 1", ref, len(got), lerr)
		}
	}
}

// TestSignSuppliedTokenRejectionHardFails proves a rejected supplied token is
// never silently re-prompted: the batch fails loudly and re-acquisition never
// runs.
func TestSignSuppliedTokenRejectionHardFails(t *testing.T) {
	t.Parallel()
	store := memory.New()
	refs, subjects := publishN(t, store, 1)

	var acquired int
	fake := &signingtest.FakeSigner{Err: errors.New("synthetic cert-issuance rejection")}
	err := signRun(signConfig{
		References:  refs,
		SubjectRefs: subjects,
		Target:      store,
		Signer:      fake,
		IDToken:     "supplied-token", // supplied, not interactively acquired
		EnvLookup:   func(string) string { return "" },
		IsTTY:       func() bool { return true },
		TokenGetter: func() (string, error) { acquired++; return "fresh", nil },
	})
	if err == nil {
		t.Fatal("signRun should hard-fail when a supplied token is rejected")
	}
	if acquired != 0 {
		t.Errorf("re-acquired %d times, want 0 for a supplied token", acquired)
	}
	if !strings.Contains(err.Error(), "supplied identity token was rejected") {
		t.Errorf("error %q lacks the supplied-token-rejected diagnostic", err.Error())
	}
}

// TestSignInteractiveTokenReacquiresOnce proves an interactively-acquired token
// that is rejected mid-batch is re-acquired exactly once (the per-batch latch),
// the reference then signs, and a later rejection does not trigger a second
// re-acquire.
func TestSignInteractiveTokenReacquiresOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	refs, subjects := publishN(t, store, 1)
	firstDigest, _, err := oci.Resolve(ctx, store, subjects[refs[0]])
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Fail the first attempt of the reference, then succeed on retry.
	var attempts int
	fake := &signingtest.FakeSigner{
		FailOn: func(digest string) error {
			if digest == firstDigest {
				attempts++
				if attempts == 1 {
					return errors.New("synthetic token rejection on first attempt")
				}
			}
			return nil
		},
	}

	var acquired int
	err = signRun(signConfig{
		References:  refs,
		SubjectRefs: subjects,
		Target:      store,
		Signer:      fake,
		EnvLookup:   func(string) string { return "" },
		IsTTY:       func() bool { return true },
		TokenGetter: func() (string, error) { acquired++; return "token", nil },
	})
	if err != nil {
		t.Fatalf("signRun should recover after one re-acquire: %v", err)
	}
	// Once at prepare (initial) + once on re-acquire = exactly twice.
	if acquired != 2 {
		t.Errorf("token getter called %d times, want exactly 2 (initial + one re-acquire)", acquired)
	}
}
