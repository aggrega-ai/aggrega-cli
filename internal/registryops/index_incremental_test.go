// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// validatingRegistry is an in-process OCI registry serving the conditional reads
// an index refresh relies on, the way a registry that validates its computed
// listings serves them: the tag listing and every referrer listing carry a strong
// validator hashed over the serialized listing, and a request presenting it in
// If-None-Match is answered 304. Manifests, blobs and the repository listing are
// served by the go-containerregistry in-memory registry.
//
// The referrer listing is served here rather than by that registry because it
// lists referrers in no fixed order — so a hash over it would change from one
// request to the next — and reports each referrer's type as its configuration's
// media type rather than the manifest's own artifact type. Here the listing is
// sorted by digest, carries each referrer's artifact type, and applies an
// artifact-type filter itself.
//
// It counts every request by kind, so a test asserts which reads a refresh made
// rather than only what it wrote.
type validatingRegistry struct {
	host  string
	inner http.Handler

	mu        sync.Mutex
	counts    map[string]int
	referrers map[string][]ocispec.Descriptor // keyed "<repo>@<subject digest>"
	failTags  map[string]bool                 // repositories whose tag listing fails
	slowTags  map[string]time.Duration        // repositories whose tag listing is delayed
	faults    []*readFault                    // reads the registry fails in passing or for good
}

func newValidatingRegistry(t *testing.T) *validatingRegistry {
	t.Helper()
	r := &validatingRegistry{
		inner:     registry.New(),
		counts:    map[string]int{},
		referrers: map[string][]ocispec.Descriptor{},
		failTags:  map[string]bool{},
		slowTags:  map[string]time.Duration{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(srv.Close)
	r.host = strings.TrimPrefix(srv.URL, "http://")
	return r
}

// requestCounts returns the requests served by kind since the last reset.
func (r *validatingRegistry) requestCounts() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.counts)
}

func (r *validatingRegistry) resetCounts() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts = map[string]int{}
}

func (r *validatingRegistry) count(kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[kind]++
	return r.counts[kind]
}

// isReferrer reports whether reference names the manifest of a referrer — a
// signature, a marker, a published-at statement — rather than a published
// version's own manifest.
func (r *validatingRegistry) isReferrer(repo, reference string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix := repo + "@"
	for key, descs := range r.referrers {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		for _, d := range descs {
			if d.Digest.String() == reference {
				return true
			}
		}
	}
	return false
}

// The request kinds the registry counts. A manifest read is counted apart by
// whose manifest it is: a published version's own manifest, which a refresh
// reads only to take the version's facets again, and a referrer's, which reading
// a signature or a marker always fetches.
const (
	kindCatalog       = "catalog"
	kindTagsListed    = "tags listed"
	kindTagsUnchanged = "tags unchanged (304)"
	kindManifestHead  = "manifest HEAD"
	kindManifestGet   = "version manifest GET"
	kindReferrerGet   = "referrer manifest GET"
	kindBlobRead      = "blob read"
	kindReferrers     = "referrers listed"
	kindReferrersSame = "referrers unchanged (304)"
	kindWrite         = "write"
)

