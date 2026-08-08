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

var errSchemeNotAllowed = cerrors.Register("externalinputs/scheme-not-allowed", cerrors.Meta{
	Message: "externalinputs: URL scheme is not allowed",
})

// ErrAcquisitionDisabled reports that an unpinned URL was requested while
// acquisition is disabled — the frozen-closure guarantee working as intended.
// The wrapping error names the URL and the remedy: re-run with acquisition
// enabled, or run the standalone acquisition pass to pin the input.
func ErrAcquisitionDisabled() error { return errAcquisitionDisabled }

var errAcquisitionDisabled = cerrors.Register("externalinputs/acquisition-disabled", cerrors.Meta{
	Message: "externalinputs: acquisition is disabled for unpinned inputs",
})

// ErrDigestMismatch reports that freshly-fetched bytes for a pinned URL do
// not match the recorded digest. The recorded digest always wins: nothing is
// written to the store and no bytes are returned. The wrapping error names
// the URL, the pinned digest, and the digest the fetched bytes produced.
func ErrDigestMismatch() error { return errDigestMismatch }

var errDigestMismatch = cerrors.Register("externalinputs/digest-mismatch", cerrors.Meta{
	Message: "externalinputs: fetched bytes mismatch the pinned digest",
})

// ErrStoreCorrupt reports that a store blob read back for a pinned URL fails
// verification against its record — a tampered or corrupted store. The
// wrapping error names the URL and both the expected and observed values.
func ErrStoreCorrupt() error { return errStoreCorrupt }

var errStoreCorrupt = cerrors.Register("externalinputs/store-corrupt", cerrors.Meta{
	Message: "externalinputs: store blob mismatches its pinned record",
})
