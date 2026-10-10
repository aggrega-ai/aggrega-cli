// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

// The build reads, from a cached signature bundle, which artifact the signature was
// made for — with the standard library alone, because the verification library may
// not enter the path a build takes (oci.SignedArtifactOf). That makes it a second
// reader of a format the verification library also reads, and this file is what
// holds the two together: over a bundle from each of the toolchain's signers, the
// build-side reading and the library's own parse must name the same algorithm and
// the same hash, and the build-side answer to "was this made for that artifact"
// must be the answer the library's verification gives.
//
// It lives here, beside the signers and the verifier, because this package is
// outside the build path; the build-side reader is imported into it, never the
// other way round.

import (
	"bytes"
	"context"
	"crypto"
	"testing"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/sign"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

const (
	parityMadeFor = "sha256:4f5e8c1b2a3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
	parityOther   = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// virtualFulcio is a certificate provider backed by an in-memory virtual
// Sigstore: it hands the bundle builder a certificate the virtual certificate
// authority issued for one identity, over the key the builder signs with.
type virtualFulcio struct{ cert []byte }

func (v virtualFulcio) GetCertificate(context.Context, sign.Keypair, *sign.CertificateProviderOptions) ([]byte, error) {
	return v.cert, nil
}

// keylessParityBundle builds a keyless bundle the way the keyless signer builds
// one — the same content type over the digest string, the same keypair adapter —
// with the signing certificate issued by the virtual Sigstore the keyless verifier
// tests sign with, and returns its serialized bytes.
func keylessParityBundle(t *testing.T, madeFor string) []byte {
	t.Helper()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	leaf, key, err := vs.GenerateLeafCert(maintainerIdentity, googleIssuer)
	if err != nil {
		t.Fatalf("GenerateLeafCert: %v", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		t.Fatalf("the virtual certificate authority's leaf key is %T, not a signer", key)
	}
	keypair, err := newHeldKeypair(signer)
	if err != nil {
		t.Fatalf("newHeldKeypair: %v", err)
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

// keyedParityBundle is a bundle from the keyed signer itself, with the public key
// that verifies it.
func keyedParityBundle(t *testing.T, madeFor string) (bundleBytes, pubPEM []byte) {
	t.Helper()
	priv, pub, err := GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := LoadKeyedSigner(priv, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	out, _, err := signer.Sign(context.Background(), madeFor)
	if err != nil {
		t.Fatalf("keyed Sign: %v", err)
	}
	return out, pub
}

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
	if bytes.Contains(out, []byte("messageSignature")) {
		t.Fatalf("the re-encoded bundle still carries the JSON names, so it tests nothing new:\n%s", out)
	}
	return out
}

// libraryReading is the verification library's own parse of a bundle's message
// digest.
func libraryReading(t *testing.T, bundleBytes []byte) (algorithm string, digest []byte) {
	t.Helper()
	b, err := LoadBundle(bundleBytes)
	if err != nil {
		t.Fatalf("the library refused the bundle: %v", err)
	}
	content, err := b.SignatureContent()
	if err != nil {
		t.Fatalf("the library read no signature content: %v", err)
	}
	msg := content.MessageSignatureContent()
	if msg == nil {
		t.Fatal("the library read no message signature from a bundle the signers write as one")
	}
	return msg.DigestAlgorithm(), msg.Digest()
}

// TestTheBuildSideReadingOfASignedArtifactAgreesWithTheLibrary is the parity: for
// each of the toolchain's signers, and for the other spellings the format admits,
// both readings of the bundle name one algorithm and one hash, and the build-side
// reading says the signature was made for the digest it was signed over and for no
// other.
func TestTheBuildSideReadingOfASignedArtifactAgreesWithTheLibrary(t *testing.T) {
	t.Parallel()
	keyed, pub := keyedParityBundle(t, parityMadeFor)
	keyless := keylessParityBundle(t, parityMadeFor)
	rows := []struct {
		name   string
		bundle []byte
	}{
		{"a bundle from the keyed signer", keyed},
		{"a keyless bundle from the virtual Sigstore", keyless},
		{"the keyed bundle under its declared field names and a numbered algorithm", respelled(t, keyed)},
		{"the keyless bundle under its declared field names and a numbered algorithm", respelled(t, keyless)},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			wantAlgorithm, wantDigest := libraryReading(t, row.bundle)
			got, recognised := oci.SignedArtifactOf(row.bundle)
			if !recognised {
				t.Fatalf("the build-side reading does not recognise a bundle the library reads as a message signature:\n%s", row.bundle)
			}
			if got.Algorithm != wantAlgorithm {
				t.Errorf("algorithm: the build side reads %q, the library %q", got.Algorithm, wantAlgorithm)
			}
			if !bytes.Equal(got.Digest, wantDigest) {
				t.Errorf("hash: the build side reads %x, the library %x", got.Digest, wantDigest)
			}
			if !got.MadeFor(parityMadeFor) {
				t.Errorf("the build side says the signature was not made for the digest it was signed over")
			}
			if got.MadeFor(parityOther) {
				t.Errorf("the build side says the signature was made for a digest it was not signed over")
			}
		})
	}

	// The build-side answer is the answer the library's verification gives, over the
	// one bundle here the library can verify offline end to end.
	t.Run("the keyed bundle verifies for exactly the artifact the build side reads it as made for", func(t *testing.T) {
		t.Parallel()
		read, _ := oci.SignedArtifactOf(keyed)
		for _, artifact := range []string{parityMadeFor, parityOther} {
			verified := VerifyKeyedBundle(keyed, pub, artifact) == nil
			if verified != read.MadeFor(artifact) {
				t.Errorf("for %s the library's verification says %v and the build-side reading says %v", artifact, verified, read.MadeFor(artifact))
			}
		}
	})
}

// TestTheBuildSideReadingDoesNotRecogniseASignedEnvelope is the bound the reading
// states: a bundle carrying a signed envelope instead of a message signature —
// a form neither of the toolchain's signers writes — is reported as not
// recognised, where the library reads no message signature from it either.
func TestTheBuildSideReadingDoesNotRecogniseASignedEnvelope(t *testing.T) {
	t.Parallel()
	priv, _, err := GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := LoadKeyedSigner(priv, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	keypair, err := newHeldKeypair(signer.signer)
	if err != nil {
		t.Fatalf("newHeldKeypair: %v", err)
	}
	pb, err := sign.Bundle(&sign.DSSEData{Data: []byte(`{"_type":"https://in-toto.io/Statement/v1"}`), PayloadType: "application/vnd.in-toto+json"},
		keypair, sign.BundleOptions{Context: context.Background()})
	if err != nil {
		t.Fatalf("build the envelope bundle: %v", err)
	}
	b, err := bundle.NewBundle(pb)
	if err != nil {
		t.Fatalf("wrap the envelope bundle: %v", err)
	}
	out, err := b.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal the envelope bundle: %v", err)
	}
	content, err := b.SignatureContent()
	if err != nil {
		t.Fatalf("the library read no signature content: %v", err)
	}
	if content.MessageSignatureContent() != nil {
		t.Fatal("the library reads a message signature from an envelope bundle, so this proves nothing")
	}
	if _, recognised := oci.SignedArtifactOf(out); recognised {
		t.Errorf("the build-side reading recognised an envelope bundle as a message signature:\n%s", out)
	}
}
