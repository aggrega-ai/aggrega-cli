// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ancestorLeak is one configuration value planted in a hostile ancestor
// directory, paired with the predicate reporting whether a resolved
// configuration carries it.
//
// One predicate serves both directions of the test, which is what keeps the pair
// honest. A resolve started at the ancestor must report every planted value
// present; a resolve of a workspace declared beneath it must report every one
// absent. A predicate that never fired in the first direction would make the
// second direction vacuous, so each failure names its own field.
type ancestorLeak struct {
	// field is the configuration key as an author writes it.
	field string
	// present reports whether cfg carries the value planted for this field.
	present func(cfg workspaceconfig.Config) bool
	// consequence says what reading this value from a directory above the
	// workspace would cost, so a failure explains why the field is planted
	// rather than only that it leaked.
	consequence string
}

// TestDeclaredWorkspaceBeatsHostileAncestorConfig proves that a workspace
// declared through the shared test helper resolves as though nothing were
// configured above it, even when the directory containing it carries a
// configuration whose every value would change a verdict.
//
// The test constructs the whole search space instead of borrowing part of one:
// it creates the ancestor, plants the hostile configuration there, creates the
// declared workspace inside it, and lets both directories die with the test.
// That makes it hermetic, independent of the order tests run in, safe to run in
// parallel, and free of residue when a run is interrupted.
//
// The planted values are chosen so that each one is genuinely consumed. The
// catalog directories, the default registry, the offline switch and the
// automatic-pull switch are each replaced wholesale by the nearest layer that
// declares them. The signing stance is the load-bearing one: it composes
// monotonically across layers, so any layer requiring a signature makes one
// required and no nearer layer can turn it back off. Shadowing is therefore not
// what protects the declared workspace from it — the only thing that does is
// that a directory above the declared root is never collected at all. That makes
// the signing stance a strictly stronger probe than any of the others here.
//
// The control is the half carrying the proof. A resolve started at the ancestor
// must observe every planted value, because otherwise the assertions below would
// hold for a file that was never a candidate: a key the loader does not read, a
// filename nothing looks for, a value dropped on the way through. Both
// directions call the same production resolution and differ only in the
// directory they start from.
//
// A neighbouring test covers the adjacent property from the other side: that a
// configuration marking a root by presence alone caps discovery at the directory
// holding it, and that an unbounded upward walk reaches a planted ancestor. This
// one covers the shared declaring helper and the monotonic signing stance.
// Neither subsumes the other, so the overlap is deliberate.
func TestDeclaredWorkspaceBeatsHostileAncestorConfig(t *testing.T) {
	t.Parallel()

	ancestor := t.TempDir()

	// A catalog directory that exists nowhere, so a leak resolves a path the
	// machine cannot satisfy rather than quietly working.
	hostileCatalog := filepath.Join(ancestor, "catalog-that-does-not-exist")
	const hostileRegistry = "ancestor-registry.invalid"
	const hostileSigningKind = "Component"

	leaks := []ancestorLeak{
		{
			field: "workspace.catalog_dirs",
			present: func(cfg workspaceconfig.Config) bool {
				for _, dir := range cfg.Workspace.CatalogDirs {
					if dir == hostileCatalog {
						return true
					}
				}
				return false
			},
			consequence: "the workspace would load entities from a directory the test never authored",
		},
		{
			field: "default_registry",
			present: func(cfg workspaceconfig.Config) bool {
				return cfg.DefaultRegistry == hostileRegistry
			},
			consequence: "every unqualified artifact reference would resolve against another host",
		},
		{
			field:       "offline",
			present:     func(cfg workspaceconfig.Config) bool { return cfg.Offline },
			consequence: "registry access would be refused, so a test exercising a pull would fail for a reason it never declared",
		},
		{
			field:       "auto_pull",
			present:     func(cfg workspaceconfig.Config) bool { return cfg.AutoPull },
			consequence: "a build would resolve dependencies and write pins where the test expected it to be read-only",
		},
		{
			field:       "signing.required",
			present:     func(cfg workspaceconfig.Config) bool { return cfg.Signing.Required },
			consequence: "an unsigned artifact would be refused, and because this stance composes monotonically no nearer configuration could relax it",
		},
		{
			field: "signing.kinds",
			present: func(cfg workspaceconfig.Config) bool {
				for _, kind := range cfg.Signing.Kinds {
					if kind == hostileSigningKind {
						return true
					}
				}
				return false
			},
			consequence: "the signing stance would apply to a kind the test never brought into scope, and the scope unions across layers rather than being replaced",
		},
	}

	// Encoded through the YAML marshaller rather than assembled from strings, so
	// a temporary path containing a character that has to be quoted is written as
	// a value the loader reads back unchanged.
	doc, err := yaml.Marshal(map[string]any{
		"workspace":        map[string]any{"catalog_dirs": []string{hostileCatalog}},
		"default_registry": hostileRegistry,
		"offline":          true,
		"auto_pull":        true,
		"signing": map[string]any{
			"required": true,
			"kinds":    []string{hostileSigningKind},
		},
	})
	if err != nil {
		t.Fatalf("encode the ancestor configuration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ancestor, workspaceconfig.ConfigFilename), doc, 0o644); err != nil {
		t.Fatalf("plant the ancestor configuration: %v", err)
	}

	// The control: resolution started at the ancestor itself. It establishes that
	// the planted file is a real candidate — loadable, and every value read —
	// which is what gives the declared-workspace assertions their meaning.
	control, err := workspaceconfig.Resolve(ancestor, "")
	if err != nil {
		t.Fatalf("resolve from the ancestor: %v", err)
	}
	if control.Root() != ancestor {
		t.Fatalf("resolving from the ancestor rooted at %q, want the ancestor %q; the planted configuration is not anchoring its own root, so it is not the candidate this test needs",
			control.Root(), ancestor)
	}
	for _, leak := range leaks {
		if !leak.present(control.Config()) {
			t.Errorf("resolving from the ancestor did not observe the planted %s; that value is not being consumed, so asserting its absence below would prove nothing",
				leak.field)
		}
	}
	if t.Failed() {
		t.Fatal("the planted ancestor is not a candidate for every value, so the declared-workspace assertions would pass vacuously")
	}

	// The declared workspace, inside the hostile ancestor, through the one path
	// tests use to declare a workspace. The helper writes the declaration but
	// does not create the directory holding it, so the directory is created here.
	declared := filepath.Join(ancestor, "project", "workspace")
	if err := os.MkdirAll(declared, 0o755); err != nil {
		t.Fatalf("create the declared workspace directory: %v", err)
	}
	resolved := resolveTempWorkspace(t, tempWorkspaceOptions{Root: declared})

	if resolved.Root() != declared {
		t.Errorf("declared workspace resolved to root %q, want the declared directory %q", resolved.Root(), declared)
	}
	for _, leak := range leaks {
		if leak.present(resolved.Config()) {
			t.Errorf("the declared workspace inherited %s from the directory above it: %s",
				leak.field, leak.consequence)
		}
	}

	// Absence of the planted values is not on its own the property. The declared
	// workspace must resolve to the documented defaults — what it would resolve
	// to with nothing configured above it at all — so the fields are compared
	// against the product's own defaults rather than against hand-written
	// literals that could drift from them.
	defaults := workspaceconfig.Default()
	if got := resolved.Config().DefaultRegistry; got != defaults.DefaultRegistry {
		t.Errorf("declared workspace resolved default_registry = %q, want the default %q", got, defaults.DefaultRegistry)
	}
	if got := resolved.Config().Offline; got != defaults.Offline {
		t.Errorf("declared workspace resolved offline = %v, want the default %v", got, defaults.Offline)
	}
	if got := resolved.Config().AutoPull; got != defaults.AutoPull {
		t.Errorf("declared workspace resolved auto_pull = %v, want the default %v", got, defaults.AutoPull)
	}
	if got := resolved.Config().Signing.Required; got != defaults.Signing.Required {
		t.Errorf("declared workspace resolved signing.required = %v, want the default %v", got, defaults.Signing.Required)
	}
	if got := len(resolved.Config().Signing.Kinds); got != len(defaults.Signing.Kinds) {
		t.Errorf("declared workspace resolved %d signing kind(s) %v, want the default %d", got, resolved.Config().Signing.Kinds, len(defaults.Signing.Kinds))
	}
}
