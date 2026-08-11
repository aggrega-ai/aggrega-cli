// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// craftJWT builds a syntactically valid JWT whose payload is the given JSON
// claims. The signature segment is a fixed placeholder: the pre-sign guard
// decodes the unverified payload and never checks the signature, so a real one
// is unnecessary.
func craftJWT(t *testing.T, claimsJSON string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claimsJSON))
	return header + "." + payload + ".sig"
}

// subjectfulIDToken returns a crafted JWT carrying a verified email, so it
// passes the pre-sign subject guard. Tests that exercise signer construction
// rather than the guard itself use it to supply a structurally usable token.
func subjectfulIDToken(t *testing.T) string {
	t.Helper()
	return craftJWT(t, `{"email":"dev@example.com","email_verified":true}`)
}

// TestCheckIDTokenSubject covers the pre-sign subject guard against crafted
// JWTs (no live OIDC). The guard mirrors the certificate authority's subject
// derivation exactly: a verified email is a subject; otherwise a non-empty sub
// is a subject; an unverified email or neither claim has no subject.
func TestCheckIDTokenSubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		token       string
		wantErr     bool
		wantSubstrs []string
	}{
		{
			name:    "verified email passes",
			token:   craftJWT(t, `{"email":"dev@example.com","email_verified":true}`),
			wantErr: false,
		},
		{
			name:    "only sub passes",
			token:   craftJWT(t, `{"sub":"workflow-identity-123"}`),
			wantErr: false,
		},
		{
			name:    "email_verified as string true passes",
			token:   craftJWT(t, `{"email":"dev@example.com","email_verified":"true"}`),
			wantErr: false,
		},
		{
			name:        "neither email nor sub fails precisely",
			token:       craftJWT(t, `{"aud":"sigstore","iss":"https://example.com"}`),
			wantErr:     true,
			wantSubstrs: []string{"no verified email and no sub claim", "OIDC flow/client"},
		},
		{
			name:        "unverified email fails",
			token:       craftJWT(t, `{"email":"dev@example.com","email_verified":false}`),
			wantErr:     true,
			wantSubstrs: []string{"did not mark verified", "verified email"},
		},
		{
			name:        "email present without email_verified fails",
			token:       craftJWT(t, `{"email":"dev@example.com"}`),
			wantErr:     true,
			wantSubstrs: []string{"did not mark verified"},
		},
		{
			name:        "not a JWT fails precisely",
			token:       "not-a-jwt",
			wantErr:     true,
			wantSubstrs: []string{"well-formed JWT"},
		},
		{
			name:        "payload not base64url fails precisely",
			token:       "header.!!!not-base64!!!.sig",
			wantErr:     true,
			wantSubstrs: []string{"base64url"},
		},
		{
			name:        "payload not JSON fails precisely",
			token:       "header." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".sig",
			wantErr:     true,
			wantSubstrs: []string{"not valid JSON"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkIDTokenSubject(tc.token)
			if tc.wantErr && err == nil {
				t.Fatalf("checkIDTokenSubject(%q) = nil, want error", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("checkIDTokenSubject(%q) = %v, want nil", tc.name, err)
			}
			if err != nil {
				for _, sub := range tc.wantSubstrs {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error %q lacks expected substring %q", err.Error(), sub)
					}
				}
				if strings.Contains(err.Error(), "may have expired") {
					t.Errorf("guard error %q wrongly suggests expiry", err.Error())
				}
			}
		})
	}
}

