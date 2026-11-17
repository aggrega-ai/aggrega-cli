// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package sourcekind names the source kinds a Recipe's or FeatureRecipe's
// structured `source:` can select, and decides which of them this program serves.
//
// A structured source names its kind, and the kind selects the adapter that
// fetches the artifact: a chart from an OCI registry or an HTTPS repository, a
// compose fragment over HTTPS, a Git repository, a module from an OCI registry. The set is held here, in a
// package that depends on nothing, so the readers that need it — the document
// check refusing a kind nothing serves, the lock-time reading of a source, and
// every adapter declaring the kind it registers for — read one declaration
// rather than several lists agreeing by hand.
//
// A kind is served when this program ships an adapter for it. The adapters
// register themselves with the source resolver as the program is assembled, and
// the binary's own tests hold the registered set to the served one, so a kind
// served here and fetched by nothing is a fault in how the program was put
// together rather than in anything an author wrote.
package sourcekind

import (
	"slices"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The source kinds this program serves, each spelled as an author writes it under
// a structured source's `kind:` key.
const (
	// HelmOCIChart is a Helm chart pulled from an OCI registry.
	HelmOCIChart = "helm-oci-chart"
	// HelmHTTPSChart is a Helm chart fetched from an HTTPS chart repository or
	// a direct archive URL.
	HelmHTTPSChart = "helm-https-chart"
	// ComposeHTTPSFragment is a compose fragment fetched over HTTPS.
	ComposeHTTPSFragment = "compose-https-fragment"
	// GitRepository is a Git repository, or a directory inside one.
	GitRepository = "git-repository"
	// BicepModuleOCI is a Bicep module published to an OCI registry.
	BicepModuleOCI = "bicep-module-oci"
	// TerraformModuleOCI is a Terraform or OpenTofu module published to an OCI
	// registry.
	TerraformModuleOCI = "terraform-module-oci"
)

// served is the set Served answers from, in sorted order so ServedKinds hands it
// out without sorting.
var served = []string{
	BicepModuleOCI,
	ComposeHTTPSFragment,
	GitRepository,
	HelmHTTPSChart,
	HelmOCIChart,
	TerraformModuleOCI,
}

// Served reports whether this program serves kind: whether an adapter ships that
// fetches a source of that kind.
//
// The comparison is exact. A kind is a fixed word, so a spelling that differs in
// case or carries a pin names no kind at all, and answering it as one would admit
// a source no adapter is registered under.
func Served(kind string) bool {
	_, found := slices.BinarySearch(served, kind)
	return found
}

// ServedKinds returns the kinds this program serves, sorted. The slice is a copy,
// so a caller reordering it changes nothing Served answers.
func ServedKinds() []string {
	return slices.Clone(served)
}

// ErrUnknownKind reports a Recipe or FeatureRecipe whose structured source names
// a kind this program serves no adapter for — a misspelling, or a kind no adapter
// ships for.
//
// The two are one condition because the author meets them the same way: the
// source cannot be fetched by anything this program carries, and the remedy is to
// name a kind it serves. It is refused as the document is read, ahead of any
// fetch, so a Release reaching such a Recipe stops before a source is resolved,
// and checking the Recipe on its own refuses it too.
func ErrUnknownKind() error { return errUnknownKind }

var errUnknownKind = cerrors.Register("unknown-source-kind", cerrors.Meta{
	ShortCode: "E0186",
	Message:   "no source adapter serves the source kind",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})
