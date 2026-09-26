// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// An authorization server, in process.
//
// It is a real HTTP server on a real address: it publishes what it is at the
// well-known path an authorization server publishes at, serves a key set at the
// address that document names, and mints tokens whose claims each proof
// chooses. What it is NOT is a real authorization server — nothing here issues
// a token to a client, and no flow of any kind is driven — because what is
// under test is this deployment's side: whether a token that arrives is
// accepted, and what a caller is told when it is not.
//
// It counts its own requests, per document, so a proof about how often the keys
// are read asserts on the server that served them rather than on anything
// inside the deployment.

// keySetPath is where the double publishes its keys. It is deliberately not a
// well-known path and not derived from the issuer: the whole point of asking an
// authorization server where its keys are is that it decides, so a proof that
// reads them from a path this deployment could have guessed would prove less.
const keySetPath = "/keys/current"

// authorizationServerDouble is one running authorization server.
type authorizationServerDouble struct {
	ts *httptest.Server

	// signer mints the tokens the proofs present, and signingKey is the private
	// half. The public half is what the key set publishes.
	signingKey *ecdsa.PrivateKey
	keyID      string

	// published is the key set served at keySetPath. A proof that needs a key
	// the set does not carry signs with a key that is not in here.
	published jose.JSONWebKeySet

	// metadataReads and keySetReads count what the deployment asked for. They
	// are read from the test's own goroutine after the requests that moved them
	// have been answered, and they are atomic because the requests that move
	// them arrive on the server's goroutines.
	metadataReads atomic.Int64
	keySetReads   atomic.Int64

	// keysAt, where a proof has set it, is the address the metadata document
	// names for the keys in place of this server's own key set. It is for the
	// proofs about an authorization server that points the key read somewhere a
	// listener must not read it from, and it is atomic because a proof moves it
	// while the server's goroutines may be reading it.
	keysAt atomic.Pointer[string]
}

// publishKeysAt makes the metadata document name address for the keys from the
// next read on.
func (d *authorizationServerDouble) publishKeysAt(address string) { d.keysAt.Store(&address) }

// newAuthorizationServerDouble starts one, with a single signing key published
// under keyID.
func newAuthorizationServerDouble(t *testing.T) *authorizationServerDouble {
	t.Helper()
	d := &authorizationServerDouble{keyID: "the-signing-key"}
	d.signingKey = newSigningKey(t)
	d.published = jose.JSONWebKeySet{Keys: []jose.JSONWebKey{publicJWK(d.signingKey, d.keyID)}}

	mux := http.NewServeMux()
	mux.HandleFunc(wellKnownAuthorizationServer, func(w http.ResponseWriter, _ *http.Request) {
		d.metadataReads.Add(1)
		keysAt := d.ts.URL + keySetPath
		if moved := d.keysAt.Load(); moved != nil {
			keysAt = *moved
		}
		writeJSON(w, map[string]any{
			"issuer":   d.issuer(),
			"jwks_uri": keysAt,
		})
	})
	mux.HandleFunc(keySetPath, func(w http.ResponseWriter, _ *http.Request) {
		d.keySetReads.Add(1)
		writeJSON(w, d.published)
	})
	d.ts = httptest.NewServer(mux)
	t.Cleanup(d.ts.Close)
	return d
}

// issuer is the identifier this server states for itself, which is what a
// deployment configures as mcp.authorization.issuer.
func (d *authorizationServerDouble) issuer() string { return d.ts.URL }

// egress is the gate a deployment reaches this server through. It counts
// nothing: what a proof asserts on is the requests that arrived here.
func (d *authorizationServerDouble) egress() NetworkEgress {
	return func() (*http.Client, error) { return d.ts.Client(), nil }
}

// authorizationConfig is what a deployment declares to accept this server's
// tokens: this server as the issuer, and the address every proof in this
// package publishes for itself.
func (d *authorizationServerDouble) authorizationConfig() AuthorizationConfig {
	return AuthorizationConfig{Issuer: d.issuer(), Resource: testResource}
}

// keySetFile writes what this server publishes to a file, for the proofs about
// a deployment that reads its keys from disk rather than from the network.
func (d *authorizationServerDouble) keySetFile(t *testing.T) string {
	t.Helper()
	return writeKeySetFile(t, d.published)
}

