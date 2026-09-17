// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package externalinputs

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrSchemeNotAllowed reports that an external-input URL uses a scheme
// outside the allowlist (https; http to a loopback host; oci). The wrapping
// error names the offending URL and scheme alongside the allowed set.
func ErrSchemeNotAllowed() error { return errSchemeNotAllowed }

var errSchemeNotAllowed = cerrors.Register("scheme-not-allowed", cerrors.Meta{
	ShortCode: "E0128",
	Message:   "externalinputs: URL scheme is not allowed",
	RulePlace: cerrors.NotARule("A render asked the host to fetch a URL whose scheme sits outside the set the " +
		"host will fetch over, so the fetch was refused. It reports an operation this host does not perform " +
		"rather than a rule refusing content, since the URL is one the render produced and no authored " +
		"document enumerates it."),
})

// ErrAcquisitionDisabled reports that an unpinned URL was requested by a render
// that may not acquire — the frozen-closure guarantee working as intended. The
// wrapping error names the URL and the remedy: run the pull step, which pins
// every external input a Release's render consumes.
func ErrAcquisitionDisabled() error { return errAcquisitionDisabled }

var errAcquisitionDisabled = cerrors.Register("acquisition-disabled", cerrors.Meta{
	ShortCode: "E0126",
	Message:   "externalinputs: acquisition is disabled for unpinned inputs",
	RulePlace: cerrors.NotARule("An unpinned URL was requested by a render that may not acquire, so the acquisition was " +
		"refused. It reports an operation this render is not permitted to perform rather than a rule " +
		"refusing content."),
})

// ErrDigestMismatch reports that freshly-fetched bytes for a pinned URL do
// not match the recorded digest. The recorded digest always wins: nothing is
// written to the store and no bytes are returned. The wrapping error names
// the URL, the pinned digest, and the digest the fetched bytes produced.
func ErrDigestMismatch() error { return errDigestMismatch }

var errDigestMismatch = cerrors.Register("digest-mismatch", cerrors.Meta{
	ShortCode: "E0127",
	Message:   "externalinputs: fetched bytes mismatch the pinned digest",
	RulePlace: cerrors.NotARule("Freshly fetched bytes for a pinned URL hash to something other than the recorded digest. The " +
		"bytes are fetched in order to be hashed, so what it reports is an integrity comparison that " +
		"did not hold."),
})

// ErrStoreCorrupt reports that a store blob read back for a pinned URL fails
// verification against its record — a tampered or corrupted store. The
// wrapping error names the URL and both the expected and observed values.
func ErrStoreCorrupt() error { return errStoreCorrupt }

var errStoreCorrupt = cerrors.Register("store-corrupt", cerrors.Meta{
	ShortCode: "E0129",
	Message:   "externalinputs: store blob mismatches its pinned record",
	RulePlace: cerrors.NotARule("A store blob read back for a pinned URL fails verification against its own record. It " +
		"reports an integrity comparison over stored bytes that did not hold rather than a rule " +
		"refusing anything an author wrote."),
})
