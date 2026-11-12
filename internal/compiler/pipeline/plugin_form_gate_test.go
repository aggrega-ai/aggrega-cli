// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuildEntityPluginLookup_EntityOnlyFormGate proves the pipeline's real
// plugin-load lookup — the exact Lookup the renderer binds for pipeline-step and
// provider plugins — rejects a non-entity reference FORM (a filesystem path or an
// artifact reference) early, before consulting the catalog for a Plugin entity,
// naming the offending reference. A well-formed bare entity URI passes the form
// gate and reaches entity translation: the form check layers with, it does not
// replace, entity existence.
func TestBuildEntityPluginLookup_EntityOnlyFormGate(t *testing.T) {
	inputs := fixtureInputs()

	entityConsulted := false
	catalogLookup := resolver.CatalogLookup{
		Plugin: func(uri string) (types.Plugin, bool) {
			entityConsulted = true
			return types.Plugin{}, false
		},
	}
	lookup := buildEntityPluginLookup(inputs, catalogLookup)

	// A non-entity FORM halts early, naming the reference, without consulting the
	// catalog for an entity.
	for _, ref := range []string{
		"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0",
		"frp-step-patch-rust.wasm",
		"./plugins/frp.wasm",
	} {
		entityConsulted = false
		_, err := lookup(ref)
		if !errors.Is(err, pluginpath.ErrPluginEntityNotFound()) {
			t.Errorf("lookup(%q): want ErrPluginEntityNotFound; got %v", ref, err)
		}
		if err != nil && !strings.Contains(err.Error(), ref) {
			t.Errorf("lookup(%q): error must name the offending reference; got %v", ref, err)
		}
		if entityConsulted {
			t.Errorf("lookup(%q): a non-entity form must be rejected before entity translation; the catalog was consulted", ref)
		}
	}

	// A well-formed provider/name:version entity URI passes the form gate and
	// reaches entity translation; it is not caught by the early gate. Because no
	// such entity exists here it still halts late as not-found — the layered
	// behavior.
	entityConsulted = false
	_, err := lookup("core/helm-renderer:0.4.1")
	if !errors.Is(err, pluginpath.ErrPluginEntityNotFound()) {
		t.Fatalf("a non-existent entity URI should still halt not-found; got %v", err)
	}
	if !entityConsulted {
		t.Error("a bare entity URI must pass the form gate and reach entity translation")
	}
}
