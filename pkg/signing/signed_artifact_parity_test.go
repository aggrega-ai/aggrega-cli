// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package signing_test

// The toolchain reads, from a signature bundle, which artifact the signature was
// made for — with the standard library alone, because the verification library
// may not enter the path a build takes (oci.SignedArtifactOf) — and a pull holds
// every bundle it verifies to that same reading. That makes the reading a second
// reader of a format the verification library also reads, and this file is what
// holds the two together, for each of the two forms a pull accepts: over bundles
// from the toolchain's keyed signer, from the keyless signer's own construction,
// and from the virtual Sigstore the verifier tests sign with, the reading must name
// what the library's own parse names, and its answer to "was this made for that
// artifact" must be the answer the library's verification gives. The verification
// compared against is the library's alone (the seams in export_test.go), never the
// product verifiers, which already refuse a bundle where the two disagree and so
// could not show a disagreement here.
//
// It lives here, beside the signers and the verifier, because this package is
// outside the build path; the reading is imported into it, never the other way
// round.

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/sign"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

const (
	parityMadeFor = "sha256:4f5e8c1b2a3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
	parityOther   = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	parityThird   = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

	parityIdentity = "publisher@example.test"
	parityIssuer   = "https://issuer.example.test"
)

// parityCandidates are the artifacts every bundle here is asked about.
var parityCandidates = []string{parityMadeFor, parityOther, parityThird}

// virtualFulcio is a certificate provider backed by an in-memory virtual
// Sigstore: it hands the bundle builder a certificate the virtual certificate
// authority issued for one identity, over the key the builder signs with.
type virtualFulcio struct{ cert []byte }

func (v virtualFulcio) GetCertificate(context.Context, sign.Keypair, *sign.CertificateProviderOptions) ([]byte, error) {
	return v.cert, nil
}

// keylessSignerBundle builds a keyless bundle the way the keyless signer builds
// one — the same content type over the digest string, the same keypair adapter —
// with the signing certificate issued by the virtual Sigstore, and returns its
// serialized bytes. It carries no log entry, so it is read, never verified.
func keylessSignerBundle(t *testing.T, madeFor string) []byte {
	t.Helper()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	leaf, key, err := vs.GenerateLeafCert(parityIdentity, parityIssuer)
	if err != nil {
		t.Fatalf("GenerateLeafCert: %v", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		t.Fatalf("the virtual certificate authority's leaf key is %T, not a signer", key)
	}
	keypair, err := signing.NewHeldKeypair(signer)
	if err != nil {
		t.Fatalf("NewHeldKeypair: %v", err)
	}
	pb, err := sign.Bundle(&sign.PlainData{Data: []byte(madeFor)}, keypair, sign.BundleOptions{
		Context:             context.Background(),
		CertificateProvider: virtualFulcio{cert: leaf.Raw},
	})
	if err != nil {
		t.Fatalf("build the keyless bundle: %v", err)
	}
	b, err := bundle.NewBundle(pb)
	if err != nil {
		t.Fatalf("wrap the keyless bundle: %v", err)
	}
	out, err := b.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal the keyless bundle: %v", err)
	}
	return out
}

