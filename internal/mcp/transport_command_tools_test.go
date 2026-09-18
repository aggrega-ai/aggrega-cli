// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// What a caller reaching a listener may name on this machine is decided by
// composition rather than by a rule read off each call, and these proofs read
// the composition's result the way a client reads it: off the surfaces a session
// advertises, and off the answer a call gets.
//
// The property is a class one on purpose. A proof that sent one known argument
// and watched it be refused would pass just as well against a list of refused
// argument names, and a list is what cannot generalise: the next flag a command
// grows that names a directory is not on it, and nothing reports that it is
// missing. So the walk below asks of every advertised tool whether this process
// resolves what that tool is sent, and the planted defects are tools nobody has
// written, carrying an argument nobody has named.

// resolvedInThisProcess names the tools whose arguments this server resolves
// itself: the curated set and the statically declared long tail.
//
// That is the whole of the distinction the walk below rests on, and it is read
// from the package's own declaration rather than listed here. A tool in this set
// carries every path it accepts through one helper, which over HTTP holds the
// value inside the workspace the process serves — a guard over this package's
// source fails any site that reaches the filesystem another way. A tool outside
// it builds a command line and hands the values to a separate process, where the
// command reads each one against the host's own filesystem and nothing between
// this server and that process can hold any of them anywhere.
//
// So the predicate is not "the name looks like a path" and not "the property is
// one we know about". It is provenance: whether the value a caller sent is
// resolved here or handed away. A schema cannot answer that — it declares a
// name, a type and a sentence, none of which says what is done with the value —
// which is why this reads the registry a name belongs to instead.
func resolvedInThisProcess() map[string]bool {
	out := map[string]bool{}
	for _, name := range StaticToolNames() {
		out[name] = true
	}
	return out
}

