// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"hash"
	"strconv"
)

// SignedArtifact is what a signature bundle says it was made for: the hash of the
// bytes that were signed, and the algorithm that took it. For a bundle the
// toolchain's signers write, the signed bytes are the artifact's manifest digest
// as a string, so the hash names the artifact the signature was made for.
type SignedArtifact struct {
	// Algorithm is the hash algorithm the bundle names for Digest, spelled as the
	// bundle format spells it: SHA2_256, SHA2_384 or SHA2_512.
	Algorithm string
	// Digest is the raw hash the bundle carries of the signed bytes.
	Digest []byte
}

// MadeFor reports whether the signature was made for the artifact whose manifest
// digest is manifestDigest: whether the hash the bundle carries is the hash,
// under the algorithm it names, of that digest's string. It is the comparison a
// pull's verification makes between the artifact it verifies and the hash the
// bundle carries, made here with no signature verified.
func (s SignedArtifact) MadeFor(manifestDigest string) bool {
	h, ok := signedHashFor(s.Algorithm)
	if !ok {
		return false
	}
	h.Write([]byte(manifestDigest))
	return bytes.Equal(h.Sum(nil), s.Digest)
}

// SignedArtifactOf reads, from the bytes of a cached signature bundle, the hash of
// what the signature was made for, and reports whether the bundle is in a form
// this reading recognises. It verifies nothing: it decodes one field of the
// bundle and nothing else.
//
// The form it recognises is a message signature — the form both of the
// toolchain's signers write, keyed and keyless alike — carrying a message digest
// under one of the hash algorithms a pull's verification accepts for it. Every
// spelling the bundle format admits for those fields is read: each field under
// its JSON name or its declared name, the algorithm by name or by number, and the
// digest in either base64 alphabet, padded or not. Anything else — a bundle that
// is not JSON, one carrying a signed envelope in place of a message signature, or
// one whose message digest is absent or names another algorithm — is reported as
// not recognised, and the caller decides what that means (see
// HoldRecordedSignature).
//
// # A deliberate second reader of the bundle format
//
// The verification library a pull uses reads this same field, and this function
// is a second, independent reading of it. That is deliberate rather than an
// oversight to unify: this package is on the path a build takes, a build verifies
// no signature, and the verification library may not enter that path at all. So
// the reading is written here with the standard library alone, and it is held to
// the library's own reading by a parity test outside the build path, over a bundle
// from each of the toolchain's signers. Replacing it with a call into the
// verification library would bring that library into every build.
func SignedArtifactOf(bundle []byte) (SignedArtifact, bool) {
	top, ok := jsonObject(bundle)
	if !ok {
		return SignedArtifact{}, false
	}
	signature, ok := protoField(top, "messageSignature", "message_signature")
	if !ok {
		return SignedArtifact{}, false
	}
	message, ok := jsonObject(signature)
	if !ok {
		return SignedArtifact{}, false
	}
	digestField, ok := protoField(message, "messageDigest", "message_digest")
	if !ok {
		return SignedArtifact{}, false
	}
	digestObject, ok := jsonObject(digestField)
	if !ok {
		return SignedArtifact{}, false
	}
	algorithmField, ok := protoField(digestObject, "algorithm")
	if !ok {
		return SignedArtifact{}, false
	}
	algorithm, ok := hashAlgorithmName(algorithmField)
	if !ok {
		return SignedArtifact{}, false
	}
	valueField, ok := protoField(digestObject, "digest")
	if !ok {
		return SignedArtifact{}, false
	}
	var encoded string
	if err := json.Unmarshal(valueField, &encoded); err != nil {
		return SignedArtifact{}, false
	}
	raw, ok := decodeProtoBytes(encoded)
	if !ok || len(raw) == 0 {
		return SignedArtifact{}, false
	}
	return SignedArtifact{Algorithm: algorithm, Digest: raw}, true
}

// jsonObject decodes data as one JSON object, its members left undecoded.
func jsonObject(data []byte) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, false
	}
	return object, true
}

// protoField returns the member of object under the first of names it carries
// that is not null. The bundle format admits each field under its JSON name and
// under its declared name, so a reader that looked under one only would miss a
// bundle written with the other.
func protoField(object map[string]json.RawMessage, names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		if value, ok := object[name]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return value, true
		}
	}
	return nil, false
}

// hashAlgorithmName reads the algorithm member of a message digest, which the
// bundle format writes by enumeration name or by enumeration number, and returns
// its name where it is one of the algorithms a pull's verification accepts for a
// message signature.
func hashAlgorithmName(field json.RawMessage) (string, bool) {
	var name string
	if err := json.Unmarshal(field, &name); err == nil {
		if _, ok := signedHashFor(name); ok {
			return name, true
		}
		return "", false
	}
	var number json.Number
	if err := json.Unmarshal(field, &number); err != nil {
		return "", false
	}
	n, err := strconv.Atoi(number.String())
	if err != nil {
		return "", false
	}
	for _, algorithm := range hashAlgorithmNumbers {
		if algorithm.number == n {
			return algorithm.name, true
		}
	}
	return "", false
}

// hashAlgorithmNumbers is each accepted algorithm's enumeration number in the
// bundle format, read where a bundle writes the algorithm as a number.
var hashAlgorithmNumbers = []struct {
	name   string
	number int
}{{"SHA2_256", 1}, {"SHA2_384", 2}, {"SHA2_512", 3}}

// signedHashFor returns a fresh hash for an algorithm a pull's verification
// accepts for a message signature, and false for any other.
func signedHashFor(algorithm string) (hash.Hash, bool) {
	switch algorithm {
	case "SHA2_256":
		return sha256.New(), true
	case "SHA2_384":
		return sha512.New384(), true
	case "SHA2_512":
		return sha512.New(), true
	}
	return nil, false
}

// decodeProtoBytes decodes a bytes member as the bundle format writes one: base64
// in the standard or the URL-safe alphabet, padded or not.
func decodeProtoBytes(s string) ([]byte, bool) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if raw, err := encoding.DecodeString(s); err == nil {
			return raw, true
		}
	}
	return nil, false
}
