// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package signing_test

// A pull verifies a signature with the verification library and then holds the
// bundle to the toolchain's own reading of which artifact it was made for, the
// reading a build holds the recorded fact to. These tests hold both halves: each
// form a pull accepts verifies through the product's verifiers exactly as the
// library alone verifies it, and the check that runs after the library refuses,
// under the catch-all verification condition and naming which case it is, the two
// bundles the reading cannot agree with.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// crossCheckRefusal is a phrase only the check after the library writes, so a
// refusal carrying it is the check's and one without it is the library's.
const crossCheckRefusal = "its cryptographic check holds"

// TestThePullVerifiesBothFormsThroughTheCrossCheck drives the two product
// verifiers over a bundle in each form, keyed and keyless: each verifies for the
// artifact it was made for, through the library and the check after it, and is
// refused for another artifact by the library itself, before the check is asked.
func TestThePullVerifiesBothFormsThroughTheCrossCheck(t *testing.T) {
	t.Parallel()
	keyedMessage, keyedMessagePub := keyedSignerBundle(t, parityMadeFor)
	keyedEnvelope, keyedEnvelopePub := keyedEnvelopeBundle(t, signingtest.Statement(parityMadeFor))
	authority, tm := parityAuthority(t)
	loggedAt := time.Now().Add(2 * time.Minute)
	keylessMessage, err := authority.Sign(parityIdentity, parityIssuer, parityMadeFor, loggedAt)
	if err != nil {
		t.Fatalf("keyless Sign: %v", err)
	}
	keylessEnvelope, err := authority.Attest(parityIdentity, parityIssuer, signingtest.Statement(parityMadeFor), loggedAt)
	if err != nil {
		t.Fatalf("Attest: %v", err)
	}
	keyed := func(pub []byte) func(*testing.T, []byte, string) error {
		return func(_ *testing.T, b []byte, artifact string) error {
			return signing.VerifyKeyedBundle(b, pub, artifact)
		}
	}
	keyless := func(t *testing.T, b []byte, artifact string) error {
		t.Helper()
		matched, err := signing.VerifyKeylessBundle(b, tm, parityIdentities, artifact)
		if err == nil && (matched.Identity != parityIdentity || matched.Issuer != parityIssuer) {
			t.Errorf("the keyless verifier matched %q/%q; want the signer %q/%q", matched.Identity, matched.Issuer, parityIdentity, parityIssuer)
		}
		return err
	}
	rows := []struct {
		name   string
		bundle []byte
		verify func(*testing.T, []byte, string) error
	}{
		{"a keyed message signature", keyedMessage, keyed(keyedMessagePub)},
		{"a keyed signed envelope", keyedEnvelope, keyed(keyedEnvelopePub)},
		{"a keyless message signature", keylessMessage.Bundle, keyless},
		{"a keyless signed envelope", keylessEnvelope.Bundle, keyless},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if err := row.verify(t, row.bundle, parityMadeFor); err != nil {
				t.Fatalf("the bundle does not verify for the artifact it was made for: %v", err)
			}
			err := row.verify(t, row.bundle, parityOther)
			if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
				t.Fatalf("for another artifact, want the verification condition, got %v", err)
			}
			if strings.Contains(err.Error(), crossCheckRefusal) {
				t.Errorf("for another artifact the library verified the bundle and only the check after it refused:\n%v", err)
			}
		})
	}
}

// TestTheCrossCheckRefusesWhereTheReadingDisagrees holds the check that runs once
// the library has verified a bundle, on its own: a bundle the reading finds made
// for the artifact passes, and each of the two disagreements is refused under the
// verification condition with an occurrence saying which it is.
func TestTheCrossCheckRefusesWhereTheReadingDisagrees(t *testing.T) {
	t.Parallel()
	message, _ := keyedSignerBundle(t, parityMadeFor)
	envelope, _ := keyedEnvelopeBundle(t, signingtest.Statement(parityMadeFor))
	unreadable := []byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json",` +
		`"dsseEnvelope":{"payloadType":"text/plain","payload":"aGVsbG8="}}`)
	rows := []struct {
		name     string
		bundle   []byte
		artifact string
		want     string
	}{
		{name: "a message signature made for the artifact passes", bundle: message, artifact: parityMadeFor},
		{name: "a signed envelope made for the artifact passes", bundle: envelope, artifact: parityMadeFor},
		{name: "a message signature made for another artifact is refused", bundle: message, artifact: parityOther,
			want: "does not name " + parityOther + " as the artifact it was made for"},
		{name: "a signed envelope made for another artifact is refused", bundle: envelope, artifact: parityOther,
			want: "does not name " + parityOther + " as the artifact it was made for"},
		{name: "a form the artifact cannot be read from is refused", bundle: unreadable, artifact: parityMadeFor,
			want: "in a form from which the artifact it was made for cannot be read"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := signing.HoldToTheArtifactVerified(row.bundle, row.artifact)
			if row.want == "" {
				if err != nil {
					t.Fatalf("the check refused a bundle the reading finds made for the artifact: %v", err)
				}
				return
			}
			if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
				t.Fatalf("want the verification condition, got %v", err)
			}
			for _, text := range []string{signing.ErrSignatureVerificationFailed().Error(), crossCheckRefusal, row.want} {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("the refusal does not carry %q:\n%v", text, err)
				}
			}
		})
	}
}
