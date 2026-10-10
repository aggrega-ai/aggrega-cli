// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// trustConditions is the set of halting conditions whose every occurrence is a
// trust or signing failure, and therefore the set whose diagnostics carry the
// trust category.
//
// # Why the set is enumerated here and nowhere else
//
// A category is a property of the occurrence rather than of the condition, so a
// condition cannot declare one beside its identity — one condition reported from
// two places prints two categories, and a value on the identity would be wrong
// for every such condition. Where a failure carries the entity it is about, the
// category is derived from that entity's kind; where it does not, it is fixed by
// the boundary that builds the envelope. A trust failure carries no authoring
// entity — nothing the reader wrote is at fault when a signature does not verify
// — so it is the second kind, and this is that boundary.
//
// Membership is therefore a judgement, and it is written at the one site that
// makes it. There is no second copy to hold in step: nothing else in the module
// asks which conditions are trust conditions.
//
// # Matched through the accessors, never against the registered codes
//
// Each entry is the declaring package's own exported accessor. Comparing
// registered code strings would type-check, would read like a match, and would
// keep compiling after a condition were re-registered under a different code —
// at which point it would silently stop matching. The accessor is the identity,
// so it cannot rot that way.
//
// # What earns a place
//
// Two remedies, one set. A reader meets either "the trust configuration you
// wrote is wrong" — an unreadable trust root, a malformed or absent trust
// policy, no trusted identities declared, a recorded gate the configuration no
// longer attaches — or "this artifact failed verification" — a signature that
// does not verify, an absent signature where one is required, an untrusted
// signer, a plugin whose recorded trusted identity no longer matches. Both are
// the same concern to route to, and the condition's short code beside the
// category in the head is what tells the reader which of the two they hit.
func trustConditions() []error {
	return []error{
		// Signing and verification.
		signing.ErrUnsupportedKey(),
		signing.ErrSignatureInvalid(),
		signing.ErrKeylessLive(),
		signing.ErrSignatureVerificationFailed(),
		signing.ErrUntrustedIdentity(),
		signing.ErrNoSignatureFound(),
		signing.ErrNoTrustedIdentities(),
		signing.ErrTrustRootLoad(),
		signing.ErrUnsignedPushRefused(),
		signing.ErrSigningPolicyRequiresSignature(),
		// A build reading a row a signing requirement covers that records no
		// verified signature.
		lockfile.ErrSignatureNotRecorded(),
		// A recorded signature fact whose cached bundle was made for a different
		// artifact than the one read for the row or entry that records it.
		oci.ErrSignatureForAnotherArtifact(),
		// A registry's advertised trust policy.
		registrytrust.ErrNoTrustAdvertised(),
		registrytrust.ErrMalformedTrust(),
		// The record of the plugin identities trusted for verdict attachments.
		pluginstrust.ErrVerdictAttachmentNotConfigured(),
		pullops.ErrVerdictPluginIdentityChanged(),
		resolver.ErrVerdictPluginTrustMismatch(),
	}
}

// matchTrustCondition reports whether err's chain names any trust or signing
// condition.
//
// It is the trust-layer counterpart of the fetch family's sentinel match: an
// identity test rather than a type assertion, because these failures originate
// as a registered identity wrapped with the key, the reference or the recorded
// digest the occurrence concerns, and never as a structured value with fields to
// read. There is no type for a ToCompilerError method to sit on.
//
// It walks the whole chain through errors.Is, so a condition raised many frames
// below whatever the command returned is still found.
func matchTrustCondition(err error) bool {
	if err == nil {
		return false
	}
	for _, condition := range trustConditions() {
		if stderrors.Is(err, condition) {
			return true
		}
	}
	return false
}

// trustFailureEnvelope builds the categorised envelope a trust or signing
// failure is rendered from, and reports whether err is one.
//
// # It carries the failure's own text and no wrap chain, deliberately
//
// The message is err.Error(), which for a wrapped chain is already the whole
// flattened text — exactly the string the uncategorised fallback prints today.
// So the envelope changes what the reader sees in one respect only: a bracketed
// head appears above text that is otherwise unchanged.
//
// The envelope carries no wrap chain because it has nothing left to say. Its
// message already holds every frame's contribution, so a chain walk beneath it
// would render the same text a second time — and a chain built by a multi-verb
// wrap would render it once per wrapped error under a "caused by (N):" header.
// The short code is read off the ORIGINAL error rather than off this envelope,
// so dropping the chain here costs the head nothing.
//
// # Rendering-only, which is what keeps it honest
//
// This envelope exists for the duration of one render and is never returned to a
// caller, so nothing about errors.Is, errors.As or the chain any command returns
// is touched. The consequence to be aware of is the other side of that: because
// no production path wraps a trust failure, the trust category reaches the
// printed head and not the one-line form CompilerError.Error produces. Giving
// the one-line form a category would mean wrapping at every boundary a trust
// failure crosses — the publish, pull, resolution and validation paths — which
// is several copies of the judgement above rather than one.
func trustFailureEnvelope(err error) (*cerrors.CompilerError, bool) {
	if !matchTrustCondition(err) {
		return nil, false
	}
	return cerrors.Unidentified(cerrors.TrustError, cerrors.SourceLocation{}, err.Error(), nil, nil), true
}
