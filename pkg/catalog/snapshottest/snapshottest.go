// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package snapshottest stands up, for a test, a registry publishing catalog
// snapshots: an in-process OCI registry on loopback that counts every request it
// answers, snapshots published to it through the product's own writer
// (catalog.WriteSnapshot) and publication (oci.PushNamed), signatures attached by
// a key the test holds, and an archive the registry can be told to serve altered.
//
// It exists so every surface that reaches the import operation — the operation
// itself, the command line, the assistant's tool and the editor session — is
// proven against one registry built one way, and a proof that an import reached
// no registry has a count to read rather than an absence to infer.
//
// The registry is the go-containerregistry in-process server, which only tests
// import: nothing in a production package imports this one.
package snapshottest

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Registry is an in-process registry on loopback publishing catalog snapshots.
type Registry struct {
	// Host is the registry's host and port, the value a workspace names as its
	// default registry to fetch from it.
	Host string

	requests atomic.Int64

	mu       sync.Mutex
	archives map[string][]byte // archive digest → the archive's bytes
	tampered bool
	asked    []string // every request answered: its method and path
}

// NewRegistry starts a registry for the rest of the test.
//
// The name is shared, deliberately, with the documentation bundle's test registry
// (docsbundletest.NewRegistry): the two stand up registries for different artifacts
// and different assertions — that one serves a documentation bundle, this one
// publishes catalog snapshots, counts and records requests and can serve an
// archive altered — so neither is a copy of the other.
func NewRegistry(t testing.TB) *Registry {
	t.Helper()
	r := &Registry{archives: map[string][]byte{}}
	// The referrers listing is left to the tag-schema fallback every OCI client
	// falls back to, deliberately: this server's own listing reports a referrer's
	// type as its config's media type rather than as the artifact type the manifest
	// carries, so a signature attached here would be listed under the empty
	// config's type and found by no client asking for signatures.
	inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		r.mu.Lock()
		r.asked = append(r.asked, req.Method+" "+req.URL.Path)
		r.mu.Unlock()
		if r.serveTampered(w, req) {
			return
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r.Host = strings.TrimPrefix(srv.URL, "http://")
	return r
}

// Requests returns how many requests the registry has answered.
func (r *Registry) Requests() int64 { return r.requests.Load() }

// Asked returns every request the registry has answered, as its method and path,
// in the order answered, from the n-th on — so a case reads what one import asked
// by passing the count it read before the import.
func (r *Registry) Asked(n int64) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n < 0 || n > int64(len(r.asked)) {
		n = int64(len(r.asked))
	}
	return append([]string(nil), r.asked[n:]...)
}

// ManifestPath returns the path a request for the snapshot manifest named ref —
// a tag or a digest — is made at, as Asked reports it.
func (r *Registry) ManifestPath(ref string) string {
	return "/v2/" + catalog.SnapshotRepository + "/manifests/" + ref
}

// Reference returns the reference the registry publishes catalog snapshots at.
func (r *Registry) Reference() string { return catalog.SnapshotReference(r.Host) }

// Publish publishes the catalog snapshot of the tree at dir under each tag given,
// through the product's own writer and publication, and returns its manifest
// digest.
func (r *Registry) Publish(t testing.TB, dir string, tags ...string) string {
	t.Helper()
	var archive bytes.Buffer
	if err := catalog.WriteSnapshot(&archive, dir); err != nil {
		t.Fatalf("write the catalog snapshot of %s: %v", dir, err)
	}
	r.mu.Lock()
	r.archives[digest.ContentDigest(archive.Bytes())] = archive.Bytes()
	r.mu.Unlock()
	repo := r.repository(t)
	var manifest string
	for _, tag := range tags {
		dg, err := oci.PushNamed(context.Background(), repo, tag, oci.MediaTypeCatalogSnapshot, "catalog.tar.gz",
			archive.Bytes(), nil)
		if err != nil {
			t.Fatalf("publish the catalog snapshot of %s as %s: %v", dir, tag, err)
		}
		manifest = dg
	}
	return manifest
}

// Sign attaches a signature by signer over the snapshot tagged tag.
func (r *Registry) Sign(t testing.TB, tag string, signer *signing.KeyedSigner) {
	t.Helper()
	repo := r.repository(t)
	dg, _, err := oci.Resolve(context.Background(), repo, tag)
	if err != nil {
		t.Fatalf("resolve the snapshot tagged %s: %v", tag, err)
	}
	sig, artifactType, err := signer.Sign(context.Background(), dg)
	if err != nil {
		t.Fatalf("sign the snapshot tagged %s: %v", tag, err)
	}
	if _, err := oci.AttachReferrer(context.Background(), repo, tag, artifactType, sig); err != nil {
		t.Fatalf("attach the signature of the snapshot tagged %s: %v", tag, err)
	}
}

// Tamper makes the registry serve every archive it published from now on with one
// byte altered, under the digest and size it was published with — the archive a
// registry or a connection that altered it would hand over.
func (r *Registry) Tamper() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tampered = true
}

// serveTampered answers a request for a published archive's bytes with them
// altered, where Tamper was called, and reports whether it answered.
func (r *Registry) serveTampered(w http.ResponseWriter, req *http.Request) bool {
	if req.Method != http.MethodGet {
		return false
	}
	i := strings.LastIndex(req.URL.Path, "/blobs/")
	if i < 0 {
		return false
	}
	r.mu.Lock()
	archive, ok := r.archives[req.URL.Path[i+len("/blobs/"):]]
	tampered := r.tampered
	r.mu.Unlock()
	if !ok || !tampered {
		return false
	}
	altered := append([]byte(nil), archive...)
	altered[len(altered)-1] ^= 0xff
	w.Header().Set("Content-Length", strconv.Itoa(len(altered)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(altered)
	return true
}

// repository returns the snapshot repository on the registry, reached over plain
// HTTP as every loopback registry is, through the client network egress hands an
// online workspace.
func (r *Registry) repository(t testing.TB) *remote.Repository {
	t.Helper()
	client, err := egress.HTTPClient(workspaceconfig.Config{})
	if err != nil {
		t.Fatalf("obtain a network client for the snapshot repository %s: %v", r.Reference(), err)
	}
	repo, err := oci.NewRemoteRepository(r.Reference(), client)
	if err != nil {
		t.Fatalf("reach the snapshot repository %s: %v", r.Reference(), err)
	}
	return repo
}

// NewKey returns a fresh signing key: the signer, and the public key a
// workspace's registries entry pins to trust it.
func NewKey(t testing.TB) (*signing.KeyedSigner, []byte) {
	t.Helper()
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("load the signing key: %v", err)
	}
	return signer, signer.PublicKeyPEM()
}

// WorkspaceConfiguration returns the workspace configuration document naming the
// registry as the default one and trusting signatures by the key whose public
// half is publicKey there, requiring one of every artifact — offline where
// offline is set. It names no catalog folder, so an import in the workspace reads
// the registry's catalog snapshot.
func (r *Registry) WorkspaceConfiguration(publicKey []byte, offline bool) string {
	var b strings.Builder
	b.WriteString("workspace: {}\n")
	b.WriteString("default_registry: " + strconv.Quote(r.Host) + "\n")
	if offline {
		b.WriteString("offline: true\n")
	}
	b.WriteString("registries:\n")
	b.WriteString("  - host: " + strconv.Quote(r.Host) + "\n")
	b.WriteString("    public_key: " + strconv.Quote(string(publicKey)) + "\n")
	b.WriteString("    signing:\n      required: true\n")
	return b.String()
}
