// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package livetrust_test

// The trust root an offline read verifies against, and where it comes from.
//
// Offline, a keyless check that pins no trust root of its own cannot obtain the
// live public root, and a read of a kept signature verifies instead against the
// one an earlier online fetch left in the TUF client's cache. These cases pin the
// three things that rests on: the file named is the one the real client writes;
// online nothing changes; and offline nothing is fetched — a machine holding no
// usable trust root is refused, saying the check could not run offline.
//
// No case reads or writes the developer's own cache: each states a directory of
// its own, and the one case that runs the real client runs it against a
// repository it serves itself.

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// trustedRootJSON returns a trust-root document of the shape the public one
// takes, made by in-memory authorities so nothing is contacted.
func trustedRootJSON(t *testing.T) []byte {
	t.Helper()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	doc, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	return doc
}

// holding returns a fresh client cache directory holding doc where the client
// keeps the trust root it last fetched — what an earlier online fetch leaves.
func holding(t *testing.T, doc []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := livetrust.CachedTrustedRootPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create the cache layout: %v", err)
	}
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		t.Fatalf("write the held trust root: %v", err)
	}
	return dir
}

// tufRepository builds a one-key TUF repository publishing target as the trust
// root, laid out the way the public repository is (consistent snapshots, targets
// fetched under their hash), and returns its root metadata and the files it
// serves by request path.
func tufRepository(t *testing.T, target []byte) (rootJSON []byte, files map[string][]byte) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate the repository key: %v", err)
	}
	key, err := metadata.KeyFromPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("convert the repository key: %v", err)
	}
	signer, err := signature.LoadSigner(priv, crypto.Hash(0))
	if err != nil {
		t.Fatalf("load the repository signer: %v", err)
	}
	expires := time.Now().UTC().Add(time.Hour)

	rootMD := metadata.Root(expires)
	for _, role := range []string{metadata.ROOT, metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS} {
		if err := rootMD.Signed.AddKey(key, role); err != nil {
			t.Fatalf("add the key to %s: %v", role, err)
		}
	}
	targetsMD := metadata.Targets(expires)
	info, err := metadata.TargetFile().FromBytes("trusted_root.json", target, "sha256")
	if err != nil {
		t.Fatalf("describe the target: %v", err)
	}
	targetsMD.Signed.Targets["trusted_root.json"] = info
	snapshotMD := metadata.Snapshot(expires)
	timestampMD := metadata.Timestamp(expires)

	files = map[string][]byte{}
	sign := func(name string, md interface {
		Sign(signature.Signer) (*metadata.Signature, error)
		ToBytes(bool) ([]byte, error)
	}) []byte {
		if _, err := md.Sign(signer); err != nil {
			t.Fatalf("sign %s: %v", name, err)
		}
		b, err := md.ToBytes(false)
		if err != nil {
			t.Fatalf("serialize %s: %v", name, err)
		}
		return b
	}
	rootJSON = sign("root", rootMD)
	files["/timestamp.json"] = sign("timestamp", timestampMD)
	files["/1.snapshot.json"] = sign("snapshot", snapshotMD)
	files["/1.targets.json"] = sign("targets", targetsMD)
	sum := sha256.Sum256(target)
	files["/targets/"+hex.EncodeToString(sum[:])+".trusted_root.json"] = target
	return rootJSON, files
}

