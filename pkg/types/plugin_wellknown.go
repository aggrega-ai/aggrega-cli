// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The scopes a well-known-annotation entry may declare. The first three are the
// Recipe input kinds, spelled exactly as the adjacent PluginInputKinds fields
// spell them, so one concept carries one name on the authoring surface. The
// fourth names a key resolved once per boundary rather than per node, for which
// input kind is not a meaningful axis.
const (
	// WellKnownAnnotationScopeSourced declares the key is read when the
	// provider renders a vendored file set.
	WellKnownAnnotationScopeSourced = "sourced"
	// WellKnownAnnotationScopeSynthesized declares the key is read when the
	// provider materialises a Workload.
	WellKnownAnnotationScopeSynthesized = "synthesized"
	// WellKnownAnnotationScopeArtifactsStandalone declares the key is read when
	// the provider renders a node whose whole output is imported artifact
	// documents.
	WellKnownAnnotationScopeArtifactsStandalone = "artifactsStandalone"
	// WellKnownAnnotationScopeBoundary declares the key is resolved once per
	// boundary — naming an assembled artifact, or supplying the placement
	// context a cross-boundary reference is shaped from — rather than on any
	// one node's render path.
	WellKnownAnnotationScopeBoundary = "boundary"
)

// ErrWellKnownAnnotationScope fires when a provider declares a well-known
// annotation at an input-kind scope its own render-behavior contract does not
// claim to render — a key declared on the synthesized path by a provider that
// declares it does not render synthesized input, for example. The two claims sit
// in one entity and contradict each other, so the entity is refused at load
// naming the key, the scope and the contract field that disagrees.
func ErrWellKnownAnnotationScope() error { return errWellKnownAnnotationScope }

var errWellKnownAnnotationScope = cerrors.Register("types/well-known-annotation-scope", cerrors.Meta{
	Message: "wellKnownAnnotations: a key is declared at a scope the provider's contract does not render",
})

// ValidateWellKnownAnnotationScopes reports whether every scope a provider
// declares on its well-known annotations is one the same entity's
// contract.inputKinds claims, returning a Loud Guardrail error wrapping
// [ErrWellKnownAnnotationScope] on the first disagreement and nil otherwise.
//
// It is the whole of what a compiler can honestly check about a support claim.
// A schema cannot know what a rendering engine reads, so nothing can prove a
// declaration true; what it can do is refuse a declaration that contradicts the
// provider's own adjacent claim, which is a comparison between two fields of one
// entity and needs no knowledge of the engine whatsoever. The residue is real
// and is not papered over: a key declared on an input kind the provider does
// render, whose engine still reads nothing there, passes here.
//
// It lives beside the type it validates rather than beside the entity-against-
// guest cross-check, because the two answer different questions. That check
// compares an authored claim to a plugin's own describe() self-report and needs
// a loaded plugin; this one compares an entity to itself, so it fires wherever
// the entity is read — including a catalog walk with no plugin binary present,
// which is the surface a catalog author actually uses.
//
// A boundary-scoped entry names no input kind and is not compared. An entry with
// no scope makes no per-path claim and is likewise not compared: it states only
// that the engine reads the key somewhere.
func ValidateWellKnownAnnotationScopes(p Plugin) error {
	if p.Spec.Provider == nil {
		return nil
	}
	for _, a := range p.Spec.Provider.WellKnownAnnotations {
		for _, scope := range a.Scope {
			field, claimed := inputKindClaim(p.Spec.Provider.Contract, scope)
			if field == "" || claimed {
				continue
			}
			return fmt.Errorf(
				"provider %q declares well-known annotation %q at scope %q, but its contract.inputKinds.%s is not claimed; declare the key only at a scope the provider renders, or claim the input kind: %w",
				p.Metadata.Name, a.Key, scope, field, ErrWellKnownAnnotationScope())
		}
	}
	return nil
}

// inputKindClaim resolves a declared scope to the contract.inputKinds field it
// constrains and whether that field is claimed. It reports an empty field name
// for a scope that names no input kind — the boundary scope — so the caller
// skips it rather than treating it as unclaimed.
//
// A nil contract claims no input kind, which is what a provider that authors no
// render-behavior contract has said; an input-kind scope declared against it is
// therefore a contradiction on the same terms as one declared against a contract
// that names the field false.
func inputKindClaim(c *ProviderContract, scope string) (field string, claimed bool) {
	var kinds PluginInputKinds
	if c != nil {
		kinds = c.InputKinds
	}
	switch scope {
	case WellKnownAnnotationScopeSourced:
		return "sourced", kinds.Sourced
	case WellKnownAnnotationScopeSynthesized:
		return "synthesized", kinds.Synthesized
	case WellKnownAnnotationScopeArtifactsStandalone:
		return "artifactsStandalone", kinds.ArtifactsStandalone
	}
	return "", false
}