// toolsReachingTheHostUnconfined names every advertised tool this process does
// not resolve the arguments of, deduplicated across the surfaces that
// advertised it and sorted so two readings are comparable.
func toolsReachingTheHostUnconfined(advertised []advertisement) []string {
	resolved := resolvedInThisProcess()
	seen := map[string]bool{}
	for _, a := range advertised {
		if !resolved[a.tool] {
			seen[a.tool] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// bothSurfacesWereWalked fails when either advertising surface contributed
// nothing, so an absence assertion cannot pass because the walk stopped early.
func bothSurfacesWereWalked(t *testing.T, advertised []advertisement) {
	t.Helper()
	if len(advertised) == 0 {
		t.Fatal("no definition was advertised at all; the walk measured nothing")
	}
	for _, surface := range []string{"tools/list", describeCapabilitiesName} {
		reached := false
		for _, a := range advertised {
			if a.surface == surface {
				reached = true
				break
			}
		}
		if !reached {
			t.Fatalf("%s advertised nothing, so the walk did not reach it", surface)
		}
	}
}

// TestAListenerAdvertisesNoToolItDoesNotResolveTheArgumentsOf is the floor this
// server holds over HTTP, asserted over everything it advertises rather than
// over a chosen tool.
//
// The two rows are one assertion read in both directions. The listener row is
// the property: nothing a caller is offered there hands a value it sent to a
// command running on the host. The standard-streams row is the control that
// keeps it from passing vacuously — the same walk over the transport that keeps
// those tools must find them — so a build that offered them nowhere would fail
// here instead of reporting the listener clean.
func TestAListenerAdvertisesNoToolItDoesNotResolveTheArgumentsOf(t *testing.T) {
	overHTTP := advertisedOverHTTP(t, newHTTPFixture(t, defaultTransportConfig(), nil))
	overStdio := advertisedOverStdio(t, newCommandToolServer(t, nil))

	bothSurfacesWereWalked(t, overHTTP)
	bothSurfacesWereWalked(t, overStdio)

	for _, name := range toolsReachingTheHostUnconfined(overHTTP) {
		for _, a := range overHTTP {
			if a.tool != name {
				continue
			}
			t.Errorf("%s advertises %s to a caller reaching the listener, and this server does not resolve "+
				"what that tool is sent — every value goes to a command run on this host, where a location "+
				"it names is reached as that command reads it. Its advertised arguments are: %s",
				a.surface, name, propertyNames(t, a.schema))
		}
	}

	kept := toolsReachingTheHostUnconfined(overStdio)
	if len(kept) == 0 {
		t.Fatal("the same walk over the transport whose caller is at this machine's keyboard found none " +
			"either, so the assertion above would pass against a build that offers them on no transport")
	}
	t.Logf("%d tool(s) run a command on this machine and are offered only to a caller at its keyboard: %v",
		len(kept), kept)
}

// propertyNames renders the properties a schema declares, in the order it
// declares them, so a failure says what the offending tool was accepting rather
// than only that it was offered.
func propertyNames(t *testing.T, schema json.RawMessage) string {
	t.Helper()
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		return "(a schema that does not parse)"
	}
	names := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// plantedCommandSurface projects a command tree holding one command nothing in
// this repository has: it carries a flag naming a directory, under a spelling no
// rule here mentions.
//
// It is projected through the same walk the fixture surface is, so what it
// yields is a tool derived exactly as a real command's is rather than one shaped
// to be caught.
func plantedCommandSurface() []CommandSurface {
	root := &cobra.Command{Use: "aggrega", Short: "the compiler"}
	planted := &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild an index",
		RunE:  func(*cobra.Command, []string) error { return nil },
	}
	planted.Flags().String("scratch-dir", "", "Directory the rebuild writes its intermediate files into")
	root.AddCommand(planted)
	return projectCommandSurface(root)
}

// plantedOperation is the operation each plant below declares, and
// plantedReach is the class it declares with it.
//
// A plant has to declare both to reach the surface the walk is about. A session
// reached over HTTP advertises only the reach classes the request's credential
// admits, and a spec carrying no class is taken to reach the machine — which no
// credential admits — so a plant declaring nothing would be advertised nowhere
// and the walk would have nothing to look at. Declaring the narrowest class is
// what puts the plant in front of the walk, and says nothing about what the plant
// does, which is the row's whole subject: the walk reads where a tool came from
// rather than what it says about itself, so a plant claiming to reach nothing is
// exactly the plant that should still be named.
//
// The operation is one no curated or deferred tool answers, so the plant is the
// twin of nothing and no other tool's advertised description changes because it
// was installed.
const plantedOperation = "version"

var plantedReach = opsurface.ReachNone

// plantedCommandTools derives a generated tool from the command above and gives
// it the operation and class every plant here declares.
func plantedCommandTools() []toolSpec {
	specs := deriveCommandTools(plantedCommandSurface())
	for i := range specs {
		specs[i].operation = plantedOperation
		specs[i].reach = plantedReach
	}
	return specs
}

// TestAToolPlantedOnAListenerIsNamedByTheWalk is the failure demonstration the
// assertion above needs in order to mean anything.
//
// The defect planted is a NEW tool reaching the host, not a removed check. That
// is the shape that separates a class property from a case: a proof whose
// planted defect is the check itself being taken away shows only that the check
// runs, while this shows what the check is able to see — a tool written after
// every rule here was, carrying an argument no rule here names.
//
// Both rows are installed after the session was composed, because composition is
// what removes them: a plant handed in with the configuration would be dropped,
// which demonstrates the composition rather than the walk.
func TestAToolPlantedOnAListenerIsNamedByTheWalk(t *testing.T) {
	for _, tc := range []struct {
		// scenario says what the row plants, so a failure reads as a rule.
		scenario string
		planted  []toolSpec
		want     string
	}{
		{
			scenario: "a tool generated from a command this repository does not have",
			planted:  plantedCommandTools(),
			want:     "cli-reindex",
		},
		{
			// Named without the prefix the generated tools carry, so the row fails
			// against a walk that had quietly become a check on the name.
			scenario: "a tool carrying a directory under a name no rule here mentions",
			planted: []toolSpec{{
				name:        "workspace-reindex",
				operation:   plantedOperation,
				reach:       plantedReach,
				description: "Rebuild an index.",
				inputSchema: `{"type":"object","properties":{"scratch_dir":{"type":"string",` +
					`"description":"Directory the rebuild writes its intermediate files into"}}}`,
				invoke: func(*server, context.Context, json.RawMessage) (*callToolResult, *rpcError) {
					return textResult("", nil)
				},
			}},
			want: "workspace-reindex",
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			f := newHTTPFixture(t, defaultTransportConfig(), nil)
			f.h.srv.commandTools = tc.planted

			advertised := advertisedOverHTTP(t, f)
			bothSurfacesWereWalked(t, advertised)

			// The precondition: the plant reached the surface. Without it the row
			// would pass against a walk that sees nothing at all.
			offered := false
			for _, a := range advertised {
				if a.tool == tc.want {
					offered = true
					if !strings.Contains(string(a.schema), "scratch_dir") {
						t.Errorf("%s was advertised without the directory argument it was planted with: %s",
							tc.want, a.schema)
					}
				}
			}
			if !offered {
				t.Fatalf("%s never reached the advertised surface, so this row measures nothing", tc.want)
			}

			named := toolsReachingTheHostUnconfined(advertised)
			if !containsStr(named, tc.want) {
				t.Errorf("the walk did not name %s, which was advertised to a caller reaching the listener "+
					"and whose arguments this server does not resolve; it named %v", tc.want, named)
			}
		})
	}
}

// recordingCommandRunner records the argument vector a generated tool built and
// reports success, so a proof reads what would have been run without running it.
func recordingCommandRunner(argv *[]string) commandRunner {
	return func(_ context.Context, args []string, stdout, _ io.Writer) error {
		*argv = append([]string(nil), args...)
		_, _ = io.WriteString(stdout, "done")
		return nil
	}
}

// runnerThatMustNotRun fails the test if a command is run at all.
func runnerThatMustNotRun(t *testing.T) commandRunner {
	return func(_ context.Context, args []string, _, _ io.Writer) error {
		t.Errorf("a command ran for a call that reached this server over HTTP: %v", args)
		return nil
	}
}

// TestACommandToolIsRefusedOverHTTPAndRunsForTheCallerAtTheKeyboard holds the
// two halves of the same rule against each other in one proof.
//
// The listener half is the property: the tool is in neither listing, a call
// naming it is answered with an error envelope that says why rather than only
// that the name is unknown, and no command runs. The standard-streams half is
// the control, and it is the same tool with the same argument — the one the
// listener half is about — so the refusal is shown to be about who is calling
// rather than about a capability that stopped working.
func TestACommandToolIsRefusedOverHTTPAndRunsForTheCallerAtTheKeyboard(t *testing.T) {
	const tool = "cli-work"

	f := newHTTPFixture(t, defaultTransportConfig(), nil)
	// Installed on the session rather than handed to the fixture, because the
	// fixture no longer installs a command runner at all: over HTTP nothing
	// reaches one. Installing it here is what turns "no command ran" from a
	// silence into an assertion.
	f.h.srv.runCommand = runnerThatMustNotRun(t)
	for _, a := range advertisedOverHTTP(t, f) {
		if a.tool == tool {
			t.Errorf("%s advertises %s to a caller reaching the listener", a.surface, tool)
		}
	}

	called := f.post(t, modernHeaders("tools/call", tool),
		modernRequest(t, 1, "tools/call", map[string]any{
			"name": tool, "arguments": map[string]any{"catalog": []string{"/etc"}},
		}))
	if called.frame.Error == nil {
		t.Fatalf("a call naming %s over HTTP was not answered with an error envelope: %s", tool, called.raw)
	}
	message := called.frame.Error.Message
	t.Logf("the listener answers a call naming %s with: %s", tool, message)
	for _, phrase := range []string{
		tool,
		"not offered when this server is reached over HTTP",
		"run at a terminal on that host",
	} {
		if !strings.Contains(message, phrase) {
			t.Errorf("the refusal does not say %q; it says: %s", phrase, message)
		}
	}

	var argv []string
	s := newCommandToolServer(t, recordingCommandRunner(&argv))
	surfaced := false
	for _, a := range advertisedOverStdio(t, s) {
		if a.tool == tool {
			surfaced = true
		}
	}
	if !surfaced {
		t.Fatalf("%s is not advertised to a caller at this machine's keyboard either, "+
			"so the refusal above is not a transport boundary", tool)
	}
	res := callDiscoveryTool(t, s, tool, map[string]any{"catalog": []string{"/anywhere"}})
	if res.IsError {
		t.Fatalf("%s failed for a caller at this machine's keyboard: %s", tool, res.Content[0].Text)
	}
	if !containsStr(argv, "--catalog=/anywhere") {
		t.Errorf("the command ran without the directory the call named: %v", argv)
	}
}
