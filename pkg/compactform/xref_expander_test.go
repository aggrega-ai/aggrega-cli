// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the cross-document $ref expansion path: URI parsing, library
// + entry lookup, composition merge semantics, cycle detection, and the
// sentinel taxonomy. Each test loads SchemaLibrary fixtures into a
// libraries map, compiles a consumer schema via Compile with the
// WithSchemaLibraries option, and asserts the resulting JSON Schema
// against the documented composition semantics.
//
// The inferred-form-mandatory rule (additionalProperties: false at the
// merged shape) is asserted at every happy-path test — the closure
// semantic is load-bearing and silently losing it would propagate as
// a typo-rejection regression across every consumer using a library
// reference.
//
// Fixture-authoring note. Library entries here use property names that
// do not collide with compact-form vocabulary (the test uses `note`
// rather than `description` for a string-typed field). The compactform
// language treats `description:` as a compact-form keyword
// (typeIdentityKeywords in vocabulary.go); an inferred-form library
// entry whose property names include `description:` routes through
// emitCompactConstraint and rejects its sibling property names as
// unknown keywords. Library authors who need `description:` as a
// property name (a legitimate use case for BaseComponentParameter-style
// entries) author the entry in explicit JSON Schema form with
// `type: object, properties: { ... }` instead of inferred form. The
// constraint is a pre-existing compactform-language authoring
// requirement; documented here so future fixture authors don't bump
// into it silently.

package compactform

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// loadLibrary parses a SchemaLibrary YAML fixture into the typed struct
// the catalog walker would produce. The test fixtures use raw YAML so the
// authoring shape mirrors what catalog files actually look like; the
// returned SchemaLibrary populates a libraries map for Compile.
func loadLibrary(t *testing.T, yamlContent string) types.SchemaLibrary {
	t.Helper()
	var lib types.SchemaLibrary
	if err := yaml.Unmarshal([]byte(yamlContent), &lib); err != nil {
		t.Fatalf("unmarshal SchemaLibrary fixture: %v", err)
	}
	return lib
}

// parseConsumer parses a consumer schema YAML fixture into the yaml.Node
// shape Compile accepts. Tests assert against the compiled Schema map.
func parseConsumer(t *testing.T, yamlContent string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &n); err != nil {
		t.Fatalf("unmarshal consumer fixture: %v", err)
	}
	return &n
}

// TestExpand_HappyPath asserts a consumer schema referencing a library
// entry compiles to a merged shape with library properties present,
// closure injected, and the source annotation attached.
func TestExpand_HappyPath(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"BaseComponents": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: 1.0.0
spec:
  schemas:
    BaseComponentParameter:
      displayName: { type: string, required: true }
      note: { type: string }
`),
	}

	consumer := parseConsumer(t, `$ref: "BaseComponents#/schemas/BaseComponentParameter"
properties:
  volumeClaim: { type: string }
`)

	out, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	props, ok := out["properties"].(Schema)
	if !ok {
		t.Fatalf("expected properties map; got %T", out["properties"])
	}
	for _, name := range []string{"displayName", "note", "volumeClaim"} {
		if _, present := props[name]; !present {
			t.Errorf("expected %q in merged properties; got keys %v", name, propertyKeys(props))
		}
	}

	if got := out["additionalProperties"]; got != false {
		t.Errorf("expected additionalProperties: false; got %v", got)
	}

	// Library-sourced properties carry the x-aggrega-source annotation;
	// locally-authored properties do not.
	for _, libProp := range []string{"displayName", "note"} {
		p, _ := props[libProp].(Schema)
		if got := LibrarySourceAnnotation(p); got != "BaseComponents#/schemas/BaseComponentParameter" {
			t.Errorf("%q annotation = %q; want BaseComponents#/schemas/BaseComponentParameter", libProp, got)
		}
	}
	if p, ok := props["volumeClaim"].(Schema); ok {
		if got := LibrarySourceAnnotation(p); got != "" {
			t.Errorf("locally-authored volumeClaim carried unexpected annotation %q", got)
		}
	}
}

// TestExpand_ConsumerOverwritesOnKeyCollision asserts a consumer-side
// property with the same name as a library-sourced property wins, and
// the library annotation is dropped for that property.
func TestExpand_ConsumerOverwritesOnKeyCollision(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"Lib": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Lib
  version: 1.0.0
spec:
  schemas:
    Entry:
      name: { type: string, required: true }
      size: { type: integer, min: 1, max: 10 }
`),
	}
	consumer := parseConsumer(t, `$ref: "Lib#/schemas/Entry"
properties:
  size: { type: integer, min: 5, max: 100 }
`)

	out, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	props, _ := out["properties"].(Schema)
	sizeSchema, _ := props["size"].(Schema)
	if sizeSchema["minimum"] != 5 {
		t.Errorf("consumer size.min should win; got minimum=%v", sizeSchema["minimum"])
	}
	if sizeSchema["maximum"] != 100 {
		t.Errorf("consumer size.max should win; got maximum=%v", sizeSchema["maximum"])
	}
	if got := LibrarySourceAnnotation(sizeSchema); got != "" {
		t.Errorf("overwritten size should not carry library annotation; got %q", got)
	}
}

