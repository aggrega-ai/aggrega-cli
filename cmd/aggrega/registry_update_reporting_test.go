// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// uppercaseKeys walks every object in a decoded index record and returns the
// paths of any key beginning with an upper-case letter.
//
// It asserts the property over the WHOLE document rather than over the fields one
// change happened to touch: a type reached from this document that declares no JSON
// key names emits its Go field names, and the walk finds it wherever it sits. A
// check written against a named field would pass while an identically-shaped defect
// one field away went unreported.
func uppercaseKeys(path string, v any) []string {
	var found []string
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			at := path + "." + k
			if r := []rune(k); len(r) > 0 && unicode.IsUpper(r[0]) {
				found = append(found, at)
			}
			found = append(found, uppercaseKeys(at, child)...)
		}
	case []any:
		for i, child := range t {
			found = append(found, uppercaseKeys(fmt.Sprintf("%s[%d]", path, i), child)...)
		}
	}
	return found
}

// TestUppercaseKeysFindsAGoFieldName is the positive control for the walk above:
// it fires on a document shaped like the one the walk inspects. Without it, a walk
// that silently found nothing would be indistinguishable from a clean document.
func TestUppercaseKeysFindsAGoFieldName(t *testing.T) {
	t.Parallel()
	var doc any
	if err := json.Unmarshal([]byte(`{"uri":"x","deprecations":[{"signer":{"BundleDigest":"y"}}]}`), &doc); err != nil {
		t.Fatalf("decode control: %v", err)
	}
	got := uppercaseKeys("record", doc)
	if len(got) != 1 || got[0] != "record.deprecations[0].signer.BundleDigest" {
		t.Fatalf("control walk found %v; want exactly the planted Go field name", got)
	}
}