func (r *validatingRegistry) serve(w http.ResponseWriter, req *http.Request) {
	if r.injectFault(w, req) {
		return
	}
	path := req.URL.Path
	switch {
	case path == "/v2/_catalog":
		r.count(kindCatalog)
		r.inner.ServeHTTP(w, req)
	case strings.HasSuffix(path, "/tags/list") && req.Method == http.MethodGet:
		r.serveTags(w, req, strings.TrimSuffix(strings.TrimPrefix(path, "/v2/"), "/tags/list"))
	case strings.Contains(path, "/referrers/") && req.Method == http.MethodGet:
		i := strings.LastIndex(path, "/referrers/")
		r.serveReferrers(w, req, strings.TrimPrefix(path[:i], "/v2/"), path[i+len("/referrers/"):])
	case strings.Contains(path, "/manifests/") && req.Method == http.MethodPut:
		i := strings.LastIndex(path, "/manifests/")
		r.count(kindWrite)
		r.putManifest(w, req, strings.TrimPrefix(path[:i], "/v2/"))
	case strings.Contains(path, "/manifests/") && req.Method == http.MethodHead:
		r.count(kindManifestHead)
		r.inner.ServeHTTP(w, req)
	case strings.Contains(path, "/manifests/") && req.Method == http.MethodGet:
		i := strings.LastIndex(path, "/manifests/")
		if r.isReferrer(strings.TrimPrefix(path[:i], "/v2/"), path[i+len("/manifests/"):]) {
			r.count(kindReferrerGet)
		} else {
			r.count(kindManifestGet)
		}
		r.inner.ServeHTTP(w, req)
	case strings.Contains(path, "/blobs/") && (req.Method == http.MethodGet || req.Method == http.MethodHead) && !strings.Contains(path, "/uploads/"):
		r.count(kindBlobRead)
		r.inner.ServeHTTP(w, req)
	case req.Method == http.MethodGet && path == "/v2/":
		r.inner.ServeHTTP(w, req)
	default:
		r.count(kindWrite)
		r.inner.ServeHTTP(w, req)
	}
}

func (r *validatingRegistry) serveTags(w http.ResponseWriter, req *http.Request, repo string) {
	r.mu.Lock()
	fail, delay := r.failTags[repo], r.slowTags[repo]
	r.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if fail {
		r.count(kindTagsListed)
		http.Error(w, "the registry failed", http.StatusInternalServerError)
		return
	}
	rec := httptest.NewRecorder()
	r.inner.ServeHTTP(rec, req)
	body := rec.Body.Bytes()
	if rec.Code != http.StatusOK {
		r.count(kindTagsListed)
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
		return
	}
	etag := listingValidator(body)
	if req.Header.Get("If-None-Match") == etag {
		r.count(kindTagsUnchanged)
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	r.count(kindTagsListed)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	_, _ = w.Write(body)
}

func (r *validatingRegistry) serveReferrers(w http.ResponseWriter, req *http.Request, repo, subject string) {
	r.mu.Lock()
	all := slices.Clone(r.referrers[repo+"@"+subject])
	r.mu.Unlock()
	slices.SortFunc(all, func(a, b ocispec.Descriptor) int { return strings.Compare(a.Digest.String(), b.Digest.String()) })
	filter := req.URL.Query().Get("artifactType")
	manifests := make([]ocispec.Descriptor, 0, len(all))
	for _, d := range all {
		if filter == "" || d.ArtifactType == filter {
			manifests = append(manifests, d)
		}
	}
	body, _ := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	})
	etag := listingValidator(body)
	w.Header().Set("ETag", etag)
	if req.Header.Get("If-None-Match") == etag {
		r.count(kindReferrersSame)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	r.count(kindReferrers)
	w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
	if filter != "" {
		w.Header().Set("OCI-Filters-Applied", "artifactType")
	}
	_, _ = w.Write(body)
}

// putManifest stores a manifest through the inner registry and, where it names a
// subject, records it as one of that subject's referrers.
func (r *validatingRegistry) putManifest(w http.ResponseWriter, req *http.Request, repo string) {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var shape struct {
		MediaType    string              `json:"mediaType"`
		ArtifactType string              `json:"artifactType"`
		Subject      *ocispec.Descriptor `json:"subject"`
	}
	_ = json.Unmarshal(data, &shape)
	if shape.Subject != nil {
		w.Header().Set("OCI-Subject", shape.Subject.Digest.String())
	}
	inner := req.Clone(req.Context())
	inner.Body = io.NopCloser(bytes.NewReader(data))
	rec := httptest.NewRecorder()
	r.inner.ServeHTTP(rec, inner)
	for k, vs := range rec.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
	if rec.Code != http.StatusCreated || shape.Subject == nil {
		return
	}
	mediaType := shape.MediaType
	if mediaType == "" {
		mediaType = req.Header.Get("Content-Type")
	}
	desc := content.NewDescriptorFromBytes(mediaType, data)
	desc.ArtifactType = shape.ArtifactType
	key := repo + "@" + shape.Subject.Digest.String()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.referrers[key] {
		if existing.Digest == desc.Digest {
			return
		}
	}
	r.referrers[key] = append(r.referrers[key], desc)
}