// TestExpand_RequiredUnion asserts library + consumer required arrays
// union in library-first, consumer-second, deduplicated order.
func TestExpand_RequiredUnion(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"Lib": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Lib
  version: 1.0.0
spec:
  schemas:
    Entry:
      a: { type: string, required: true }
      b: { type: string, required: true }
`),
	}
	consumer := parseConsumer(t, `$ref: "Lib#/schemas/Entry"
properties:
  c: { type: string }
required: [c]
`)

	out, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	required, ok := out["required"].([]string)
	if !ok {
		t.Fatalf("expected required []string; got %T", out["required"])
	}
	want := []string{"a", "b", "c"}
	if len(required) != len(want) {
		t.Fatalf("required = %v; want %v", required, want)
	}
	for i := range want {
		if required[i] != want[i] {
			t.Errorf("required[%d] = %q; want %q (full got %v)", i, required[i], want[i], required)
		}
	}
}

// TestExpand_UnresolvedLibrary asserts a $ref against a missing library
// surfaces ErrUnresolvedSchemaLibraryRef carrying the URI.
func TestExpand_UnresolvedLibrary(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{}
	consumer := parseConsumer(t, `$ref: "MissingLib#/schemas/Foo"
properties: {}
`)

	_, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err == nil {
		t.Fatal("expected error for unresolved library; got nil")
	}
	if !errors.Is(err, ErrUnresolvedSchemaLibraryRef) {
		t.Errorf("error = %v; want ErrUnresolvedSchemaLibraryRef", err)
	}
	if !strings.Contains(err.Error(), "MissingLib") {
		t.Errorf("error message should name the unresolved library; got %q", err.Error())
	}
}

// TestExpand_UnresolvedEntry asserts a $ref to a present library but
// absent entry surfaces ErrUnresolvedSchemaLibraryRef naming the entry.
func TestExpand_UnresolvedEntry(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"Lib": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Lib
  version: 1.0.0
spec:
  schemas:
    Present:
      x: { type: string }
`),
	}
	consumer := parseConsumer(t, `$ref: "Lib#/schemas/Missing"
properties: {}
`)
	_, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, ErrUnresolvedSchemaLibraryRef) {
		t.Errorf("error = %v; want ErrUnresolvedSchemaLibraryRef", err)
	}
	if !strings.Contains(err.Error(), "Missing") {
		t.Errorf("error message should name the missing entry; got %q", err.Error())
	}
}

// TestExpand_CycleDetected asserts a library-to-library cycle (A's entry
// references B, B's entry references A) surfaces ErrCircularRef with the
// chain in the error message.
func TestExpand_CycleDetected(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"A": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: A
  version: 1.0.0
spec:
  schemas:
    Cycle:
      $ref: "B#/schemas/Cycle"
`),
		"B": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: B
  version: 1.0.0
spec:
  schemas:
    Cycle:
      $ref: "A#/schemas/Cycle"
`),
	}
	consumer := parseConsumer(t, `$ref: "A#/schemas/Cycle"
properties: {}
`)
	_, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err == nil {
		t.Fatal("expected cycle error; got nil")
	}
	if !errors.Is(err, ErrCircularRef) {
		t.Errorf("error = %v; want ErrCircularRef", err)
	}
	// Shared-primitive conformance: the $ref cycle sentinel IS refcycle.ErrCycle,
	// the same identity the $import surface reports — one detector guards both, so
	// a mutation to the primitive's logic surfaces in pkg/refcycle's unit test and
	// a change to the shared sentinel breaks both surfaces' cycle assertions.
	if !errors.Is(err, refcycle.ErrCycle) {
		t.Errorf("error = %v; want the shared refcycle.ErrCycle identity", err)
	}
	if !strings.Contains(err.Error(), "->") {
		t.Errorf("cycle error should name the chain; got %q", err.Error())
	}
}