// TestIndexRecordEmitsOneKeyConvention proves every key the index writer emits for
// a record carrying a verified withdrawal is lowerCamel, matching the convention
// its sibling keys already use. A script reading these files sees one convention
// across the whole document rather than one per contributing Go type.
func TestIndexRecordEmitsOneKeyConvention(t *testing.T) {
	t.Parallel()
	rec := registryindex.Record{
		URI:            "oci://hub.aggrega.ai/component/core/api-server",
		Name:           "component/core/api-server",
		Version:        "2.0.0",
		Versions:       []string{"1.0.0", "2.0.0"},
		Kind:           "Component",
		ManifestDigest: "sha256:aaaa",
		ListingDigest:  "sha256:bbbb",
		Deprecations: []registryindex.VersionDeprecation{{
			Version: "1.0.0",
			Marker: deprecation.Marker{
				Severity: deprecation.SeverityRetracted,
				Reason:   "superseded by 2.0.0",
				IssuedOn: "2026-05-01T00:00:00Z",
			},
			Signer: &lockfile.VerifiedSignature{
				BundleDigest: "sha256:cccc",
				Identity:     "publisher@example.com",
				Issuer:       "https://issuer.example.com",
				Mode:         "keyless",
			},
		}},
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	var doc any
	if uerr := json.Unmarshal(data, &doc); uerr != nil {
		t.Fatalf("decode record: %v", uerr)
	}
	if got := uppercaseKeys("record", doc); len(got) > 0 {
		t.Errorf("index record emits Go field names at %v; the document is lowerCamel throughout:\n%s", got, data)
	}
}

// TestLoadHostIndexReadsGoFieldNameSigner proves a per-repo index file written
// before the signature type declared its JSON key names still loads through the
// real loader with the signer intact. A record whose signer failed to decode would
// read as unsigned, and an unsigned record is not honored — so a decode failure
// here would quietly stop excluding a version that had been withdrawn.
func TestLoadHostIndexReadsGoFieldNameSigner(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const cacheDir = "cache"
	const host = "hub.aggrega.ai"
	const repo = "component/core/api-server"

	if err := os.MkdirAll(oci.IndexBasePath(root, cacheDir, host), 0o755); err != nil {
		t.Fatalf("mkdir host index dir: %v", err)
	}
	priorShape := `{
  "uri": "oci://hub.aggrega.ai/component/core/api-server",
  "name": "component/core/api-server",
  "version": "2.0.0",
  "kind": "Component",
  "deprecations": [
    {
      "version": "1.0.0",
      "marker": {
        "severity": "retracted",
        "reason": "superseded by 2.0.0",
        "issuedOn": "2026-05-01T00:00:00Z"
      },
      "signer": {
        "BundleDigest": "sha256:cccc",
        "Identity": "publisher@example.com",
        "Issuer": "https://issuer.example.com",
        "Mode": "keyless"
      }
    }
  ]
}
`
	if err := os.WriteFile(oci.IndexRepoPath(root, cacheDir, host, repo), []byte(priorShape), 0o644); err != nil {
		t.Fatalf("write prior-shaped record: %v", err)
	}

	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	if len(loaded.Records) != 1 || len(loaded.Records[0].Deprecations) != 1 {
		t.Fatalf("loaded %+v; want one record carrying one deprecation", loaded.Records)
	}
	got := loaded.Records[0].Deprecations[0]
	if !registryindex.Honored(got) {
		t.Fatal("prior-shaped record decoded without a signer, so it would not be honored")
	}
	want := lockfile.VerifiedSignature{
		BundleDigest: "sha256:cccc",
		Identity:     "publisher@example.com",
		Issuer:       "https://issuer.example.com",
		Mode:         "keyless",
	}
	if *got.Signer != want {
		t.Errorf("prior-shaped signer decoded to %+v; want %+v", *got.Signer, want)
	}
}

// TestRegistryUpdateReportsScanOutcome proves the walk states the outcome of its
// deprecation scan on every path, so recording a marker, recording none, and never
// looking are three distinguishable outputs rather than two of them being silence.
func TestRegistryUpdateReportsScanOutcome(t *testing.T) {
	// Not parallel: mutates the process-global registryIndexTarget seam other tests read.
	const host = "hub.aggrega.ai"
	const repo = "component/core/api-server"

	for _, tc := range []struct {
		name        string
		withMarker  bool
		skipVerify  bool
		wantLines   []string
		absentLines []string
	}{
		{
			name:       "records a marker",
			withMarker: true,
			wantLines: []string{
				"deprecation: component/core/api-server:1.0.0: marker present and honored; recorded as retracted",
				"Recorded 1 deprecation markers from hub.aggrega.ai",
			},
		},
		{
			name:      "examined and recorded none",
			wantLines: []string{"Recorded 0 deprecation markers from hub.aggrega.ai"},
			absentLines: []string{
				"Deprecation markers not examined",
			},
		},
		{
			name:       "did not examine",
			withMarker: true,
			skipVerify: true,
			wantLines: []string{
				"Deprecation markers not examined for hub.aggrega.ai: signature verification is disabled for this registry",
			},
			absentLines: []string{"Recorded 0 deprecation markers"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
			target := seedComponentRepo(t, []string{"1.0.0", "2.0.0"}, meta)
			signer, content := keyedMarkerSigner(t)
			if tc.withMarker {
				attachSignedMarkerToTag(t, target.Store, "1.0.0", deprecation.Marker{
					Severity: deprecation.SeverityRetracted,
					Reason:   "superseded by 2.0.0",
					IssuedOn: "2026-05-01T00:00:00Z",
				}, signer)
			}
			if tc.skipVerify {
				content += "skip_signature_verification: true\n"
			}
			withIndexTarget(t, fakeIndexSource{
				repos:   []string{repo},
				targets: map[string]oras.ReadOnlyTarget{repo: target},
			})

			root := t.TempDir()
			rw := resolveWorkspaceWithConfig(t, root, content)
			frozen := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

			var out bytes.Buffer
			if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozen}); err != nil {
				t.Fatalf("registryUpdateRun: %v", err)
			}
			for _, want := range tc.wantLines {
				if !strings.Contains(out.String(), want) {
					t.Errorf("walk output does not report %q; got:\n%s", want, out.String())
				}
			}
			for _, absent := range tc.absentLines {
				if strings.Contains(out.String(), absent) {
					t.Errorf("walk output reports %q, which belongs to a different outcome; got:\n%s", absent, out.String())
				}
			}
		})
	}
}
