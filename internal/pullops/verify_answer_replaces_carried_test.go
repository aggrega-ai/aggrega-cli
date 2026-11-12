// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestVerifyClosureSignaturesAnswerReplacesACarriedSignature holds the signature
// pass to its role as the authority for every row it asks the registry about.
// A pull carries a recorded signature forward onto an unchanged pin before the
// pass runs, so the pass meets rows that already hold one. For a row the content
// cache served that is the point; for a row fetched from the registry this run,
// what the registry answered now is the fact, and an answer of "nothing attached"
// must clear the carried signature rather than leave the lockfile claiming a
// verification the registry no longer backs.
//
// Each case differs from its neighbour in one input: whether the row was fetched
// from the registry this run.
func TestVerifyClosureSignaturesAnswerReplacesACarriedSignature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	artifact := []byte("\x00asm\x01\x00\x00\x00 unsigned")
	contentDg := digest.ContentDigest(artifact)
	manifestDg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, artifact, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	const uri = "open.example.com/plugin/linter/unsigned"

	cases := []struct {
		name          string
		path          string
		wantSignature bool
	}{
		{"fetched from the registry, which now attaches no signature: the carried signature is cleared", closure.ResolutionPathRegistry, false},
		{"served from the content cache, whose carried signature the build accepts and the pass does not ask about: it stays", closure.ResolutionPathCache, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			// The carried signature names a bundle that is in the cache and was made for
			// the row's own manifest, so a build accepts it: the cache-served row carries
			// nothing that would have the pass ask about it.
			sig := lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried-from-the-prior-lockfile",
				BundleDigest: cacheBundleMadeFor(t, root, manifestDg)}
			cl := closure.Closure{
				Plugins: []closure.ResolvedRef{{URI: uri, Digest: contentDg, ManifestDigest: manifestDg, ResolutionPath: tc.path}},
			}
			lf := &lockfile.Lockfile{
				Plugins: []lockfile.Plugin{{URI: uri, Digest: contentDg, ManifestDigest: manifestDg, Signature: &sig}},
			}
			// No policy for the host: verify-if-present, so an absent signature is not
			// a halt and the pass returns cleanly either way.
			if err := VerifyClosureSignatures(ctx, lf, cl, workspaceconfig.Config{}, root, ".aggrega/cache", nil, pluginTargetFor(store, "1.0.0")); err != nil {
				t.Fatalf("VerifyClosureSignatures: %v", err)
			}
			got := lf.Plugins[0].Signature
			if tc.wantSignature && got == nil {
				t.Fatal("the carried signature on a row the pass did not ask about was cleared")
			}
			if !tc.wantSignature && got != nil {
				t.Fatalf("the row keeps a carried signature (%+v) the registry, asked this run, no longer backs", *got)
			}
		})
	}
}