// deleteTag removes one tag the way a registry's tag deletion does.
func (r *validatingRegistry) deleteTag(t *testing.T, repo, tag string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, "http://"+r.host+"/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete %s:%s: %v", repo, tag, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("delete %s:%s: status %d", repo, tag, resp.StatusCode)
	}
}

func listingValidator(body []byte) string {
	sum := sha256.Sum256(body)
	return `"sha256:` + hex.EncodeToString(sum[:]) + `"`
}

// repository opens one repository of the registry for publishing fixtures.
func (r *validatingRegistry) repository(t *testing.T, repo string) *remote.Repository {
	t.Helper()
	target, err := oci.NewRemoteRepository(r.host+"/"+repo, &http.Client{})
	if err != nil {
		t.Fatalf("open %s: %v", repo, err)
	}
	return target
}

// publish pushes one component version with searchable annotations and returns
// its manifest digest.
func publish(t *testing.T, target *remote.Repository, tag, content string) string {
	t.Helper()
	ann, err := oci.SearchableMetadata{Kind: "Component", Description: "component " + content}.Annotations()
	if err != nil {
		t.Fatalf("annotations: %v", err)
	}
	digest, err := oci.Push(context.Background(), target, tag, oci.MediaTypeComponent, []byte("# "+content+"\n"), ann)
	if err != nil {
		t.Fatalf("publish %s: %v", tag, err)
	}
	return digest
}

// signTag attaches a signature of the version tag names, made by signer.
func signTag(t *testing.T, target *remote.Repository, tag string, signer signing.Signer) {
	t.Helper()
	ctx := context.Background()
	digest, _, err := oci.Resolve(ctx, target, tag)
	if err != nil {
		t.Fatalf("resolve %s: %v", tag, err)
	}
	bundle, artifactType, err := signer.Sign(ctx, digest)
	if err != nil {
		t.Fatalf("sign %s: %v", tag, err)
	}
	if _, err := oci.AttachReferrer(ctx, target, tag, artifactType, bundle); err != nil {
		t.Fatalf("attach the signature of %s: %v", tag, err)
	}
}

// deprecateTag attaches a signed deprecation marker to the version tag names,
// through the attach path the deprecate command takes.
func deprecateTag(t *testing.T, target *remote.Repository, repo, tag string, signer signing.Signer) {
	t.Helper()
	marker := deprecation.Marker{Severity: deprecation.SeverityWarning, Reason: "superseded", IssuedOn: "2026-05-01T00:00:00Z"}
	if _, err := registryindex.AttachSignedMarker(context.Background(), io.Discard, target, tag, repo+":"+tag, marker, signer); err != nil {
		t.Fatalf("deprecate %s:%s: %v", repo, tag, err)
	}
}