// TestExpand_MixedSpellingCycleKeyedByIdentity asserts a library-to-library
// cycle that traverses one entry spelled two ways — a bare-name reference and a
// host-less `:version` reference of the SAME entry — is detected and reported by
// canonical identity, not by the raw reference string. Because the expander
// accepts and strips `:version` (both spellings resolve to one entry), the cycle
// guard must key on the canonical `<Library>#/schemas/<Entry>` identity; keying
// on the raw reference would report the cycle in mixed spellings (the `:version`
// form leaking into the chain) and detect it later than the identity would.
//
// Non-vacuity: with the cycle key reverted to the raw reference string, the
// `:version` spelling appears in the reported chain (the `A:1.0.0` assertion below
// goes red); the cycle is still eventually caught — one hop late — because every
// entry's outgoing reference is fixed, so this is mis-keyed detection, not the
// unbounded recursion it might first appear (a static reference graph always
// repeats some raw key on any path).
func TestExpand_MixedSpellingCycleKeyedByIdentity(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"A": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: A
  version: 1.0.0
spec:
  schemas:
    Cycle:
      $ref: "B#/schemas/Cycle"
`),
		"B": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: B
  version: 1.0.0
spec:
  schemas:
    Cycle:
      $ref: "A:1.0.0#/schemas/Cycle"
`),
	}
	consumer := parseConsumer(t, `$ref: "A#/schemas/Cycle"
properties: {}
`)
	_, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err == nil {
		t.Fatal("expected cycle error; got nil")
	}
	if !errors.Is(err, ErrCircularRef) {
		t.Fatalf("error = %v; want ErrCircularRef", err)
	}
	// The cycle is keyed and reported by canonical identity: the `:version`
	// spelling never appears in the chain. Keying on the raw reference would
	// leak `A:1.0.0#/schemas/Cycle` into the message.
	if strings.Contains(err.Error(), "A:1.0.0") {
		t.Errorf("cycle chain should report canonical identities, not :version spellings; got %q", err.Error())
	}
}

