// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"hash"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// SignedArtifact is what a signature bundle says it was made for: the hashes it
// names for the bytes the signature binds, each under the algorithm that took it.
// For a signature the toolchain verifies, those bytes are the artifact's manifest
// digest as a string, so the hashes name the artifact the signature was made for.
type SignedArtifact struct {
	// Digests are the hashes the signature names, in the order the bundle carries
	// them. A message signature names one: the hash of the bytes it signed. A
	// signed envelope names the hashes its statement lists for its subjects, of
	// which only those a pull's verification compares are kept — see
	// SignedArtifactOf — so a hash listed under an algorithm that verification
	// never compares is not here. It is empty for an envelope whose statement
	// binds no artifact at all.
	Digests []SignedDigest
}

// SignedDigest is one hash a signature names for what it was made for.
type SignedDigest struct {
	// Algorithm is the hash algorithm that took Digest, spelled as the bundle
	// format spells a message digest's algorithm: SHA2_256, SHA2_384 or SHA2_512.
	Algorithm string
	// Digest is the raw hash.
	Digest []byte
}

// MadeFor reports whether the signature was made for the artifact whose manifest
// digest is manifestDigest: whether one of the hashes the bundle names is the
// hash, under its algorithm, of that digest's string. It is the comparison a
// pull's verification makes between the artifact it verifies and the hashes the
// bundle names, made here with no signature verified.
func (s SignedArtifact) MadeFor(manifestDigest string) bool {
	for _, d := range s.Digests {
		h, ok := signedHashFor(d.Algorithm)
		if !ok {
			continue
		}
		h.Write([]byte(manifestDigest))
		if bytes.Equal(h.Sum(nil), d.Digest) {
			return true
		}
	}
	return false
}

// SignedArtifactOf reads, from the bytes of a signature bundle, the hashes of what
// the signature was made for, and reports whether the bundle is in a form this
// reading recognises. It verifies nothing: it decodes the fields that say which
// artifact the signature binds, and nothing else.
//
// It is the one answer to the question "which artifact was this signature made
// for", for a pull and a build alike. A pull asks it of every bundle its
// verification accepts, and refuses one whose answer is not the artifact it just
// verified; a build asks it of the cached bundle a recorded fact names, and
// refuses a fact made for another artifact than the one it read (see
// HoldRecordedSignature).
//
// It recognises the two forms a pull's verification accepts, read as that
// verification reads them:
//
//   - A message signature — the form both of the toolchain's signers write,
//     keyed and keyless alike: the one hash it carries of the bytes it signed,
//     under one of the algorithms a pull's verification accepts for it (SHA2_256,
//     SHA2_384, SHA2_512).
//   - A signed envelope whose payload is an in-toto statement: the hashes its
//     statement lists for its subjects, of those a pull's verification compares.
//     That verification chooses, subject by subject, the smallest set of hash
//     algorithms covering every subject — a subject already holding a hash under
//     an algorithm chosen for an earlier one adds none, and one that does not
//     adds the strongest of SHA-512, SHA-384 and SHA-256 it holds — and compares
//     the artifact only under the algorithms chosen. So a hash listed under an
//     algorithm left unchosen is never compared, and is not read here. A hash
//     under an algorithm it does not support, or one that is not hexadecimal, is
//     ignored. A statement listing no subject, more subjects or more hashes per
//     subject than that verification admits, or no hash under a supported
//     algorithm binds no artifact, and is read as naming none.
//
// Every spelling the bundle format's JSON mapping admits for the fields read is
// read the way that mapping reads it: each field under its JSON name or its
// declared name, a member written as null as absent, the message digest's
// algorithm by name or by number, and bytes in either base64 alphabet, padded or
// not. A bundle that is not JSON, one carrying both forms or neither, an envelope
// whose payload is not an in-toto statement or cannot be decoded, and a message
// signature whose digest is absent, empty or under another algorithm are reported
// as not recognised, and the caller decides what that means.
//
// # A deliberate second reader of the bundle format
//
// The verification library a pull uses reads these same fields, and this function
// is a second, independent reading of them. That is deliberate rather than an
// oversight to unify: this package is on the path a build takes, a build verifies
// no signature, and the verification library may not enter that path at all. So
// the reading is written here with the standard library alone, and is held to the
// library's own reading twice over: by a parity test outside the build path, over
// a bundle in each form from the virtual Sigstore the verification tests sign
// with and from the toolchain's keyed signer, and at every pull, whose
// verification refuses a bundle this reading does not say was made for the
// artifact the library verified. Replacing it with a call into the verification
// library would bring that library into every build; moving it into the
// verification package would take it away from the build that needs it.
func SignedArtifactOf(bundle []byte) (SignedArtifact, bool) {
	top, ok := jsonObject(bundle)
	if !ok {
		return SignedArtifact{}, false
	}
	message, hasMessage := protoField(top, "messageSignature", "message_signature")
	envelope, hasEnvelope := protoField(top, "dsseEnvelope", "dsse_envelope")
	switch {
	case hasMessage && !hasEnvelope:
		return messageSignatureArtifact(message)
	case hasEnvelope && !hasMessage:
		return envelopeArtifact(envelope)
	}
	return SignedArtifact{}, false
}

