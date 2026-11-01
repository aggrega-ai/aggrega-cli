// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package livetrusttest serves a TUF repository publishing a trust root of a
// test's choosing, and points every live trust-root fetch of the test binary at
// it, so a test drives the live fetch — through every caller that reaches it —
// without reaching the public repository or writing this machine's cache.
//
// The repository is laid out the way the public one is (consistent snapshots,
// targets fetched under their hash), and its root has been rotated once: a client
// starts from root version 1 and the repository serves version 2. The public
// repository's root has moved on from the one a client library carries in the
// same way, which is what makes two clients refreshing one cache directory at once
// write different bytes to it.
//
// It is a non-test package only because several packages' tests share it.
package livetrusttest

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
)

// Repository is a served TUF repository every live trust-root fetch of the test
// binary reads.
type Repository struct {
	initialRoot []byte
	url         string
	fetches     atomic.Int64
}

// Serve builds and serves a repository publishing trustedRoot as the public trust
// root, and points every live fetch of the test binary at it, keeping what each
// fetch obtains in a cache directory of the test's own, for the rest of tb. A test
// calling it must not run in parallel with another test that fetches the live
// root.
func Serve(tb testing.TB, trustedRoot []byte) *Repository {
	tb.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatalf("generate the repository key: %v", err)
	}
	key, err := metadata.KeyFromPublicKey(priv.Public())
	if err != nil {
		tb.Fatalf("convert the repository key: %v", err)
	}
	signer, err := signature.LoadSigner(priv, crypto.Hash(0))
	if err != nil {
		tb.Fatalf("load the repository signer: %v", err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	sign := func(name string, md interface {
		Sign(signature.Signer) (*metadata.Signature, error)
		ToBytes(bool) ([]byte, error)
	}) []byte {
		if _, err := md.Sign(signer); err != nil {
			tb.Fatalf("sign %s: %v", name, err)
		}
		b, err := md.ToBytes(false)
		if err != nil {
			tb.Fatalf("serialize %s: %v", name, err)
		}
		return b
	}
	rootAt := func(version int64) []byte {
		md := metadata.Root(expires)
		md.Signed.Version = version
		for _, role := range []string{metadata.ROOT, metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS} {
			if err := md.Signed.AddKey(key, role); err != nil {
				tb.Fatalf("add the key to %s: %v", role, err)
			}
		}
		return sign("root", md)
	}
	targets := metadata.Targets(expires)
	info, err := metadata.TargetFile().FromBytes("trusted_root.json", trustedRoot, "sha256")
	if err != nil {
		tb.Fatalf("describe the trust root: %v", err)
	}
	targets.Signed.Targets["trusted_root.json"] = info
	sum := sha256.Sum256(trustedRoot)
	files := map[string][]byte{
		"/2.root.json":     rootAt(2),
		"/timestamp.json":  sign("timestamp", metadata.Timestamp(expires)),
		"/1.snapshot.json": sign("snapshot", metadata.Snapshot(expires)),
		"/1.targets.json":  sign("targets", targets),
		"/targets/" + hex.EncodeToString(sum[:]) + ".trusted_root.json": trustedRoot,
	}

	r := &Repository{initialRoot: rootAt(1)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Every fetch that gets past setting up its client reads the timestamp, and
		// reads it once, so its reads count the fetches.
		if req.URL.Path == "/timestamp.json" {
			r.fetches.Add(1)
		}
		body, ok := files[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	tb.Cleanup(srv.Close)
	r.url = srv.URL
	r.FreshCache(tb)
	return r
}

// FreshCache points every live fetch at a new, empty cache directory, as a
// machine that has never fetched the trust root has.
func (r *Repository) FreshCache(tb testing.TB) {
	tb.Helper()
	livetrust.UseTestRepository(tb, r.initialRoot, r.url, tb.TempDir())
}

// Fetches returns how many live fetches have read the repository so far.
func (r *Repository) Fetches() int {
	return int(r.fetches.Load())
}