// ociLibFixture is a workspace-local SchemaLibrary whose Storage entry
// declares a default, shared by the identity-reference tests.
func ociLibFixture(t *testing.T) map[string]types.SchemaLibrary {
	t.Helper()
	return map[string]types.SchemaLibrary{
		"DbSizing": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`),
	}
}

// TestExpand_BareNameIdentityResolves asserts the bare-name identity form
// resolves: the entry's property merges, its default materializes, and the
// source annotation is the canonical `<Library>#/schemas/<Entry>` value. The
// version is supplied by the lockfile, not the reference.
func TestExpand_BareNameIdentityResolves(t *testing.T) {
	t.Parallel()

	libs := ociLibFixture(t)
	consumer := parseConsumer(t, `$ref: "DbSizing#/schemas/Storage"
properties: {}
`)
	out, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err != nil {
		t.Fatalf("bare-name identity $ref should resolve; got %v", err)
	}
	props, ok := out["properties"].(Schema)
	if !ok {
		t.Fatalf("expected properties map; got %T", out["properties"])
	}
	sizeSchema, ok := props["size"].(Schema)
	if !ok {
		t.Fatalf("expected size property from the library entry; got keys %v", propertyKeys(props))
	}
	if got := sizeSchema["default"]; got != "10Gi" {
		t.Errorf("library default should materialize; got default=%v", got)
	}
	if got := LibrarySourceAnnotation(sizeSchema); got != "DbSizing#/schemas/Storage" {
		t.Errorf("annotation = %q; want DbSizing#/schemas/Storage", got)
	}
}

// TestExpand_HostBearingRefRejected asserts a reference carrying a registry
// host (the `oci://…` form) is rejected loudly with ErrHostInLibraryRef, and
// the diagnostic suggests the identity-form equivalent. The message is
// domain-only — it reaches user terminals and carries no process vocabulary.
func TestExpand_HostBearingRefRejected(t *testing.T) {
	t.Parallel()

	libs := ociLibFixture(t)
	consumer := parseConsumer(t, `$ref: "oci://hub.aggrega.ai/DbSizing:1.0.0#/schemas/Storage"
properties: {}
`)
	_, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if !errors.Is(err, ErrHostInLibraryRef) {
		t.Fatalf("error = %v; want ErrHostInLibraryRef", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "DbSizing#/schemas/Storage") {
		t.Errorf("rejection should suggest the identity form `DbSizing#/schemas/Storage`; got %q", msg)
	}
	for _, leak := range []string{"WI", "Wave", "intent", "TANDEM", "LDR 00", "Scope"} {
		if strings.Contains(msg, leak) {
			t.Errorf("rejection message leaks process vocabulary %q: %s", leak, msg)
		}
	}
}

// TestExpand_VersionFloorAcceptedAndStripped asserts a host-less `:version`
// reference (`<Library>:<version>#/schemas/<Entry>`) is accepted: the expander
// strips the asserted version and resolves the bare-name identity. The asserted
// version is an optional minimum-version floor consumed by selection at lock
// time — not part of the reference's resolution — so the resolved content and
// the source annotation match the bare-name form exactly (no version present).
func TestExpand_VersionFloorAcceptedAndStripped(t *testing.T) {
	t.Parallel()

	libs := ociLibFixture(t)
	consumer := parseConsumer(t, `$ref: "DbSizing:1.0.0#/schemas/Storage"
properties: {}
`)
	out, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err != nil {
		t.Fatalf("host-less :version $ref should resolve (stripped to bare-name); got %v", err)
	}
	props, ok := out["properties"].(Schema)
	if !ok {
		t.Fatalf("expected properties map; got %T", out["properties"])
	}
	sizeSchema, ok := props["size"].(Schema)
	if !ok {
		t.Fatalf("expected size property from the library entry; got keys %v", propertyKeys(props))
	}
	if got := sizeSchema["default"]; got != "10Gi" {
		t.Errorf("library default should materialize; got default=%v", got)
	}
	// The asserted version never appears in the resolved identity — the
	// annotation is the host-less, version-less bare-name form.
	if got := LibrarySourceAnnotation(sizeSchema); got != "DbSizing#/schemas/Storage" {
		t.Errorf("annotation = %q; want DbSizing#/schemas/Storage (version stripped)", got)
	}
}

// TestExpand_SiblingAllowlistRejects asserts unsupported siblings of a
// cross-document $ref reject with ErrUnknownKeyword carrying the
// allowlist in the message.
func TestExpand_SiblingAllowlistRejects(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"Lib": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Lib
  version: 1.0.0
spec:
  schemas:
    Entry:
      x: { type: string }
`),
	}
	consumer := parseConsumer(t, `$ref: "Lib#/schemas/Entry"
format: email
`)
	_, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err == nil {
		t.Fatal("expected sibling rejection; got nil")
	}
	if !errors.Is(err, ErrUnknownKeyword) {
		t.Errorf("error = %v; want ErrUnknownKeyword", err)
	}
}

// TestExpand_AdditionalPropertiesSiblingPointsToEscape asserts the
// targeted error message hint for `additionalProperties:` sibling
// (the closure point that authors might try to override). The message
// names the $jsonschema: escape as the documented workaround.
func TestExpand_AdditionalPropertiesSiblingPointsToEscape(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"Lib": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Lib
  version: 1.0.0
spec:
  schemas:
    Entry:
      x: { type: string }
`),
	}
	consumer := parseConsumer(t, `$ref: "Lib#/schemas/Entry"
additionalProperties: true
`)
	_, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err == nil {
		t.Fatal("expected sibling rejection; got nil")
	}
	if !strings.Contains(err.Error(), "$jsonschema") {
		t.Errorf("error should mention $jsonschema escape workaround; got %q", err.Error())
	}
}

// TestExpand_ClosureSurvivesOpenLibraryEntry is the R-H mitigation test:
// a library entry that declares additionalProperties: true is overridden
// at the merged shape so additionalProperties: false applies. The
// inferred-form-mandatory rule wins regardless of library authoring.
func TestExpand_ClosureSurvivesOpenLibraryEntry(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"OpenLib": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: OpenLib
  version: 1.0.0
spec:
  schemas:
    OpenEntry:
      $jsonschema: |
        type: object
        properties:
          x: { type: string }
        additionalProperties: true
`),
	}
	consumer := parseConsumer(t, `$ref: "OpenLib#/schemas/OpenEntry"
properties:
  y: { type: string }
`)
	out, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := out["additionalProperties"]; got != false {
		t.Errorf("merged shape should be closed; additionalProperties = %v, want false", got)
	}
}

// TestExpand_NestedAnnotationRecursion asserts the x-aggrega-source
// annotation propagates into nested object properties of a library
// entry, not only the top-level properties. Deep-nested fields stay
// attributed to their originating library.
func TestExpand_NestedAnnotationRecursion(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"Nested": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Nested
  version: 1.0.0
spec:
  schemas:
    Outer:
      inner:
        deepField: { type: string }
        anotherDeep: { type: integer }
`),
	}
	consumer := parseConsumer(t, `$ref: "Nested#/schemas/Outer"
properties: {}
`)
	out, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	props, _ := out["properties"].(Schema)
	inner, _ := props["inner"].(Schema)
	if got := LibrarySourceAnnotation(inner); got != "Nested#/schemas/Outer" {
		t.Errorf("inner annotation = %q; want Nested#/schemas/Outer", got)
	}
	innerProps, _ := inner["properties"].(Schema)
	deep, _ := innerProps["deepField"].(Schema)
	if got := LibrarySourceAnnotation(deep); got != "Nested#/schemas/Outer" {
		t.Errorf("deepField annotation = %q; want Nested#/schemas/Outer", got)
	}
}

