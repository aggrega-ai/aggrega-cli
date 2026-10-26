// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestASignatureThatDoesNotVerifyPrintsItsSentenceOnce renders, through the
// command line's own diagnostic formatter, the refusal of a keyed signature that
// does not verify and of a signature bundle that does not verify, each produced
// by the real verification over a signature made in the test, and asserts the
// whole rendered diagnostic: the trust head carrying the condition's short code,
// then one message line that opens with the registered sentence and says
// "signing:" exactly once.
//
// The registered sentences already open "signing:", so a raise site that wraps
// one under a "signing:" prefix of its own prints the prefix twice. The
// verification library's own detail follows the sentence and is not fixed here,
// which is why the line is pinned by its opening, its single prefix and its being
// the diagnostic's last line rather than by a literal.
//
// No command reaches these refusals offline: a keyed signature is verified at a
// pull from a registry, and the detached form has no command of its own. So the
// refusal is produced by the verification itself and rendered the way the root
// command renders every failure it returns.
func TestASignatureThatDoesNotVerifyPrintsItsSentenceOnce(t *testing.T) {
	t.Parallel()

	newSigner := func(t *testing.T) *signing.KeyedSigner {
		t.Helper()
		privPEM, _, err := signing.GenerateKeyedPEM()
		if err != nil {
			t.Fatalf("generate a key: %v", err)
		}
		signer, err := signing.LoadKeyedSigner(privPEM, nil)
		if err != nil {
			t.Fatalf("load the key: %v", err)
		}
		return signer
	}
	const digest = "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	cases := []struct {
		name      string
		condition error
		refuse    func(t *testing.T) error
	}{
		{
			name:      "a detached signature over a payload that was altered",
			condition: signing.ErrSignatureInvalid(),
			refuse: func(t *testing.T) error {
				signer := newSigner(t)
				sig, err := signer.SignPayload([]byte("the payload as signed"))
				if err != nil {
					t.Fatalf("sign: %v", err)
				}
				return signing.VerifyKeyed(signer.PublicKeyPEM(), []byte("the payload as altered"), sig)
			},
		},
		{
			name:      "a signature bundle checked against another key",
			condition: signing.ErrSignatureVerificationFailed(),
			refuse: func(t *testing.T) error {
				bundle, _, err := newSigner(t).Sign(context.Background(), digest)
				if err != nil {
					t.Fatalf("sign: %v", err)
				}
				return signing.VerifyKeyedBundle(bundle, newSigner(t).PublicKeyPEM(), digest)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.refuse(t)
			if err == nil {
				t.Fatal("the signature verified, so there is no refusal to render")
			}
			short, ok := cerrors.ShortCodeOf(tc.condition)
			if !ok {
				t.Fatal("the condition carries no short code")
			}
			sentence, ok := cerrors.MessageOf(tc.condition)
			if !ok {
				t.Fatal("the condition carries no registered sentence")
			}

			lines := strings.Split(formatCompilerError(err), "\n")
			if len(lines) != 2 {
				t.Fatalf("the diagnostic is %d line(s); want the head and one message line:\n%s",
					len(lines), strings.Join(lines, "\n"))
			}
			if want := "[" + string(cerrors.TrustError) + " " + short + "]"; lines[0] != want {
				t.Errorf("head = %q; want %q", lines[0], want)
			}
			message := lines[1]
			if !strings.HasPrefix(message, "  "+sentence+": ") {
				t.Errorf("the message line does not open with the registered sentence %q:\n%s", sentence, message)
			}
			if n := strings.Count(message, "signing:"); n != 1 {
				t.Errorf("the message line says %q %d time(s); want once:\n%s", "signing:", n, message)
			}
		})
	}
}
