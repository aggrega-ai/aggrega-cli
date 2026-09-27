// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// signTag signs the manifest digest tag resolves to and attaches the bundle as a
// signature referrer, through the same sign and attach primitives a publication
// uses, so the index build reads back a signature of the shape the product writes.
func signTag(t *testing.T, target *memTagTarget, tag string, signer signing.Signer) {
	t.Helper()
	ctx := context.Background()
	digest, exists, err := oci.Resolve(ctx, target.Store, tag)
	if err != nil || !exists {
		t.Fatalf("resolve %s: exists=%v err=%v", tag, exists, err)
	}
	bundle, artifactType, err := signer.Sign(ctx, digest)
	if err != nil {
		t.Fatalf("sign %s: %v", tag, err)
	}
	if _, err := oci.AttachReferrer(ctx, target.Store, tag, artifactType, bundle); err != nil {
		t.Fatalf("attach signature to %s: %v", tag, err)
	}
}

// TestRegistryUpdateRecordsEachPublishedVersion proves the index build records,
// for every version it lists and in the same order, the manifest digest the tag
// resolves to, and names a signer only for a signature that verified against the
// host's own trust policy. Each row changes one thing about the policy against
// one fixed registry state, in which 1.0.0 is signed and 1.1.0 is not, so a row
// failing names the condition that broke it.
func TestRegistryUpdateRecordsEachPublishedVersion(t *testing.T) {
	// Not parallel: mutates the process-global index-target seam other tests read.
	const host = "hub.aggrega.ai"
	const repo = "component/core/api-server"

	signer, keyedConfig := keyedMarkerSigner(t)
	_, otherKeyConfig := keyedMarkerSigner(t)
	noAnchorConfig := updateTestConfig
	skippedConfig := keyedConfig + "    skip_signature_verification: true\n"

	cases := []struct {
		name string
		// config is the workspace configuration the walk runs under.
		config string
		// signed10 reports whether 1.0.0 is expected to carry a verified signer.
		signed10 bool
	}{
		{name: "pinned key verifies the signature it made", config: keyedConfig, signed10: true},
		{name: "a different pinned key names no signer", config: otherKeyConfig, signed10: false},
		{name: "a policy pinning no anchor judges nothing", config: noAnchorConfig, signed10: false},
		{name: "verification switched off judges nothing", config: skippedConfig, signed10: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
			target := seedComponentRepo(t, []string{"1.0.0", "1.1.0"}, meta)
			signTag(t, target, "1.0.0", signer)
			withIndexTarget(t, fakeIndexSource{repos: []string{repo}, targets: map[string]oras.ReadOnlyTarget{repo: target}})

			root := t.TempDir()
			rw := resolveWorkspaceWithConfig(t, root, tc.config)
			var out bytes.Buffer
			if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozenClock()}); err != nil {
				t.Fatalf("registryUpdateRun: %v\n%s", err, out.String())
			}
			loaded, err := registryindex.LoadHost(root, rw.Config().ResolvedArtifactCacheDir(), host)
			if err != nil {
				t.Fatalf("LoadHost: %v", err)
			}
			if len(loaded.Records) != 1 {
				t.Fatalf("records = %d; want 1\n%s", len(loaded.Records), out.String())
			}
			rec := loaded.Records[0]
			if !rec.CoversVersions() {
				t.Fatalf("record does not cover its versions: versions=%v published=%+v", rec.Versions, rec.Published)
			}
			for i, v := range []string{"1.0.0", "1.1.0"} {
				want, _, err := oci.Resolve(t.Context(), target.Store, v)
				if err != nil {
					t.Fatalf("resolve %s: %v", v, err)
				}
				if rec.Published[i].Version != v || rec.Published[i].ManifestDigest != want {
					t.Errorf("published[%d] = %+v; want version %s at %s", i, rec.Published[i], v, want)
				}
			}
			if rec.Published[1].ManifestDigest != rec.ManifestDigest {
				t.Errorf("latest version's published digest %s differs from the record's manifest digest %s",
					rec.Published[1].ManifestDigest, rec.ManifestDigest)
			}
			if rec.Published[1].Signer != nil {
				t.Errorf("unsigned 1.1.0 carries signer %+v; want none", rec.Published[1].Signer)
			}
			got := rec.Published[0].Signer
			if !tc.signed10 {
				if got != nil {
					t.Errorf("1.0.0 carries signer %+v; want none under this policy", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("signed 1.0.0 carries no signer under the key that signed it\n%s", out.String())
			}
			if got.Mode != "keyed" || !strings.HasPrefix(got.Identity, "keyed:sha256:") || !strings.HasPrefix(got.BundleDigest, "sha256:") {
				t.Errorf("1.0.0 signer = %+v; want a keyed signer named by its key fingerprint, with the bundle's digest", got)
			}
		})
	}
}

// TestRegistryUpdateRereadsARecordWithoutPublishedVersions proves a refresh does
// not reuse a prior record that gives no digest for a version it lists — the
// shape a record written before per-version digests were recorded has — even when
// the tag set and the latest digest are unchanged, so every record a refresh
// leaves on disk addresses each of its versions.
func TestRegistryUpdateRereadsARecordWithoutPublishedVersions(t *testing.T) {
	// Not parallel: mutates the process-global index-target seam other tests read.
	const host = "hub.aggrega.ai"
	const repo = "component/core/api-server"
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	target := seedComponentRepo(t, []string{"1.0.0", "1.1.0"}, meta)
	withIndexTarget(t, fakeIndexSource{repos: []string{repo}, targets: map[string]oras.ReadOnlyTarget{repo: target}})

	root := t.TempDir()
	rw := resolveWorkspaceWithConfig(t, root, updateTestConfig)
	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	var out bytes.Buffer
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozenClock()}); err != nil {
		t.Fatalf("registryUpdateRun (build): %v", err)
	}
	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil || len(loaded.Records) != 1 {
		t.Fatalf("LoadHost after build: records=%d err=%v", len(loaded.Records), err)
	}

	// Rewrite the prior index through the product's own writer with the per-version
	// entries stripped, leaving every validator the reuse path compares intact.
	stripped := loaded.Records[0]
	stripped.Published = nil
	if err := registryops.WriteHostIndex(root, cacheDir, registryops.HostIndex{Host: host, Records: []registryindex.Record{stripped}}, frozenClock()()); err != nil {
		t.Fatalf("WriteHostIndex: %v", err)
	}

	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozenClock()}); err != nil {
		t.Fatalf("registryUpdateRun (refresh): %v", err)
	}
	reloaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil || len(reloaded.Records) != 1 {
		t.Fatalf("LoadHost after refresh: records=%d err=%v", len(reloaded.Records), err)
	}
	if !reloaded.Records[0].CoversVersions() {
		t.Errorf("refresh reused a record that gives no digest for its versions: %+v", reloaded.Records[0])
	}
}
