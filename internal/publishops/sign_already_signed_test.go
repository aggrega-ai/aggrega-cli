// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// attachKeyedWith attaches to version 1.0.0 in store a keyed signature made with
// the private key at keyPath.
func attachKeyedWith(t *testing.T, store *memory.Store, keyPath string) {
	t.Helper()
	ctx := context.Background()
	pemBytes, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(pemBytes, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	digest, _, err := oci.Resolve(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	bundle, artifactType, err := signer.Sign(ctx, digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, "1.0.0", artifactType, bundle); err != nil {
		t.Fatalf("attach keyed signature: %v", err)
	}
}

// alreadySignedRow is one state a published version can be in when a signing
// batch reaches it, and what the batch must do with it. Each row carries one fault
// against the control.
type alreadySignedRow struct {
	name   string
	defect string
	// ws is the workspace the batch signs in, which holds the destination's policy.
	ws func(k completionKeys) string
	// land attaches to version 1.0.0 what the row needs, and returns the line the
	// batch must write for a reference it finds already signed, or "" where it must
	// write none.
	land func(t *testing.T, k completionKeys, store *memory.Store, digest, ref string) string
	// wrap, when set, is the target the batch reaches instead of store.
	wrap func(store *memory.Store) oras.Target
	// wantAttached is how many signatures the batch must attach, and so how many
	// times it must ask its signer to sign.
	wantAttached int
	// wantErr, when set, is a fragment the batch's error must carry; the reference is
	// then not reported signed.
	wantErr string
}

// TestASigningBatchLeavesAReferenceItFindsAlreadySigned proves, through the
// signing batch, that a reference already carrying a signature its registry's
// trust policy verifies is reported signed with nothing attached and its signer
// never asked, naming who signed it; that a reference carrying none, or only one
// that policy does not admit, is signed as before; that a registry pinning no trust
// anchor is signed as before whatever the reference carries; and that a reference
// whose signatures cannot be listed is reported failed with nothing signed. The
// first row is the control for the skip; every other row names the defect it
// catches.
func TestASigningBatchLeavesAReferenceItFindsAlreadySigned(t *testing.T) {
	t.Parallel()
	k := newCompletionKeys(t)
	keyed := func(k completionKeys) string { return keyedWS(k.pubPEM) }

	rows := []alreadySignedRow{
		{
			name:   "control: a keyed signature the destination's policy verifies",
			defect: "a reference already signed is signed again, adding a signature and a log entry on every run",
			ws:     keyed,
			land: func(t *testing.T, k completionKeys, store *memory.Store, digest, ref string) string {
				attachKeyedWith(t, store, k.keyPath)
				policy, _ := resolveIn(t, keyedWS(k.pubPEM), nil).Config().RegistryTrustPolicy(datedRegistry)
				signer := fmt.Sprintf("keyed:sha256:%x", sha256.Sum256([]byte(policy.PublicKey)))
				return fmt.Sprintf("Already signed: %s (%s) by %s; nothing attached", ref, digest, signer)
			},
		},
		{
			name:   "a keyless signature the destination's policy verifies",
			defect: "a keyless signature already attached is not found, or the line does not say who signed it and when",
			ws:     func(k completionKeys) string { return k.keylessWS() },
			land: func(t *testing.T, k completionKeys, store *memory.Store, digest, ref string) string {
				sig, err := k.authority.Sign(completionIdentity, completionIssuer, digest, time.Now().Add(2*time.Minute))
				if err != nil {
					t.Fatalf("keyless Sign: %v", err)
				}
				if _, err := oci.AttachReferrer(context.Background(), store, "1.0.0", signing.BundleMediaType, sig.Bundle); err != nil {
					t.Fatalf("attach keyless signature: %v", err)
				}
				return fmt.Sprintf("Already signed: %s (%s) by %s, logged %s; nothing attached",
					ref, digest, completionIdentity, oci.FormatPublishedAt(sig.LoggedAt))
			},
		},
		{
			name:         "no signature at all",
			defect:       "an unsigned reference is not signed",
			ws:           keyed,
			land:         func(*testing.T, completionKeys, *memory.Store, string, string) string { return "" },
			wantAttached: 1,
		},
		{
			name:   "only a signature the destination's policy does not admit",
			defect: "a signature the policy refuses is taken as the reference's own, so the reference stays refused by every verifying reader",
			ws:     keyed,
			land: func(t *testing.T, k completionKeys, store *memory.Store, _, _ string) string {
				attachKeyedWith(t, store, k.foreignKey)
				return ""
			},
			wantAttached: 1,
		},
		{
			name:   "a registry pinning no trust anchor, carrying a signature",
			defect: "a registry with nothing to verify against has its references left unsigned",
			ws:     func(k completionKeys) string { return k.unanchoredWS },
			land: func(t *testing.T, k completionKeys, store *memory.Store, _, _ string) string {
				attachKeyedWith(t, store, k.keyPath)
				return ""
			},
			wantAttached: 1,
		},
		{
			name:   "a signature listing that cannot be completed",
			defect: "an unanswered listing is read as an unsigned reference and a signature is attached on it",
			ws:     keyed,
			land: func(t *testing.T, k completionKeys, store *memory.Store, _, _ string) string {
				attachKeyedWith(t, store, k.keyPath)
				return ""
			},
			wrap:    func(store *memory.Store) oras.Target { return &failsListing{Store: store} },
			wantErr: "whether it already carries a signature that verifies could not be read, so nothing was signed",
		},
	}

	// The rows share one in-memory authority and run one after another, so none
	// depends on that authority being safe to sign through from two goroutines.
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()
			store := memory.New()
			digest, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("# a published version\n"), nil)
			if err != nil {
				t.Fatalf("Push: %v", err)
			}
			ref := datedRegistry + "/component/core/api-server:1.0.0"
			wantLine := row.land(t, k, store, digest, ref)
			before := countReferrers(t, store, "1.0.0", signing.BundleMediaType)

			var target oras.Target = store
			if row.wrap != nil {
				target = row.wrap(store)
			}
			signer := &signingtest.FakeSigner{}
			var out bytes.Buffer
			res, err := Sign(ctx, SignConfig{
				Workspace: resolveIn(t, row.ws(k), nil), References: []string{ref},
				SubjectRefs: map[string]string{ref: "1.0.0"}, Target: target, Signer: signer,
				IDToken: "supplied-token", EnvLookup: func(string) string { return "" }, Out: &out,
			})

			attached := countReferrers(t, store, "1.0.0", signing.BundleMediaType) - before
			if attached != row.wantAttached || len(signer.Signed) != row.wantAttached {
				t.Fatalf("attached %d signatures and asked the signer %d times; want %d of each (defect: %s)\n%s",
					attached, len(signer.Signed), row.wantAttached, row.defect, out.String())
			}

			if row.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), row.wantErr) || !strings.Contains(err.Error(), ref) {
					t.Fatalf("error = %v; want one naming %s and carrying %q (defect: %s)", err, ref, row.wantErr, row.defect)
				}
				if len(res.Signed) != 0 {
					t.Errorf("a failed batch reported %+v as signed", res.Signed)
				}
				return
			}
			if err != nil {
				t.Fatalf("Sign: %v (defect: %s)\n%s", err, row.defect, out.String())
			}
			wantSigned := SignedArtifact{Reference: ref, Digest: digest, Attached: row.wantAttached > 0}
			if len(res.Signed) != 1 || res.Signed[0] != wantSigned {
				t.Errorf("the batch reported %+v; want %+v (defect: %s)", res.Signed, wantSigned, row.defect)
			}
			// The reach is read off what the batch attached: a reference found already
			// signed attached nothing, so the batch reached no further than this machine.
			if reach := res.Reach(); reach.BeyondThisMachine != (row.wantAttached > 0) ||
				len(reach.Registries) != 1 || reach.Registries[0] != datedRegistry {
				t.Errorf("Reach() = %+v; want registries [%s] and beyond this machine %v (defect: %s)",
					reach, datedRegistry, row.wantAttached > 0, row.defect)
			}

			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
			attachedLine := fmt.Sprintf("Signature attached: %s (%s)", ref, digest)
			for _, want := range []struct {
				line    string
				present bool
			}{{wantLine, wantLine != ""}, {attachedLine, row.wantAttached > 0}} {
				if want.line == "" {
					continue
				}
				found := false
				for _, l := range lines {
					if l == want.line {
						found = true
					}
				}
				if found != want.present {
					t.Errorf("the account carries %q = %v; want %v (defect: %s)\n%s", want.line, found, want.present, row.defect, out.String())
				}
			}
			if wantLine == "" && strings.Contains(out.String(), "Already signed:") {
				t.Errorf("the account reports the reference already signed (defect: %s)\n%s", row.defect, out.String())
			}
		})
	}
}
