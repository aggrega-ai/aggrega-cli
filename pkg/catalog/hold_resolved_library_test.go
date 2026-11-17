// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	heldSchemaLibraryYAML = `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Held
  version: 1.0.0
spec:
  schemas:
    Settings:
      level: { type: string, default: "held" }
`
	heldMappingLibraryYAML = `apiVersion: aggrega.ai/v1beta1
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

// TestHoldResolvedLibraryHoldsTheResolvedVersion: a hold is decided by the version
// the resolution recorded. Where the view already holds the library at that
// version, the hold leaves the view as it was. Where no catalog directory holds the
// name, a library the resolution served from the content cache or a registry at a
// version the view does not hold becomes readable at exactly that version — also
// where a lockfile pin names the library at another version, which gives way to
// the resolution of the raised version.
//
// A catalog directory holding the name at other versions only never reaches a
// hold: its selection is final for the name, so the resolution halts with the
// unsatisfiable-version condition instead (TestLibraryFloorUnmetInOwningDirectory).
func TestHoldResolvedLibraryHoldsTheResolvedVersion(t *testing.T) {
	t.Parallel()
	axis := closure.LibraryAxisNamed("MappingLibraries")
	doc := func(version string) string {
		return strings.Replace(heldMappingLibraryYAML, "version: 1.0.0", "version: "+version, 1)
	}

	t.Run("a version the view holds", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		writeFixture(t, ws, "held-1.0.0.yaml", doc("1.0.0"))
		writeFixture(t, ws, "held-1.1.0.yaml", doc("1.1.0"))
		subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
		subject.HoldResolvedLibrary(axis, closure.ResolvedLibrary{
			URI: "HeldMappings", ResolvedVersion: "1.0.0", Digest: contentDigest([]byte(doc("1.0.0"))),
			ResolutionPath: closure.ResolutionPathCatalog,
		})
		if _, pinned := subject.pins[PinKey{Kind: axis.Kind, URI: "HeldMappings"}]; pinned {
			t.Error("the hold pinned a library the view holds at the resolved version")
		}
		if lib, ok := subject.MappingLibrary("HeldMappings:1.0.0"); !ok || lib.Metadata.Version != "1.0.0" {
			t.Errorf("read of HeldMappings:1.0.0 = %q (ok %v); want 1.0.0", lib.Metadata.Version, ok)
		}
	})

	t.Run("a version the view does not hold, of a name no catalog directory holds", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		writePinnedSlot(t, ws, cacheUnderCatalogDir, "HeldMappings:1.0.0", oci.MediaTypeMappingLibrary, []byte(doc("1.0.0")))
		pin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "HeldMappings:1.1.0", oci.MediaTypeMappingLibrary, []byte(doc("1.1.0")))
		subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
		if _, held := subject.MappingLibrary("HeldMappings"); held {
			t.Fatal("the view holds HeldMappings before the hold, so the row proves nothing about the hold")
		}
		subject.HoldResolvedLibrary(axis, closure.ResolvedLibrary{
			URI: "HeldMappings", ResolvedVersion: pin.ResolvedVersion, Digest: pin.Digest, ManifestDigest: pin.ManifestDigest,
			ResolutionPath: closure.ResolutionPathRegistry,
		})
		if lib, ok := subject.MappingLibrary("HeldMappings:1.1.0"); !ok || lib.Metadata.Version != "1.1.0" {
			t.Errorf("read of HeldMappings:1.1.0 = %q (ok %v); want the resolved 1.1.0", lib.Metadata.Version, ok)
		}
		if skips := subject.Skips(); len(skips) != 0 {
			t.Errorf("the hold recorded refusals: %v", skips)
		}
	})

	t.Run("a version above the one a lockfile pin names", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		pinned := writePinnedSlot(t, ws, cacheUnderCatalogDir, "HeldMappings:1.0.0", oci.MediaTypeMappingLibrary, []byte(doc("1.0.0")))
		raised := writePinnedSlot(t, ws, cacheUnderCatalogDir, "HeldMappings:1.1.0", oci.MediaTypeMappingLibrary, []byte(doc("1.1.0")))
		key := PinKey{Kind: axis.Kind, URI: "HeldMappings"}
		subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
		subject.SetPins(Pins{key: pinned})
		if lib, held := subject.MappingLibrary("HeldMappings"); !held || lib.Metadata.Version != "1.0.0" {
			t.Fatalf("the view holds HeldMappings at %q (held %v); the row needs the pin's 1.0.0", lib.Metadata.Version, held)
		}
		subject.HoldResolvedLibrary(axis, closure.ResolvedLibrary{
			URI: "HeldMappings", ResolvedVersion: raised.ResolvedVersion, Digest: raised.Digest, ManifestDigest: raised.ManifestDigest,
			ResolutionPath: closure.ResolutionPathRegistry,
		})
		if lib, ok := subject.MappingLibrary("HeldMappings:1.1.0"); !ok || lib.Metadata.Version != "1.1.0" {
			t.Errorf("read of HeldMappings:1.1.0 = %q (ok %v); want the raised 1.1.0", lib.Metadata.Version, ok)
		}
		if got := subject.pins[key].ResolvedVersion; got != "1.1.0" {
			t.Errorf("the view pins HeldMappings at %q; want the raised 1.1.0", got)
		}
	})
}
