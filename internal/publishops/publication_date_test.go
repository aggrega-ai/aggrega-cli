// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// datedComponentBody is a Component the publication path reads and bundles
// unchanged.
const datedComponentBody = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`

const datedRegistry = "reg.example.test"

// writeDatedComponent writes the Component alone in a version directory of its
// own, as a publication requires.
func writeDatedComponent(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "1.0.0", "entity.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(datedComponentBody), 0o644); err != nil {
		t.Fatalf("write entity: %v", err)
	}
	return path
}

// TestAPublicationStatesItsDateUnlessItsSignatureCarriesOne proves where a
// publication writes a published-at referrer and where it does not, and that the
// referrer moves neither the tag nor the digest the publication reported. Each row
// names the defect it catches.
func TestAPublicationStatesItsDateUnlessItsSignatureCarriesOne(t *testing.T) {
	t.Parallel()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	keyless, err := authority.Sign("publisher@example.test", "https://issuer.example.test",
		"sha256:1111111111111111111111111111111111111111111111111111111111111111", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("keyless Sign: %v", err)
	}
	stated := time.Date(2026, 9, 27, 18, 3, 52, 0, time.UTC)
	yes, no := true, false

	cases := []struct {
		name   string
		defect string
		// publish runs one publication into store and returns the digest it reported.
		publish func(t *testing.T, store *memory.Store, out *bytes.Buffer) string
		// tag is the version tag the publication lands at.
		tag       string
		wantDated bool
	}{
		{
			name: "an unsigned entity publication", defect: "an unsigned publication states no date", tag: "1.0.0", wantDated: true,
			publish: func(t *testing.T, store *memory.Store, out *bytes.Buffer) string {
				res, err := Push(context.Background(), PushConfig{
					EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &no,
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: store,
					Now: func() time.Time { return stated.Add(400 * time.Millisecond) }, Out: out,
				})
				if err != nil {
					t.Fatalf("Push: %v", err)
				}
				return res.Digest
			},
		},
		{
			name: "a keyed-signed entity publication", defect: "a keyed signature is taken to carry a time", tag: "1.0.0", wantDated: true,
			publish: func(t *testing.T, store *memory.Store, out *bytes.Buffer) string {
				res, err := Push(context.Background(), PushConfig{
					EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &yes, KeyPath: heldKeyPEM(t),
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: store,
					Now: func() time.Time { return stated }, Out: out,
				})
				if err != nil {
					t.Fatalf("Push: %v", err)
				}
				return res.Digest
			},
		},
		{
			name: "a signature carrying a transparency-log time", defect: "a keyless publication also states its own clock", tag: "1.0.0", wantDated: false,
			publish: func(t *testing.T, store *memory.Store, out *bytes.Buffer) string {
				res, err := Push(context.Background(), PushConfig{
					EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &yes,
					Signer:    &signingtest.FakeSigner{Bundle: keyless.Bundle},
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: store,
					Now: func() time.Time { return stated }, Out: out,
				})
				if err != nil {
					t.Fatalf("Push: %v", err)
				}
				return res.Digest
			},
		},
		{
			name: "a signature that is not a readable bundle", defect: "an unreadable signature is taken to carry a time", tag: "1.0.0", wantDated: true,
			publish: func(t *testing.T, store *memory.Store, out *bytes.Buffer) string {
				res, err := Push(context.Background(), PushConfig{
					EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &yes,
					Signer:    &signingtest.FakeSigner{},
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: store,
					Now: func() time.Time { return stated }, Out: out,
				})
				if err != nil {
					t.Fatalf("Push: %v", err)
				}
				return res.Digest
			},
		},
		{
			name: "a data artifact publication", defect: "a data artifact states no date", tag: "2.0.0", wantDated: true,
			publish: func(t *testing.T, store *memory.Store, out *bytes.Buffer) string {
				file := filepath.Join(t.TempDir(), "bundle.bin")
				if err := os.WriteFile(file, []byte("artifact bytes"), 0o644); err != nil {
					t.Fatalf("write artifact: %v", err)
				}
				res, err := PushArtifact(context.Background(), PushArtifactConfig{
					FilePath: file, Name: "demo", Tag: "2.0.0", Registry: datedRegistry,
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: store,
					Now: func() time.Time { return stated }, Out: out,
				})
				if err != nil {
					t.Fatalf("PushArtifact: %v", err)
				}
				return res.Digest
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := memory.New()
			var out bytes.Buffer
			reported := tc.publish(t, store, &out)

			at, found, err := oci.ReadPublishedAt(ctx, store, tc.tag)
			if err != nil {
				t.Fatalf("ReadPublishedAt: %v", err)
			}
			if found != tc.wantDated {
				t.Fatalf("a published-at referrer found = %v; want %v (defect: %s)\n%s", found, tc.wantDated, tc.defect, out.String())
			}
			announced := strings.Contains(out.String(), "Publication date attached: ")
			if announced != tc.wantDated {
				t.Errorf("the account announces a date = %v; want %v\n%s", announced, tc.wantDated, out.String())
			}
			if tc.wantDated {
				if !at.Equal(stated) {
					t.Errorf("stated date = %v; want the publisher's clock %v, to the second", at, stated)
				}
				if !strings.Contains(out.String(), "(2026-09-27T18:03:52Z)") {
					t.Errorf("the account does not carry the stated date\n%s", out.String())
				}
			}
			tagged, exists, err := oci.Resolve(ctx, store, tc.tag)
			if err != nil || !exists {
				t.Fatalf("Resolve %s: exists=%v err=%v", tc.tag, exists, err)
			}
			if tagged != reported {
				t.Fatalf("the tag resolves to %s, not the digest the publication reported, %s: the date moved the manifest", tagged, reported)
			}
		})
	}
}

// TestARepublicationOfTheSameContentStatesNoSecondDate proves a publication that
// finds its bytes already there writes nothing, so the first statement stands.
func TestARepublicationOfTheSameContentStatesNoSecondDate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	no := false
	first := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC)
	for i, at := range []time.Time{first, first.Add(time.Hour)} {
		var out bytes.Buffer
		res, err := Push(ctx, PushConfig{
			EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &no,
			Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: store,
			Now: func() time.Time { return at }, Out: &out,
		})
		if err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
		if i == 1 && !res.AlreadyPublished {
			t.Fatalf("the second publication of the same content was not reported as already published\n%s", out.String())
		}
	}
	refs, err := oci.ListReferrers(ctx, store, "1.0.0", oci.MediaTypePublishedAt)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("published-at referrers = %d; want 1 (defect: a re-publication states a second date)", len(refs))
	}
	at, _, err := oci.ReadPublishedAt(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("ReadPublishedAt: %v", err)
	}
	if !at.Equal(first) {
		t.Errorf("date = %v; want the first publication's %v", at, first)
	}
}
