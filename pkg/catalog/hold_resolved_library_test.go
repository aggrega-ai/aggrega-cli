// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// heldSchemaLibraryYAML and heldMappingLibraryYAML are the libraries a dependency
// resolution has just resolved from a registry: present in the cache, named by no
// pin yet.
const (
	heldSchemaLibraryYAML = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Held
  version: 1.0.0
spec:
  schemas:
    Settings:
      level: { type: string, default: "held" }
`
	heldMappingLibraryYAML = `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: HeldMappings
  version: 1.0.0
spec:
  mappings:
    Values: 'level: {{ .values.level }}'
`
)

// TestHoldResolvedLibraryReadsTheLibraryAtItsResolution drives the view's hold of a
// library a resolution has just resolved, which is what lets the discovery read the
// library's own body. Each row resolves one library whose cache slot was written by
// the resolution's own fetch, holds it, and reads it back through the per-kind read
// the discovery uses.
//
// A library resolved at the digest its slot holds becomes readable, on the schema
// axis and on a template axis alike. A resolution whose digest the slot does not
// hold is not folded, as it would not be under a lockfile's pin. A pin the view
// already holds for the library governs and is not replaced by the resolution's:
// held to other bytes, the library stays unreadable.
func TestHoldResolvedLibraryReadsTheLibraryAtItsResolution(t *testing.T) {
	t.Parallel()
	otherBytes := "sha256:" + strings.Repeat("0", 64)
	rows := []struct {
		name      string
		axis      string
		uri       string
		slotURI   string
		mediaType string
		body      string
		// digest replaces the resolution's digest where set.
		digest string
		// pinned is a pin the view already holds for the library, held to other bytes.
		pinned bool
		want   bool
	}{
		{name: "a schema library at the digest its slot holds", axis: "SchemaLibraries", uri: "Held", slotURI: "Held:1.0.0",
			mediaType: oci.MediaTypeSchemaLibrary, body: heldSchemaLibraryYAML, want: true},
		{name: "a mapping library at the digest its slot holds", axis: "MappingLibraries", uri: "HeldMappings", slotURI: "HeldMappings:1.0.0",
			mediaType: oci.MediaTypeMappingLibrary, body: heldMappingLibraryYAML, want: true},
		{name: "a schema library resolved at a digest its slot does not hold", axis: "SchemaLibraries", uri: "Held", slotURI: "Held:1.0.0",
			mediaType: oci.MediaTypeSchemaLibrary, body: heldSchemaLibraryYAML, digest: otherBytes, want: false},
		{name: "a schema library a pin already names at other bytes", axis: "SchemaLibraries", uri: "Held", slotURI: "Held:1.0.0",
			mediaType: oci.MediaTypeSchemaLibrary, body: heldSchemaLibraryYAML, pinned: true, want: false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			pin := writePinnedSlot(t, ws, cacheUnderCatalogDir, row.slotURI, row.mediaType, []byte(row.body))
			axis := closure.LibraryAxisNamed(row.axis)
			resolved := closure.ResolvedLibrary{
				URI:             row.uri,
				ResolvedVersion: pin.ResolvedVersion,
				Digest:          pin.Digest,
				ManifestDigest:  pin.ManifestDigest,
				ResolutionPath:  closure.ResolutionPathRegistry,
			}
			if row.digest != "" {
				resolved.Digest = row.digest
			}

			subject := NewLazyLookup(ws, cacheUnderCatalogDir, nil, nil)
			if row.pinned {
				subject.SetPins(Pins{{Kind: kindOfMediaType(t, row.mediaType), URI: row.uri}: {ResolvedVersion: pin.ResolvedVersion, Digest: otherBytes}})
			}
			if _, held := subject.LibraryName(axis, row.uri); held {
				t.Fatalf("the view holds %s before the hold, so the row proves nothing about the hold", row.uri)
			}
			subject.HoldResolvedLibrary(axis, resolved)
			name, held := subject.LibraryName(axis, row.uri)
			if held != row.want {
				t.Fatalf("after the hold the view holds %s: %v; want %v", row.uri, held, row.want)
			}
			if held && name != row.uri {
				t.Errorf("the view holds the library as %q; want %q", name, row.uri)
			}
		})
	}
}

// TestHoldResolvedLibraryLeavesTheCallersPinsAsTheyWere holds the one property a
// hold must keep toward whoever installed the view's pins: the view keeps the map
// SetPins was handed, which may be shared beyond the view, so a hold replaces the
// view's pin set rather than writing a library into that map.
func TestHoldResolvedLibraryLeavesTheCallersPinsAsTheyWere(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	pin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "Held:1.0.0", oci.MediaTypeSchemaLibrary, []byte(heldSchemaLibraryYAML))
	callers := Pins{{Kind: "SchemaLibrary", URI: "Other"}: {ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("1", 64)}}

	subject := NewLazyLookup(ws, cacheUnderCatalogDir, nil, nil)
	subject.SetPins(callers)
	axis := closure.LibraryAxisNamed("SchemaLibraries")
	subject.HoldResolvedLibrary(axis, closure.ResolvedLibrary{URI: "Held", ResolvedVersion: pin.ResolvedVersion, Digest: pin.Digest})

	if _, held := subject.LibraryName(axis, "Held"); !held {
		t.Fatal("the hold did not make the library readable, so the row proves nothing about the caller's map")
	}
	if _, written := callers[PinKey{Kind: axis.Kind, URI: "Held"}]; written || len(callers) != 1 {
		t.Errorf("the hold wrote into the map SetPins was handed: %v", callers)
	}
}