// TestExpand_NoCatalogSurfacesUnresolved asserts a cross-document $ref
// compiled without WithSchemaLibraries surfaces
// ErrUnresolvedSchemaLibraryRef rather than silently passing through
// as a literal. The diagnostic at compile time is the design choice
// here — silent passthrough would defer the problem to validation
// failures downstream.
func TestExpand_NoCatalogSurfacesUnresolved(t *testing.T) {
	t.Parallel()

	consumer := parseConsumer(t, `$ref: "Lib#/schemas/Entry"
properties: {}
`)
	_, err := Compile(consumer)
	if err == nil {
		t.Fatal("expected error for cross-document $ref without catalog; got nil")
	}
	if !errors.Is(err, ErrUnresolvedSchemaLibraryRef) {
		t.Errorf("error = %v; want ErrUnresolvedSchemaLibraryRef", err)
	}
}

// TestExpand_MultiEntryLibrarySelectiveMerge asserts a consumer's
// $ref targeting one entry in a multi-entry library merges only the
// referenced entry's properties — not every entry in the library.
// Negative-assertion guard: the byte-compare against expected.json
// indirectly proves this (an accidental merge of other entries would
// fail deep-equal), but an explicit test pins the invariant against
// future fixture changes.
func TestExpand_MultiEntryLibrarySelectiveMerge(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{
		"MultiLib": loadLibrary(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: MultiLib
  version: 1.0.0
spec:
  schemas:
    Wanted:
      keep: { type: string, required: true }
    Unrelated:
      strayField: { type: string }
      anotherStrayField: { type: integer }
`),
	}
	consumer := parseConsumer(t, `$ref: "MultiLib#/schemas/Wanted"
properties: {}
`)
	out, err := Compile(consumer, WithSchemaLibraries(libs), WithConsumerFile("test/consumer.yaml"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	props, _ := out["properties"].(Schema)
	if _, ok := props["keep"]; !ok {
		t.Error("expected referenced entry property `keep`; missing")
	}
	for _, name := range []string{"strayField", "anotherStrayField"} {
		if _, present := props[name]; present {
			t.Errorf("unrelated entry property %q leaked into merged shape; keys = %v", name, propertyKeys(props))
		}
	}
}

// TestExpand_InDocumentRefStillPassesThrough asserts an in-document
// $ref: "#/$defs/..." compiles to a literal $ref passthrough — Step C's
// dispatch branch must not regress the existing behaviour.
func TestExpand_InDocumentRefStillPassesThrough(t *testing.T) {
	t.Parallel()

	libs := map[string]types.SchemaLibrary{}
	consumer := parseConsumer(t, `$ref: "#/$defs/Inner"
`)
	out, err := Compile(consumer, WithSchemaLibraries(libs))
	if err != nil {
		t.Fatalf("in-document $ref should pass through; got %v", err)
	}
	if got := out["$ref"]; got != "#/$defs/Inner" {
		t.Errorf("expected literal $ref passthrough; got %v", got)
	}
}

// TestParseCrossDocRefURI directly exercises the URI-parse helper for
// edge-case coverage that doesn't surface naturally through Compile.
func TestParseCrossDocRefURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		input     string
		wantOK    bool
		wantLib   string
		wantEntry string
	}{
		{"basic", "Lib#/schemas/Entry", true, "Lib", "Entry"},
		{"empty", "", false, "", ""},
		{"in-document", "#/$defs/X", false, "", ""},
		{"no-fragment", "Lib", false, "", ""},
		{"wrong-fragment-prefix", "Lib#/foo/Entry", false, "", ""},
		{"empty-document", "#/schemas/Entry", false, "", ""},
		{"empty-entry", "Lib#/schemas/", false, "", ""},
		{"nested-entry-slash", "Lib#/schemas/Outer/Inner", false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseCrossDocRefURI(c.input)
			if ok != c.wantOK {
				t.Errorf("ok = %v; want %v", ok, c.wantOK)
			}
			if got.library != c.wantLib {
				t.Errorf("library = %q; want %q", got.library, c.wantLib)
			}
			if got.entry != c.wantEntry {
				t.Errorf("entry = %q; want %q", got.entry, c.wantEntry)
			}
		})
	}
}

// propertyKeys collects the keys of a Schema-typed properties map for
// diagnostic messages on assertion failures.
func propertyKeys(s Schema) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	return out
}

// TestXRefFixture_BasicEndToEnd loads the testdata/xref-basic fixture
// (a BaseComponents SchemaLibrary entity + a consumer schema authored
// with cross-document $ref + one local property) and asserts the
// compiled output matches the inspected golden at expected.json. The
// golden carries the post-expansion structure: library properties
// present, consumer property merged in, closure injected, library-
// sourced properties annotated, locally-authored properties not
// annotated. The byte-compare pairs with the semantic-invariants pin
// per the standing inspect-goldens-before-saving rule.
func TestXRefFixture_BasicEndToEnd(t *testing.T) {
	t.Parallel()

	libBytes, err := os.ReadFile(filepath.Join("testdata", "xref-basic", "library.yaml"))
	if err != nil {
		t.Fatalf("read library fixture: %v", err)
	}
	var lib types.SchemaLibrary
	if err := yaml.Unmarshal(libBytes, &lib); err != nil {
		t.Fatalf("unmarshal library fixture: %v", err)
	}
	libs := map[string]types.SchemaLibrary{"BaseComponents": lib}

	consumerBytes, err := os.ReadFile(filepath.Join("testdata", "xref-basic", "consumer.yaml"))
	if err != nil {
		t.Fatalf("read consumer fixture: %v", err)
	}
	var consumer yaml.Node
	if err := yaml.Unmarshal(consumerBytes, &consumer); err != nil {
		t.Fatalf("unmarshal consumer fixture: %v", err)
	}

	out, err := Compile(&consumer, WithSchemaLibraries(libs), WithConsumerFile("testdata/xref-basic/consumer.yaml"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenBytes, err := os.ReadFile(filepath.Join("testdata", "xref-basic", "expected.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var golden any
	if err := json.Unmarshal(goldenBytes, &golden); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}

	gotNormalised, err := normaliseViaJSON(out)
	if err != nil {
		t.Fatalf("normalise got: %v", err)
	}
	goldenNormalised, err := normaliseViaJSON(golden)
	if err != nil {
		t.Fatalf("normalise golden: %v", err)
	}

	if !reflect.DeepEqual(gotNormalised, goldenNormalised) {
		gotJSON, _ := json.MarshalIndent(gotNormalised, "", "  ")
		goldenJSON, _ := json.MarshalIndent(goldenNormalised, "", "  ")
		t.Fatalf("xref-basic fixture: schema mismatch\n--- got ---\n%s\n--- golden ---\n%s",
			gotJSON, goldenJSON)
	}

	// Semantic invariants paired with the byte-compare per the standing
	// inspect-goldens rule. The byte-compare proves the compiled output
	// matches a frozen golden; the invariants prove the golden itself
	// captures the contracted behaviour (and stop a regression where a
	// silent semantic shift gets re-pinned as the new golden).
	props, _ := out["properties"].(Schema)
	if got := out["additionalProperties"]; got != false {
		t.Errorf("invariant: merged shape closure not injected; additionalProperties = %v", got)
	}
	if _, ok := props["name"]; !ok {
		t.Error("invariant: library property `name` missing from merged shape")
	}
	if _, ok := props["volumeClaim"]; !ok {
		t.Error("invariant: consumer property `volumeClaim` missing from merged shape")
	}
	if got := LibrarySourceAnnotation(props["name"].(Schema)); got != "BaseComponents#/schemas/BaseComponentParameter" {
		t.Errorf("invariant: library-sourced annotation absent on `name`; got %q", got)
	}
	if got := LibrarySourceAnnotation(props["volumeClaim"].(Schema)); got != "" {
		t.Errorf("invariant: locally-authored `volumeClaim` carried unexpected annotation %q", got)
	}
}
