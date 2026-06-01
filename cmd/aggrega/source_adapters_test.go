// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
)

// TestShippedSourceAdaptersRegistered guards the production wiring: the default
// dispatcher must have an adapter registered for every shipped source kind. The
// adapters self-register on import via source_adapters.go's blank imports; if a
// future refactor drops one of those imports, this fails loudly here instead of
// surfacing as every remote source pull failing "no adapter registered".
func TestShippedSourceAdaptersRegistered(t *testing.T) {
	t.Parallel()
	registered := map[string]bool{}
	for _, k := range sourceresolver.Kinds() {
		registered[k] = true
	}
	for _, kind := range []string{
		"helm-oci-chart",
		"helm-https-chart",
		"compose-https-fragment",
		"terraform-module-git",
	} {
		if !registered[kind] {
			t.Errorf("source kind %q has no adapter in the default dispatcher — the wiring import in source_adapters.go is missing", kind)
		}
	}
	// terraform-module-oci is reserved in the schema but ships no adapter yet; it
	// must NOT be registered until its adapter lands.
	if registered["terraform-module-oci"] {
		t.Errorf("terraform-module-oci is reserved and must not have a registered adapter yet")
	}
}
