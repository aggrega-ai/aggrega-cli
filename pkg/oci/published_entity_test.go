// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestPullPublishedEntityReadsTheDescriptorEachShapeCarriesItsEntityIn drives the
// three published shapes through the one reading a person's view is built on, and
// asserts both halves every row owes: that the bytes served are the ones the
// publish put there, and that the document the unwrapping hands back is the
// authored entity.
//
// The Plugin row is the one that separates this reading from Pull. A plugin
// manifest's content layer is its executable, so a reading that took the content
// layer for every kind would hand back the executable under the entity's name —
// and the entity-bytes assertion, not the error check, is what would see it.
func TestPullPublishedEntityReadsTheDescriptorEachShapeCarriesItsEntityIn(t *testing.T) {
	t.Parallel()

	component := entityBody("Component", "core/test/svc")

	ver := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(filepath.Join(ver, "source"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	recipe := entityBody("Recipe", "svc")
	mustWrite(t, filepath.Join(ver, "recipe.yaml"), recipe)
	mustWrite(t, filepath.Join(ver, "source", "values.yaml"), []byte("replicas: 1\n"))
	archive, archiveType, err := oci.Bundle("Recipe", filepath.Join(ver, "recipe.yaml"))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}

	plugin := entityBody("Plugin", "patch")
	wasm := []byte("\x00asm\x01\x00\x00\x00an executable, never an entity")

	rows := []struct {
		name        string
		publish     func(t *testing.T, store *memory.Store)
		wantServed  []byte
		wantType    string
		wantArchive bool
		wantEntity  []byte
	}{
		{
			name: "a document is served as the document",
			publish: func(t *testing.T, store *memory.Store) {
				if _, err := oci.Push(context.Background(), store, "1.0.0", oci.MediaTypeComponent, component, nil); err != nil {
					t.Fatalf("Push: %v", err)
				}
			},
			wantServed: component, wantType: oci.MediaTypeComponent, wantEntity: component,
		},
		{
			name: "an archive is served as the archive and unwraps to its entity",
			publish: func(t *testing.T, store *memory.Store) {
				if _, err := oci.Push(context.Background(), store, "1.0.0", archiveType, archive, nil); err != nil {
					t.Fatalf("Push: %v", err)
				}
			},
			wantServed: archive, wantType: oci.MediaTypeRecipe, wantArchive: true, wantEntity: recipe,
		},
		{
			name: "a plugin manifest is read through its config blob, not its executable",
			publish: func(t *testing.T, store *memory.Store) {
				if _, err := oci.PushPluginManifest(context.Background(), store, "1.0.0", plugin, wasm, nil); err != nil {
					t.Fatalf("PushPluginManifest: %v", err)
				}
			},
			wantServed: plugin, wantType: oci.MediaTypePlugin, wantEntity: plugin,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			store := memory.New()
			row.publish(t, store)

			got, err := oci.PullPublishedEntity(context.Background(), store, "1.0.0")
			if err != nil {
				t.Fatalf("PullPublishedEntity: %v", err)
			}
			if !bytes.Equal(got.Bytes, row.wantServed) {
				t.Errorf("served bytes are not what was published (%d bytes, want %d)", len(got.Bytes), len(row.wantServed))
			}
			if got.MediaType != row.wantType {
				t.Errorf("media type = %q; want %q", got.MediaType, row.wantType)
			}
			if got.Digest == "" || len(got.ManifestBytes) == 0 {
				t.Errorf("the manifest the read went through is not reported: digest %q, %d manifest bytes", got.Digest, len(got.ManifestBytes))
			}
			if oci.IsTarGzBundle(got.Bytes) != row.wantArchive {
				t.Errorf("archive shape = %v; want %v", oci.IsTarGzBundle(got.Bytes), row.wantArchive)
			}
			entity, err := oci.EntityYAML(got.Bytes)
			if err != nil {
				t.Fatalf("EntityYAML: %v", err)
			}
			if !bytes.Equal(entity, row.wantEntity) {
				t.Errorf("entity document is not the authored one.\n got: %q\nwant: %q", entity, row.wantEntity)
			}
		})
	}
}

// TestPullPublishedEntityRefusesAReferenceNothingIsPublishedUnder pins that a
// missing coordinate is the fetch-not-found condition rather than an empty answer,
// which a caller could not tell from an entity with nothing to say.
func TestPullPublishedEntityRefusesAReferenceNothingIsPublishedUnder(t *testing.T) {
	t.Parallel()
	store := memory.New()
	if _, err := oci.Push(context.Background(), store, "1.0.0", oci.MediaTypeComponent, entityBody("Component", "core/test/svc"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	got, err := oci.PullPublishedEntity(context.Background(), store, "9.9.9")
	if !errors.Is(err, oci.ErrFetchNotFound()) {
		t.Fatalf("err = %v; want ErrFetchNotFound", err)
	}
	if got != nil {
		t.Errorf("a refused read returned a result: %+v", got)
	}
}

// TestEntityYAMLNamesAnArchiveThatDoesNotUnpack pins the unpack condition at the
// unwrapper itself, so every reader of published bytes meets it rather than a bare
// archive error. Each row carries one fault, and the whole archive is the positive
// control that the fixture unpacks when nothing is wrong with it.
//
// Each refusal is also asserted NOT to be the no-entity condition: an archive that
// does not unpack and one that unpacks and holds nothing ask different things of
// whoever reads them, and a reading that reported the first as the second would
// send a consumer looking for a missing file in bytes that cannot be opened.
func TestEntityYAMLNamesAnArchiveThatDoesNotUnpack(t *testing.T) {
	t.Parallel()
	ver := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(ver, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := append(entityBody("Recipe", "svc"), bytes.Repeat([]byte("# a body long enough to cut in half\n"), 200)...)
	mustWrite(t, filepath.Join(ver, "recipe.yaml"), body)
	archive, _, err := oci.Bundle("Recipe", filepath.Join(ver, "recipe.yaml"))
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if _, err := oci.EntityYAML(archive); err != nil {
		t.Fatalf("control: the whole archive must unpack: %v", err)
	}

	for _, row := range []struct {
		name  string
		bytes []byte
	}{
		{"not a gzip stream after its magic bytes", append([]byte{0x1f, 0x8b}, []byte("this is not a gzip stream")...)},
		{"an archive cut short", archive[:len(archive)/2]},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if !oci.IsTarGzBundle(row.bytes) {
				t.Fatal("the fixture must begin as an archive, or this row tests the document path instead")
			}
			_, err := oci.EntityYAML(row.bytes)
			if !errors.Is(err, oci.ErrBundleUnreadable()) {
				t.Fatalf("err = %v; want ErrBundleUnreadable", err)
			}
			if errors.Is(err, oci.ErrBundleNoEntityDocument()) {
				t.Errorf("an archive that does not unpack was reported as one holding no entity: %v", err)
			}
		})
	}
}