// messageSignatureArtifact reads a message signature's one hash of what it signed.
func messageSignatureArtifact(field json.RawMessage) (SignedArtifact, bool) {
	message, ok := jsonObject(field)
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
	raw, ok := protoBytes(valueField)
	if !ok || len(raw) == 0 {
		return SignedArtifact{}, false
	}
	return SignedArtifact{Digests: []SignedDigest{{Algorithm: algorithm, Digest: raw}}}, true
}

// inTotoPayloadType is the payload type of a signed envelope whose payload is an
// in-toto statement — the only payload a pull's verification reads a statement
// from.
const inTotoPayloadType = "application/vnd.in-toto+json"

// maxStatementSubjects and maxSubjectDigests are the most subjects a statement,
// and the most hashes a subject, a pull's verification admits. A statement over
// either is refused there, so it binds no artifact.
const (
	maxStatementSubjects = 1024
	maxSubjectDigests    = 32
)

// envelopeArtifact reads, from a signed envelope, the hashes its in-toto
// statement lists for its subjects under the algorithms a pull's verification
// compares.
func envelopeArtifact(field json.RawMessage) (SignedArtifact, bool) {
	envelope, ok := jsonObject(field)
	if !ok {
		return SignedArtifact{}, false
	}
	// The payload type is one field with one spelling: the envelope format
	// declares it under the name its JSON mapping also uses.
	typeField, ok := protoField(envelope, "payloadType")
	if !ok {
		return SignedArtifact{}, false
	}
	var payloadType string
	if err := json.Unmarshal(typeField, &payloadType); err != nil || payloadType != inTotoPayloadType {
		return SignedArtifact{}, false
	}
	payloadField, ok := protoField(envelope, "payload")
	if !ok {
		return SignedArtifact{}, false
	}
	payload, ok := protoBytes(payloadField)
	if !ok {
		return SignedArtifact{}, false
	}
	statement, ok := jsonObject(payload)
	if !ok {
		return SignedArtifact{}, false
	}
	subjects, ok := statementSubjects(statement)
	if !ok {
		return SignedArtifact{}, false
	}
	return SignedArtifact{Digests: comparedSubjectDigests(subjects)}, true
}

// statementSubjects reads the digest map of each subject a statement lists, in
// the order it lists them. An absent or null list is no subject, and an absent or
// null digest map is a subject with no hash; a list, a subject or a hash of
// another JSON type is not a statement this reading recognises.
func statementSubjects(statement map[string]json.RawMessage) ([]map[string]string, bool) {
	listField, ok := protoField(statement, "subject")
	if !ok {
		return nil, true
	}
	var list []json.RawMessage
	if err := json.Unmarshal(listField, &list); err != nil {
		return nil, false
	}
	subjects := make([]map[string]string, 0, len(list))
	for _, element := range list {
		subject, ok := jsonObject(element)
		if !ok {
			return nil, false
		}
		digests := map[string]string{}
		if digestField, ok := protoField(subject, "digest"); ok {
			if err := json.Unmarshal(digestField, &digests); err != nil {
				return nil, false
			}
		}
		subjects = append(subjects, digests)
	}
	return subjects, true
}