// keyedSignerBundle is a bundle from the keyed signer itself, with the public key
// that verifies it.
func keyedSignerBundle(t *testing.T, madeFor string) (bundleBytes, pubPEM []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(priv, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	out, _, err := signer.Sign(context.Background(), madeFor)
	if err != nil {
		t.Fatalf("keyed Sign: %v", err)
	}
	return out, pub
}

// keyedEnvelopeBundle is a signed envelope over statement, made with a held key,
// with the public key that verifies it.
func keyedEnvelopeBundle(t *testing.T, statement []byte) (bundleBytes, pubPEM []byte) {
	t.Helper()
	out, pub, err := signingtest.KeyedEnvelope(statement)
	if err != nil {
		t.Fatalf("KeyedEnvelope: %v", err)
	}
	return out, pub
}

// parityAuthority is a virtual Sigstore whose signatures the keyless verifier
// verifies offline, with the trust material that verifies them.
func parityAuthority(t *testing.T) (*signingtest.KeylessAuthority, root.TrustedMaterial) {
	t.Helper()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	rootJSON, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	rootPath := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(rootPath, rootJSON, 0o644); err != nil {
		t.Fatalf("write trusted root: %v", err)
	}
	tm, err := signing.TrustedMaterialFromRootPath(rootPath)
	if err != nil {
		t.Fatalf("TrustedMaterialFromRootPath: %v", err)
	}
	return authority, tm
}

// parityIdentities is the trusted-identity set every keyless bundle here verifies
// under.
var parityIdentities = []signing.TrustedIdentity{{Identity: parityIdentity, Issuer: parityIssuer}}

// respelled re-encodes a bundle under the spellings the bundle format's JSON
// mapping admits besides the one the signers write — each field under its
// declared name and the algorithm as a number — which the library parses alike.
func respelled(t *testing.T, bundleBytes []byte) []byte {
	t.Helper()
	pb := &protobundle.Bundle{}
	if err := protojson.Unmarshal(bundleBytes, pb); err != nil {
		t.Fatalf("decode the bundle: %v", err)
	}
	out, err := protojson.MarshalOptions{UseProtoNames: true, UseEnumNumbers: true}.Marshal(pb)
	if err != nil {
		t.Fatalf("re-encode the bundle: %v", err)
	}
	if bytes.Contains(out, []byte("messageSignature")) || bytes.Contains(out, []byte("dsseEnvelope")) {
		t.Fatalf("the re-encoded bundle still carries the JSON names, so it tests nothing new:\n%s", out)
	}
	return out
}

// libraryMessageReading is the verification library's own parse of a bundle's
// message digest.
func libraryMessageReading(t *testing.T, bundleBytes []byte) oci.SignedDigest {
	t.Helper()
	b, err := signing.LoadBundle(bundleBytes)
	if err != nil {
		t.Fatalf("the library refused the bundle: %v", err)
	}
	content, err := b.SignatureContent()
	if err != nil {
		t.Fatalf("the library read no signature content: %v", err)
	}
	msg := content.MessageSignatureContent()
	if msg == nil {
		t.Fatal("the library read no message signature from a bundle written as one")
	}
	return oci.SignedDigest{Algorithm: msg.DigestAlgorithm(), Digest: msg.Digest()}
}

// libraryStatementDigests is every hash the verification library's own parse of a
// signed envelope's statement lists for its subjects, under the bundle format's
// spelling of each supported algorithm. A hash under an algorithm the library
// does not support is left out, as the library leaves it out of its comparison;
// which of these the library's verification compares is what the behavioural
// half of the parity holds.
func libraryStatementDigests(t *testing.T, bundleBytes []byte) []oci.SignedDigest {
	t.Helper()
	b, err := signing.LoadBundle(bundleBytes)
	if err != nil {
		t.Fatalf("the library refused the bundle: %v", err)
	}
	envelope, err := b.Envelope()
	if err != nil {
		t.Fatalf("the library read no signed envelope from a bundle written as one: %v", err)
	}
	statement, err := envelope.Statement()
	if err != nil {
		t.Fatalf("the library read no statement from the envelope: %v", err)
	}
	var out []oci.SignedDigest
	for _, subject := range statement.GetSubject() {
		for name, value := range subject.GetDigest() {
			algorithm, ok := map[string]string{
				"sha256": "SHA2_256", "SHA2_256": "SHA2_256",
				"sha384": "SHA2_384", "SHA2_384": "SHA2_384",
				"sha512": "SHA2_512", "SHA2_512": "SHA2_512",
			}[name]
			if !ok {
				continue
			}
			raw, err := hex.DecodeString(value)
			if err != nil {
				continue
			}
			out = append(out, oci.SignedDigest{Algorithm: algorithm, Digest: raw})
		}
	}
	return out
}

// containsDigest reports whether digests holds d.
func containsDigest(digests []oci.SignedDigest, d oci.SignedDigest) bool {
	return slices.ContainsFunc(digests, func(x oci.SignedDigest) bool {
		return x.Algorithm == d.Algorithm && bytes.Equal(x.Digest, d.Digest)
	})
}

// TestTheBuildSideReadingOfASignedArtifactAgreesWithTheLibrary is the parity over
// the message-signature form: for each of the toolchain's signers, and for the
// other spellings the format admits, both readings of the bundle name one
// algorithm and one hash, and the reading's answer for each candidate artifact is
// the library's verification's answer wherever the bundle can be verified offline.
func TestTheBuildSideReadingOfASignedArtifactAgreesWithTheLibrary(t *testing.T) {
	t.Parallel()
	keyed, pub := keyedSignerBundle(t, parityMadeFor)
	keylessConstruction := keylessSignerBundle(t, parityMadeFor)
	authority, tm := parityAuthority(t)
	logged, err := authority.Sign(parityIdentity, parityIssuer, parityMadeFor, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("keyless Sign: %v", err)
	}
	keyedVerify := func(b []byte, artifact string) bool { return signing.VerifyKeyedWithLibrary(b, pub, artifact) == nil }
	keylessVerify := func(b []byte, artifact string) bool {
		_, err := signing.VerifyKeylessWithLibrary(b, tm, parityIdentities, artifact)
		return err == nil
	}
	rows := []struct {
		name   string
		bundle []byte
		// verify is the library's verification of the bundle for an artifact, or nil
		// where the bundle cannot be verified offline.
		verify func([]byte, string) bool
	}{
		{"a bundle from the keyed signer", keyed, keyedVerify},
		{"a keyless bundle as the keyless signer builds one", keylessConstruction, nil},
		{"a keyless bundle from the virtual Sigstore, log entry and all", logged.Bundle, keylessVerify},
		{"the keyed bundle under its declared field names and a numbered algorithm", respelled(t, keyed), keyedVerify},
		{"the keyless signer's bundle under its declared field names and a numbered algorithm", respelled(t, keylessConstruction), nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			want := libraryMessageReading(t, row.bundle)
			got, recognised := oci.SignedArtifactOf(row.bundle)
			if !recognised {
				t.Fatalf("the reading does not recognise a bundle the library reads as a message signature:\n%s", row.bundle)
			}
			if len(got.Digests) != 1 {
				t.Fatalf("the reading names %d hashes for a message signature, which carries one: %+v", len(got.Digests), got.Digests)
			}
			if got.Digests[0].Algorithm != want.Algorithm {
				t.Errorf("algorithm: the reading reads %q, the library %q", got.Digests[0].Algorithm, want.Algorithm)
			}
			if !bytes.Equal(got.Digests[0].Digest, want.Digest) {
				t.Errorf("hash: the reading reads %x, the library %x", got.Digests[0].Digest, want.Digest)
			}
			if !got.MadeFor(parityMadeFor) {
				t.Errorf("the reading says the signature was not made for the digest it was signed over")
			}
			for _, artifact := range parityCandidates {
				if artifact != parityMadeFor && got.MadeFor(artifact) {
					t.Errorf("the reading says the signature was made for %s, which it was not signed over", artifact)
				}
				if row.verify == nil {
					continue
				}
				if verified := row.verify(row.bundle, artifact); verified != got.MadeFor(artifact) {
					t.Errorf("for %s the library's verification says %v and the reading says %v", artifact, verified, got.MadeFor(artifact))
				}
			}
		})
	}
}

