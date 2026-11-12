// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraformcore_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore"
)

// oneWave builds the contributor list for a boundary in which every component
// carries the wave field's default, which is the shape of every boundary whose
// Release declared no wave at all.
//
// Each contributor's directory is the position a component placed directly
// beneath the assembly gets — the enclosing segment followed by the component's
// own identifier. It is spelled out here rather than defaulted inside the
// assembler, because a component's directory is the one case where the position
// happens to be derivable from the identifier, and a default would let every
// case in this package pass while a contributor whose position is NOT derivable
// that way silently reached the shape this fixture models.
func oneWave(ids ...string) []terraformcore.RootComponent {
	out := make([]terraformcore.RootComponent, 0, len(ids))
	for _, id := range ids {
		out = append(out, terraformcore.RootComponent{ID: id, OutputDirectory: componentDir(id)})
	}
	return out
}

// componentDir is the directory a component placed directly beneath the
// assembly has its own files in, as the host composes it.
func componentDir(id string) string {
	return terraformcore.ModulesDirName + "/" + id
}

// atWave builds one contributor carrying a wave, at the position a component
// placed directly beneath the assembly gets.
func atWave(id string, wave int) terraformcore.RootComponent {
	return terraformcore.RootComponent{ID: id, OutputDirectory: componentDir(id), SyncWave: wave}
}

// rootBody assembles one boundary and returns the root configuration's bytes,
// failing the test rather than returning an error the caller has to re-check.
func rootBody(t *testing.T, components []terraformcore.RootComponent) string {
	t.Helper()
	files, err := terraformcore.AssembleRoot(components)
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly one root configuration file, got %d", len(files))
	}
	return string(files[0].Content)
}

// TestAssembleRootChainsToPrecedingPopulatedWave is the ordering property in
// full, and the fixture is deliberately hostile to the easy implementation: the
// waves are non-contiguous, the lowest is negative, and one value between two
// populated waves has no component at all. A contiguous 0/1/2 fixture would pass
// on an implementation that simply named the previous integer.
//
// It also pins the fan-out bound: a wave names the immediately-preceding
// POPULATED wave and no earlier one, because the engine chains the rest
// transitively. Asserting the block's exact text is what makes that falsifiable —
// an assertion that the attribute merely exists would pass on an implementation
// that named every earlier wave.
func TestAssembleRootChainsToPrecedingPopulatedWave(t *testing.T) {
	t.Parallel()

	// Waves -5, 0 and 7 are populated; 3 is not. Two components share wave 0 so
	// the fan-out is visible in the wave that follows it.
	body := rootBody(t, []terraformcore.RootComponent{
		atWave("bootstrap", -5),
		atWave("db", 0),
		atWave("cache", 0),
		atWave("app", 7),
	})

	for _, want := range []string{
		// The lowest populated wave names nothing: nothing precedes it.
		"module \"bootstrap\" {\n  source = \"./modules/bootstrap\"\n}\n",
		// The wave-0 components chain to wave -5, skipping no populated wave and
		// inventing none.
		"module \"db\" {\n  source = \"./modules/db\"\n\n  depends_on = [module.bootstrap]\n}\n",
		"module \"cache\" {\n  source = \"./modules/cache\"\n\n  depends_on = [module.bootstrap]\n}\n",
		// The wave-7 component chains to wave 0 — both of its components, in
		// contributor order — and NOT to wave -5, which it reaches transitively
		// through wave 0. The unpopulated wave 3 between them changes nothing.
		"module \"app\" {\n  source = \"./modules/app\"\n\n  depends_on = [module.db, module.cache]\n}\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("root configuration is missing:\n%s\ngot:\n%s", want, body)
		}
	}

	// The fan-out bound, stated as an absence: the wave-7 component must not name
	// the wave -5 component, which is what a fan-out to every earlier wave would
	// emit.
	if strings.Contains(body, "module.db, module.cache, module.bootstrap") ||
		strings.Contains(body, "module.bootstrap, module.db") {
		t.Errorf("wave 7 must name the immediately-preceding populated wave only, not every earlier wave:\n%s", body)
	}
	// The attribute is counted in its emitted form, not as a bare token: the
	// header comment explains what a depends_on is for, so a token count reads one
	// higher than the number of attributes and would pass on the wrong number.
	if n := strings.Count(body, "\n  depends_on = ["); n != 3 {
		t.Errorf("expected 3 depends_on attributes (one per component above the lowest wave), got %d:\n%s", n, body)
	}
}

