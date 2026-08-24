// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraformcore_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore"
)

// TestAssembleRootDeclaresOneModulePerComponent is the shape the whole layout
// rests on: every component contributing to a boundary is one module block in one
// root configuration, each pointing at that component's own sub-directory. Two
// components landing in one configuration is what makes a relationship between
// them expressible at all.
func TestAssembleRootDeclaresOneModulePerComponent(t *testing.T) {
	t.Parallel()

	files, err := terraformcore.AssembleRoot(oneWave("store", "store2"))
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly one root configuration file, got %d: %+v", len(files), files)
	}
	if files[0].Path != terraformcore.RootConfigFileName {
		t.Errorf("root configuration path = %q, want %q", files[0].Path, terraformcore.RootConfigFileName)
	}
	body := string(files[0].Content)
	for _, want := range []string{
		"module \"store\" {\n  source = \"./modules/store\"\n}\n",
		"module \"store2\" {\n  source = \"./modules/store2\"\n}\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("root configuration is missing:\n%s\ngot:\n%s", want, body)
		}
	}
	if n := strings.Count(body, "\nmodule "); n != 2 {
		t.Errorf("expected 2 module blocks, got %d:\n%s", n, body)
	}
}

// TestAssembleRootEmitsInSuppliedOrder pins determinism at the one place order
// could escape: the caller's contributor order is the emission order, so
// identical input yields byte-identical output.
func TestAssembleRootEmitsInSuppliedOrder(t *testing.T) {
	t.Parallel()

	forward, err := terraformcore.AssembleRoot(oneWave("alpha", "beta", "gamma"))
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}
	reverse, err := terraformcore.AssembleRoot(oneWave("gamma", "beta", "alpha"))
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}
	if string(forward[0].Content) == string(reverse[0].Content) {
		t.Fatal("contributor order must reach the output; two orders produced identical bytes")
	}

	again, err := terraformcore.AssembleRoot(oneWave("alpha", "beta", "gamma"))
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}
	if string(again[0].Content) != string(forward[0].Content) {
		t.Error("identical input produced different bytes")
	}
	idx := func(body, id string) int { return strings.Index(body, "module \""+id+"\"") }
	body := string(forward[0].Content)
	if !(idx(body, "alpha") < idx(body, "beta") && idx(body, "beta") < idx(body, "gamma")) {
		t.Errorf("module blocks are not in the supplied order:\n%s", body)
	}
}

// TestAssembleRootRefusesCollidingModuleNames is the Loud Guardrail: two
// components whose identifiers map onto one Terraform module name would produce a
// configuration the engine rejects, so the build halts here instead, naming both
// components. The check is capable of refusing — the sibling case below shows the
// same shape passing — so a green result is not decoration.
func TestAssembleRootRefusesCollidingModuleNames(t *testing.T) {
	t.Parallel()

	_, err := terraformcore.AssembleRoot(oneWave("a.b", "a_b"))
	if err == nil {
		t.Fatal("two components mapping onto one module name must halt")
	}
	for _, want := range []string{"a.b", "a_b", "a_b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt message must name %q; got: %v", want, err)
		}
	}

	if _, err := terraformcore.AssembleRoot(oneWave("a.b", "a-b")); err != nil {
		t.Errorf("distinct module names must not halt: %v", err)
	}
}

// TestModuleNameFitsEngineGrammar pins the mapping against the grammar the engine
// actually enforces: a module name begins with a letter or an underscore and then
// carries letters, digits, underscores and dashes. A component identifier is a
// dot-joined path of names, so the dot that joins a nested component's path and a
// leading digit or dash are the two things that fall outside it.
func TestModuleNameFitsEngineGrammar(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"store":          "store",
		"store2":         "store2",
		"app-db":         "app-db",
		"_internal":      "_internal",
		"platform.appdb": "platform_appdb",
		"a.b.c":          "a_b_c",
		"1db":            "_1db",
		"-lead":          "_-lead",
	}
	for in, want := range cases {
		if got := terraformcore.ModuleName(in); got != want {
			t.Errorf("ModuleName(%q) = %q, want %q", in, got, want)
		}
	}
	for in := range cases {
		got := terraformcore.ModuleName(in)
		if got == "" {
			t.Fatalf("ModuleName(%q) is empty", in)
		}
		if c := got[0]; !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && c != '_' {
			t.Errorf("ModuleName(%q) = %q begins with %q, which the engine rejects", in, got, string(c))
		}
		for i := 0; i < len(got); i++ {
			c := got[i]
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
			if !ok {
				t.Errorf("ModuleName(%q) = %q carries %q, which the engine rejects", in, got, string(c))
			}
		}
	}
}

// TestModulesDirNameReachesBothSides holds the one thing that must not drift: the
// directory a module block points at and the directory the host is told to nest
// per-component output under are the same value, composed from one constant.
func TestModulesDirNameReachesBothSides(t *testing.T) {
	t.Parallel()

	files, err := terraformcore.AssembleRoot(oneWave("one"))
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}
	want := "source = \"./" + terraformcore.ModulesDirName + "/one\""
	if !strings.Contains(string(files[0].Content), want) {
		t.Errorf("module source must compose %s; got:\n%s", terraformcore.ModulesDirName, files[0].Content)
	}
	if strings.ContainsAny(terraformcore.ModulesDirName, `/\`) || terraformcore.ModulesDirName == "." || terraformcore.ModulesDirName == ".." {
		t.Errorf("ModulesDirName must name one directory; got %q", terraformcore.ModulesDirName)
	}
}
