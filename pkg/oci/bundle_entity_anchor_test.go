// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// entityBody is the minimal authored shape every entity document shares: the
// preamble the anchor reads, plus a name that tells two fixtures apart.
func entityBody(kind, name string) []byte {
	return []byte("apiVersion: aggrega.ai/v1alpha1\nkind: " + kind +
		"\nmetadata:\n  name: " + name + "\n  version: 1.0.0\n")
}

// TestBundledEntityIsFoundWhateverTheEntityFileIsNamed covers the anchor's
// subject directly: the bundler imposes no name on the entity file, so the
// consume side must locate the entity by what the document IS rather than by
// what it is called.
//
// The `recipe.yaml` row is the convention every entity in the shipped catalog
// follows, and it is here as the control: a repair that found only the
// unconventional names and lost the conventional one would break every artifact
// already published.
func TestBundledEntityIsFoundWhateverTheEntityFileIsNamed(t *testing.T) {
	t.Parallel()
	for _, fileName := range []string{
		"recipe.yaml",
		"featurerecipe.yaml",
		"feature-recipe.yaml",
		"mariadb.yaml",
		"entity.yml",
	} {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()
			ver := filepath.Join(t.TempDir(), "1.0.0")
			if err := os.MkdirAll(filepath.Join(ver, "source"), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			body := entityBody("Recipe", "helm/mariadb")
			mustWrite(t, filepath.Join(ver, fileName), body)
			// Co-located source, which must never be mistaken for the entity.
			mustWrite(t, filepath.Join(ver, "source", "Chart.yaml"), []byte("name: mariadb\nversion: 25.0.8\n"))

			archive, _, err := oci.Bundle("Recipe", filepath.Join(ver, fileName), []string{"source/Chart.yaml"})
			if err != nil {
				t.Fatalf("Bundle: %v", err)
			}
			got, err := oci.EntityYAML(archive)
			if err != nil {
				t.Fatalf("a bundle whose entity file is named %q must still yield its entity: %v", fileName, err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("unbundled entity is not the authored document.\n got: %q\nwant: %q", got, body)
			}
		})
	}
}

