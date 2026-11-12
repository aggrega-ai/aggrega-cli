// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraformcore_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore"
)

// valuesOf builds the resolved values map a sourced render carries, in the given
// key order, so a test fixes the wire order it asserts against rather than
// inheriting a map's iteration order.
func valuesOf(t *testing.T, pairs ...any) types.OrderedMap[any] {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("valuesOf needs key/value pairs; got %d arguments", len(pairs))
	}
	om := types.NewOrderedMap[any]()
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			t.Fatalf("valuesOf key at %d is not a string: %#v", i, pairs[i])
		}
		om.Set(k, pairs[i+1])
	}
	return *om
}

// TestOverrideFileNamePlacement pins the PLACEMENT rule, which is the one whose
// violation is silent. Terraform reads a module's configuration from a single
// directory without descending, so an override file written to a subdirectory is
// ignored with no diagnostic: the build stays green, the module renders, and the
// sensitivity marker is simply never applied. A regression that gave the filename
// a directory component would therefore pass every other test in this package.
//
// The assertion is that both appended filenames are bare, because that is what
// makes the file that marks an input a sibling of the module's own declarations —
// which is where the declaration it overrides lives.
func TestOverrideFileNamePlacement(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"sensitivity override", terraformcore.OverrideFileName},
		{"build provenance", terraformcore.ProvenanceFileName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if strings.ContainsAny(tc.value, `/\`) {
				t.Errorf("%s filename %q carries a directory component; Terraform does not descend into "+
					"subdirectories when loading a module's configuration, so the file would be ignored silently",
					tc.name, tc.value)
			}
			if tc.value != strings.TrimSpace(tc.value) || tc.value == "" {
				t.Errorf("%s filename %q is empty or carries surrounding whitespace", tc.name, tc.value)
			}
		})
	}
}

// TestOverrideFileNameIsAnOverrideName pins the NAMING rule. Terraform treats a
// file as an override only when its name is the bare `override` or ends in
// `_override` (before the extension). Any other name is loaded as an additional
// configuration file, at which point the marker file's `variable` blocks are read
// as second declarations of variables the module already declares and the build
// halts with a duplicate-declaration error. That failure is loud rather than
// silent, but it is a hard build break, so the name is pinned too.
func TestOverrideFileNameIsAnOverrideName(t *testing.T) {
	const ext = ".tf.json"

	name := terraformcore.OverrideFileName
	if !strings.HasSuffix(name, ext) {
		t.Fatalf("override filename %q does not use Terraform's JSON configuration extension %q; the "+
			"document is emitted as JSON, so an HCL extension would not parse", name, ext)
	}
	stem := strings.TrimSuffix(name, ext)
	if stem != "override" && !strings.HasSuffix(stem, "_override") {
		t.Errorf("override filename %q has stem %q, which Terraform does not treat as an override: the stem "+
			"must be the bare \"override\" or end in \"_override\". Any other name is loaded as an additional "+
			"configuration file and its variable blocks halt the build as duplicate declarations", name, stem)
	}
}

// TestSensitivityOverrideDocument walks the emission itself: what is marked, what
// is not, and the exact bytes.
func TestSensitivityOverrideDocument(t *testing.T) {
	t.Run("marks a marked input and leaves the rest alone", func(t *testing.T) {
		values := valuesOf(t, "allocated_storage", 20, "db_password", "s3cret", "db_name", "appdb")
		marking := map[string]string{"db_password": "values.db_password"}

		got, err := terraformcore.SensitivityOverride(values, marking)
		if err != nil {
			t.Fatalf("SensitivityOverride: %v", err)
		}
		want := `{
  "variable": {
    "db_password": {
      "sensitive": true
    }
  }
}
`
		if string(got) != want {
			t.Errorf("override document mismatch:\n got:\n%s\nwant:\n%s", got, want)
		}
		t.Logf("emitted %s:\n%s", terraformcore.OverrideFileName, got)
	})

	t.Run("emits nothing when the payload marks nothing", func(t *testing.T) {
		values := valuesOf(t, "db_password", "s3cret")
		got, err := terraformcore.SensitivityOverride(values, nil)
		if err != nil {
			t.Fatalf("SensitivityOverride: %v", err)
		}
		if got != nil {
			t.Errorf("an unmarked node must gain no override file; got:\n%s", got)
		}
	})

	t.Run("a marked key that is not a module input is not emitted", func(t *testing.T) {
		// A marking travels for whatever shape its payload carries. Emitting an
		// override for a name the configuration does not declare is a hard error
		// ("no base variable declaration to override"), so the emitted set is
		// bounded by the inputs this render is actually writing.
		values := valuesOf(t, "db_name", "appdb")
		marking := map[string]string{"SOME_ENV_VAR": "featuresSchema.credentials"}

		got, err := terraformcore.SensitivityOverride(values, marking)
		if err != nil {
			t.Fatalf("SensitivityOverride: %v", err)
		}
		if got != nil {
			t.Errorf("a marked key that names no module input must not be emitted; got:\n%s", got)
		}
	})

	t.Run("marks every marked input, in the wire's own key order", func(t *testing.T) {
		values := valuesOf(t, "zeta", "z", "alpha", "a", "middle", "m")
		marking := map[string]string{"zeta": "values.zeta", "alpha": "values.alpha"}

		got, err := terraformcore.SensitivityOverride(values, marking)
		if err != nil {
			t.Fatalf("SensitivityOverride: %v", err)
		}
		// zeta before alpha: the values map's order, not a re-sort of the marking.
		want := `{
  "variable": {
    "zeta": {
      "sensitive": true
    },
    "alpha": {
      "sensitive": true
    }
  }
}
`
		if string(got) != want {
			t.Errorf("override document must follow the values map's key order:\n got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("byte-identical across repeated emission", func(t *testing.T) {
		values := valuesOf(t, "b", 1, "a", 2, "c", 3)
		marking := map[string]string{"a": "values.a", "b": "values.b", "c": "values.c"}

		first, err := terraformcore.SensitivityOverride(values, marking)
		if err != nil {
			t.Fatalf("SensitivityOverride: %v", err)
		}
		for i := 0; i < 16; i++ {
			again, err := terraformcore.SensitivityOverride(values, marking)
			if err != nil {
				t.Fatalf("SensitivityOverride (repeat %d): %v", i, err)
			}
			if string(again) != string(first) {
				t.Fatalf("emission %d differs from the first; the marking is a Go map and must not be "+
					"iterated:\n got:\n%s\nfirst:\n%s", i, again, first)
			}
		}
	})
}

// TestSensitivityOverrideReadsTheMarkingAndNeverTheName pins the direction of the
// derivation. The guest translates a declared fact into the target's primitive; it
// must never conclude that an input is sensitive from how it is spelled. Both rows
// carry a name that reads unmistakably like a credential, and neither is marked by
// the payload, so an implementation that recognised names would emit a document
// here and fail.
func TestSensitivityOverrideReadsTheMarkingAndNeverTheName(t *testing.T) {
	for _, name := range []string{"db_password", "password", "secret", "api_key", "token", "private_key"} {
		t.Run(name, func(t *testing.T) {
			values := valuesOf(t, name, "whatever")
			got, err := terraformcore.SensitivityOverride(values, map[string]string{"unrelated": "values.unrelated"})
			if err != nil {
				t.Fatalf("SensitivityOverride: %v", err)
			}
			if got != nil {
				t.Errorf("input %q was marked without the payload marking it; sensitivity must be read from "+
					"the payload and never inferred from a name:\n%s", name, got)
			}
		})
	}
}

// TestAppendedJSONEmissionShape pins the shape of the one JSON document the
// guest appends beside a vendored module: a trailing newline and a two-space
// indent, with no tabs. Nothing else compares it — the end-to-end byte-fidelity
// assertions cover the vendored module, not the file the guest appends — so an
// emitter that drifted to a different indent width or dropped the trailing
// newline would still "work" and would be covered nowhere.
//
// What this covers and what it does not: it covers the override document the
// sensitivity marking produces, which is the only JSON document the guest
// appends. It does not cover the root configuration that carries a component's
// input values, which is native Terraform syntax written by the boundary
// assembler and asserted against its own emitted bytes.
func TestAppendedJSONEmissionShape(t *testing.T) {
	values := valuesOf(t, "db_password", "s3cret", "db_name", "appdb")

	override, err := terraformcore.SensitivityOverride(values, map[string]string{"db_password": "values.db_password"})
	if err != nil {
		t.Fatalf("SensitivityOverride: %v", err)
	}

	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"sensitivity override", override},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.body) == 0 {
				t.Fatalf("%s produced no bytes", tc.name)
			}
			if tc.body[len(tc.body)-1] != '\n' {
				t.Errorf("%s does not end in a newline: %q", tc.name, tc.body)
			}
			lines := strings.Split(strings.TrimSuffix(string(tc.body), "\n"), "\n")
			if len(lines) < 2 {
				t.Fatalf("%s is not indented across lines: %q", tc.name, tc.body)
			}
			if !strings.HasPrefix(lines[1], "  ") || strings.HasPrefix(lines[1], "   ") {
				t.Errorf("%s does not use a two-space indent; second line is %q", tc.name, lines[1])
			}
			if strings.Contains(string(tc.body), "\t") {
				t.Errorf("%s carries a tab; the emission indents with spaces: %q", tc.name, tc.body)
			}
		})
	}
}