// TestTheBuildSideReadingOfASignedEnvelopeAgreesWithTheLibrary is the parity over
// the signed-envelope form. Every hash the reading names is one the library's own
// parse of the statement lists, and for every candidate artifact the reading says
// "made for" exactly where the library's verification binds it — including the
// statements where which algorithms the library chooses decides the answer, where
// a hash listed under an algorithm left unchosen must not count.
func TestTheBuildSideReadingOfASignedEnvelopeAgreesWithTheLibrary(t *testing.T) {
	t.Parallel()
	h := signingtest.HashOf
	tooManySubjects := make([]map[string]string, 1025)
	for i := range tooManySubjects {
		tooManySubjects[i] = map[string]string{"sha256": h("sha256", parityMadeFor)}
	}
	tooManyDigests := map[string]string{"sha256": h("sha256", parityMadeFor)}
	for i := 0; len(tooManyDigests) <= 32; i++ {
		tooManyDigests[fmt.Sprintf("x-%02d", i)] = "00"
	}
	rows := []struct {
		name      string
		statement []byte
		// madeFor is the candidates the signature binds; every other candidate it
		// does not.
		madeFor []string
	}{
		{name: "a statement naming one artifact", statement: signingtest.Statement(parityMadeFor), madeFor: []string{parityMadeFor}},
		{name: "a statement naming two artifacts", statement: signingtest.Statement(parityMadeFor, parityOther),
			madeFor: []string{parityMadeFor, parityOther}},
		{name: "a hash under an algorithm the library leaves unchosen is never compared",
			statement: signingtest.StatementWithSubjects(
				map[string]string{"sha512": h("sha512", parityMadeFor)},
				map[string]string{"sha512": h("sha512", parityOther), "sha256": h("sha256", parityThird)},
			),
			madeFor: []string{parityMadeFor, parityOther}},
		{name: "a subject listing two algorithms is compared under the stronger only",
			statement: signingtest.StatementWithSubjects(
				map[string]string{"sha256": h("sha256", parityMadeFor), "sha512": h("sha512", parityOther)},
			),
			madeFor: []string{parityOther}},
		{name: "subjects under different algorithms each choose their own",
			statement: signingtest.StatementWithSubjects(
				map[string]string{"sha256": h("sha256", parityMadeFor)},
				map[string]string{"sha384": h("sha384", parityOther)},
			),
			madeFor: []string{parityMadeFor, parityOther}},
		{name: "the bundle format's own algorithm names in a statement",
			statement: signingtest.StatementWithSubjects(map[string]string{"SHA2_256": h("sha256", parityMadeFor)}),
			madeFor:   []string{parityMadeFor}},
		{name: "a hash in upper-case hexadecimal",
			statement: signingtest.StatementWithSubjects(map[string]string{"sha256": strings.ToUpper(h("sha256", parityMadeFor))}),
			madeFor:   []string{parityMadeFor}},
		{name: "an unsupported algorithm and a hash that is not hexadecimal are ignored",
			statement: signingtest.StatementWithSubjects(
				map[string]string{"md5": "00", "sha256": "not hexadecimal"},
				map[string]string{"sha256": h("sha256", parityMadeFor)},
			),
			madeFor: []string{parityMadeFor}},
		{name: "a statement listing no subject binds nothing", statement: signingtest.StatementWithSubjects()},
		{name: "a statement with no hash under a supported algorithm binds nothing",
			statement: signingtest.StatementWithSubjects(map[string]string{"md5": "00"})},
		{name: "a statement listing more subjects than the library admits binds nothing",
			statement: signingtest.StatementWithSubjects(tooManySubjects...)},
		{name: "a subject listing more hashes than the library admits binds nothing",
			statement: signingtest.StatementWithSubjects(tooManyDigests)},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			envelope, pub := keyedEnvelopeBundle(t, row.statement)
			assertEnvelopeParity(t, envelope, row.madeFor, func(artifact string) bool {
				return signing.VerifyKeyedWithLibrary(envelope, pub, artifact) == nil
			})
			t.Run("under its declared field names", func(t *testing.T) {
				respelt := respelled(t, envelope)
				assertEnvelopeParity(t, respelt, row.madeFor, func(artifact string) bool {
					return signing.VerifyKeyedWithLibrary(respelt, pub, artifact) == nil
				})
			})
		})
	}

	t.Run("a keyless envelope from the virtual Sigstore, log entry and all", func(t *testing.T) {
		t.Parallel()
		authority, tm := parityAuthority(t)
		logged, err := authority.Attest(parityIdentity, parityIssuer, signingtest.Statement(parityMadeFor), time.Now().Add(2*time.Minute))
		if err != nil {
			t.Fatalf("Attest: %v", err)
		}
		assertEnvelopeParity(t, logged.Bundle, []string{parityMadeFor}, func(artifact string) bool {
			_, err := signing.VerifyKeylessWithLibrary(logged.Bundle, tm, parityIdentities, artifact)
			return err == nil
		})
	})
}