// TestBundleRootDocumentThatIsNotAnEntityIsNotMistakenForOne is the calibration
// for the rule above: a bundle root may legitimately carry a document that is
// not an entity, and a rule that took "a root-level YAML" for "the entity"
// would report the wrong bytes or a false ambiguity.
//
// The compose-fragment fixture is the shape the shipped catalog actually
// carries beside a FeatureRecipe: a plain YAML document with no entity
// preamble. The Kubernetes-manifest fixture is the harder one — it declares a
// `kind:`, so only reading that kind against the set this transport publishes
// tells the two apart.
func TestBundleRootDocumentThatIsNotAnEntityIsNotMistakenForOne(t *testing.T) {
	t.Parallel()
	ver := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(ver, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := entityBody("FeatureRecipe", "compose-bridge-fr")
	mustWrite(t, filepath.Join(ver, "recipe.yaml"), body)
	mustWrite(t, filepath.Join(ver, "bridge-fragment.yaml"),
		[]byte("# Compose fragment loaded by the bridge.\nservices:\n  aggrega-gw:\n    image: nginx:alpine\n"))
	mustWrite(t, filepath.Join(ver, "gateway.yaml"),
		[]byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: gw\n"))

	archive, _, err := oci.Bundle("FeatureRecipe", filepath.Join(ver, "recipe.yaml"),
		[]string{"bridge-fragment.yaml", "gateway.yaml"})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	got, err := oci.EntityYAML(archive)
	if err != nil {
		t.Fatalf("a bundle carrying non-entity documents beside its entity must still yield the entity: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("unbundled entity is not the authored document.\n got: %q\nwant: %q", got, body)
	}
}

// TestBundledEntityIsFoundOnlyAtTheBundleRoot pins the anchor's scope. A
// Recipe's co-located source tree may carry whole entity documents of its own —
// a chart that deploys an Aggregate, say — and those are payload, not the
// entity the artifact was published for.
func TestBundledEntityIsFoundOnlyAtTheBundleRoot(t *testing.T) {
	t.Parallel()
	ver := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(filepath.Join(ver, "source"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := entityBody("Recipe", "helm/mariadb")
	mustWrite(t, filepath.Join(ver, "recipe.yaml"), body)
	mustWrite(t, filepath.Join(ver, "source", "nested.yaml"), entityBody("Component", "demo/nested"))

	archive, _, err := oci.Bundle("Recipe", filepath.Join(ver, "recipe.yaml"), []string{"source/nested.yaml"})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	got, err := oci.EntityYAML(archive)
	if err != nil {
		t.Fatalf("a nested entity document is payload, not a second anchor: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("a nested entity document was taken for the anchor.\n got: %q\nwant: %q", got, body)
	}
}

// TestAnArchiveCarryingBothSiblingsIsOneArtifactAndTheReadSideRefuses drives
// the read side over the artifact another client can produce from a directory
// holding two entities.
//
// A publication through this product never packs it: each entity carries only
// the files it declares, and a document is no declaration of its neighbour's.
// But a client that packs a whole directory does, and packed from either entity
// the result is the same archive, so the two coordinates it is tagged at carry
// the same bytes. The read side is what must not then silently hand one of them
// back as the other: it refuses, naming both documents.
//
// The byte-identity half is the premise and is asserted first — if the two
// archives ever stopped being identical the refusal below would be proving
// something else.
func TestAnArchiveCarryingBothSiblingsIsOneArtifactAndTheReadSideRefuses(t *testing.T) {
	t.Parallel()
	ver := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(ver, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	first := filepath.Join(ver, "recipe.yaml")
	second := filepath.Join(ver, "db-recipe.yaml")
	mustWrite(t, first, entityBody("Recipe", "helm/frontend"))
	mustWrite(t, second, entityBody("Recipe", "helm/database"))

	firstArchive, _, err := oci.Bundle("Recipe", first, []string{"db-recipe.yaml"})
	if err != nil {
		t.Fatalf("Bundle first: %v", err)
	}
	secondArchive, _, err := oci.Bundle("Recipe", second, []string{"recipe.yaml"})
	if err != nil {
		t.Fatalf("Bundle second: %v", err)
	}
	if !bytes.Equal(firstArchive, secondArchive) {
		t.Fatalf("the whole-directory archive packed from either sibling is no longer one artifact "+
			"(%d vs %d bytes); the refusal below would be proving something else",
			len(firstArchive), len(secondArchive))
	}
	t.Logf("two coordinates, one artifact: %d bytes, byte-identical", len(firstArchive))

	_, err = oci.EntityYAML(firstArchive)
	if !errors.Is(err, oci.ErrBundleAmbiguousEntityDocument()) {
		t.Fatalf("an artifact carrying two entity documents must be refused, not resolved to one of them; got %v", err)
	}
	// The diagnostic names both documents, because "one of these two" is the
	// whole of what an author has to act on.
	for _, want := range []string{"recipe.yaml", "db-recipe.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q so the author can see which documents collided; got %q", want, err)
		}
	}
}

// TestBundleWithNoEntityDocumentIsRefused covers the other half of the
// negative: an artifact whose root carries no entity at all. Before this
// refusal existed the only reachable outcome was an error naming one file name,
// which said nothing true about a bundle whose entity was simply named
// something else.
func TestBundleWithNoEntityDocumentIsRefused(t *testing.T) {
	t.Parallel()
	ver := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(filepath.Join(ver, "source"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Bundle a real entity so the archive is well-formed, then rebuild it
	// without the entity: the subject is an artifact that unpacks cleanly and
	// holds no entity, not one that fails to unpack.
	mustWrite(t, filepath.Join(ver, "recipe.yaml"), entityBody("Recipe", "helm/mariadb"))
	mustWrite(t, filepath.Join(ver, "source", "Chart.yaml"), []byte("name: mariadb\n"))
	archive, _, err := oci.Bundle("Recipe", filepath.Join(ver, "recipe.yaml"), []string{"source/Chart.yaml"})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	stripped := repackWithout(t, archive, "recipe.yaml")

	if _, err := oci.EntityYAML(stripped); !errors.Is(err, oci.ErrBundleNoEntityDocument()) {
		t.Fatalf("an artifact with no entity document must be refused by name; got %v", err)
	}
	// Calibration: the same archive WITH its entity resolves, so the refusal
	// above is the missing entity and not the repack.
	if _, err := oci.EntityYAML(archive); err != nil {
		t.Fatalf("the unstripped archive must still resolve, or the refusal above proves nothing: %v", err)
	}
}

// TestEveryKindCarryingFilesRoundTripsThroughTheAnchor walks the kinds the
// transport itself publishes rather than a list written here, bundles each with
// one file carried beside its document, unwraps it, and checks the entity that
// comes back is the one that went in.
//
// Every kind reaches the anchor here, because whether a publication is an
// archive follows from what it carries rather than from its kind: a kind the
// anchor could not locate inside an archive would publish cleanly and never
// unpack the first time an entity of it declared a file.
//
// The non-emptiness guard is calibration: an empty enumeration would run the
// loop zero times and pass while checking nothing.
func TestEveryKindCarryingFilesRoundTripsThroughTheAnchor(t *testing.T) {
	t.Parallel()
	kinds := oci.MediaTypeKinds()
	if len(kinds) == 0 {
		t.Fatal("the transport publishes no kinds at all, so the loop below asserts nothing")
	}
	for _, kind := range kinds {
		ver := filepath.Join(t.TempDir(), "1.0.0")
		if err := os.MkdirAll(ver, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// Deliberately NOT named for its kind: the entity file's name is exactly
		// what the anchor must not depend on.
		path := filepath.Join(ver, "authored.yaml")
		body := entityBody(kind, "demo/x")
		mustWrite(t, path, body)
		mustWrite(t, filepath.Join(ver, "companion.txt"), []byte("carried\n"))

		data, _, err := oci.Bundle(kind, path, []string{"companion.txt"})
		if err != nil {
			t.Fatalf("kind %q: Bundle: %v", kind, err)
		}
		if !oci.IsTarGzBundle(data) {
			t.Errorf("kind %q carried a file and did not publish as an archive", kind)
			continue
		}
		got, uerr := oci.EntityYAML(data)
		if uerr != nil {
			t.Errorf("kind %q ships as an archive but its entity cannot be located in it: %v", kind, uerr)
			continue
		}
		if !bytes.Equal(got, body) {
			t.Errorf("kind %q: unbundled entity is not the authored document.\n got: %q\nwant: %q", kind, got, body)
		}
	}
}

// repackWithout rewrites archive without the named root entry, preserving every
// other entry verbatim. It builds the negative fixture from a real bundle so
// the archive under test differs from a working one in exactly one way.
func repackWithout(t *testing.T, archive []byte, drop string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer func() { _ = gz.Close() }()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tr := tar.NewReader(gz)
	dropped := false
	for {
		hdr, nerr := tr.Next()
		if nerr != nil {
			break
		}
		if hdr.Name == drop {
			dropped = true
			continue
		}
		body := make([]byte, hdr.Size)
		if _, rerr := readFull(tr, body); rerr != nil {
			t.Fatalf("read %s: %v", hdr.Name, rerr)
		}
		if werr := tw.WriteHeader(hdr); werr != nil {
			t.Fatalf("write header %s: %v", hdr.Name, werr)
		}
		if _, werr := tw.Write(body); werr != nil {
			t.Fatalf("write %s: %v", hdr.Name, werr)
		}
	}
	if !dropped {
		t.Fatalf("the fixture never contained %q, so stripping it changed nothing", drop)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func readFull(r *tar.Reader, into []byte) (int, error) {
	read := 0
	for read < len(into) {
		n, err := r.Read(into[read:])
		read += n
		if err != nil {
			if read == len(into) {
				return read, nil
			}
			return read, err
		}
	}
	return read, nil
}
