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
// contributors whose positions map onto one Terraform module name would produce a
// configuration the engine rejects, so the build halts here instead, naming both
// positions and the name they collided on. The check is capable of refusing — the
// sibling case below shows the same shape passing — so a green result is not
// decoration.
//
// The halt names each contributor by the DIRECTORY its files sit in, which is
// asserted rather than assumed: a contributor that is not one of the graph's own
// components arrives under a dispatch identity no author wrote, so a message
// quoting the identifier would hand a reader a marker they cannot look up.
func TestAssembleRootRefusesCollidingModuleNames(t *testing.T) {
	t.Parallel()

	_, err := terraformcore.AssembleRoot(oneWave("a.b", "a_b"))
	if err == nil {
		t.Fatal("two components mapping onto one module name must halt")
	}
	for _, want := range []string{componentDir("a.b"), componentDir("a_b"), "a_b"} {
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

// withValues builds one contributor carrying a wave and a value set, in the key
// order given, so a case fixes the order it asserts against.
func withValues(t *testing.T, id string, wave int, pairs ...any) terraformcore.RootComponent {
	t.Helper()
	return terraformcore.RootComponent{ID: id, OutputDirectory: componentDir(id), SyncWave: wave, Values: omOf(t, pairs...)}
}

// TestAssembleRootEmitsValuesAsModuleArguments pins the whole block byte for
// byte, arguments and meta argument together, because the property under test is
// positional: an argument is an expression the engine evaluates, and it only
// reaches that position if it is written inside the module block. A test that
// merely looked for the value's text somewhere in the file would pass while the
// value sat in a comment.
//
// The block order is `source`, then the arguments in the order the values
// arrive, then `depends_on`. The two consuming component's argument names are
// padded to a common width, which is the engine's own canonical formatting for a
// run of single-line arguments.
func TestAssembleRootEmitsValuesAsModuleArguments(t *testing.T) {
	t.Parallel()

	body := rootBody(t, []terraformcore.RootComponent{
		withValues(t, "db", 0, "allocated_storage", float64(20), "db_name", "appdb"),
		withValues(t, "app", 1, "db_endpoint", "${module.db.endpoint}", "replicas", float64(2)),
	})

	wantDB := "module \"db\" {\n" +
		"  source = \"./modules/db\"\n" +
		"\n" +
		"  allocated_storage = 20\n" +
		"  db_name           = \"appdb\"\n" +
		"}\n"
	if !strings.Contains(body, wantDB) {
		t.Errorf("the producing component's block is not the expected bytes\nwant:\n%s\ngot:\n%s", wantDB, body)
	}

	wantApp := "module \"app\" {\n" +
		"  source = \"./modules/app\"\n" +
		"\n" +
		"  db_endpoint = \"${module.db.endpoint}\"\n" +
		"  replicas    = 2\n" +
		"\n" +
		"  depends_on = [module.db]\n" +
		"}\n"
	if !strings.Contains(body, wantApp) {
		t.Errorf("the consuming component's block is not the expected bytes\nwant:\n%s\ngot:\n%s", wantApp, body)
	}
}

// TestAssembleRootLeavesAnInterpolationUnresolved is the item's own property,
// asserted where it lands: a value written as an interpolation reaches the
// configuration as the text the Recipe supplied. Nothing here resolves it — the
// assembler reads no other component's output, and the reference is left for the
// engine to evaluate.
func TestAssembleRootLeavesAnInterpolationUnresolved(t *testing.T) {
	t.Parallel()

	const pointer = "${module.db.endpoint}"
	body := rootBody(t, []terraformcore.RootComponent{
		withValues(t, "db", 0, "db_name", "appdb"),
		withValues(t, "app", 0, "db_endpoint", pointer),
	})
	if !strings.Contains(body, "  db_endpoint = \""+pointer+"\"\n") {
		t.Errorf("the interpolation must reach the argument verbatim; got:\n%s", body)
	}
	// A resolved pointer would have replaced the reference with a value. Nothing
	// in the assembler can do that, and this pins it.
	if strings.Contains(body, "appdb\"\n  db_endpoint") {
		t.Errorf("the pointer appears to have been rewritten; got:\n%s", body)
	}
}

// TestAssembleRootEmitsNoArgumentsForAValuelessComponent holds the no-values
// shape: a component with no values gets a module block with a source and
// nothing else, so a module that relies on its own declared defaults keeps
// doing so and no empty argument group is invented.
func TestAssembleRootEmitsNoArgumentsForAValuelessComponent(t *testing.T) {
	t.Parallel()

	body := rootBody(t, oneWave("one"))
	want := "module \"one\" {\n  source = \"./modules/one\"\n}\n"
	if !strings.Contains(body, want) {
		t.Errorf("a valueless component's block must carry only its source\nwant:\n%s\ngot:\n%s", want, body)
	}
}

// TestAssembleRootRefusesAnUnwritableArgumentName is the second Loud Guardrail
// on this path. An argument's name is the name of the input variable it binds,
// so a name outside the engine's grammar cannot be mapped into it the way a
// module name is — a mapped name would bind a variable the module never
// declared. The halt names the component and the key, so the author can find the
// value they wrote.
//
// The check is capable of refusing and capable of passing: the sibling
// assertion drives the same shape with a legal name and expects no halt, so a
// green result here is not decoration.
func TestAssembleRootRefusesAnUnwritableArgumentName(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"db.name", "1st_choice", "db name", ""} {
		t.Run("refuses/"+key, func(t *testing.T) {
			_, err := terraformcore.AssembleRoot([]terraformcore.RootComponent{
				withValues(t, "db", 0, key, "appdb"),
			})
			if err == nil {
				t.Fatalf("a value named %q cannot be written as a module input and must halt", key)
			}
			for _, want := range []string{"db", key} {
				if want == "" {
					continue
				}
				if !strings.Contains(err.Error(), want) {
					t.Errorf("halt message must name %q; got: %v", want, err)
				}
			}
		})
	}

	if _, err := terraformcore.AssembleRoot([]terraformcore.RootComponent{
		withValues(t, "db", 0, "db_name", "appdb"),
	}); err != nil {
		t.Errorf("a legal argument name must not halt: %v", err)
	}
}

// TestAssembleRootRefusesBeforeEmitting proves the refusal is not partial: a
// boundary whose second component carries an unwritable name yields no files at
// all, rather than a configuration holding the first component and missing the
// second.
func TestAssembleRootRefusesBeforeEmitting(t *testing.T) {
	t.Parallel()

	files, err := terraformcore.AssembleRoot([]terraformcore.RootComponent{
		withValues(t, "db", 0, "db_name", "appdb"),
		withValues(t, "app", 0, "db.endpoint", "x"),
	})
	if err == nil {
		t.Fatal("expected a halt")
	}
	if files != nil {
		t.Errorf("a refused boundary must emit nothing; got %d files: %+v", len(files), files)
	}
}

// TestAssembleRootArgumentsAreDeterministic holds the Deterministic Output
// invariant across the new surface: identical input yields identical bytes, and
// the caller's key order is the emission order rather than a map's iteration
// order.
func TestAssembleRootArgumentsAreDeterministic(t *testing.T) {
	t.Parallel()

	build := func(pairs ...any) string {
		return rootBody(t, []terraformcore.RootComponent{withValues(t, "db", 0, pairs...)})
	}
	forward := build("alpha", "1", "beta", "2", "gamma", "3")
	if again := build("alpha", "1", "beta", "2", "gamma", "3"); again != forward {
		t.Error("identical input produced different bytes")
	}
	reverse := build("gamma", "3", "beta", "2", "alpha", "1")
	if reverse == forward {
		t.Fatal("the caller's key order must reach the output; two orders produced identical bytes")
	}
	idx := func(body, key string) int { return strings.Index(body, "  "+key) }
	if !(idx(forward, "alpha") < idx(forward, "beta") && idx(forward, "beta") < idx(forward, "gamma")) {
		t.Errorf("arguments are not in the supplied order:\n%s", forward)
	}
}
