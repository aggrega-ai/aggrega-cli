// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1 && !light

package plugintest_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// tableTestMaxPages is a generous describe-load memory cap so a large embedded
// provider guest instantiates purely to have its describe() read, rather than
// bottoming out on the platform default cap.
const tableTestMaxPages uint32 = 4096

// declaredTypeOf derives an embedded guest's declared plugin type from its bare
// identity: the first path segment (the socket) is the plugin type.
func declaredTypeOf(uri string) string {
	if i := strings.IndexByte(uri, '/'); i > 0 {
		return uri[:i]
	}
	return uri
}

// TestValidateEmbeddedGuests validates every guest embedded in the CLI binary
// through the plugintest facade, asserting each one describes cleanly and that
// its declared type — derived from its socket URI, independent of the binary's
// self-report — matches the type it reports. Every embedded guest is a shipping
// artifact, so a failure here is a real latent mistype, not an environment
// issue: the guests are prebuilt bytes and never skip.
func TestValidateEmbeddedGuests(t *testing.T) {
	uris := embedded.URIs()
	if len(uris) == 0 {
		t.Fatal("embedded guest set is empty; expected the CLI to embed provider, parser, mutator, and linter guests")
	}
	t.Logf("validating %d embedded guests: %v", len(uris), uris)

	for _, uri := range uris {
		t.Run(uri, func(t *testing.T) {
			b, ok := embedded.Lookup(uri)
			if !ok || len(b) == 0 {
				t.Fatalf("embedded.Lookup(%q) = (%d bytes, %v), want a non-empty hit", uri, len(b), ok)
			}
			declared := declaredTypeOf(uri)
			report, err := plugintest.ValidateGuest(b, plugintest.Options{
				DeclaredType:   declared,
				MaxMemoryPages: tableTestMaxPages,
				Identity:       uri,
			})
			if err != nil {
				t.Fatalf("embedded guest %q declared type %q did not validate: %v", uri, declared, err)
			}
			if report.PluginType != declared {
				t.Fatalf("embedded guest %q reported type %q, want declared %q", uri, report.PluginType, declared)
			}
			t.Logf("PASS %-30s type=%-8s exports=%v", uri, report.PluginType, report.Exports)
		})
	}
}

// pdkFixture names a PDK example directory, the language it is built for, and
// the plugin type its describe() self-report is expected to state. The artifact
// name follows "<example>-<lang>.wasm"; the wasmtest builder resolves it to
// aggrega-pdk/<lang>/examples/<example>/ and builds on demand.
type pdkFixture struct {
	example      string
	lang         string
	declaredType string
}

// pdkFixtures is the PDK example set covered here: the examples each shipped PDK
// language provides a build.sh for and whose describe() self-report states an
// unambiguous plugin type. mutator, parse, and policy are present in all three
// languages; the render-node provider example ships in rust and typescript
// under a fixture-convention-compatible name; the cli example ships a build.sh
// only in typescript. Together they exercise the mutator, parser, policy,
// provider, and cli sockets across languages.
func pdkFixtures() []pdkFixture {
	var out []pdkFixture
	for _, lang := range []string{"go", "rust", "typescript"} {
		out = append(out,
			pdkFixture{example: "mutator", lang: lang, declaredType: "governance"},
			pdkFixture{example: "parse", lang: lang, declaredType: "parser"},
			pdkFixture{example: "policy", lang: lang, declaredType: "governance"},
		)
	}
	out = append(out,
		pdkFixture{example: "render-node", lang: "rust", declaredType: "provider"},
		pdkFixture{example: "render-node", lang: "typescript", declaredType: "provider"},
		pdkFixture{example: "cli", lang: "typescript", declaredType: "cli"},
	)
	return out
}

// TestValidatePDKExampleFixtures builds each covered PDK example on demand and
// validates it through the facade with its declared plugin type. A fixture
// whose toolchain is absent surfaces a distinct, greppable TOOLCHAIN-ABSENT
// skip from the shared builder — an environment issue, not a weakened
// assertion; a fixture that builds but fails validation is a real defect the
// facade surfaces loudly.
func TestValidatePDKExampleFixtures(t *testing.T) {
	for _, f := range pdkFixtures() {
		artifact := f.example + "-" + f.lang + ".wasm"
		t.Run(artifact, func(t *testing.T) {
			bytes := wasmtest.EnsureFixtureBytes(t, artifact)
			report, err := plugintest.ValidateGuest(bytes, plugintest.Options{
				DeclaredType:   f.declaredType,
				MaxMemoryPages: tableTestMaxPages,
				Identity:       artifact,
			})
			if err != nil {
				t.Fatalf("PDK fixture %s declared type %q did not validate: %v", artifact, f.declaredType, err)
			}
			if report.PluginType != f.declaredType {
				t.Fatalf("PDK fixture %s reported type %q, want declared %q", artifact, report.PluginType, f.declaredType)
			}
			// Dogfood the facade's test sugar over the same fixture.
			plugintest.RequireValid(t, bytes, f.declaredType)
			t.Logf("PASS %-22s type=%-8s exports=%v", artifact, report.PluginType, report.Exports)
		})
	}
}