// tokenClaimSet is one token's claims, as a proof chooses them. The zero value
// is filled in by mint with what a valid token for this deployment carries, so
// each proof states only the thing it is changing.
type tokenClaimSet struct {
	issuer    string
	subject   string
	audience  []string
	notBefore time.Time
	expiry    time.Time

	// scope and scopeList are the two spellings an authorization server
	// publishes the granted scopes under.
	scope     string
	scopeList []string
	clientID  string
	azp       string

	// tokenType is what the token declares itself to be. Empty declares
	// nothing, which is a token type this deployment accepts.
	tokenType string

	// noSubject leaves the subject claim off entirely. It is a flag rather than
	// an empty subject because an empty one is what the zero value means, and
	// the zero value is filled in with a subject: a token asserting nothing
	// about who presented it has to be asked for.
	noSubject bool
}

// mint signs one token, filling every claim the proof left alone with what a
// token this deployment accepts carries.
//
// Signing with the double's own key under its own published identifier is the
// default; signWith and keyID override each half, for the proofs about a
// signature this deployment cannot verify.
func (d *authorizationServerDouble) mint(t *testing.T, claims tokenClaimSet) string {
	t.Helper()
	return d.mintSignedBy(t, claims, d.signingKey, d.keyID)
}

// mintSignedBy is mint with the signing key and the key identifier chosen, so a
// proof can present a token signed by a key the published set does not carry,
// and one naming a key identifier it has never heard of.
func (d *authorizationServerDouble) mintSignedBy(t *testing.T, claims tokenClaimSet,
	key *ecdsa.PrivateKey, keyID string) string {
	t.Helper()
	if claims.issuer == "" {
		claims.issuer = d.issuer()
	}
	if claims.subject == "" && !claims.noSubject {
		claims.subject = testSubject
	}
	if claims.audience == nil {
		claims.audience = []string{testResource}
	}
	if claims.expiry.IsZero() {
		claims.expiry = time.Now().Add(time.Hour)
	}

	headers := map[jose.HeaderKey]any{}
	if claims.tokenType != "" {
		headers[jose.HeaderType] = claims.tokenType
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{ExtraHeaders: headers}).WithHeader("kid", keyID),
	)
	if err != nil {
		t.Fatalf("build a signer: %v", err)
	}

	registered := jwt.Claims{
		Issuer:   claims.issuer,
		Subject:  claims.subject,
		Audience: claims.audience,
		Expiry:   jwt.NewNumericDate(claims.expiry),
	}
	if !claims.notBefore.IsZero() {
		registered.NotBefore = jwt.NewNumericDate(claims.notBefore)
	}
	extra := map[string]any{}
	if claims.scope != "" {
		extra["scope"] = claims.scope
	}
	if claims.scopeList != nil {
		extra["scp"] = claims.scopeList
	}
	if claims.clientID != "" {
		extra["client_id"] = claims.clientID
	}
	if claims.azp != "" {
		extra["azp"] = claims.azp
	}

	raw, err := jwt.Signed(signer).Claims(registered).Claims(extra).Serialize()
	if err != nil {
		t.Fatalf("mint a token: %v", err)
	}
	return raw
}

// testSubject is who every token these proofs present says presented it. It is
// compared to nothing by this deployment — which is the point — so it exists
// only so the proofs can read back what was recorded.
const testSubject = "caller@example.test"

// newSigningKey produces one signing key. ES256 rather than RSA because it is
// asymmetric, which is what this deployment requires, and small enough to
// generate once per proof without the generation showing up as wall clock.
func newSigningKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}
	return key
}

// publicJWK is the published half of a signing key, under the identifier a
// token naming it would carry.
func publicJWK(key *ecdsa.PrivateKey, keyID string) jose.JSONWebKey {
	return jose.JSONWebKey{Key: key.Public(), KeyID: keyID, Algorithm: string(jose.ES256), Use: "sig"}
}

// writeKeySetFile writes a key set where a deployment configured with
// mcp.authorization.jwks_file would read it.
func writeKeySetFile(t *testing.T, set jose.JSONWebKeySet) string {
	t.Helper()
	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal a key set: %v", err)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write a key set: %v", err)
	}
	return path
}

// writeJSON answers one document from the double.
func writeJSON(w http.ResponseWriter, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	_, _ = w.Write(raw)
}

// refusingEgress is a gate that permits no outbound request, which is what an
// offline workspace's gate does. The reason it carries is the one a reader
// meets, so a proof can show the refusal reaching the listener's own message.
func refusingEgress(reason error) NetworkEgress {
	return func() (*http.Client, error) { return nil, reason }
}

// countingEgress is a gate that opens and records having been asked. It is what
// the offline proofs assert on: a deployment reading its keys from a file never
// asks, and the count is how that is read rather than inferred.
type countingEgress struct {
	asked atomic.Int64
}

func (c *countingEgress) gate() NetworkEgress {
	return func() (*http.Client, error) {
		c.asked.Add(1)
		return http.DefaultClient, nil
	}
}
