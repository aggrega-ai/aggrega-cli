// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package signing

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// heldKeypair adapts an in-process crypto.Signer (a held ECDSA P-256 key,
// or a per-signature ephemeral key for the keyless flow) to sigstore-go's
// sign.Keypair interface, so the same key-handling builds a Sigstore
// bundle for both the keyed and the keyless paths. The single Keypair
// implementation keeps key-to-bundle wiring in one place.
type heldKeypair struct {
	signer     crypto.Signer
	algDetails signature.AlgorithmDetails
	hint       []byte
}

// newHeldKeypair wraps an ECDSA P-256 signer as a sign.Keypair. The hint
// is the base64-encoded SHA-256 of the PKIX public key — the fingerprint
// sigstore uses to identify the verification key.
func newHeldKeypair(signer crypto.Signer) (*heldKeypair, error) {
	algDetails, err := signature.GetAlgorithmDetails(protocommon.PublicKeyDetails_PKIX_ECDSA_P256_SHA_256)
	if err != nil {
		return nil, fmt.Errorf("signing: algorithm details: %w", err)
	}
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return nil, fmt.Errorf("signing: marshal public key: %w", err)
	}
	hashed := sha256.Sum256(pubKeyBytes)
	hint := []byte(base64.StdEncoding.EncodeToString(hashed[:]))
	return &heldKeypair{signer: signer, algDetails: algDetails, hint: hint}, nil
}

func (h *heldKeypair) GetHashAlgorithm() protocommon.HashAlgorithm {
	return h.algDetails.GetProtoHashType()
}

func (h *heldKeypair) GetSigningAlgorithm() protocommon.PublicKeyDetails {
	return h.algDetails.GetSignatureAlgorithm()
}

func (h *heldKeypair) GetHint() []byte { return h.hint }

func (h *heldKeypair) GetKeyAlgorithm() string { return "ECDSA" }

func (h *heldKeypair) GetPublicKey() crypto.PublicKey { return h.signer.Public() }

func (h *heldKeypair) GetPublicKeyPem() (string, error) {
	pem, err := cryptoutils.MarshalPublicKeyToPEM(h.signer.Public())
	if err != nil {
		return "", err
	}
	return string(pem), nil
}

func (h *heldKeypair) SignData(_ context.Context, data []byte) ([]byte, []byte, error) {
	hf := h.algDetails.GetHashType()
	dataToSign := data
	if hf != crypto.Hash(0) {
		hasher := hf.New()
		hasher.Write(data)
		dataToSign = hasher.Sum(nil)
	}
	sig, err := h.signer.Sign(rand.Reader, dataToSign, hf)
	if err != nil {
		return nil, nil, err
	}
	return sig, dataToSign, nil
}
