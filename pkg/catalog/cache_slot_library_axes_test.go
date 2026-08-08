// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestCacheSlotUsesLibraryFilenameOnEveryAxis pins the cache-slot write to every
// library axis the closure declares.
//
// A pulled library lands under the canonical library filename so the slot matches
// the local-authoring layout an author would recognise. That branch used to name
// two of the axes, so a library on either of the other two was written under the
// kind-agnostic filename instead — the same content in a shape that does not match
// the layout, differing by axis for no reason a reader could discover.
//
// Driving the assertion off the declared axis set rather than a hand-written list
// is the point: an axis added later is asserted here by being declared.
func TestCacheSlotUsesLibraryFilenameOnEveryAxis(t *testing.T) {
	for _, axis := range closure.LibraryAxes() {
		t.Run(axis.Kind, func(t *testing.T) {
			ws := t.TempDir()
			const cacheDir = ".aggrega/cache"

			mediaType, err := oci.MediaTypeForKind(axis.Kind)
			if err != nil {
				t.Fatalf("no mediatype for declared axis kind %q: %v", axis.Kind, err)
			}

			const uri, version = "ProbeLib", "1.0.0"
			body := []byte("kind: " + axis.Kind + "\n")
			res := &oci.PullResult{Bytes: body, MediaType: mediaType}

			if werr := writeEntityToCacheSlot(ws, cacheDir, uri, version, res); werr != nil {
				t.Fatalf("writeEntityToCacheSlot: %v", werr)
			}

			slot := oci.CachePath(ws, cacheDir, uri, version)
			got, rerr := os.ReadFile(filepath.Join(slot, "library.yaml"))
			if rerr != nil {
				t.Fatalf("a %s pulled into the cache was not written under the canonical library filename, "+
					"so its slot does not match the local-authoring layout the other axes produce: %v",
					axis.Kind, rerr)
			}
			if string(got) != string(body) {
				t.Errorf("cached %s body = %q, want %q", axis.Kind, got, body)
			}
		})
	}
}