// TestKeylessSubjectFailure proves the classifier matches the certificate
// authority's two plain-string subject-failure texts and nothing else.
func TestKeylessSubjectFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not a subject failure", nil, false},
		{
			"no subject found in claims is a subject failure",
			errors.New("signing: keyless bundle: no subject found in claims"),
			true,
		},
		{
			"not verified by identity provider is a subject failure",
			errors.New("signing: keyless bundle: not verified by identity provider"),
			true,
		},
		{
			"a generic rejection is not a subject failure",
			errors.New("signing: keyless bundle: certificate request rejected"),
			false,
		},
		{
			"a transient outage is not a subject failure",
			errors.New("signing: keyless bundle: connection refused"),
			false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := keylessSubjectFailure(tc.err); got != tc.want {
				t.Errorf("keylessSubjectFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestSignNoSubjectFailureDiagnostic proves a no-subject keyless failure is
// classified at the batch wrap site: the message names the missing
// verified-email/subject and never suggests expiry or re-export, because
// re-minting cannot fix a structurally subject-less identity. The batch resolves
// the identity exactly once. The injected signer bypasses the pre-sign guard, so
// this exercises the classifier on the certificate authority's own error text.
func TestSignNoSubjectFailureDiagnostic(t *testing.T) {
	t.Parallel()
	store := memory.New()
	refs, subjects := publishN(t, store, 1)

	var acquired int
	fake := &signingtest.FakeSigner{Err: errors.New("signing: keyless bundle: no subject found in claims")}
	err := signRun(signConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{}),
		References:  refs,
		SubjectRefs: subjects,
		Target:      store,
		Signer:      fake,
		EnvLookup:   func(string) string { return "" },
		IsTTY:       func() bool { return true },
		TokenGetter: func() (string, error) { acquired++; return "interactive-token", nil },
	})
	if err == nil {
		t.Fatal("signRun should fail when the token carries no subject")
	}
	if acquired != 1 {
		t.Errorf("token getter called %d times, want exactly 1 (the batch resolves the identity once)", acquired)
	}
	msg := err.Error()
	if !strings.Contains(msg, "no verified email or subject claim") {
		t.Errorf("error %q lacks the no-subject diagnostic", msg)
	}
	if !strings.Contains(msg, "OIDC flow/client") {
		t.Errorf("error %q does not point at the OIDC flow/client", msg)
	}
	if strings.Contains(msg, "may have expired") {
		t.Errorf("error %q wrongly suggests expiry for a subject failure", msg)
	}
	// The message may mention re-exporting only to say it will not help; it must
	// never advise it as a remedy (the expiry path's "re-export a fresh" framing).
	if strings.Contains(msg, "re-export a fresh") {
		t.Errorf("error %q wrongly advises re-export for a subject failure", msg)
	}
	if !strings.Contains(msg, "will not help") {
		t.Errorf("error %q should state that re-acquiring will not help", msg)
	}
}

// TestSignUnverifiedEmailFailureDiagnostic proves an unverified-email keyless
// failure is handled in the same no-subject class: the precise diagnostic, no
// expiry framing, identity resolved once.
func TestSignUnverifiedEmailFailureDiagnostic(t *testing.T) {
	t.Parallel()
	store := memory.New()
	refs, subjects := publishN(t, store, 1)

	var acquired int
	fake := &signingtest.FakeSigner{Err: errors.New("signing: keyless bundle: not verified by identity provider")}
	err := signRun(signConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{}),
		References:  refs,
		SubjectRefs: subjects,
		Target:      store,
		Signer:      fake,
		EnvLookup:   func(string) string { return "" },
		IsTTY:       func() bool { return true },
		TokenGetter: func() (string, error) { acquired++; return "interactive-token", nil },
	})
	if err == nil {
		t.Fatal("signRun should fail when the token's email is unverified")
	}
	if acquired != 1 {
		t.Errorf("token getter called %d times, want exactly 1 (the batch resolves the identity once)", acquired)
	}
	msg := err.Error()
	if !strings.Contains(msg, "no verified email or subject claim") {
		t.Errorf("error %q lacks the no-subject diagnostic", msg)
	}
	if strings.Contains(msg, "may have expired") {
		t.Errorf("error %q wrongly suggests expiry for an unverified-email failure", msg)
	}
}