// TestTheTrustRootIsReadWhereAnEarlierFetchLeftIt runs the real TUF client the
// live fetch uses against a repository this case serves, into a cache directory
// of the case's own, and pins that the file the offline read takes is the one
// that fetch wrote — and that the production location names the repository the
// live fetch reads from. A read looking anywhere else would find nothing on a
// machine that had fetched a trust root, and be refused for want of one.
func TestTheTrustRootIsReadWhereAnEarlierFetchLeftIt(t *testing.T) {
	t.Parallel()
	doc := trustedRootJSON(t)
	rootJSON, files := tufRepository(t, doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	cacheDir := t.TempDir()
	if _, err := root.FetchTrustedRootWithOptions(&tuf.Options{
		Root:              rootJSON,
		CachePath:         cacheDir,
		RepositoryBaseURL: srv.URL,
	}); err != nil {
		t.Fatalf("the client's fetch from the served repository: %v", err)
	}

	held, err := os.ReadFile(livetrust.CachedTrustedRootPathFor(cacheDir, srv.URL))
	if err != nil {
		t.Fatalf("the fetch left no trust root where the offline read looks: %v", err)
	}
	if string(held) != string(doc) {
		t.Error("the file where the offline read looks is not the trust root the fetch obtained")
	}
	if got, want := livetrust.CachedTrustedRootPath(cacheDir), livetrust.CachedTrustedRootPathFor(cacheDir, tuf.DefaultOptions().RepositoryBaseURL); got != want {
		t.Errorf("the offline read looks at %s, but the live fetch keeps what it fetched at %s", got, want)
	}
}

// TestOfflineTheReadIsGivenTheTrustRootLastFetched is the offline half: the read
// is handed the file an earlier fetch left, and not the live fetch it cannot make.
func TestOfflineTheReadIsGivenTheTrustRootLastFetched(t *testing.T) {
	t.Parallel()
	dir := holding(t, trustedRootJSON(t))
	got, err := livetrust.LastFetchedRootFile(workspaceconfig.Config{Offline: true}, dir)
	if err != nil {
		t.Fatalf("offline, with a trust root held, the read was refused: %v", err)
	}
	if want := livetrust.CachedTrustedRootPath(dir); got != want {
		t.Errorf("offline, the read was handed %q, want the held trust root %q", got, want)
	}
}

// TestOnlineTheReadObtainsTheLiveRootAsBefore is the other half: online the read
// is handed nothing, so it obtains the live trust root exactly as it did — a trust
// root held on the machine changes nothing while the network can be reached.
func TestOnlineTheReadObtainsTheLiveRootAsBefore(t *testing.T) {
	t.Parallel()
	dir := holding(t, trustedRootJSON(t))
	got, err := livetrust.LastFetchedRootFile(workspaceconfig.Config{}, dir)
	if err != nil || got != "" {
		t.Errorf("online, the read was handed (%q, %v), want nothing, so it obtains the live root", got, err)
	}
}

// TestOfflineAMachineHoldingNoTrustRootIsRefused pins each way an offline read
// finds nothing usable to verify against. Each is refused as the offline
// condition, saying the check could not run offline and why, and naming no
// filesystem path. The last row is the default location inside a test binary,
// which resolves to none: a case that states no cache directory never reads the
// developer's.
func TestOfflineAMachineHoldingNoTrustRootIsRefused(t *testing.T) {
	t.Parallel()
	notATrustRoot := holding(t, []byte("this is not a trust root"))
	for _, tc := range []struct {
		name, cacheDir, why string
	}{
		{"nothing fetched", t.TempDir(), "none has been fetched here"},
		{"what is held is not a trust root", notATrustRoot, "does not read as a trust root"},
		{"no cache directory stated in a test binary", "", "no cache directory was stated for this run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := livetrust.LastFetchedRootFile(workspaceconfig.Config{Offline: true}, tc.cacheDir)
			if err == nil {
				t.Fatalf("offline, the read was handed %q with nothing usable held", got)
			}
			if !errors.Is(err, egress.ErrOfflineEgress()) {
				t.Errorf("the refusal is not the offline condition: %v", err)
			}
			for _, want := range []string{"could not run offline", tc.why} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if tc.cacheDir != "" && strings.Contains(err.Error(), tc.cacheDir) {
				t.Errorf("the refusal names the cache directory, which is not reported to every reader: %v", err)
			}
		})
	}
}

// TestTheOfflineReadBuildsNothingThatCouldFetch is the "never fetched" guarantee
// as structure rather than as an outcome. Everything the offline read runs —
// LastFetchedRootFile and each function of this package it calls, followed through
// — is read from source, and none of it may reach the live fetch, build a TUF
// client, ask a client for a target, or obtain a transport. An outcome test cannot
// say this alone: a fetch attempted in a sandbox with no network fails, and the
// refusal it produces looks like the one a never-fetching read gives.
func TestTheOfflineReadBuildsNothingThatCouldFetch(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "livetrust.go", nil, 0)
	if err != nil {
		t.Fatalf("parse the package source: %v", err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil {
			funcs[fd.Name.Name] = fd
		}
	}
	fetching := map[string]bool{}
	for _, q := range []string{
		"tuf.New",
		"tuf.DefaultClient",
		"root.NewLiveTrustedRoot",
		"root.NewLiveTrustedRootFromTarget",
		"root.NewLiveTrustedRootFromTargetWithPeriod",
		"root.FetchTrustedRoot",
		"root.FetchTrustedRootWithOptions",
		"root.GetTrustedRoot",
		"egress.HTTPClient",
		"egress.RoundTripper",
	} {
		fetching[q] = true
	}
	reached := map[string]bool{}
	var follow func(name string)
	follow = func(name string) {
		fd, ok := funcs[name]
		if !ok || reached[name] {
			return
		}
		reached[name] = true
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := v.X.(*ast.Ident); ok {
					if q := pkg.Name + "." + v.Sel.Name; fetching[q] || pkg.Name == "http" || pkg.Name == "net" {
						t.Errorf("the offline read reaches %s (in %s), which can fetch", q, name)
					}
				}
			case *ast.Ident:
				if v.Name == "TrustedRoot" {
					t.Errorf("the offline read reaches the live fetch, TrustedRoot (in %s)", name)
				}
				follow(v.Name)
			}
			return true
		})
	}
	follow("LastFetchedRootFile")
	// A walk that stopped short would vouch for less than the read runs.
	for _, want := range []string{"heldRootFile", "clientCacheDir", "CachedTrustedRootPath", "cachedTrustedRootPath"} {
		if !reached[want] {
			t.Fatalf("the walk never reached %s, which the offline read runs, so it proves nothing about it", want)
		}
	}
}
