// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package signingtest

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/sign"
)

// InTotoPayloadType is the payload type of a signed envelope whose payload is an
// in-toto statement — the only envelope payload whose statement a pull's
// verification reads.
const InTotoPayloadType = "application/vnd.in-toto+json"

// statementType and statementPredicateType are the statement type an in-toto
// statement declares and the predicate type the statements made here carry.
const (
	statementType          = "https://in-toto.io/Statement/v1"
	statementPredicateType = "https://example.test/attestation/v1"
)

// statementSubject is one subject of an in-toto statement: a name and the hashes
// that identify it, keyed by algorithm.
type statementSubject struct {
	Name   string            `json:"name,omitempty"`
	Digest map[string]string `json:"digest"`
}

// statement is an in-toto statement as its JSON mapping spells it.
type statement struct {
	Type          string             `json:"_type"`
	Subject       []statementSubject `json:"subject"`
	PredicateType string             `json:"predicateType"`
	Predicate     map[string]any     `json:"predicate"`
}

// Statement returns an in-toto statement naming each of artifactDigests as a
// subject the way a pull's verification binds an artifact to a signed envelope:
// by the SHA-256 hash of the digest's string, which is the artifact a pull
// verifies a signature for. An envelope over it verifies, in a pull, for each of
// the digests and for no other.
func Statement(artifactDigests ...string) []byte {
	subjects := make([]map[string]string, 0, len(artifactDigests))
	for _, d := range artifactDigests {
		subjects = append(subjects, map[string]string{"sha256": HashOf("sha256", d)})
	}
	return StatementWithSubjects(subjects...)
}

// StatementWithSubjects returns an in-toto statement whose subjects carry exactly
// the given hashes, each keyed by the algorithm name the statement lists it
// under, in the order given. It is for a statement Statement does not write: one
// whose subjects list a hash under several algorithms, under an algorithm a pull
// does not compare, or none at all.
func StatementWithSubjects(subjects ...map[string]string) []byte {
	s := statement{
		Type:          statementType,
		Subject:       make([]statementSubject, 0, len(subjects)),
		PredicateType: statementPredicateType,
		Predicate:     map[string]any{},
	}
	for i, digests := range subjects {
		s.Subject = append(s.Subject, statementSubject{Name: fmt.Sprintf("subject-%d", i), Digest: digests})
	}
	out, err := json.Marshal(s)
	if err != nil {
		// A statement of strings and string maps always marshals.
		panic(fmt.Sprintf("marshal an in-toto statement: %v", err))
	}
	return out
}

// HashOf returns the hexadecimal hash of s's bytes under algorithm, named as a
// statement names one: "sha256", "sha384" or "sha512". It panics on any other
// name, which is a mistake in the calling test.
func HashOf(algorithm, s string) string {
	switch algorithm {
	case "sha256":
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	case "sha384":
		sum := sha512.Sum384([]byte(s))
		return hex.EncodeToString(sum[:])
	case "sha512":
		sum := sha512.Sum512([]byte(s))
		return hex.EncodeToString(sum[:])
	}
	panic(fmt.Sprintf("HashOf: unsupported algorithm %q", algorithm))
}

// KeyedEnvelope signs statement as a signed envelope with a fresh held key and
// no transparency log, as a keyed signature is made, and returns the serialized
// bundle with the PEM public key that verifies it. It is the envelope form of
// what the toolchain's keyed signer makes as a message signature.
func KeyedEnvelope(statement []byte) (bundleJSON, publicKeyPEM []byte, err error) {
	keypair, err := sign.NewEphemeralKeypair(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("generate a signing key: %w", err)
	}
	pb, err := sign.Bundle(&sign.DSSEData{Data: statement, PayloadType: InTotoPayloadType}, keypair,
		sign.BundleOptions{Context: context.Background()})
	if err != nil {
		return nil, nil, fmt.Errorf("sign the envelope: %w", err)
	}
	b, err := bundle.NewBundle(pb)
	if err != nil {
		return nil, nil, fmt.Errorf("assemble bundle: %w", err)
	}
	out, err := b.MarshalJSON()
	if err != nil {
		return nil, nil, fmt.Errorf("marshal bundle: %w", err)
	}
	pub, err := keypair.GetPublicKeyPem()
	if err != nil {
		return nil, nil, fmt.Errorf("encode the public key: %w", err)
	}
	return out, []byte(pub), nil
}
