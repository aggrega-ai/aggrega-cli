// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/digitorus/timestamp"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// keylessServiceLiveness is a LIVENESS CEILING, not a performance budget: it
// separates a signature that returns from one that never does. The waits the
// signing library itself takes between attempts are seconds long, and nothing here
// measures them.
const keylessServiceLiveness = 2 * time.Minute

// attemptBudget is how often each signing service is asked for one request at
// most: the first attempt and two retries.
const attemptBudget = 3

// leastWaits are the waits every signing service's client takes before its second
// and its third attempt, stated here independently of the signer so the test
// holds the signer to them rather than reading them back from it.
//
// They are asserted as LOWER BOUNDS on the gap between two requests a stand-in
// received — an ordering check, not a performance budget. The gap is the client's
// own timer plus the time the exchange took; a timer never fires early and a busy
// machine only lengthens the exchange, so load cannot make the check fail, while a
// client that does not wait shows a gap far below the bound.
var leastWaits = []time.Duration{time.Second, 2 * time.Second}

// limitedService is a loopback stand-in for one signing service. It answers its
// first `limited` requests with `status` — every request, where limited is
// negative — and every later one with its ordinary answer, and it records when
// each request arrived. It is safe for concurrent use.
type limitedService struct {
	name    string
	status  int
	limited int
	answer  func(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte)

	mu       sync.Mutex
	arrivals []time.Time
}

func (s *limitedService) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("%s: read the request: %v", s.name, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.arrivals = append(s.arrivals, time.Now())
		n := len(s.arrivals)
		s.mu.Unlock()
		if s.limited < 0 || n <= s.limited {
			w.WriteHeader(s.status)
			return
		}
		s.answer(t, w, r, body)
	})
}

func (s *limitedService) received() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.arrivals)
}

// assertWaited holds the gaps between the requests the stand-in received to
// leastWaits: the n-th retry arrived at least the n-th wait after the attempt
// before it.
func (s *limitedService) assertWaited(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 1; i < len(s.arrivals) && i <= len(leastWaits); i++ {
		if gap := s.arrivals[i].Sub(s.arrivals[i-1]); gap < leastWaits[i-1] {
			t.Errorf("the %s was asked again %v after the previous attempt; its client must wait at least %v first",
				s.name, gap, leastWaits[i-1])
		}
	}
}

