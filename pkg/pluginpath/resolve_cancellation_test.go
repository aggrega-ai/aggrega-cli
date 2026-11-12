// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginpath

import (
	"context"
	"errors"
	"testing"
)

// unresolvableURI names a plugin no tier can serve: it is not embedded in the
// binary, it is not a filesystem path, and the resolvers below are built with no
// colocated directories and no content cache. So a resolution of it walks the whole
// cascade and exhausts it, which is the outcome the withdrawn case must NOT reach.
const unresolvableURI = "registry.example/parking/plugin:1.0.0"

// TestAPluginResolutionWhoseContextHasAlreadyEndedConsultsNoTier covers a caller
// that withdrew before resolution began: it is answered with the withdrawal rather
// than walking the cascade first. The live-context control must fail DIFFERENTLY —
// it walks every tier and reports exhaustion — so a run where both rows report the
// same thing cannot pass.
//
// A planted defect makes this fail: remove the entry check from ResolveContext
// and the withdrawn row reports the tier-exhaustion refusal, matching the control.
func TestAPluginResolutionWhoseContextHasAlreadyEndedConsultsNoTier(t *testing.T) {
	t.Parallel()
	r := NewResolver(ResolutionContext{}, PinPostureNone, PinMaps{})

	withdrawn, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.ResolveContext(withdrawn, unresolvableURI)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a withdrawn caller got %v; want an error wrapping context.Canceled", err)
	}
	if errors.Is(err, ErrPluginArtifactNotFound()) {
		t.Errorf("a withdrawn caller was answered by the cascade (%v); the withdrawal should "+
			"have been reported before any tier was consulted", err)
	}

	_, liveErr := r.ResolveContext(context.Background(), unresolvableURI)
	if !errors.Is(liveErr, ErrPluginArtifactNotFound()) {
		t.Fatalf("the live-context control got %v; want the tier-exhaustion refusal — without a "+
			"control that fails differently, the assertion above proves nothing", liveErr)
	}
}

// TestTheSeamFacingResolveStillWalksTheCascade pins that the seam-facing form walks
// the same cascade as the context-taking one. Lookup binds Resolve, so a regression
// here would change every socket's resolution while the withdrawal test above went
// on passing.
func TestTheSeamFacingResolveStillWalksTheCascade(t *testing.T) {
	t.Parallel()
	r := NewResolver(ResolutionContext{}, PinPostureNone, PinMaps{})

	_, err := r.Resolve(unresolvableURI)
	if !errors.Is(err, ErrPluginArtifactNotFound()) {
		t.Errorf("Resolve(%q) returned %v; want the tier-exhaustion refusal", unresolvableURI, err)
	}

	// A Resolver bound as a Lookup is the seam every socket uses; it must still
	// satisfy the type and still resolve.
	var seam Lookup = r.Resolve
	if _, seamErr := seam(unresolvableURI); !errors.Is(seamErr, ErrPluginArtifactNotFound()) {
		t.Errorf("the seam-bound method value returned %v; want the tier-exhaustion refusal", seamErr)
	}
}