// TestValidateEntity_Mutator exercises entity mode end-to-end: a minimal
// authored Plugin entity for the label-injector governance guest — declaring its
// type, its single mutate export, and no host functions — validates against the
// embedded guest through the full entity cross-check (type, capability claim,
// command mirror, host-function record).
func TestValidateEntity_Mutator(t *testing.T) {
	const uri = "governance/label-injector"
	b, ok := embedded.Lookup(uri)
	if !ok || len(b) == 0 {
		t.Fatalf("embedded.Lookup(%q) = (%d bytes, %v), want a non-empty hit", uri, len(b), ok)
	}
	// The entity's authored metadata.version must equal what the guest reports
	// through describe(); the publish cross-check halts on a mismatch. Read the
	// guest's self-reported version so the fixture tracks the build stamp rather
	// than hard-coding a string that drifts from the embedded binary.
	descRaw, ok := embedded.LookupDescriptor(uri)
	if !ok {
		t.Fatalf("embedded.LookupDescriptor(%q) returned no descriptor", uri)
	}
	desc, _, err := entitysynth.ParseDescriptorJSON(descRaw)
	if err != nil {
		t.Fatalf("parse descriptor for %q: %v", uri, err)
	}
	if desc.Metadata == nil || desc.Metadata.Version == "" {
		t.Fatalf("embedded guest %q reports no describe() version", uri)
	}
	entity := types.Plugin{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Plugin",
		Metadata: types.PluginMetadata{
			Name:       uri,
			Version:    desc.Metadata.Version,
			PluginType: "governance",
		},
		Spec: types.PluginSpec{
			AbiVersion: "v1alpha1",
			Exports:    []types.PluginExport{{Name: "mutate"}},
		},
	}

	report, err := plugintest.ValidateEntity(b, entity)
	if err != nil {
		t.Fatalf("entity-mode validation of %q failed: %v", uri, err)
	}
	if report.PluginType != "governance" {
		t.Fatalf("entity-mode report type = %q, want governance", report.PluginType)
	}
	// Dogfood the entity-mode test sugar over the same entity.
	plugintest.RequireValidEntity(t, b, entity)
	t.Logf("PASS entity-mode %s type=%s exports=%v notices=%v", uri, report.PluginType, report.Exports, report.Notices)
}

// TestValidateGuest_WrongDeclaredType_Fails proves the facade is non-vacuous:
// declaring a shipping guest as a different plugin type halts with the
// re-exported ErrTypeMismatch sentinel rather than silently passing.
func TestValidateGuest_WrongDeclaredType_Fails(t *testing.T) {
	uris := embedded.URIs()
	if len(uris) == 0 {
		t.Fatal("no embedded guests to probe")
	}
	uri := uris[0]
	trueType := declaredTypeOf(uri)
	wrongType := "governance"
	if trueType == wrongType {
		wrongType = "provider"
	}

	b, ok := embedded.Lookup(uri)
	if !ok || len(b) == 0 {
		t.Fatalf("embedded.Lookup(%q) = (%d bytes, %v), want a non-empty hit", uri, len(b), ok)
	}
	_, err := plugintest.ValidateGuest(b, plugintest.Options{
		DeclaredType:   wrongType,
		MaxMemoryPages: tableTestMaxPages,
		Identity:       uri,
	})
	if err == nil {
		t.Fatalf("declaring guest %q (true type %q) as %q unexpectedly validated", uri, trueType, wrongType)
	}
	if !errors.Is(err, plugintest.ErrTypeMismatch()) {
		t.Fatalf("wrong-type error for %q was not ErrTypeMismatch: %v", uri, err)
	}
	t.Logf("PASS wrong-type guard: guest %q declared %q -> %v", uri, wrongType, err)
}
