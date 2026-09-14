// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceresolver

import (
	"context"
	"errors"
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// The four accessors below re-export the canonical fetch-error identities
// declared at aggrega-cli/pkg/oci/errors.go. Each yields the canonical identity
// itself rather than a second identity carrying the same text, so errors.Is
// against either name resolves equivalently for any wrapped error chain.
//
// Consumers that want to match against fetch errors without also importing
// pkg/oci use these re-exports directly. Identity is asserted at test time via
// the == comparison of the values they yield; the test will fail if a future
// maintainer declares a fresh identity on this side instead of pointing at the
// canonical one. The declarations deliberately restate no message: the message
// is registered once, beside the canonical declaration, and a second
// registration of one code is refused.

// ErrFetchNotFound re-exports the canonical not-found fetch identity.
func ErrFetchNotFound() error { return oci.ErrFetchNotFound() }

// ErrFetchUnauthorized re-exports the canonical unauthorized fetch identity.
func ErrFetchUnauthorized() error { return oci.ErrFetchUnauthorized() }

// ErrFetchNetworkUnreachable re-exports the canonical network-unreachable fetch identity.
func ErrFetchNetworkUnreachable() error { return oci.ErrFetchNetworkUnreachable() }

// ErrFetchIntegrity re-exports the canonical integrity-failure fetch identity.
func ErrFetchIntegrity() error { return oci.ErrFetchIntegrity() }

// ErrUnknownKind is returned by Dispatcher.Resolve when no adapter
// is registered for spec.Kind. Distinct from the fetch sentinels;
// names a dispatch-side failure rather than a transport-time
// failure.
func ErrUnknownKind() error { return errUnknownKind }

var errUnknownKind = cerrors.Register("unknown-source-kind", cerrors.Meta{
	ShortCode: "E0186",
	Message:   "sourceresolver: no adapter registered for source kind",
	RulePlace: cerrors.NotARule("No adapter is registered for the source kind a resolution dispatched on. It reports a " +
		"dispatch that found no handler — how this program is assembled — rather than a rule over " +
		"anything an author wrote."),
})

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
//
// A resolution the caller stopped is the one case Wrap does not dress
// as a fetch failure. Every adapter learns about a withdrawal the same
// way any other transport error arrives — its own error mapping sees a
// broken fetch and reaches for the nearest fetch sentinel — and the
// result would tell someone who stopped the run themselves that the
// network is unreachable and their proxy may need configuring. So a
// cause carrying the caller's withdrawal is reported as the withdrawal,
// with no sentinel and no remediation, and it is returned whole rather
// than flattened into text so a caller can still match it.
func Wrap(spec SourceSpec, sentinel error, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("kind=%s url=%s version=%s: %w",
			spec.Kind, spec.URL, spec.Version, cause)
	}
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
