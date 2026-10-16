// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestAModuleIsHeldToTheDigestItMustMatch holds the one comparison of a resolved
// module's bytes against a digest they must match, from both of its callers.
//
// Through HoldToDigest, bytes whose content digest is the one named pass, and any
// other bytes are refused under the integrity condition, the refusal naming the
// reference, the digest the bytes carry and what they were held to, followed by
// the digest they had to carry. Through the cascade's own pin check, the refusal
// of a module that does not match its lockfile pin is still, word for word, the
// sentence the cascade has always written — the comparison moved into one home
// without the cascade's wording moving with it.
func TestAModuleIsHeldToTheDigestItMustMatch(t *testing.T) {
	const uri = "importer/held:1.0.0"
	accepted := []byte("the-accepted-bytes")
	acceptedDigest := digest.ContentDigest(accepted)
	other := []byte("other-bytes")
	otherDigest := digest.ContentDigest(other)
	const heldTo = "the digest a release publishes"

	if err := HoldToDigest(ResolvedPlugin{URI: uri, Tier: TierCache, Bytes: accepted}, acceptedDigest, heldTo); err != nil {
		t.Fatalf("the module the digest names was refused: %v", err)
	}

	err := HoldToDigest(ResolvedPlugin{URI: uri, Tier: TierCache, Bytes: other}, acceptedDigest, heldTo)
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("a module the digest does not name was not refused under the integrity condition: %v", err)
	}
	for _, want := range []string{
		fmt.Sprintf("plugin %q", uri),
		"content digest " + otherDigest,
		"does not match " + heldTo + " " + acceptedDigest,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not carry %q", err.Error(), want)
		}
	}

	colocatedDir := writeColocated(t, filepath.Join(t.TempDir(), "colocated"), uri, other)
	_, err = Resolver{
		colocatedDirs: map[string]ColocatedDir{uri: {Dir: colocatedDir}},
		pins:          map[string]string{uri: acceptedDigest},
	}.Resolve(uri)
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("a module that does not match its lockfile pin was not refused under the integrity condition: %v", err)
	}
	want := fmt.Sprintf("plugin %q resolved from %s: content digest %s does not match lockfile pin %s: ",
		uri, TierColocated.Source(), otherDigest, acceptedDigest)
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the lockfile pin refusal reads %q; want it to open with %q", err.Error(), want)
	}
}
