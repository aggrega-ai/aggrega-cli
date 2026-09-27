// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signingtest

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
)

// KeylessAuthority makes keyless signatures offline: an in-memory certificate
// authority, transparency log and timestamp authority, whose trusted root
// document a workspace pins to verify what it signs through the product's own
// keyless path — the certificate chain, the log entry and its signed promise,
// the timestamp — with nothing contacted.
//
// The in-memory authorities are valid for about an hour from creation, so what
// one signs is verified soon after, and each signature's log time must fall
// within the ten minutes its signing certificate is valid for.
type KeylessAuthority struct {
	vs *ca.VirtualSigstore
}

// KeylessSignature is one signature a KeylessAuthority made.
type KeylessSignature struct {
	// Bundle is the serialized Sigstore bundle, as a signature referrer carries it.
	Bundle []byte
	// LoggedAt is the time the bundle's log entry records, in UTC, to the second.
	LoggedAt time.Time
	// SignedAt is when the signature was made, taken just before its timestamp
	// authority stamped it, so the stamped time is at or a moment after it. A
	// caller that places LoggedAt minutes later can tell the two apart, and so
	// catch a reader that took the timestamp authority's time for the log's.
	SignedAt time.Time
}

// bundleMediaTypeV01 is the Sigstore bundle version whose log entries may carry
// the log's signed promise without an inclusion proof, which is the entry the
// in-memory log produces.
const bundleMediaTypeV01 = "application/vnd.dev.sigstore.bundle+json;version=0.1"

// hashedRekordKind and hashedRekordVersion name the log entry type the
// in-memory log records a message signature as.
const (
	hashedRekordKind    = "hashedrekord"
	hashedRekordVersion = "0.0.1"
)

// NewKeylessAuthority creates the in-memory authorities.
func NewKeylessAuthority() (*KeylessAuthority, error) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		return nil, fmt.Errorf("in-memory sigstore: %w", err)
	}
	return &KeylessAuthority{vs: vs}, nil
}

// Sign signs artifactDigest as identity, vouched for by issuer, and has the
// in-memory log record the signature at loggedAt, truncated to the second.
// loggedAt must lie within ten minutes after the call.
func (a *KeylessAuthority) Sign(identity, issuer, artifactDigest string, loggedAt time.Time) (KeylessSignature, error) {
	signedAt := time.Now().UTC()
	loggedAt = loggedAt.UTC().Truncate(time.Second)
	entity, err := a.vs.SignAtTime(identity, issuer, []byte(artifactDigest), loggedAt)
	if err != nil {
		return KeylessSignature{}, fmt.Errorf("sign: %w", err)
	}

	vc, err := entity.VerificationContent()
	if err != nil {
		return KeylessSignature{}, err
	}
	leaf := vc.Certificate()
	if leaf == nil {
		return KeylessSignature{}, fmt.Errorf("the in-memory signature carries no certificate")
	}
	sc, err := entity.SignatureContent()
	if err != nil {
		return KeylessSignature{}, err
	}
	msg := sc.MessageSignatureContent()
	if msg == nil {
		return KeylessSignature{}, fmt.Errorf("the in-memory signature is not a message signature")
	}
	algorithm, ok := protocommon.HashAlgorithm_value[msg.DigestAlgorithm()]
	if !ok {
		return KeylessSignature{}, fmt.Errorf("unknown digest algorithm %q", msg.DigestAlgorithm())
	}
	timestamps, err := entity.Timestamps()
	if err != nil {
		return KeylessSignature{}, err
	}
	entries, err := entity.TlogEntries()
	if err != nil {
		return KeylessSignature{}, err
	}

	logID, err := a.vs.RekorLogID()
	if err != nil {
		return KeylessSignature{}, err
	}
	var tlogEntries []*protorekor.TransparencyLogEntry
	for _, entry := range entries {
		tle, err := a.entryWithPromise(entry, logID)
		if err != nil {
			return KeylessSignature{}, err
		}
		tlogEntries = append(tlogEntries, tle)
	}
	var signed []*protocommon.RFC3161SignedTimestamp
	for _, ts := range timestamps {
		signed = append(signed, &protocommon.RFC3161SignedTimestamp{SignedTimestamp: ts})
	}

	pb := &protobundle.Bundle{
		MediaType: bundleMediaTypeV01,
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content: &protobundle.VerificationMaterial_X509CertificateChain{
				X509CertificateChain: &protocommon.X509CertificateChain{
					Certificates: []*protocommon.X509Certificate{{RawBytes: leaf.Raw}},
				},
			},
			TlogEntries:               tlogEntries,
			TimestampVerificationData: &protobundle.TimestampVerificationData{Rfc3161Timestamps: signed},
		},
		Content: &protobundle.Bundle_MessageSignature{
			MessageSignature: &protocommon.MessageSignature{
				MessageDigest: &protocommon.HashOutput{
					Algorithm: protocommon.HashAlgorithm(algorithm),
					Digest:    msg.Digest(),
				},
				Signature: msg.Signature(),
			},
		},
	}
	b, err := bundle.NewBundle(pb)
	if err != nil {
		return KeylessSignature{}, fmt.Errorf("assemble bundle: %w", err)
	}
	bundleJSON, err := b.MarshalJSON()
	if err != nil {
		return KeylessSignature{}, fmt.Errorf("marshal bundle: %w", err)
	}
	return KeylessSignature{Bundle: bundleJSON, LoggedAt: loggedAt, SignedAt: signedAt}, nil
}