// TestAssembleRootWaveOrderingIsWaveOrderNotContributorOrder holds the thing that
// would otherwise pass unnoticed: the chain follows the declared wave values, not
// the order the host happened to list the contributors in. The contributors here
// are supplied in descending wave order, so an implementation that chained
// neighbours in the supplied list would produce the reverse of the correct answer.
func TestAssembleRootWaveOrderingIsWaveOrderNotContributorOrder(t *testing.T) {
	t.Parallel()

	body := rootBody(t, []terraformcore.RootComponent{
		atWave("last", 2),
		atWave("middle", 1),
		atWave("first", 0),
	})

	for _, want := range []string{
		"module \"last\" {\n  source = \"./modules/last\"\n\n  depends_on = [module.middle]\n}\n",
		"module \"middle\" {\n  source = \"./modules/middle\"\n\n  depends_on = [module.first]\n}\n",
		"module \"first\" {\n  source = \"./modules/first\"\n}\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("root configuration is missing:\n%s\ngot:\n%s", want, body)
		}
	}
	// Block order is still the contributor order, which is what keeps the emitted
	// bytes a function of the host's listing; only the dependency direction is
	// governed by the wave.
	idx := func(id string) int { return strings.Index(body, "module \""+id+"\"") }
	if !(idx("last") < idx("middle") && idx("middle") < idx("first")) {
		t.Errorf("module blocks are not in the supplied order:\n%s", body)
	}
}

// TestAssembleRootSingleWaveEmitsNoOrdering is the byte-identity guarantee, and
// it is a guarantee about STRUCTURE rather than about luck: fewer than two
// populated waves means the ordering pass records nothing at all, so the bytes are
// the bytes the assembler emitted before it could express any ordering.
//
// The expected value is the exact output of the implementation that carried no
// ordering pass, captured from it rather than transcribed from its format string.
// A change that perturbed the no-ordering layout — a stray blank line, a reordered
// attribute — would fail here, which is the whole point: this is the assertion a
// GitOps diff of an unchanged deployment depends on.
func TestAssembleRootSingleWaveEmitsNoOrdering(t *testing.T) {
	t.Parallel()

	const want = "# Root configuration for this boundary. Each component rendered here is one\n" +
		"# module block below, and the directory that block points at holds that\n" +
		"# component's own files. One configuration means one dependency graph, which is\n" +
		"# what lets a reference or a depends_on reach from one component to another.\n" +
		"\nmodule \"db\" {\n  source = \"./modules/db\"\n}\n" +
		"\nmodule \"app\" {\n  source = \"./modules/app\"\n}\n" +
		"\nmodule \"cache\" {\n  source = \"./modules/cache\"\n}\n"

	// A boundary whose Release declared no wave: every component carries the
	// field's default.
	if got := rootBody(t, oneWave("db", "app", "cache")); got != want {
		t.Errorf("a boundary with one populated wave must emit the no-ordering bytes exactly.\ngot:\n%q\nwant:\n%q", got, want)
	}

	// One populated wave that is not the default, and one that is negative: both
	// are a single wave, so both emit the same bytes. Nothing precedes a boundary's
	// only wave, whatever integer it carries.
	for _, wave := range []int{4, -1} {
		components := oneWave("db", "app", "cache")
		for i := range components {
			components[i].SyncWave = wave
		}
		if got := rootBody(t, components); got != want {
			t.Errorf("a boundary whose only populated wave is %d must emit the no-ordering bytes exactly.\ngot:\n%q", wave, got)
		}
	}
}

// TestAssembleRootWaveOrderingIsDeterministic pins the one place a wave-derived
// list could stop being a function of the input: the waves are grouped through a
// map, so the key order has to be imposed rather than inherited.
func TestAssembleRootWaveOrderingIsDeterministic(t *testing.T) {
	t.Parallel()

	components := []terraformcore.RootComponent{
		atWave("a", -3),
		atWave("b", 0),
		atWave("c", 0),
		atWave("d", 2),
		atWave("e", 9),
	}
	first := rootBody(t, components)
	for i := 0; i < 32; i++ {
		if got := rootBody(t, components); got != first {
			t.Fatalf("identical input produced different bytes on iteration %d:\ngot:\n%s\nfirst:\n%s", i, got, first)
		}
	}
}

// TestAssembleRootWaveOrderingSurvivesModuleNameMapping holds the join between the
// two rules: a dependency names the Terraform MODULE name, which for a nested
// component is not its identifier. Naming the identifier would emit a reference to
// a module the configuration does not declare, and the engine would reject it.
func TestAssembleRootWaveOrderingSurvivesModuleNameMapping(t *testing.T) {
	t.Parallel()

	body := rootBody(t, []terraformcore.RootComponent{
		atWave("platform.db", 0),
		atWave("1app", 1),
	})

	want := "module \"_1app\" {\n  source = \"./modules/1app\"\n\n  depends_on = [module.platform_db]\n}\n"
	if !strings.Contains(body, want) {
		t.Errorf("a dependency must name the mapped module name, not the component identifier:\n%s\ngot:\n%s", want, body)
	}
	if strings.Contains(body, "module.platform.db") {
		t.Errorf("dependency names the component identifier, which the configuration does not declare:\n%s", body)
	}
}
