// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrSignatureNotRecorded reports that a build reads an artifact the content
// cache served — one a pull acquired from a registry — whose lockfile row records
// no verified signature, while the workspace's trust configuration requires one
// for it. A plugin attached at a verdict role has no lockfile row, and the same
// condition reports one whose entry in the record of plugin identities beside the
// lockfile records none, or which that record holds no entry for. A pull verifies
// such an artifact's signature when it fetches it and records the verified fact; a
// build reads the recorded fact rather than verifying again, because a build
// reaches no registry. An artifact with nothing recorded therefore leaves the build
// holding something nothing says was ever verified, and the build stops rather
// than render from it.
//
// Whether a signature is required is the same decision a pull makes when it
// fetches the artifact, so the settings that let a pull admit an artifact with no
// signature — skip_signature_verification, globally or on the artifact's own
// registry entry where the top-level policy does not require the kind, and an
// advisory registry — let the build admit the row too. An artifact read from a
// catalog directory, from inside the compiler binary, or beside the Release is
// never held to it: no registry signature exists for bytes that did not come
// from one.
func ErrSignatureNotRecorded() error { return errSignatureNotRecorded }

var errSignatureNotRecorded = cerrors.Register("signature-not-recorded", cerrors.Meta{
	ShortCode: "E0331",
	Message:   "signing: no verified signature is recorded for an artifact that requires one",
	RulePlace: cerrors.NotARule("A lockfile row, or a verdict plugin's entry in the record beside the lockfile, for " +
		"an artifact acquired from a registry records no verified signature while the trust configuration requires " +
		"one for it. It reports a verification fact that is absent from the record the build reads rather than a " +
		"rule over authored content."),
})

// SignatureNotRecorded returns the refusal for one row a build holds to a
// signing requirement that records no verified signature: uri is the row's
// reference, lockfilePath the path of the lockfile the row is read from,
// requiredBy names the settings that require the signature, in the configuration
// file's own words, and release is the Release as the reader named it, or a
// placeholder where the reporting surface holds none.
//
// The message is the condition's own sentence, then the row, the lockfile that
// records no signature for it and the requirement, then the command that verifies
// the artifact and records the fact in that lockfile. It unwraps to the condition,
// so errors.Is and the condition's codes reach it however many frames a caller
// wraps around it.
func SignatureNotRecorded(uri, lockfilePath, requiredBy, release string) error {
	return signatureNotRecorded(uri, lockfilePath, "artifact", requiredBy, release)
}

// PluginSignatureNotRecorded is SignatureNotRecorded for a plugin attached at a
// verdict role, whose verified signature is recorded not on a lockfile row but in
// the record of plugin identities kept beside the lockfile: coordinate is the
// plugin's identity coordinate, record the path of that record, and requiredBy and
// release as SignatureNotRecorded takes them.
//
// It is the same condition, because it is the same fact — a build about to run an
// artifact a pull acquired from a registry, with nothing recorded to say its
// signature was verified — and the same remedy; only where the fact is recorded
// differs, so the occurrence names that record and the remedy records into it.
func PluginSignatureNotRecorded(coordinate, record, requiredBy, release string) error {
	return signatureNotRecorded(coordinate, record, "plugin", requiredBy, release)
}

// signatureNotRecorded composes one occurrence of the condition for either store:
// the entity, the file that records no signature for it, the settings that require
// one, and the pull that verifies it and records into that file. The two stores'
// occurrences share this one composition, so they cannot come to name different
// things or word the same thing apart.
func signatureNotRecorded(subject, file, noun, requiredBy, release string) error {
	values := []string{fmt.Sprintf("%q", subject), file + " records no signature for it"}
	if requiredBy != "" {
		values = append(values, "required by "+requiredBy)
	}
	advice := fmt.Sprintf("remediation: run `aggrega pull --release %s` to verify the %s's signature at its "+
		"registry and record it in %s", release, noun, file)
	return &signatureNotRecordedError{message: cerrors.ComposeMessage(errSignatureNotRecorded, values, advice)}
}

// signatureNotRecordedError renders the composed refusal and unwraps to the
// condition.
type signatureNotRecordedError struct {
	message string
}

func (e *signatureNotRecordedError) Error() string { return e.message }

func (e *signatureNotRecordedError) Unwrap() error { return errSignatureNotRecorded }
