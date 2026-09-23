// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	orasoci "oras.land/oras-go/v2/content/oci"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// artifactsLibraryPublishBody is a complete, schema-shaped ArtifactsLibrary: a
// PascalCase bare identity, a semver version matching its version directory, and
// one named bundle carrying two output files whose bodies are Go-template
// fragments authored inline as YAML strings. Every file body being inline is the
// property that decides how this kind publishes — the entity references nothing
// beside it, so the artifact is the entity file and nothing else.
const artifactsLibraryPublishBody = `apiVersion: aggrega.ai/v1alpha1
kind: ArtifactsLibrary
metadata:
  name: SharedArtifacts
  version: 1.0.0
  description: Reusable artifact bundles shared across recipes.
spec:
  artifacts:
    StoreBundle:
      store-config.yaml: |
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: store-{{ .values.port }}-config
        data:
          port: "{{ .values.port }}"
      store-policy.yaml: |
        apiVersion: networking.k8s.io/v1
        kind: NetworkPolicy
        metadata:
          name: store-{{ .values.port }}-policy
`

// TestPublishArtifactsLibraryEndToEnd publishes a real ArtifactsLibrary through
// the production publish path and inspects what actually landed, in both
// directions: that the expected artifact is there in the expected shape, and that
// nothing else came with it.
//
// It is written as a publish rather than as an assertion about the bundler's
// dispatch on purpose. No catalog entity declares this kind, so nothing exercised
// either branch, and absence of a consumer establishes non-regression only — never
// correctness. A test asserting "the switch has an arm for this kind" would pass
// against an arm that bundles the wrong shape; only publishing and reading the
// bytes back distinguishes the two.
//
// The destination is a file-backed OCI store in the test's own temp directory. It
// is a real store with a real on-disk OCI layout — pack, tag, resolve and fetch
// all run — and it is created and discarded with the test, so the proof mutates no
// registry anything else reads.
//
// The absence half is the load-bearing half, and one check carries it: a stray
// file is written into the entity's version directory before the push. For a kind
// that publishes as an archive of that directory the stray file would ride along;
// for one that publishes the entity file alone it cannot. Asserting the pushed
// bytes equal the entity verbatim therefore does more than confirm a round trip —
// it discriminates between the two shapes the bundler can produce, which is
// exactly the choice that had to be made for this kind.
func TestPublishArtifactsLibraryEndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	// The catalog layout for every library kind: library.yaml inside a directory
	// named for the entity's metadata.version.
	versionDir := filepath.Join(root, "artifactslibrary", "shared-artifacts", "1.0.0")
	entityPath := filepath.Join(versionDir, "library.yaml")
	writeEntityFixture(t, entityPath, artifactsLibraryPublishBody)

	// The stray sibling: present on disk beside the entity, and required to be
	// absent from the published artifact. Its body is a distinctive token so a
	// substring search over the pushed bytes cannot match it by accident.
	const strayToken = "STRAY-SIBLING-MUST-NOT-BE-PUBLISHED"
	strayPath := filepath.Join(versionDir, "notes.txt")
	writeEntityFixture(t, strayPath, strayToken+"\n")
	// Calibration for the absence assertion below: a fixture that never landed
	// would make "the stray file is not in the artifact" pass for the wrong
	// reason, and a vacuous pass reads exactly like a real one.
	if _, err := os.Stat(strayPath); err != nil {
		t.Fatalf("the stray sibling fixture must exist on disk for its absence to mean anything: %v", err)
	}

	store, err := orasoci.New(t.TempDir())
	if err != nil {
		t.Fatalf("orasoci.New: %v", err)
	}

	var out bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: entityPath,
		Registry:   "example.com",
		PushTarget: store,
		Out:        &out,
	}); err != nil {
		t.Fatalf("publishing an ArtifactsLibrary must succeed: %v", err)
	}

	// PRESENCE — the reference. The kind lowercases into its own repository
	// segment and the PascalCase bare identity lowercases into the path, so the
	// published reference is the one a consumer's pin will address.
	const wantRef = "Push complete: example.com/artifactslibrary/sharedartifacts:1.0.0"
	if !strings.Contains(out.String(), wantRef) {
		t.Errorf("publish output missing %q; got:\n%s", wantRef, out.String())
	}
	t.Logf("publish output:\n%s", out.String())

	pulled, err := oci.Pull(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("pulling the published ArtifactsLibrary back: %v", err)
	}

	// PRESENCE — the media type the transport recorded is this kind's own.
	if pulled.MediaType != oci.MediaTypeArtifactsLibrary {
		t.Errorf("published artifactType = %q; want %q", pulled.MediaType, oci.MediaTypeArtifactsLibrary)
	}

	// PRESENCE — the content is the entity, byte for byte. ABSENCE — therefore
	// not an archive of the entity's directory, and the stray sibling is not in
	// it.
	if string(pulled.Bytes) != artifactsLibraryPublishBody {
		t.Errorf("published bytes are not the entity verbatim.\n got (%d bytes):\n%s\nwant (%d bytes):\n%s",
			len(pulled.Bytes), pulled.Bytes, len(artifactsLibraryPublishBody), artifactsLibraryPublishBody)
	}
	if oci.IsTarGzBundle(pulled.Bytes) {
		t.Error("published content carries the gzip magic: this kind was bundled as an archive. " +
			"A library entity is self-contained, so an archive of its directory would sweep in " +
			"co-located files that are not part of the artifact")
	}
	// The stray-file search decompresses first when the content is an archive.
	// Searching the raw bytes alone would be blind in exactly the case this check
	// targets: inside a tar.gz the stray file's bytes are compressed, so a
	// substring scan finds nothing and the check reports absence for a payload
	// that in fact carries the file.
	if publishedCarries(t, pulled.Bytes, strayToken) {
		t.Error("a file sitting beside the entity was published with it; the artifact must be the entity alone")
	}

	// PRESENCE + ABSENCE over the manifest structure: exactly one content blob,
	// carrying this kind's media type, and no second layer. A kind that packed a
	// companion blob — the shape a Plugin publish uses for its .wasm — would show
	// a second layer here.
	var manifest ocispec.Manifest
	if err := json.Unmarshal(pulled.ManifestBytes, &manifest); err != nil {
		t.Fatalf("parse published manifest: %v", err)
	}
	if len(manifest.Layers) != 1 {
		t.Errorf("published manifest carries %d layers; want exactly 1 (the entity body)", len(manifest.Layers))
	} else if manifest.Layers[0].MediaType != oci.MediaTypeArtifactsLibrary {
		t.Errorf("layer mediaType = %q; want %q", manifest.Layers[0].MediaType, oci.MediaTypeArtifactsLibrary)
	}
	if manifest.ArtifactType != oci.MediaTypeArtifactsLibrary {
		t.Errorf("manifest artifactType = %q; want %q", manifest.ArtifactType, oci.MediaTypeArtifactsLibrary)
	}

	// PRESENCE — the searchable annotations name the kind, so a registry index
	// can find this artifact as an ArtifactsLibrary without fetching its body.
	if got := manifest.Annotations[oci.AnnotationKind]; got != "ArtifactsLibrary" {
		t.Errorf("manifest %s annotation = %q; want %q", oci.AnnotationKind, got, "ArtifactsLibrary")
	}

	// ABSENCE — the publish tagged exactly one version and created no others.
	tags, err := listStoreTags(t, store)
	if err != nil {
		t.Fatalf("list published tags: %v", err)
	}
	if len(tags) != 1 || tags[0] != "1.0.0" {
		t.Errorf("published tags = %v; want exactly [1.0.0] — the publish created something extra", tags)
	}
}