// attachUnsignedMarker attaches a deprecation marker carrying no signature and
// returns its descriptor, so a test can sign it later.
func attachUnsignedMarker(t *testing.T, target *remote.Repository, tag string) ocispec.Descriptor {
	t.Helper()
	payload, err := deprecation.Marshal(deprecation.Marker{Severity: deprecation.SeverityWarning, Reason: "unsigned", IssuedOn: "2026-05-02T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	desc, err := oci.AttachReferrer(context.Background(), target, tag, deprecation.ArtifactType, payload)
	if err != nil {
		t.Fatalf("attach an unsigned marker to %s: %v", tag, err)
	}
	return desc
}

// keyedSigner returns a keyed signer and its public key's PEM.
func keyedSigner(t *testing.T) (signing.Signer, string) {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	return signer, string(pubPEM)
}

// refreshWorkspace resolves a workspace at root whose global layer, written at
// globalPath, configures host and, where publicKey is set, pins that key for it.
// Calling it again with another key over the same root is how a test changes the
// registry's trust policy between two refreshes of one index.
func refreshWorkspace(t *testing.T, root, globalPath, host, publicKey string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "registries:\n  - host: %s\n", host)
	if publicKey != "" {
		b.WriteString("    public_key: |\n")
		for _, line := range strings.Split(strings.TrimRight(publicKey, "\n"), "\n") {
			b.WriteString("      " + line + "\n")
		}
	}
	if err := os.WriteFile(globalPath, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	wsPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(wsPath, []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write the workspace layer: %v", err)
	}
	lookup := func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(root, root, lookup)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	return rw
}

// refreshFixture is one registry and one workspace indexing it.
type refreshFixture struct {
	reg        *validatingRegistry
	root       string
	globalPath string
	rw         *workspaceconfig.ResolvedWorkspace
}

func newRefreshFixture(t *testing.T, publicKey string) *refreshFixture {
	t.Helper()
	reg := newValidatingRegistry(t)
	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	return &refreshFixture{reg: reg, root: root, globalPath: globalPath,
		rw: refreshWorkspace(t, root, globalPath, reg.host, publicKey)}
}

func (f *refreshFixture) update(t *testing.T, opts UpdateOptions) string {
	t.Helper()
	opts.Now = func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) }
	var out bytes.Buffer
	if _, err := Update(t.Context(), f.rw, opts, &out); err != nil {
		t.Fatalf("Update: %v\n%s", err, out.String())
	}
	return out.String()
}

func (f *refreshFixture) record(t *testing.T, repo string) registryindex.Record {
	t.Helper()
	loaded, err := registryindex.LoadHost(f.root, f.rw.Config().ResolvedArtifactCacheDir(), f.reg.host)
	if err != nil {
		t.Fatalf("load the index: %v", err)
	}
	uri := "oci://" + f.reg.host + "/" + repo
	for _, rec := range loaded.Records {
		if rec.URI == uri {
			return rec
		}
	}
	t.Fatalf("the index holds no record for %s: %+v", repo, loaded.Records)
	return registryindex.Record{}
}

func (f *refreshFixture) files(t *testing.T) map[string]string {
	t.Helper()
	return indexSnapshot(t, f.root, f.rw.Config().ResolvedArtifactCacheDir(), f.reg.host)
}

