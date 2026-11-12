// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestCarryForwardVerifiedFactsAcrossAnUnchangedPin pins what a re-pull carries
// from the prior lockfile onto the rows it writes: both recorded facts — the
// verified signature and the verified deprecation marker — and only across an
// unchanged pin, onto a row that records none of its own.
//
// Each row varies one input from the carrying case: the content digest, the
// manifest digest, a side that records no manifest digest, a fact already on the
// new row, the section the row sits in, and the absence of a prior lockfile.
func TestCarryForwardVerifiedFactsAcrossAnUnchangedPin(t *testing.T) {
	t.Parallel()
	sig := &lockfile.VerifiedSignature{Mode: "keyed", Identity: "publisher", BundleDigest: "sha256:bundle"}
	dep := &lockfile.DeprecationRecord{Message: "use 2.0.0", Severity: "warning"}
	fresh := &lockfile.VerifiedSignature{Mode: "keyed", Identity: "found-this-run", BundleDigest: "sha256:fresh"}

	const uri = "pipeline-step/patch-server:1.0.0"
	entity := func(digest, manifestDigest string, s *lockfile.VerifiedSignature, d *lockfile.DeprecationRecord) *lockfile.Lockfile {
		return &lockfile.Lockfile{Entities: []lockfile.Entity{{URI: uri, Digest: digest, ManifestDigest: manifestDigest, Signature: s, Deprecation: d}}}
	}

	cases := []struct {
		name    string
		prior   *lockfile.Lockfile
		now     *lockfile.Lockfile
		wantSig *lockfile.VerifiedSignature
		wantDep *lockfile.DeprecationRecord
	}{
		{"unchanged pin carries both facts", entity("sha256:c", "sha256:m", sig, dep), entity("sha256:c", "sha256:m", nil, nil), sig, dep},
		{"a moved content digest carries nothing", entity("sha256:c", "sha256:m", sig, dep), entity("sha256:c2", "sha256:m", nil, nil), nil, nil},
		{"a moved manifest digest carries nothing", entity("sha256:c", "sha256:m", sig, dep), entity("sha256:c", "sha256:m2", nil, nil), nil, nil},
		{"a prior row that recorded no manifest digest carries on the content digest", entity("sha256:c", "", nil, dep), entity("sha256:c", "sha256:m", nil, nil), nil, dep},
		{"a new row that records no manifest digest carries on the content digest", entity("sha256:c", "sha256:m", sig, dep), entity("sha256:c", "", nil, nil), sig, dep},
		{"a fact already on the new row is kept", entity("sha256:c", "sha256:m", sig, dep), entity("sha256:c", "sha256:m", fresh, nil), fresh, dep},
		{"no prior lockfile carries nothing", nil, entity("sha256:c", "sha256:m", nil, nil), nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			carryForwardVerifiedFacts(tc.now, tc.prior)
			got := tc.now.Entities[0]
			if got.Signature != tc.wantSig {
				t.Errorf("signature = %+v; want %+v", got.Signature, tc.wantSig)
			}
			if got.Deprecation != tc.wantDep {
				t.Errorf("deprecation = %+v; want %+v", got.Deprecation, tc.wantDep)
			}
		})
	}

	// Every section that can record a fact carries it: a row in each section of the
	// prior lockfile, and the same unchanged pin in each section of the new one.
	t.Run("every fact-bearing section carries", func(t *testing.T) {
		t.Parallel()
		build := func(s *lockfile.VerifiedSignature, d *lockfile.DeprecationRecord) *lockfile.Lockfile {
			return &lockfile.Lockfile{
				Entities:          []lockfile.Entity{{URI: "e", Digest: "sha256:e", ManifestDigest: "sha256:me", Signature: s, Deprecation: d}},
				SchemaLibraries:   []lockfile.SchemaLibrary{{URI: "s", Digest: "sha256:s", ManifestDigest: "sha256:ms", Signature: s, Deprecation: d}},
				MappingLibraries:  []lockfile.MappingLibrary{{URI: "m", Digest: "sha256:m", ManifestDigest: "sha256:mm", Signature: s, Deprecation: d}},
				WorkloadLibraries: []lockfile.WorkloadLibrary{{URI: "w", Digest: "sha256:w", ManifestDigest: "sha256:mw", Signature: s, Deprecation: d}},
				ArtifactLibraries: []lockfile.ArtifactLibrary{{URI: "a", Digest: "sha256:a", ManifestDigest: "sha256:ma", Signature: s, Deprecation: d}},
				Plugins:           []lockfile.Plugin{{URI: "p", Digest: "sha256:p", ManifestDigest: "sha256:mp", Signature: s, Deprecation: d}},
			}
		}
		now := build(nil, nil)
		carryForwardVerifiedFacts(now, build(sig, dep))
		rows := now.VerifiedFactRows()
		if len(rows) != 6 {
			t.Fatalf("the fixture builds one row per fact-bearing section; got %d rows", len(rows))
		}
		for _, row := range rows {
			if row.Signature() != sig || row.Deprecation() != dep {
				t.Errorf("row %q: signature=%+v deprecation=%+v; want both carried", row.URI, row.Signature(), row.Deprecation())
			}
		}
	})
}
