// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// remediationFor returns the operator-actionable remediation hint for an error
// raised on the registry-pull path. It covers the fetch sentinels declared in
// pkg/oci and, additionally, the reference-binding sentinels declared in this
// package, which the fetch mapping cannot reach.
//
// It delegates to oci.RemediationFor first and consults this package's own
// sentinels only when that returns the empty string, so the fetch mapping keeps
// precedence and none of its strings are restated here. Like the function it
// wraps it returns the empty string for an unrecognised error, so a caller can
// decide whether to omit the remediation entirely, and it matches errors.Is
// against wrapped chains. registryHost is passed through unchanged.
//
// This is deliberately a SECOND remediation mapping rather than an extension of
// oci.RemediationFor, and it must not be folded into that one. The reason is
// import direction: this package imports pkg/oci, so pkg/oci cannot name
// ErrUnscopedRecipeReference — or any sentinel declared here — without inverting
// a live import edge. Anyone consolidating the two mappings later should move
// them in this direction, into this package, and never the other way; hoisting
// these arms into pkg/oci reintroduces that cycle and will not compile.
func remediationFor(err error, registryHost string) string {
	if rem := oci.RemediationFor(err, registryHost); rem != "" {
		return rem
	}
	switch {
	case errors.Is(err, ErrUnscopedRecipeReference()):
		// Reaching a registry pull at all means the local catalog directories were
		// searched first and none held this reference, so naming the local remedy
		// here is a statement about what already happened rather than a guess.
		return "remediation: no local catalog directory held this reference, so it was looked for in the registry, where a provider segment is required; re-run as aggrega pull --release <release> --catalog <dir> so the directory that holds it is searched first (--catalog is repeatable)"
	default:
		return ""
	}
}