// TestPublishArtifactsLibraryIsDeterministic pins that two independent publishes
// of the same ArtifactsLibrary land the same manifest digest. It matters for this
// kind specifically because the digest is what the immutability guard compares: a
// non-reproducible bundle would make an identical re-push look like an attempt to
// overwrite a published version, and the operator would have no way to tell that
// from a real conflict.
func TestPublishArtifactsLibraryIsDeterministic(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "1.0.0", "library.yaml")
	writeEntityFixture(t, entityPath, artifactsLibraryPublishBody)

	store, err := orasoci.New(t.TempDir())
	if err != nil {
		t.Fatalf("orasoci.New: %v", err)
	}
	var first bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), EntityPath: entityPath, Registry: "example.com", PushTarget: store, Out: &first}); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if !strings.Contains(first.String(), "Push complete:") {
		t.Fatalf("first publish did not complete; got:\n%s", first.String())
	}

	var second bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), EntityPath: entityPath, Registry: "example.com", PushTarget: store, Out: &second}); err != nil {
		t.Fatalf("re-publishing identical content must be an idempotent no-op, not a conflict: %v", err)
	}
	if !strings.Contains(second.String(), "Already published:") {
		t.Errorf("identical re-publish should report 'Already published:'; got:\n%s", second.String())
	}
}

// publishedCarries reports whether token appears anywhere in a published payload,
// looking through a tar.gz wrapper when there is one. The gzip branch is what
// makes a negative result mean something: compressed bytes do not contain their
// own plaintext, so a raw substring scan over an archive reports absence for
// content the archive actually carries.
func publishedCarries(t *testing.T, payload []byte, token string) bool {
	t.Helper()
	if !oci.IsTarGzBundle(payload) {
		return bytes.Contains(payload, []byte(token))
	}
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("payload has gzip magic but does not decompress: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatalf("read published archive: %v", err)
		}
		if strings.Contains(hdr.Name, token) {
			return true
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read published archive entry %s: %v", hdr.Name, err)
		}
		if bytes.Contains(body, []byte(token)) {
			return true
		}
	}
}

// listStoreTags reads every tag a file-backed OCI store holds.
func listStoreTags(t *testing.T, store *orasoci.Store) ([]string, error) {
	t.Helper()
	var tags []string
	err := store.Tags(context.Background(), "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	})
	return tags, err
}
