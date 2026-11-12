// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package publishops

import "github.com/aggrega-ai/aggrega-cli/pkg/registryhost"

// Reach states how far one publication call reached: every registry host it
// addressed, and whether it attached anything a registry now serves to other
// people.
//
// It is computed here, from what the operation reports it attached, and from
// nowhere else. A surface answering a caller converts it into its own wire
// spelling and adds nothing to it, so the rule for when a call reached beyond
// this machine has one home whichever surface reports it.
type Reach struct {
	// Registries names every registry host the call addressed, each once, in the
	// order the call first addressed it. A dry run names the destination it would
	// have addressed.
	Registries []string

	// BeyondThisMachine is true exactly when the call attached something a
	// registry now serves: uploaded content, a signature, a publication date or a
	// deprecation marker. It is false on a dry run, and on a call that found
	// everything it would have attached already there — a publication run again
	// over a complete version, or a signing batch whose every reference already
	// carried a signature that verifies.
	BeyondThisMachine bool
}

// reachOf is the one statement of the rule. references are what the call
// addressed, in the order it addressed them; attached is whether it attached
// anything at all.
//
// The set below records which hosts are already listed and is never iterated, so
// the order of Registries is the order of references and nothing else.
func reachOf(references []string, attached bool) Reach {
	registries := make([]string, 0, len(references))
	listed := make(map[string]bool, len(references))
	for _, reference := range references {
		host := registryhost.Of(reference)
		if listed[host] {
			continue
		}
		listed[host] = true
		registries = append(registries, host)
	}
	return Reach{Registries: registries, BeyondThisMachine: attached}
}

// Reach reports how far the catalog-entity publication reached. A first
// publication uploads its content; a version already published uploads none and
// reached beyond this machine only where it attached a signature or a date.
func (r PushResult) Reach() Reach {
	uploaded := !r.DryRun && !r.AlreadyPublished
	return reachOf([]string{r.Reference}, uploaded || r.Signed || r.Dated)
}

// Reach reports how far the data-artifact publication reached, on the terms the
// catalog-entity publication's reach states; a data artifact signs nothing, so
// only its content and its date can have been attached.
func (r PushArtifactResult) Reach() Reach {
	uploaded := !r.DryRun && !r.AlreadyPublished
	return reachOf([]string{r.Reference}, uploaded || r.Dated)
}

// Reach reports how far the signing batch reached: every registry its references
// name, and whether it attached any signature — a reference found already signed
// attached nothing.
func (r SignResult) Reach() Reach {
	references := make([]string, 0, len(r.Signed))
	attached := false
	for _, a := range r.Signed {
		references = append(references, a.Reference)
		attached = attached || a.Attached
	}
	return reachOf(references, attached)
}

// Reach reports how far the deprecation reached. Every marker it reports is one it
// attached, so it reached beyond this machine wherever it reports any.
func (r DeprecateResult) Reach() Reach {
	references := make([]string, 0, len(r.Marked))
	for _, m := range r.Marked {
		references = append(references, m.Reference)
	}
	return reachOf(references, len(r.Marked) > 0)
}
