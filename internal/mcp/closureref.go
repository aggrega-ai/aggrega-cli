// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// closureRef is one reference a resolution reached, as this surface reports it:
// the identity, the version it resolved to, and the kind an author writes.
//
// It is the one shape every tool here reports a resolved reference under, so a
// caller reading two tools meets one entry shape rather than two that happen to
// agree. The version and the kind are omitted when unknown rather than reported
// blank, because a reference that resolved to no version and one that resolved to
// an empty string are different statements and only one of them is true.
//
// Source is where the run that produced the entry took the reference from, in the
// words the command line's pull detail prints for the same resolution. Only an
// answer reporting one run's dependency resolution carries it: it describes what
// that run did on this machine, so a description of a Release's resolved state
// names none. It is omitted for a reference whose lookup recorded no source.
type closureRef struct {
	URI     string `json:"uri"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Source  string `json:"source,omitempty"`
}

// closureRefOf renders one resolved reference into the reported shape, settling
// the kind the way the closure's own axis contract settles it: an axis whose
// members all publish under one kind states that kind, and an axis whose members
// mix kinds leaves it empty and carries a hint per reference.
//
// The kind vocabulary is therefore spelled here once for this surface rather than
// at each tool that reports a reference, and the hint is translated through the
// shared translation every surface reporting a resolution uses — so one entry
// reads the same whichever tool returned it.
func closureRefOf(uri, version, axisKind, kindHint string) closureRef {
	kind := axisKind
	if kind == "" {
		kind = closure.DocumentKind(closure.EntityKindOf(kindHint))
	}
	return closureRef{URI: uri, Version: version, Kind: kind}
}

// closureRefsAcrossAxes renders every reference a resolution reached, across all
// of the closure's sections, in the order the closure declares them, each naming
// where this run took it from.
//
// The source is read from what the resolution recorded for the reference in
// memory, never from how the reference is spelled, and worded by the composition
// the command line's pull detail prints through — so a caller reading this answer
// and a person reading that column are told one source in one spelling. The
// identity, version and kind are rendered by the one projection every tool here
// shares; only the source is added here, because only a resolution's answer
// carries it.
//
// It ranges over the closure's own declared axis set rather than naming the
// sections here. That set is the single enumeration of them, and a section added
// to it is carried into this answer by construction — whereas a tool that listed
// the sections itself would go on reporting the old set, with nothing failing to
// say a section had stopped being reported.
//
// The order is the resolution's own and is not re-sorted. It is already a function
// of the Release's content rather than of anything ambient, which is the same
// property that lets one resolution be written to a lockfile and compared against
// one, so imposing a second order here would be a second statement of the same
// thing and free to disagree with the first.
func closureRefsAcrossAxes(cl closure.Closure) []closureRef {
	axes := cl.VerifiableAxes()
	out := make([]closureRef, 0, len(axes))
	for _, axis := range axes {
		for _, ref := range axis.Refs {
			reported := closureRefOf(ref.URI, ref.ResolvedVersion, axis.Kind, ref.KindHint)
			reported.Source = closure.ReportedResolutionSource(ref.ResolutionPath)
			out = append(out, reported)
		}
	}
	return out
}
