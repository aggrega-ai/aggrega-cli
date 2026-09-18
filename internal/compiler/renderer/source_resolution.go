// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// frSourceURI is the FeatureRecipe analogue of the recipe URI used for source-spec
// evaluation: metadata.name, optionally suffixed with :version.
func frSourceURI(fr *types.FeatureRecipe) string {
	uri := fr.Metadata.Name
	if fr.Metadata.Version != "" {
		uri = uri + ":" + fr.Metadata.Version
	}
	return uri
}

// wrapSourceResolutionError categorises a source-resolution error: fetch sentinels
// route to FETCH_ERROR with an appended remediation pointer (unless the chain already
// carries one); everything else routes to TRANSLATION_ERROR. Shared by the component
// node loop and the FeatureRecipe render sites so both surface source-fetch failures
// identically. label is the leading "node %q ... source resolution" message.
func wrapSourceResolutionError(err error, nodeID, label string) error {
	category := cerrors.TranslationError
	message := label
	if sentinel, ok := matchFetchSentinel(err); ok {
		category = cerrors.FetchError
		if !strings.Contains(err.Error(), "remediation:") {
			if rem := oci.RemediationFor(sentinel, hostFromSourceErr(err)); rem != "" {
				message = message + "; " + rem
			}
		}
	}
	return cerrors.Wrap(err, category, cerrors.SourceLocation{File: nodeID}, message)
}

// matchFetchSentinel walks the error chain via errors.Is against each
// fetch-family sentinel: the four remote-acquisition faults declared at
// pkg/oci/errors.go plus the offline-egress refusal from pkg/egress. Returns
// the matched sentinel + true on first hit; returns (nil, false)
// when the chain carries no fetch sentinel.
//
// The orchestrator uses the matched sentinel to (1) route the
// CompilerError through cerrors.FetchError instead of
// cerrors.TranslationError and (2) call oci.RemediationFor to append
// the operator-actionable remediation when the underlying chain
// doesn't already carry one (e.g., build-time errors from
// pkg/sourcecache that didn't pass through sourceresolver.Wrap).
func matchFetchSentinel(err error) (error, bool) {
	switch {
	case errors.Is(err, oci.ErrFetchNotFound()):
		return oci.ErrFetchNotFound(), true
	case errors.Is(err, oci.ErrFetchUnauthorized()):
		return oci.ErrFetchUnauthorized(), true
	case errors.Is(err, oci.ErrFetchNetworkUnreachable()):
		return oci.ErrFetchNetworkUnreachable(), true
	case errors.Is(err, oci.ErrFetchIntegrity()):
		return oci.ErrFetchIntegrity(), true
	case errors.Is(err, egress.ErrOfflineEgress()):
		// An offline-egress refusal is a fetch-family fault: from the user's
		// perspective it is an inability to fetch. It carries its own remediation,
		// so the classifier appends none.
		return egress.ErrOfflineEgress(), true
	default:
		return nil, false
	}
}

// hostFromSourceErr is a best-effort extractor for the registry
// host substring inside a source-resolution error chain. The
// orchestrator passes it to oci.RemediationFor for build-time
// errors that didn't pass through sourceresolver.Wrap (which would
// otherwise have done the extraction at the lock-time side).
//
// The current implementation scans the error message for a
// "url=<URL>" segment (the format sourceresolver.Wrap emits) and
// returns the host portion of the matched URL. When no URL segment
// is found — typical of pure sourcecache errors — returns "" so
// RemediationFor uses the placeholder.
func hostFromSourceErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	idx := strings.Index(msg, "url=")
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len("url="):]
	end := strings.IndexAny(rest, " \t")
	if end > 0 {
		rest = rest[:end]
	}
	for _, scheme := range []string{"oci://", "https://", "http://", "git+ssh://", "git://", "ssh://"} {
		if strings.HasPrefix(rest, scheme) {
			body := strings.TrimPrefix(rest, scheme)
			if slash := strings.Index(body, "/"); slash > 0 {
				return body[:slash]
			}
			return body
		}
	}
	return ""
}
