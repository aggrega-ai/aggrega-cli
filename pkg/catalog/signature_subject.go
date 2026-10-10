// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// SignatureSubject is where a pinned reference's signature is asked about: the
// catalog kind it publishes under, the registry location its signature referrers
// are attached at, and the registry host whose trust policy decides what that
// signature must be.
type SignatureSubject struct {
	// Kind is the canonical catalog kind — "Component", "SchemaLibrary",
	// "Plugin" — or empty where the reference's kind cannot be determined.
	Kind string
	// Location is the OCI repository reference the reference was bound to, or
	// empty for a reference that carries its own host and is not bound.
	Location string
	// Host is the registry host the reference resolves from, read out of
	// Location where the reference was bound and out of the reference itself
	// otherwise.
	Host string
}

// SignatureSubjectOf returns where the signature of one closure reference is asked
// about, and false where the reference names no registry at all — the references
// a signature check never asks about, because no registry signature can exist for
// them.
//
// It is the one binding of a reference to its signature subject. The pull's
// signature pass reads it to decide which references to verify and against which
// location and policy; the build's check of recorded signatures reads it to decide
// which policy covers a row the content cache served. A second binding at either
// site could put one reference under two hosts, and the two answers to whether its
// signature is required would then disagree with nothing reporting it.
//
// registry is the default registry's location prefix, the one a bare identity is
// bound under — registryhost.Location.Prefix of the workspace's default registry.
// The kind comes from the axis, and on a mixed-kind axis from the reference's own
// collection-site hint. A catalog entity or library carries a host-less bare
// identity, so it is bound under registry through RegistryReference, the single
// home of the join push publishes at and every pull tier fetches from; a
// host-carrying artifact reference is not bound, and its own host is read instead.
//
// Two shapes report false. A member of a single-kind axis — a library — that
// cannot be bound has no reference to fall back to: its identity is host-less, and
// reading its version suffix's colon as a host:port authority would invent a
// location. And a reference whose host is not a registry host
// (registryhost.NamesHost) carries no registry signature to check.
func SignatureSubjectOf(registry string, axis closure.VerifiableAxis, ref closure.VerifiableRef) (SignatureSubject, bool) {
	kind, location, bound := signatureBinding(registry, axis, ref)
	if axis.Kind != "" && !bound {
		return SignatureSubject{}, false
	}
	host := registryhost.Of(ref.URI)
	if bound {
		host = registryhost.Of(location)
	}
	if !registryhost.NamesHost(host) {
		return SignatureSubject{}, false
	}
	return SignatureSubject{Kind: kind, Location: location, Host: host}, true
}

// PluginSignatureSubjectOf returns where the signature of the Plugin entity uri
// names is asked about, and false where it names no registry — SignatureSubjectOf
// for a reference on a mixed-kind axis carrying the Plugin kind hint.
//
// It serves a plugin no closure axis holds: one attached at a verdict role, which
// shapes no rendered byte and so is recorded beside the lockfile rather than in
// the closure. The pull that caches such a plugin verifies its signature through
// this binding, and the build holds the fact that pull recorded through it, so the
// two read one host and one kind for it exactly as they do for a closure reference.
func PluginSignatureSubjectOf(registry, uri string) (SignatureSubject, bool) {
	return SignatureSubjectOf(registry, closure.VerifiableAxis{}, closure.VerifiableRef{URI: uri, KindHint: pluginEntityKindHint})
}

// signatureBinding resolves one closure reference's catalog kind and the registry
// location its signature referrers are attached at, reporting bound=false when no
// location can be formed — the same posture the puller takes when it cannot bind
// a bare identity.
//
// The two shapes differ only in where the kind comes from, and the axis says
// which: a mixed-kind axis carries a collection-site hint per reference, and a
// single-kind axis states the kind once. A library identity is not
// scope-qualified — unlike a Recipe's provider/name or a Plugin's pluginType/name
// — so its location is the kind path segment plus the bare name, which is what the
// location rule produces for an empty scope.
func signatureBinding(registry string, axis closure.VerifiableAxis, ref closure.VerifiableRef) (kind, location string, bound bool) {
	if axis.Kind == "" {
		// The canonical kind comes from the closure kind hint through the single
		// normalization primitive, so the signing policy's kind scope is consulted
		// without the kind vocabulary being spelled again here.
		location, bound = BindEntityLocation(registry, ref.KindHint, ref.URI)
		return EntityKindForHint(ref.KindHint), location, bound
	}
	if registry == "" {
		return axis.Kind, "", false
	}
	identity, _, _ := resolver.SplitVersionedURI(ref.URI)
	return axis.Kind, RegistryReference(registry, axis.Kind, "", identity), true
}