// TestARefreshReadsAgainWhatChangedSinceTheEarlierIndex follows an earlier index
// through one change to the registry per row and proves a refresh without a full
// re-read records what the change made true. Each row names the defect it
// catches; with the conditional refresh reduced to comparing the latest version's
// digest only, every row but the trust-policy and validator rows keeps the earlier
// answer and fails.
func TestARefreshReadsAgainWhatChangedSinceTheEarlierIndex(t *testing.T) {
	t.Parallel()
	const repo = "component/core/api"

	t.Run("an older version published again under its tag is read again", func(t *testing.T) {
		t.Parallel()
		f := newRefreshFixture(t, "")
		target := f.reg.repository(t, repo)
		publish(t, target, "1.0.0", "first")
		publish(t, target, "1.1.0", "second")
		f.update(t, UpdateOptions{})

		moved := publish(t, target, "1.0.0", "first, published again")
		f.update(t, UpdateOptions{})

		rec := f.record(t, repo)
		if rec.Published[0].Version != "1.0.0" || rec.Published[0].ManifestDigest != moved {
			t.Errorf("1.0.0 records %s; want the digest it was published again at, %s", rec.Published[0].ManifestDigest, moved)
		}
	})

	t.Run("a signature attached to an unchanged version is read and verified", func(t *testing.T) {
		t.Parallel()
		signer, pub := keyedSigner(t)
		f := newRefreshFixture(t, pub)
		target := f.reg.repository(t, repo)
		publish(t, target, "1.0.0", "unsigned at first")
		f.update(t, UpdateOptions{})
		if f.record(t, repo).Published[0].Signer != nil {
			t.Fatal("the fixture's version is signed before the signature is attached")
		}

		signTag(t, target, "1.0.0", signer)
		f.reg.resetCounts()
		f.update(t, UpdateOptions{})

		pv := f.record(t, repo).Published[0]
		if pv.Signer == nil || pv.Signer.Mode != "keyed" {
			t.Errorf("1.0.0 records signer %+v; want the keyed signature attached after the earlier index", pv.Signer)
		}
		// The signature's own manifest is fetched to verify it; the version's manifest
		// is not, since its digest is unchanged and its facets are kept.
		counts := f.reg.requestCounts()
		if counts[kindManifestGet] != 0 {
			t.Errorf("the refresh read the version's own manifest %d time(s); its digest is unchanged, so its facets are kept (requests %v)", counts[kindManifestGet], counts)
		}
		if counts[kindReferrerGet] == 0 {
			t.Errorf("the refresh fetched no referrer manifest, so the signature it records was not read (requests %v)", counts)
		}
	})

	t.Run("a deprecation marker attached to an unchanged version is recorded", func(t *testing.T) {
		t.Parallel()
		signer, pub := keyedSigner(t)
		f := newRefreshFixture(t, pub)
		target := f.reg.repository(t, repo)
		publish(t, target, "1.0.0", "about to be deprecated")
		publish(t, target, "1.1.0", "its replacement")
		f.update(t, UpdateOptions{})

		deprecateTag(t, target, repo, "1.0.0", signer)
		out := f.update(t, UpdateOptions{})

		rec := f.record(t, repo)
		if len(rec.Deprecations) != 1 || rec.Deprecations[0].Version != "1.0.0" || rec.Deprecations[0].Signer == nil {
			t.Errorf("deprecations = %+v; want the signed marker on 1.0.0\n%s", rec.Deprecations, out)
		}
	})

	t.Run("a signature attached to a marker after it was read is picked up", func(t *testing.T) {
		t.Parallel()
		signer, pub := keyedSigner(t)
		f := newRefreshFixture(t, pub)
		target := f.reg.repository(t, repo)
		publish(t, target, "1.0.0", "marked before its marker is signed")
		markerDesc := attachUnsignedMarker(t, target, "1.0.0")
		f.update(t, UpdateOptions{})
		if len(f.record(t, repo).Deprecations) != 0 {
			t.Fatal("an unsigned marker was recorded")
		}

		bundle, artifactType, err := signer.Sign(context.Background(), markerDesc.Digest.String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := oci.AttachReferrerToDescriptor(context.Background(), target, markerDesc, artifactType, bundle); err != nil {
			t.Fatalf("sign the marker: %v", err)
		}
		f.update(t, UpdateOptions{})

		if deps := f.record(t, repo).Deprecations; len(deps) != 1 || deps[0].Signer == nil {
			t.Errorf("deprecations = %+v; want the marker, now signed", deps)
		}
	})

	t.Run("a trust-policy change reads every version again", func(t *testing.T) {
		t.Parallel()
		signer, pub := keyedSigner(t)
		_, otherPub := keyedSigner(t)
		f := newRefreshFixture(t, otherPub)
		target := f.reg.repository(t, repo)
		publish(t, target, "1.0.0", "signed by the key pinned later")
		signTag(t, target, "1.0.0", signer)
		f.update(t, UpdateOptions{})
		if f.record(t, repo).Published[0].Signer != nil {
			t.Fatal("a signature verified against a key that did not make it")
		}

		f.rw = refreshWorkspace(t, f.root, f.globalPath, f.reg.host, pub)
		f.reg.resetCounts()
		f.update(t, UpdateOptions{})

		if f.record(t, repo).Published[0].Signer == nil {
			t.Error("after the policy pinned the signing key, the version still records no signer")
		}
		if got := f.reg.requestCounts()[kindManifestGet]; got == 0 {
			t.Error("the refresh under a changed policy kept the earlier record's facets instead of reading everything again")
		}
	})

	t.Run("a deleted version is dropped", func(t *testing.T) {
		t.Parallel()
		f := newRefreshFixture(t, "")
		target := f.reg.repository(t, repo)
		publish(t, target, "1.0.0", "about to be deleted")
		publish(t, target, "1.1.0", "kept")
		f.update(t, UpdateOptions{})

		f.reg.deleteTag(t, repo, "1.0.0")
		f.update(t, UpdateOptions{})

		rec := f.record(t, repo)
		if !slices.Equal(rec.Versions, []string{"1.1.0"}) || len(rec.Published) != 1 || rec.Published[0].Version != "1.1.0" {
			t.Errorf("versions %v, published %+v; want only 1.1.0", rec.Versions, rec.Published)
		}
	})

	t.Run("a record without validators is read again in full", func(t *testing.T) {
		t.Parallel()
		signer, pub := keyedSigner(t)
		f := newRefreshFixture(t, pub)
		target := f.reg.repository(t, repo)
		publish(t, target, "1.0.0", "signed")
		signTag(t, target, "1.0.0", signer)
		f.update(t, UpdateOptions{})
		built := f.files(t)

		// Rewrite the index through the product's writer with the records alone: the
		// shape an index has where nothing was recorded for a refresh to compare.
		cacheDir := f.rw.Config().ResolvedArtifactCacheDir()
		loaded, err := registryindex.LoadHost(f.root, cacheDir, f.reg.host)
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteHostIndex(f.root, cacheDir, HostIndex{Host: f.reg.host, Records: loaded.Records},
			time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		f.reg.resetCounts()
		f.update(t, UpdateOptions{})

		counts := f.reg.requestCounts()
		if counts[kindManifestGet] == 0 || counts[kindReferrers] == 0 {
			t.Errorf("requests %v; want the manifest and the referrers read again", counts)
		}
		if got := f.files(t); !maps.Equal(got, built) {
			t.Errorf("the re-read index differs from the first build:\nbuilt %v\nnow   %v", built, got)
		}
	})

	t.Run("an unchanged registry is answered by unchanged listings and digest resolves alone", func(t *testing.T) {
		t.Parallel()
		signer, pub := keyedSigner(t)
		f := newRefreshFixture(t, pub)
		api := f.reg.repository(t, repo)
		publish(t, api, "1.0.0", "signed and deprecated")
		publish(t, api, "1.1.0", "signed")
		signTag(t, api, "1.0.0", signer)
		signTag(t, api, "1.1.0", signer)
		deprecateTag(t, api, repo, "1.0.0", signer)
		publish(t, f.reg.repository(t, "component/core/web"), "0.1.0", "unsigned")
		f.update(t, UpdateOptions{})
		built := f.files(t)

		f.reg.resetCounts()
		out := f.update(t, UpdateOptions{})

		// Two repositories, three versions, one judged marker: one catalog listing,
		// one unchanged tag listing per repository, one digest resolve per version,
		// and one unchanged referrer listing per version plus one for the marker.
		want := map[string]int{
			kindCatalog:       1,
			kindTagsUnchanged: 2,
			kindManifestHead:  3,
			kindReferrersSame: 4,
		}
		if got := f.reg.requestCounts(); !maps.Equal(got, want) {
			t.Errorf("requests by kind = %v; want exactly %v", got, want)
		}
		if got := f.files(t); !maps.Equal(got, built) {
			t.Errorf("the refresh changed the index:\nbuilt %v\nnow   %v", built, got)
		}
		if !strings.Contains(out, "Recorded 1 deprecation markers from "+f.reg.host) {
			t.Errorf("the refresh does not report the marker the index still records:\n%s", out)
		}
	})
}

// TestTheParallelWalkWritesAndPrintsWhatASequentialWalkDoes indexes one registry
// state twice from scratch, once reading one repository at a time and once with
// the walk's own bound, and requires the index files and everything printed to be
// identical. The registry delays the first repository's tag listing so the
// parallel walk finishes later repositories first: output in completion order
// would differ from the sequential walk and fail the comparison.
//
// The positive control is in the output itself: it must carry a skipped
// repository, an honored marker and a marker that was not honored, so the
// comparison is over the lines a reordering would move rather than over a quiet
// walk.
func TestTheParallelWalkWritesAndPrintsWhatASequentialWalkDoes(t *testing.T) {
	t.Parallel()
	signer, pub := keyedSigner(t)
	reg := newValidatingRegistry(t)

	first := reg.repository(t, "component/a/first")
	publish(t, first, "1.0.0", "first")
	deprecateTag(t, first, "component/a/first", "1.0.0", signer)
	for i := range 12 {
		publish(t, reg.repository(t, fmt.Sprintf("component/b/plain-%02d", i)), "1.0.0", fmt.Sprintf("plain %d", i))
	}
	marked := reg.repository(t, "component/c/unsigned-marker")
	publish(t, marked, "2.0.0", "carries an unsigned marker")
	attachUnsignedMarker(t, marked, "2.0.0")
	publish(t, reg.repository(t, "component/d/unreadable"), "1.0.0", "its tag listing fails")
	publish(t, reg.repository(t, "component/e/no-release"), "latest", "no released version")
	reg.mu.Lock()
	reg.failTags["component/d/unreadable"] = true
	reg.slowTags["component/a/first"] = 150 * time.Millisecond
	reg.mu.Unlock()

	run := func(workers int) (map[string]string, string) {
		root := t.TempDir()
		rw := refreshWorkspace(t, root, filepath.Join(t.TempDir(), "config.yaml"), reg.host, pub)
		var out bytes.Buffer
		if _, err := Update(t.Context(), rw, UpdateOptions{workers: workers,
			Now: func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) }}, &out); err != nil {
			t.Fatalf("Update with %d worker(s): %v\n%s", workers, err, out.String())
		}
		return indexSnapshot(t, root, rw.Config().ResolvedArtifactCacheDir(), reg.host), out.String()
	}
	sequentialFiles, sequentialOut := run(1)
	parallelFiles, parallelOut := run(indexWalkWorkers)

	for _, want := range []string{
		"skipped component/d/unreadable",
		"component/a/first:1.0.0: marker present and honored",
		"component/c/unsigned-marker:2.0.0: marker present but not honored",
	} {
		if !strings.Contains(sequentialOut, want) {
			t.Fatalf("the sequential walk's output lacks %q, so the comparison below is not over the lines it means to hold:\n%s", want, sequentialOut)
		}
	}
	if parallelOut != sequentialOut {
		t.Errorf("the parallel walk printed differently from the sequential one:\nsequential:\n%s\nparallel:\n%s", sequentialOut, parallelOut)
	}
	if !maps.Equal(parallelFiles, sequentialFiles) {
		t.Errorf("the parallel walk wrote different index files:\nsequential %v\nparallel   %v", sequentialFiles, parallelFiles)
	}
	if len(sequentialFiles) != 15 {
		t.Errorf("the index holds %d files; want 14 records and the freshness file", len(sequentialFiles))
	}
}

