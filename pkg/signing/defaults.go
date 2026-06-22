// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

// The public-good Sigstore service URLs, used by the keyless signer when a
// caller supplies no explicit endpoint. They are the community-operated
// instances Sigstore publishes for general use: Fulcio issues the
// short-lived signing certificate, Rekor records the signature in the
// public transparency log, and the timestamp authority stamps it. A caller
// targeting a private Sigstore deployment overrides these per call.
const (
	// DefaultFulcioURL is the public-good Fulcio certificate-authority URL.
	DefaultFulcioURL = "https://fulcio.sigstore.dev"
	// DefaultRekorURL is the public-good Rekor transparency-log URL.
	DefaultRekorURL = "https://rekor.sigstore.dev"
	// DefaultTimestampURL is the public-good RFC3161 timestamp-authority URL.
	DefaultTimestampURL = "https://timestamp.sigstore.dev"

	// DefaultOIDCIssuer is the public-good Sigstore OAuth issuer used to
	// acquire a short-lived OIDC identity token for interactive keyless
	// signing. It is the federated login broker the community Fulcio trusts.
	DefaultOIDCIssuer = "https://oauth2.sigstore.dev/auth"
	// DefaultOIDCClientID is the public-good OAuth client identifier the
	// Sigstore issuer recognises for the interactive token-acquisition flow.
	DefaultOIDCClientID = "sigstore"
)