// entryWithPromise returns the protobuf form of one in-memory log entry with the
// kind, the version and the log's signed promise a serialized bundle carries.
// The in-memory log keeps its promise to itself, so it is signed again here over
// the same payload the log signs: the entry's body, integrated time, index and
// log.
func (a *KeylessAuthority) entryWithPromise(entry *tlog.Entry, logID string) (*protorekor.TransparencyLogEntry, error) {
	tle := entry.TransparencyLogEntry()
	if tle == nil {
		return nil, fmt.Errorf("the in-memory log entry has no protobuf form")
	}
	set, err := a.vs.RekorSignPayload(tlog.RekorPayload{
		Body:           base64.StdEncoding.EncodeToString(tle.CanonicalizedBody),
		IntegratedTime: tle.IntegratedTime,
		LogIndex:       tle.LogIndex,
		LogID:          logID,
	})
	if err != nil {
		return nil, fmt.Errorf("sign the log's promise: %w", err)
	}
	return &protorekor.TransparencyLogEntry{
		LogIndex:          tle.LogIndex,
		LogId:             tle.LogId,
		KindVersion:       &protorekor.KindVersion{Kind: hashedRekordKind, Version: hashedRekordVersion},
		IntegratedTime:    tle.IntegratedTime,
		InclusionPromise:  &protorekor.InclusionPromise{SignedEntryTimestamp: set},
		CanonicalizedBody: tle.CanonicalizedBody,
	}, nil
}

// TrustedRoot serializes the in-memory authorities as a trusted root document.
// The in-memory log names itself by the text of its key's hex digest; a trusted
// root names a log by the digest's bytes, so each log's name is decoded here, or
// the document would pin a log no entry names.
func (a *KeylessAuthority) TrustedRoot() ([]byte, error) {
	rekor, err := logsByKeyID(a.vs.RekorLogs())
	if err != nil {
		return nil, err
	}
	ct, err := logsByKeyID(a.vs.CTLogs())
	if err != nil {
		return nil, err
	}
	tr, err := root.NewTrustedRoot(root.TrustedRootMediaType01, a.vs.FulcioCertificateAuthorities(), ct, a.vs.TimestampingAuthorities(), rekor)
	if err != nil {
		return nil, fmt.Errorf("trusted root: %w", err)
	}
	out, err := tr.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal trusted root: %w", err)
	}
	return out, nil
}

// logsByKeyID copies a log set with each log's identifier decoded from the hex
// text its map key spells.
func logsByKeyID(logs map[string]*root.TransparencyLog) (map[string]*root.TransparencyLog, error) {
	out := make(map[string]*root.TransparencyLog, len(logs))
	for keyID, log := range logs {
		raw, err := hex.DecodeString(keyID)
		if err != nil {
			return nil, fmt.Errorf("log %q is not named by a hex key digest: %w", keyID, err)
		}
		copied := *log
		copied.ID = raw
		out[keyID] = &copied
	}
	return out, nil
}