// comparedSubjectDigests returns, subject by subject, the hashes a pull's
// verification compares an artifact against: those under the algorithms it
// chooses (chosenSubjectAlgorithms), decoded from hexadecimal, with a hash that
// does not decode left out. Within a subject they are taken in the order of their
// algorithm names, so the answer does not depend on how the statement orders
// them. A statement past the verification's limits, or listing no subject, binds
// no artifact, so it yields none.
func comparedSubjectDigests(subjects []map[string]string) []SignedDigest {
	if len(subjects) == 0 || len(subjects) > maxStatementSubjects {
		return nil
	}
	for _, subject := range subjects {
		if len(subject) > maxSubjectDigests {
			return nil
		}
	}
	chosen := chosenSubjectAlgorithms(subjects)
	var out []SignedDigest
	for _, subject := range subjects {
		for _, name := range sortedNames(subject) {
			algorithm, ok := subjectAlgorithm(name)
			if !ok || !slices.Contains(chosen, algorithm) {
				continue
			}
			raw, err := hex.DecodeString(subject[name])
			if err != nil {
				continue
			}
			out = append(out, SignedDigest{Algorithm: algorithm, Digest: raw})
		}
	}
	return out
}

// subjectAlgorithmsStrongestFirst is the order a pull's verification prefers the
// algorithms it supports in when it chooses one for a subject.
var subjectAlgorithmsStrongestFirst = []string{"SHA2_512", "SHA2_384", "SHA2_256"}

// chosenSubjectAlgorithms is the smallest set of supported algorithms covering
// every subject, chosen as a pull's verification chooses it: subject by subject,
// one that already holds a hash under a chosen algorithm adds nothing, and one
// that does not adds the strongest supported algorithm it holds a hash under.
func chosenSubjectAlgorithms(subjects []map[string]string) []string {
	var chosen []string
	for _, subject := range subjects {
		var held []string
		for _, name := range sortedNames(subject) {
			if algorithm, ok := subjectAlgorithm(name); ok {
				held = append(held, algorithm)
			}
		}
		if slices.ContainsFunc(held, func(a string) bool { return slices.Contains(chosen, a) }) {
			continue
		}
		for _, algorithm := range subjectAlgorithmsStrongestFirst {
			if slices.Contains(held, algorithm) {
				chosen = append(chosen, algorithm)
				break
			}
		}
	}
	return chosen
}

// subjectAlgorithm reads the name a statement lists a subject's hash under as one
// of the algorithms a pull's verification supports, accepting the name a
// statement conventionally uses and the bundle format's own; any other name is
// one it ignores.
func subjectAlgorithm(name string) (string, bool) {
	switch name {
	case "sha256", "SHA2_256":
		return "SHA2_256", true
	case "sha384", "SHA2_384":
		return "SHA2_384", true
	case "sha512", "SHA2_512":
		return "SHA2_512", true
	}
	return "", false
}

// sortedNames returns a digest map's names in ascending order.
func sortedNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
// under its declared name, and reads a member written as null as absent, so a
// reader that looked under one name only, or took a null for a value, would read
// a bundle otherwise than the format does.
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
// message signature. A number is read as the format reads one: any JSON number
// whose value is a whole number, so 2, 2.0 and 20e-1 name the same algorithm.
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
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || value != math.Trunc(value) {
		return "", false
	}
	for _, algorithm := range hashAlgorithmNumbers {
		if float64(algorithm.number) == value {
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
// accepts, and false for any other.
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

// protoBytes decodes a bytes member as the bundle format's JSON mapping decodes
// one: a JSON string in base64, in the URL-safe alphabet where it carries a
// character only that alphabet has and in the standard one otherwise, and
// unpadded where its length is not a multiple of four.
func protoBytes(field json.RawMessage) ([]byte, bool) {
	var s string
	if err := json.Unmarshal(field, &s); err != nil {
		return nil, false
	}
	encoding := base64.StdEncoding
	if strings.ContainsAny(s, "-_") {
		encoding = base64.URLEncoding
	}
	if len(s)%4 != 0 {
		encoding = encoding.WithPadding(base64.NoPadding)
	}
	raw, err := encoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	return raw, true
}
