// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signingtest

import (
	"encoding/base64"
	"testing"
)

// CraftJWT builds a syntactically valid JWT whose payload is the given JSON
// claims. The signature segment is a fixed placeholder: the pre-sign subject
// guard decodes the unverified payload and never checks the signature, so a real
// one is unnecessary.
//
// It lives here, beside the fake signer, because more than one package exercises
// the subject guard and the signer construction that consumes its output, and a
// crafted-token fixture written twice is two fixtures free to disagree about what
// a usable token looks like.
func CraftJWT(t *testing.T, claimsJSON string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claimsJSON))
	return header + "." + payload + ".sig"
}

// SubjectfulIDToken returns a crafted JWT carrying a verified email, so it
// passes the pre-sign subject guard. Tests that exercise signer construction
// rather than the guard itself use it to supply a structurally usable token.
func SubjectfulIDToken(t *testing.T) string {
	t.Helper()
	return CraftJWT(t, `{"email":"dev@example.com","email_verified":true}`)
}
