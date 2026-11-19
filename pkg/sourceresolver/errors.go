// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourceresolver

import (
	"context"
	"errors"
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
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

// ErrNoAdapterRegistered is returned by Dispatcher.Resolve when no adapter is
// registered for spec.Kind. Distinct from the fetch sentinels; it names a
// dispatch-side failure rather than a transport-time one.
//
// An author does not reach it by writing a kind nothing serves: that is refused
// as the Recipe or FeatureRecipe is read (sourcekind.ErrUnknownKind), before any
// resolution dispatches. What is left is a kind this program declares it serves
// with no adapter registered for it, or a caller dispatching a kind it assembled
// itself — a fault in how the program was put together.
func ErrNoAdapterRegistered() error { return errNoAdapterRegistered }

var errNoAdapterRegistered = cerrors.Register("source-adapter-not-registered", cerrors.Meta{
	ShortCode: "E0359",
	Message:   "no source adapter is registered in this program for the source kind",
	RulePlace: cerrors.NotARule("A resolution dispatched on a source kind no adapter is registered for. An authored " +
		"kind no adapter serves is refused as its document is read, so what reaches this is how the program " +
		"was assembled — a served kind whose adapter was not linked, or a caller dispatching a kind of its " +
		"own — rather than a rule over anything an author wrote."),
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
// a git-repository ref-not-found surface) that benefit from the
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
	remediation := remediationFor(spec, sentinel)
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

// WrapOCIPull reports a failed pull of an OCI artifact by what the registry
// answered, so every OCI source kind reads one failure the same way.
//
// A cause carrying the caller's withdrawal is reported as Wrap reports one. A
// cause carrying a fetch identity (not found, unauthorized, network unreachable,
// integrity, server error, interrupted transfer) or an artifact-shape identity
// (no content layer, an ambiguous content layer, a retired media type) is
// wrapped under that same identity, whatever else the cause reads as.
// Otherwise a failure [oci.Unreachable] reads as a transport failure is
// reported as network unreachable; a registry answer [oci.ServerError] reads as
// a server error is reported as a server error; and a transfer [oci.Interrupted]
// reads as broken off is reported as interrupted. Any other cause is returned
// with the source's kind, url and version and stays matchable through
// errors.Is, claiming no fetch condition.
func WrapOCIPull(spec SourceSpec, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Wrap(spec, nil, err)
	}
	for _, identity := range []error{
		oci.ErrFetchNotFound(),
		oci.ErrFetchUnauthorized(),
		oci.ErrFetchNetworkUnreachable(),
		oci.ErrFetchIntegrity(),
		oci.ErrFetchServerError(),
		oci.ErrFetchInterrupted(),
		oci.ErrNoContentLayer(),
		oci.ErrAmbiguousContentLayer(),
		oci.ErrRetiredMediaType(),
	} {
		if errors.Is(err, identity) {
			return Wrap(spec, identity, err)
		}
	}
	if oci.Unreachable(err) {
		return Wrap(spec, oci.ErrFetchNetworkUnreachable(), err)
	}
	if oci.ServerError(err) {
		return Wrap(spec, oci.ErrFetchServerError(), err)
	}
	if oci.Interrupted(err) {
		return Wrap(spec, oci.ErrFetchInterrupted(), err)
	}
	return fmt.Errorf("kind=%s url=%s version=%s: %w", spec.Kind, spec.URL, spec.Version, err)
}

// remediationFor is the advice appended to a fetch failure on spec.
//
// A refused credential on a source fetched over http or https is answered by the
// netrc file, which is where those fetches read their credential, so the advice
// names the entry to write and the file the fetch read. Every other case takes
// the remediation the registry fetches share.
func remediationFor(spec SourceSpec, sentinel error) string {
	if errors.Is(sentinel, ErrFetchUnauthorized()) {
		if host, ok := netrc.HostFor(spec.URL); ok {
			file := "the netrc file"
			if path, ok := spec.Netrc.Path(); ok {
				file += " " + path
			}
			return "remediation: add an entry for " + host + " (machine " + host +
				" login <user> password <secret>) to " + file + ", then run the command again"
		}
	}
	return oci.RemediationFor(sentinel, registryhost.Of(spec.URL))
}