// certificateAuthority answers a signing-certificate request the way the public
// certificate authority does: a certificate for the public key the request
// carries, issued by an in-memory intermediate, with the chain behind it.
func certificateAuthority(t *testing.T) func(*testing.T, http.ResponseWriter, *http.Request, []byte) {
	t.Helper()
	rootCert, rootKey, err := ca.GenerateRootCa()
	if err != nil {
		t.Fatalf("GenerateRootCa: %v", err)
	}
	interCert, interKey, err := ca.GenerateFulcioIntermediate(rootCert, rootKey)
	if err != nil {
		t.Fatalf("GenerateFulcioIntermediate: %v", err)
	}
	return func(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte) {
		if r.URL.Path != "/api/v2/signingCert" {
			t.Errorf("certificate authority: unexpected path %s", r.URL.Path)
		}
		var req struct {
			PublicKeyRequest struct {
				PublicKey struct {
					Content string `json:"content"`
				} `json:"publicKey"`
			} `json:"publicKeyRequest"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("certificate authority: decode the request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		block, _ := pem.Decode([]byte(req.PublicKeyRequest.PublicKey.Content))
		if block == nil {
			t.Error("certificate authority: the request carries no public key")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			t.Errorf("certificate authority: parse the public key: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
		leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			SerialNumber:   serial,
			EmailAddresses: []string{"dev@example.com"},
			NotBefore:      time.Now().Add(-time.Minute),
			NotAfter:       time.Now().Add(10 * time.Minute),
			KeyUsage:       x509.KeyUsageDigitalSignature,
			ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
			ExtraExtensions: []pkix.Extension{{
				Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}, Value: []byte("https://issuer.example.test"),
			}},
		}, interCert, pub, interKey)
		if err != nil {
			t.Errorf("certificate authority: issue the certificate: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		chain := []string{
			string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
			string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interCert.Raw})),
			string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCert.Raw})),
		}
		answer := map[string]any{"signedCertificateEmbeddedSct": map[string]any{"chain": map[string]any{"certificates": chain}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	}
}

// timestampAuthority answers an RFC 3161 request with a granted time-stamp over
// the hash the request carries, signed by an in-memory time-stamping key.
func timestampAuthority(t *testing.T) func(*testing.T, http.ResponseWriter, *http.Request, []byte) {
	t.Helper()
	rootCert, rootKey, err := ca.GenerateRootCa()
	if err != nil {
		t.Fatalf("GenerateRootCa: %v", err)
	}
	tsaCert, tsaKey, err := ca.GenerateTSAIntermediate(rootCert, rootKey)
	if err != nil {
		t.Fatalf("GenerateTSAIntermediate: %v", err)
	}
	return func(t *testing.T, w http.ResponseWriter, _ *http.Request, body []byte) {
		req, err := timestamp.ParseRequest(body)
		if err != nil {
			t.Errorf("timestamp authority: parse the request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		stamp := timestamp.Timestamp{
			HashAlgorithm: req.HashAlgorithm,
			HashedMessage: req.HashedMessage,
			Time:          time.Now(),
			Policy:        asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 2},
			Nonce:         req.Nonce,
		}
		resp, err := stamp.CreateResponseWithOpts(tsaCert, tsaKey, req.HashAlgorithm)
		if err != nil {
			t.Errorf("timestamp authority: create the response: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/timestamp-reply")
		_, _ = w.Write(resp)
	}
}

// transparencyLog answers a log-entry upload the way the public transparency log
// does: the entry it was sent, recorded at an index, with an inclusion proof and a
// signed entry timestamp from an in-memory log key.
func transparencyLog(t *testing.T) func(*testing.T, http.ResponseWriter, *http.Request, []byte) {
	t.Helper()
	virtual, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	logID, err := virtual.RekorLogID()
	if err != nil {
		t.Fatalf("RekorLogID: %v", err)
	}
	return func(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte) {
		if r.URL.Path != "/api/v1/log/entries" {
			t.Errorf("transparency log: unexpected path %s", r.URL.Path)
		}
		proof, err := virtual.GetInclusionProof(body)
		if err != nil {
			t.Errorf("transparency log: inclusion proof: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		encoded := base64.StdEncoding.EncodeToString(body)
		integrated := time.Now().Unix()
		set, err := virtual.RekorSignPayload(tlog.RekorPayload{
			Body: encoded, IntegratedTime: integrated, LogIndex: 0, LogID: logID,
		})
		if err != nil {
			t.Errorf("transparency log: signed entry timestamp: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		sum := sha256.Sum256(body)
		uuid := hex.EncodeToString(sum[:])
		answer := map[string]any{uuid: map[string]any{
			"body":           encoded,
			"integratedTime": integrated,
			"logID":          logID,
			"logIndex":       0,
			"verification": map[string]any{
				"inclusionProof":       proof,
				"signedEntryTimestamp": base64.StdEncoding.EncodeToString(set),
			},
		}}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", uuid)
		w.Header().Set("Location", "http://"+r.Host+"/api/v1/log/entries/"+uuid)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(answer)
	}
}

// keylessAgainst is a keyless signer whose three services are the given
// stand-ins, served on loopback, with a crafted identity token and an online
// workspace configuration, so the egress gate hands the certificate authority and
// timestamp authority clients its ordinary transport.
func keylessAgainst(t *testing.T, fulcio, tsa, rekor *limitedService) *signing.KeylessSigner {
	t.Helper()
	serve := func(s *limitedService) string {
		srv := httptest.NewServer(s.handler(t))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	token := signingtest.SubjectfulIDToken(t)
	return &signing.KeylessSigner{
		TokenSource:  func(context.Context) (string, error) { return token, nil },
		FulcioURL:    serve(fulcio),
		RekorURL:     serve(rekor),
		TimestampURL: serve(tsa) + "/api/v1/timestamp",
		Config:       workspaceconfig.Config{},
	}
}

// TestALimitedSigningServiceIsAskedAgainAndTheSignatureCompletes signs through
// loopback stand-ins for the certificate authority, the timestamp authority and
// the transparency log, each of which answers its first request with a
// rate-limited (429) or server-error (503) status and its ordinary answer after.
// Each stand-in must receive the request again, no sooner than a second after the
// first, and the signature must complete with a bundle carrying the certificate,
// the time-stamp and the log entry.
func TestALimitedSigningServiceIsAskedAgainAndTheSignatureCompletes(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			fulcio := &limitedService{name: "certificate authority", status: status, limited: 1, answer: certificateAuthority(t)}
			tsa := &limitedService{name: "timestamp authority", status: status, limited: 1, answer: timestampAuthority(t)}
			rekor := &limitedService{name: "transparency log", status: status, limited: 1, answer: transparencyLog(t)}
			signer := keylessAgainst(t, fulcio, tsa, rekor)

			ctx, cancel := context.WithTimeout(context.Background(), keylessServiceLiveness)
			defer cancel()
			out, artifactType, err := signer.Sign(ctx, "sha256:"+hex.EncodeToString(make([]byte, 32)))

			for _, s := range []*limitedService{fulcio, tsa, rekor} {
				if got := s.received(); got != 2 {
					t.Errorf("the %s received %d request(s); want 2 — the limited one and the one asked again", s.name, got)
				}
				s.assertWaited(t)
			}
			if err != nil {
				t.Fatalf("the signature did not complete: %v", err)
			}
			if artifactType != signing.BundleMediaType {
				t.Errorf("artifact type %q; want %q", artifactType, signing.BundleMediaType)
			}
			b, err := signing.LoadBundle(out)
			if err != nil {
				t.Fatalf("the signature is not a bundle: %v", err)
			}
			vc, err := b.VerificationContent()
			if err != nil || vc.Certificate() == nil {
				t.Errorf("the bundle carries no signing certificate (err %v)", err)
			}
			if stamps, err := b.Timestamps(); err != nil || len(stamps) != 1 {
				t.Errorf("the bundle carries %d time-stamp(s) (err %v); want 1", len(stamps), err)
			}
			entries, err := b.TlogEntries()
			if err != nil || len(entries) != 1 || !entries[0].HasInclusionProof() {
				t.Errorf("the bundle carries %d log entr(ies) (err %v); want 1 with its inclusion proof", len(entries), err)
			}
		})
	}
}

// TestALimitedSigningServiceIsAskedAtMostThreeTimes keeps one service limited on
// every request while the ones before it answer ordinarily, and requires that
// service to be asked exactly three times — the first attempt and two retries,
// the first no sooner than 1 s and the second no sooner than 2 s after the attempt
// before it — and the signature to fail. Every service after it is never reached.
func TestALimitedSigningServiceIsAskedAtMostThreeTimes(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name                      string
		fulcioLim, tsaLim, rekLim int
		want                      [3]int
	}{
		{"the certificate authority", -1, 0, 0, [3]int{attemptBudget, 0, 0}},
		{"the timestamp authority", 0, -1, 0, [3]int{1, attemptBudget, 0}},
		{"the transparency log", 0, 0, -1, [3]int{1, 1, attemptBudget}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			limited := http.StatusTooManyRequests
			fulcio := &limitedService{name: "certificate authority", status: limited, limited: row.fulcioLim, answer: certificateAuthority(t)}
			tsa := &limitedService{name: "timestamp authority", status: limited, limited: row.tsaLim, answer: timestampAuthority(t)}
			rekor := &limitedService{name: "transparency log", status: limited, limited: row.rekLim, answer: transparencyLog(t)}
			signer := keylessAgainst(t, fulcio, tsa, rekor)

			ctx, cancel := context.WithTimeout(context.Background(), keylessServiceLiveness)
			defer cancel()
			_, _, err := signer.Sign(ctx, "sha256:"+hex.EncodeToString(make([]byte, 32)))
			if err == nil {
				t.Error("a service limited on every request let the signature complete")
			}
			for i, s := range []*limitedService{fulcio, tsa, rekor} {
				if got := s.received(); got != row.want[i] {
					t.Errorf("the %s received %d request(s); want %d", s.name, got, row.want[i])
				}
				s.assertWaited(t)
			}
		})
	}
}
