// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// noRegistriesNotice is the sentence a completed operation over an empty registry
// set reports. It is pinned here because the two surfaces that still print it are
// asserted byte-for-byte below, and a test comparing against a literal typed twice
// would pass while the two drifted apart.
const noRegistriesNotice = "No registries configured.\n"

// TestSearchRefusesWhenNoRegistryIsConfigured proves that a search with nothing to
// read is a refusal rather than a result: it returns an error, leaves standard
// output completely empty, and names the command that registers a registry.
//
// Standard output is asserted for every rendering rather than for the default one,
// because the machine-readable renderings are the ones a caller parses: a refusal
// that left a JSON document behind would be read as a successful empty search by
// exactly the consumer this distinction exists for.
func TestSearchRefusesWhenNoRegistryIsConfigured(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})

	for _, format := range []string{"table", "json", "names"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			out, errOut, err := runSearch(t, rw, searchOptions{Query: "postgres", Output: format})

			if err == nil {
				t.Fatalf("searchRun with no registries configured (-o %s): got nil error; want a refusal", format)
			}
			if !errors.Is(err, errNoRegistriesConfigured) {
				t.Errorf("error %q does not match errNoRegistriesConfigured; a caller telling this refusal "+
					"from the others has only the identity to match on", err)
			}
			if !strings.Contains(err.Error(), "aggrega config init") {
				t.Errorf("error %q must name 'aggrega config init', which is the command that registers a registry", err)
			}
			if out != "" {
				t.Errorf("stdout = %q; want empty, so a caller cannot read a refusal as a completed empty search", out)
			}
			if errOut != "" {
				t.Errorf("errOut = %q; want empty — no index was consulted, so there is no freshness line to write", errOut)
			}
		})
	}
}

// TestSearchRefusalIsDistinctFromTheNoIndexRefusal is the positive control for the
// assertion above: a workspace that DOES configure a registry, whose index has not
// been built, must still fail — and must fail as a different refusal.
//
// Without it, matching on errNoRegistriesConfigured would be satisfied by any
// implementation that returned that one value for every search failure, and the
// row above could not tell the two apart.
func TestSearchRefusalIsDistinctFromTheNoIndexRefusal(t *testing.T) {
	t.Parallel()
	content := "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n  - host: hub.aggrega.ai\n    name: hub\n"
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), content)

	_, _, err := runSearch(t, rw, searchOptions{Output: "names"})
	if err == nil {
		t.Fatal("searchRun (a registry configured, no index built): got nil error; want a refusal")
	}
	if errors.Is(err, errNoRegistriesConfigured) {
		t.Errorf("error %q matched errNoRegistriesConfigured, but a registry IS configured; "+
			"the two refusals mean different things and a caller distinguishes them by identity", err)
	}
}

// TestSearchWithNoRegistriesExitsNonZero drives the real command tree through the
// exit-status boundary, so the refusal is proved to reach a process status rather
// than only a returned error. A returned error that some layer above swallowed
// would leave every assertion in this file green and the shipped command still
// reporting success.
func TestSearchWithNoRegistriesExitsNonZero(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"search", "postgres"})

	code := executeRoot(context.Background(), root)
	if code == 0 {
		t.Errorf("`aggrega search postgres` with no registries configured exited %d; want non-zero, "+
			"so a caller branching on the status can tell it from a search that matched nothing\n"+
			"stdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	if out.String() != "" {
		t.Errorf("stdout = %q; want empty", out.String())
	}
}

// TestRegistryListWithNoRegistriesIsUnchanged pins the contract this item
// deliberately leaves alone. Listing registries when there are none is a complete
// listing of an empty set, so it belongs on standard output with a zero status.
//
// It is asserted byte-for-byte rather than by substring because the risk here is a
// later change that "unifies" the three surfaces printing this sentence onto the
// search's stream and status — which a substring assertion would survive.
func TestRegistryListWithNoRegistriesIsUnchanged(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})

	var out bytes.Buffer
	if err := registryListRun(context.Background(), registryListConfig{ws: rw, Out: &out}); err != nil {
		t.Fatalf("registryListRun: %v; listing an empty set is a completed listing, not a refusal", err)
	}
	if out.String() != noRegistriesNotice {
		t.Errorf("stdout = %q; want %q", out.String(), noRegistriesNotice)
	}
}

// TestRegistryUpdateWithNoRegistriesIsUnchanged pins the other contract left alone.
// An index build with nothing to walk has nothing to do, so it too reports on
// standard output and exits zero.
//
// It reaches no network: the walk returns at the empty host set before any host is
// contacted.
func TestRegistryUpdateWithNoRegistriesIsUnchanged(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})

	var out bytes.Buffer
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out}); err != nil {
		t.Fatalf("registryUpdateRun: %v; an index build with nothing to walk is a completed build, not a refusal", err)
	}
	if out.String() != noRegistriesNotice {
		t.Errorf("stdout = %q; want %q", out.String(), noRegistriesNotice)
	}
}