// takeCountingSource is an index source counting the repositories a walk takes.
// The walk opens a repository through Repository before it makes any request
// about it, so the count is the number of repositories its workers took —
// including one taken after the stop, whose requests then fail inside the client
// without reaching the registry, so that no count of the registry's own requests
// could ever see it.
//
// With stopAt set, the take that reaches that count stops the walk, under the
// same lock the count is kept under: no later take is counted before the stop has
// happened. It passes every read, the referrer revalidation included, through to
// the source it wraps.
type takeCountingSource struct {
	IndexSource
	mu     sync.Mutex
	taken  int
	stopAt int
	stop   func()
}

func (s *takeCountingSource) Repository(repo string) (oras.ReadOnlyTarget, error) {
	s.mu.Lock()
	s.taken++
	if s.stopAt > 0 && s.taken == s.stopAt {
		s.stop()
	}
	s.mu.Unlock()
	return s.IndexSource.Repository(repo)
}

func (s *takeCountingSource) ConditionalReferrers(ctx context.Context, repo, digest, ifNoneMatch string) (string, bool, error) {
	rv, ok := s.IndexSource.(ReferrerRevalidator)
	if !ok {
		return "", false, nil
	}
	return rv.ConditionalReferrers(ctx, repo, digest, ifNoneMatch)
}

