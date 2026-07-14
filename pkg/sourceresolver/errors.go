// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceresolver

import (
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// Re-exports of the canonical fetch-error sentinels declared at
// aggrega-cli/pkg/oci/errors.go. The re-exports identify the same
// underlying sentinel instances (assignment-by-reference, not
// copy), so errors.Is against either name resolves equivalently
// for any wrapped error chain.
//
// Consumers that want to match against fetch errors without also
// importing pkg/oci use these re-exports directly. Identity is
// asserted at test time via the == comparison; the test will fail
// if a future maintainer reaches for errors.New on this side
// instead of preserving the reference.
var (
	ErrFetchNotFound           = oci.ErrFetchNotFound
	ErrFetchUnauthorized       = oci.ErrFetchUnauthorized
	ErrFetchNetworkUnreachable = oci.ErrFetchNetworkUnreachable
	ErrFetchIntegrity          = oci.ErrFetchIntegrity
)

// ErrUnknownKind is returned by Dispatcher.Resolve when no adapter
// is registered for spec.Kind. Distinct from the fetch sentinels;
// names a dispatch-side failure rather than a transport-time
// failure.
var ErrUnknownKind = errors.New("sourceresolver: no adapter registered for source kind")

// Wrap returns an error that satisfies errors.Is(returned, sentinel)
// and carries source-resolver context (kind, URL, version) plus
// the underlying cause's stringified message. Adapters use Wrap to
// produce uniform diagnostic surfaces across all fetch failures.
//
// When cause is nil, the wrapped error omits the cause clause and
// returns just the context-plus-sentinel form.
//
// The sentinel parameter is not restricted to the four ErrFetch*
// sentinels by design — adapters MAY pass any error value as the
// sentinel. The typical case is one of the four fetch sentinels;
// future evolution may introduce kind-specific sentinels (e.g.,
// a terraform-git ref-not-found surface) that benefit from the
// same uniform wrapping. The function is generic across sentinel
// types.
//
// Future evolution may extend the helper to embed structured
// remediation pointers (e.g., a registry-auth-fail wrapping might
// include the `docker login <registry>` command). The signature
// stays stable; the additional context lands inside the function.
func Wrap(spec SourceSpec, sentinel error, cause error) error {
	host := registryhost.Of(spec.URL)
	remediation := oci.RemediationFor(sentinel, host)
	if cause == nil {
		base := fmt.Errorf("sourceresolver: kind=%s url=%s version=%s: %w",
			spec.Kind, spec.URL, spec.Version, sentinel)
		if remediation != "" {
			return fmt.Errorf("%w; %s", base, remediation)
		}
		return base
	}
	base := fmt.Errorf("sourceresolver: kind=%s url=%s version=%s: %w (cause: %s)",
		spec.Kind, spec.URL, spec.Version, sentinel, cause)
	if remediation != "" {
		return fmt.Errorf("%w; %s", base, remediation)
	}
	return base
}
