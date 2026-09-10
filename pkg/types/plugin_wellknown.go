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

var errWellKnownAnnotationScope = cerrors.Register("well-known-annotation-scope", cerrors.Meta{
	ShortCode: "E0198",
	Message:   "wellKnownAnnotations: a key is declared at a scope the provider's contract does not render",
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
//
// An entity that is not a provider needs no guard of its own: it carries a zero
// provider block, whose annotation list is empty, so the walk below reports
// nothing without having to ask first whether a block is there.
func ValidateWellKnownAnnotationScopes(p Plugin) error {
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

// WellKnownAnnotationHonoured reports whether a provider's own support claim
// covers key for a node rendering at the given input-kind scope. It is the one
// read of that claim outside the load-time contradiction check: a render surface
// asks it to tell an author whose annotation reaches a provider that cannot act
// on it, so the answer must be derived from the declaration and never from a
// second table restating it.
//
// The claim covers the key when the provider declares an entry for it and that
// entry either names this input-kind scope, names the boundary scope, or names no
// scope at all. The last two are not laxity:
//
//   - A boundary-scoped key is resolved once for the whole boundary rather than
//     on any node's render path, so it is genuinely honoured for every node
//     beneath that boundary whatever input kind each of them renders. Since a
//     boundary annotation reaches every such node's merged annotations, comparing
//     against the input-kind scope alone would report a key the provider does act
//     on as one it ignores.
//   - An entry with no scope states only that the engine reads the key
//     somewhere. It makes no claim about any one path, so there is nothing to
//     contradict and nothing honest to report.
//
// A provider that declares a support claim which names no entry for the key is
// not covered: it has said which keys it reads, and this is not one of them.
//
// 🔴 An ABSENT claim is a different thing from a claim that omits the key, and
// reports covered. A provider that declares no support claim at all has said
// nothing, and inferring "this key is not read" from silence would put a negative
// claim in the provider author's mouth and report it to a deployment author as
// the provider's own word. The residue is real and is not papered over: a
// provider that reads none of these keys and declares nothing is indistinguishable
// here from one that reads them all, so its authors get no advisory. That gap is
// closed by the provider declaring its claim, never by this read guessing at one.
// An entity carrying no provider block reports covered for the same reason, one
// level up: it makes no render claim to compare against, and its zero block
// declares an empty annotation list, which is the same silence.
func WellKnownAnnotationHonoured(p Plugin, key, inputKindScope string) bool {
	if len(p.Spec.Provider.WellKnownAnnotations) == 0 {
		return true
	}
	for _, a := range p.Spec.Provider.WellKnownAnnotations {
		if a.Key != key {
			continue
		}
		if len(a.Scope) == 0 {
			return true
		}
		for _, scope := range a.Scope {
			if scope == inputKindScope || scope == WellKnownAnnotationScopeBoundary {
				return true
			}
		}
	}
	return false
}

// inputKindClaim resolves a declared scope to the contract.inputKinds field it
// constrains and whether that field is claimed. It reports an empty field name
// for a scope that names no input kind — the boundary scope — so the caller
// skips it rather than treating it as unclaimed.
//
// A zero contract claims no input kind, which is what a provider that authors no
// render-behavior contract has said; an input-kind scope declared against it is
// therefore a contradiction on the same terms as one declared against a contract
// that names the field false.
func inputKindClaim(c ProviderContract, scope string) (field string, claimed bool) {
	kinds := c.InputKinds
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