func (s *takeCountingSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taken
}

// TestAStopMidWalkStopsEveryWorkerAndKeepsTheEarlierIndex stops a parallel
// refresh as it takes its third repository and proves the stop reaches every
// worker: no worker takes another repository after it. It counts the
// repositories the workers TAKE rather than the requests the registry receives,
// because a worker that takes a repository after the stop makes requests that
// fail inside the client without reaching the registry — a walk that never looks
// at the stop and one that does leave the same requests on the registry, and only
// the take count tells them apart.
//
// The bound is structural, with no clock in it. At the moment of the stop exactly
// three repositories have been opened, so at most two of them can have finished;
// every other repository taken by then is held by one of the workers, at most one
// each. A worker that honours the stop takes nothing more, so no more than
// 2+indexWalkWorkers repositories are ever taken. A worker that ignores it goes on
// taking every repository there is. The registry holds more repositories than the
// bound, so the two cannot be confused.
//
// It also proves the registry's earlier index is left byte for byte. The control
// runs the same refresh with nobody stopping it and takes every repository,
// which is what makes the short count a measurement of the stop.
func TestAStopMidWalkStopsEveryWorkerAndKeepsTheEarlierIndex(t *testing.T) {
	// Not parallel: it points the process-wide index-target seam.
	const repos = 40
	if repos <= 2+indexWalkWorkers {
		t.Fatalf("the fixture's %d repositories do not exceed the bound %d, so a walk ignoring the stop could pass", repos, 2+indexWalkWorkers)
	}
	f := newRefreshFixture(t, "")
	for i := range repos {
		publish(t, f.reg.repository(t, fmt.Sprintf("component/core/repo-%02d", i)), "1.0.0", fmt.Sprintf("repo %d", i))
	}
	var current *takeCountingSource
	prev := IndexTarget
	IndexTarget = func(cfg workspaceconfig.Config, host string) (IndexSource, error) {
		src, err := prev(cfg, host)
		if err != nil {
			return nil, err
		}
		current.IndexSource = src
		return current, nil
	}
	t.Cleanup(func() { IndexTarget = prev })

	current = &takeCountingSource{}
	f.update(t, UpdateOptions{})
	before := f.files(t)

	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	stopped := &takeCountingSource{stopAt: 3, stop: stop}
	current = stopped
	res, err := Update(ctx, f.rw, UpdateOptions{}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped refresh must report the stop; got %v", err)
	}
	if len(res.Hosts) != 1 || res.Hosts[0].Err == "" {
		t.Errorf("the registry being walked must be reported as not indexed; got %+v", res.Hosts)
	}
	if taken := stopped.count(); taken > 2+indexWalkWorkers {
		t.Errorf("the workers took %d of %d repositories after a stop at the third; at most %d can be taken when every worker honours the stop",
			taken, repos, 2+indexWalkWorkers)
	}
	if after := f.files(t); !maps.Equal(after, before) {
		t.Errorf("the stopped refresh changed the earlier index:\nbefore %v\nafter  %v", before, after)
	}

	control := &takeCountingSource{}
	current = control
	f.update(t, UpdateOptions{})
	if taken := control.count(); taken != repos {
		t.Errorf("the control refresh took %d of %d repositories; without it the short count above is not evidence of a stop", taken, repos)
	}
}
