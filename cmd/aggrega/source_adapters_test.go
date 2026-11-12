// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
)

// TestShippedSourceAdaptersRegistered guards the production wiring: the default
// dispatcher has an adapter registered for exactly the source kinds this program
// declares it serves. The adapters self-register on import via
// source_adapters.go's blank imports.
//
// Both directions are held. A served kind with no adapter is a source an authored
// Recipe is admitted with and nothing can fetch, which the dispatcher reports as a
// fault in how the program was assembled; a registered adapter for a kind the
// served set omits is one the document check refuses before it could ever be
// dispatched to. Either way the served declaration and the binary disagree, and
// this is where that is caught rather than at a pull.
//
// The reserved terraform-module-oci kind is in neither set, so an adapter
// registered for it fails here as one the served set omits.
func TestShippedSourceAdaptersRegistered(t *testing.T) {
	t.Parallel()
	registered := sourceresolver.Kinds()
	served := sourcekind.ServedKinds()
	if len(served) == 0 {
		t.Fatal("the served set is empty, so the comparison below would pass over nothing")
	}
	for _, kind := range served {
		if !slices.Contains(registered, kind) {
			t.Errorf("source kind %q is served but has no adapter in the default dispatcher — the wiring "+
				"import in source_adapters.go is missing", kind)
		}
	}
	for _, kind := range registered {
		if !sourcekind.Served(kind) {
			t.Errorf("an adapter is registered for source kind %q, which the served set does not declare, "+
				"so the document check refuses every Recipe naming it", kind)
		}
	}
}