// assertEnvelopeParity holds one signed envelope: the reading recognises it, names
// only hashes the library's parse of its statement lists, and says it was made
// for exactly the candidates in madeFor — and the library's verification agrees,
// candidate by candidate.
func assertEnvelopeParity(t *testing.T, envelope []byte, madeFor []string, libraryVerifies func(string) bool) {
	t.Helper()
	got, recognised := oci.SignedArtifactOf(envelope)
	if !recognised {
		t.Fatalf("the reading does not recognise a signed envelope the library reads:\n%s", envelope)
	}
	listed := libraryStatementDigests(t, envelope)
	for _, d := range got.Digests {
		if !containsDigest(listed, d) {
			t.Errorf("the reading names %s %x, which the library's parse of the statement does not list", d.Algorithm, d.Digest)
		}
	}
	for _, artifact := range parityCandidates {
		want := slices.Contains(madeFor, artifact)
		if got.MadeFor(artifact) != want {
			t.Errorf("for %s the reading says made-for %v, want %v", artifact, got.MadeFor(artifact), want)
		}
		if verified := libraryVerifies(artifact); verified != got.MadeFor(artifact) {
			t.Errorf("for %s the library's verification says %v and the reading says %v", artifact, verified, got.MadeFor(artifact))
		}
	}
}
