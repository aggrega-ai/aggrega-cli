// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

// The public-good Sigstore service URLs, used by the keyless signer when a
// caller supplies no explicit endpoint. They are the community-operated
// instances Sigstore publishes for general use: Fulcio issues the
// short-lived signing certificate and Rekor records the signature in the
// public transparency log. A caller targeting a private Sigstore deployment
// overrides these per call. There is no default timestamp-authority URL: the
// RFC3161 timestamp authority is opt-in, since the keyless trusted timestamp
// comes from Rekor's transparency-log inclusion.
const (
	// DefaultFulcioURL is the public-good Fulcio certificate-authority URL.
	DefaultFulcioURL = "https://fulcio.sigstore.dev"
	// DefaultRekorURL is the public-good Rekor transparency-log URL.
	DefaultRekorURL = "https://rekor.sigstore.dev"

	// DefaultOIDCIssuer is the public-good Sigstore OAuth issuer used to
	// acquire a short-lived OIDC identity token for interactive keyless
	// signing. It is the federated login broker the community Fulcio trusts.
	DefaultOIDCIssuer = "https://oauth2.sigstore.dev/auth"
	// DefaultOIDCClientID is the public-good OAuth client identifier the
	// Sigstore issuer recognises for the interactive token-acquisition flow.
	DefaultOIDCClientID = "sigstore"
)
