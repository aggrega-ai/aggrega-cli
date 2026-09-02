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

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
)

// fixtureCommandTree builds a command tree standing in for the real one: a
// runnable leaf carrying flags of every derived shape, a leaf declaring
// positional arguments, a grouping command holding a runnable child, and two
// commands the exemption list names.
//
// The root is named for the binary because an exemption is recorded against a
// full invocation path, so a fixture rooted at any other name would not exercise
// the exemption match at all.
func fixtureCommandTree() *cobra.Command {
	root := &cobra.Command{Use: "aggrega", Short: "the compiler"}
	root.PersistentFlags().String("workspace-root", "", "Workspace root override")

	work := &cobra.Command{
		Use:   "work",
		Short: "Do the work",
		RunE:  func(*cobra.Command, []string) error { return nil },
	}
	work.Flags().String("output-dir", "", "Destination directory")
	work.Flags().Bool("dry-run", false, "Report without writing")
	work.Flags().Int("depth", 3, "How deep to go")
	work.Flags().StringArray("catalog", nil, "Catalog directory; repeatable")
	work.Flags().String("secret", "", "Hidden knob")
	_ = work.Flags().MarkHidden("secret")

	positional := &cobra.Command{
		Use:   "inspect <entity-path>",
		Short: "Inspect an entity",
		RunE:  func(*cobra.Command, []string) error { return nil },
	}

	group := &cobra.Command{Use: "group", Short: "A grouping command"}
	group.AddCommand(&cobra.Command{
		Use:   "nested",
		Short: "A nested operation",
		RunE:  func(*cobra.Command, []string) error { return nil },
	})

	root.AddCommand(work, positional, group)
	// Two commands the exemption list names, so the derivation's exclusion of
	// them is exercised rather than assumed.
	root.AddCommand(&cobra.Command{
		Use: "serve", Short: "Run the daemon",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use: "mcp", Short: "Run this server",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	return root
}

// fixtureCommandSurface projects the fixture tree exactly as the command layer
// projects the real one: the shared catalog, plus each command's own answer to
// whether it runs.
func fixtureCommandSurface() []CommandSurface {
	root := fixtureCommandTree()
	runnable := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		runnable[c.CommandPath()] = c.Runnable()
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	cat := clisurface.Catalog(root)
	out := make([]CommandSurface, 0, len(cat))
	for _, d := range cat {
		out = append(out, CommandSurface{Descriptor: d, Runnable: runnable[d.Path]})
	}
	return out
}

// newCommandToolServer builds a server serving the fixture command surface,
// with the command runner injected so no subprocess is spawned.
func newCommandToolServer(t *testing.T, run commandRunner) *server {
	t.Helper()
	return &server{
		out:          io.Discard,
		logw:         io.Discard,
		startDir:     t.TempDir(),
		runCommand:   run,
		commandTools: deriveCommandTools(fixtureCommandSurface()),
	}
}

func toolSpecNames(specs []toolSpec) []string {
	out := make([]string, 0, len(specs))
	for i := range specs {
		out = append(out, specs[i].name)
	}
	return out
}

// missingFrom names the members of want that have absent from got. It names
// them rather than counting them, because two sets both short by one still
// agree on length.
func missingFrom(want, got []string) []string {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	sort.Strings(missing)
	return missing
}

// TestCommandToolsCoverEveryNonExemptCommand is the completeness guard: every
// command that runs an operation and is not exempt has a generated tool, and
// nothing else does.
//
// The expected set is computed from the surface independently of the derivation
// under test, and the two are compared by name in both directions, so a command
// dropped by the derivation is named here rather than showing up as a count that
// happens to match.
func TestCommandToolsCoverEveryNonExemptCommand(t *testing.T) {
	surface := fixtureCommandSurface()

	var want []string
	for _, cs := range surface {
		if !cs.Runnable || cs.Descriptor.Hidden {
			continue
		}
		if _, exempt := CommandExempted(cs.Descriptor.Path); exempt {
			continue
		}
		want = append(want, commandToolName(cs.Descriptor.Path))
	}
	got := toolSpecNames(deriveCommandTools(surface))

	if missing := missingFrom(want, got); len(missing) > 0 {
		t.Errorf("commands with no generated tool: %v (derived %v)", missing, got)
	}
	if extra := missingFrom(got, want); len(extra) > 0 {
		t.Errorf("generated tools for commands that should have none: %v", extra)
	}

	// The fixture's grouping command runs nothing, so it yields no tool while
	// its runnable child does.
	if containsStr(got, "cli-group") {
		t.Errorf("a grouping command that runs nothing produced a tool: %v", got)
	}
	if !containsStr(got, "cli-group-nested") {
		t.Errorf("a nested operation produced no tool: %v", got)
	}
	// An exempt command produces no tool however runnable it is.
	for _, exempt := range []string{"cli-serve", "cli-mcp"} {
		if containsStr(got, exempt) {
			t.Errorf("exempt command produced a tool %q: %v", exempt, got)
		}
	}
}

// TestCommandToolNamesNeverCollideWithCuratedTools guards the one boundary that
// silently destroys a curated tool: a generated name equal to a curated or
// statically deferred one would shadow it in the lookup, replacing a
// hand-shaped goal tool with a thin command wrapper.
//
// Commands named validate, build, lint and get-schema all exist, and curated
// tools of exactly those names exist too, so this holds only because generated
// names carry a prefix. Removing that prefix fails this test.
func TestCommandToolNamesNeverCollideWithCuratedTools(t *testing.T) {
	reserved := map[string]bool{}
	for _, name := range append(toolSpecNames(toolRegistry), toolSpecNames(deferredToolRegistry)...) {
		reserved[name] = true
	}
	for _, name := range toolSpecNames(deriveCommandTools(fixtureCommandSurface())) {
		if reserved[name] {
			t.Errorf("generated command tool %q collides with an existing tool of the same name", name)
		}
	}

	// The collision is real rather than hypothetical: these command names and
	// these curated tool names are the same words, so the prefix is what keeps
	// them apart.
	for _, shared := range []string{"validate", "build", "lint", "get-schema"} {
		if !reserved[shared] {
			t.Errorf("expected %q to be an existing tool name; the collision this guards is no longer real", shared)
		}
		if commandToolName("aggrega "+shared) == shared {
			t.Errorf("derived name for the %q command is unprefixed and would shadow the curated tool", shared)
		}
	}
}

// TestCommandExemptionsAreReasonedAndRead asserts the exemption list is usable
// as data: every entry names a command path and carries a reason, no path is
// listed twice, and the derivation actually reads the list rather than
// hard-coding the same set.
func TestCommandExemptionsAreReasonedAndRead(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range commandExemptions {
		if strings.TrimSpace(e.Path) == "" {
			t.Errorf("exemption with no command path: %+v", e)
		}
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("exemption for %q carries no reason", e.Path)
		}
		if seen[e.Path] {
			t.Errorf("exemption for %q listed twice", e.Path)
		}
		seen[e.Path] = true
		if !strings.HasPrefix(e.Path, "aggrega ") {
			t.Errorf("exemption path %q is not a full invocation path, so it can never match a descriptor", e.Path)
		}
	}

	// auth token is exempt on its own ground, and the reason records which
	// ground: it has no non-interactive form, as distinct from being unusable
	// over a tool protocol. That distinction is what keeps the signing verbs,
	// which do have one, off this list.
	authToken, ok := CommandExempted("aggrega auth token")
	if !ok {
		t.Fatal("aggrega auth token is not exempt; it acquires a token interactively unconditionally")
	}
	if !strings.Contains(authToken.Reason, "non-interactive") {
		t.Errorf("auth token's exemption reason does not record that it has no non-interactive form: %q", authToken.Reason)
	}
	for _, verb := range []string{"aggrega push", "aggrega sign", "aggrega deprecate"} {
		if _, exempt := CommandExempted(verb); exempt {
			t.Errorf("%q is exempt, but it resolves an identity from supplied or ambient inputs and refuses "+
				"loudly without one, so it is usable here", verb)
		}
	}

	// The derivation reads the list rather than carrying its own copy of the
	// same set. The fixture's serve and mcp commands are runnable and otherwise
	// indistinguishable from its work command, which is derived; they are
	// excluded, and the only thing that distinguishes them is their presence
	// here.
	surface := fixtureCommandSurface()
	derived := toolSpecNames(deriveCommandTools(surface))
	if !containsStr(derived, "cli-work") {
		t.Fatalf("fixture precondition: an unexempt runnable command should be derived, got %v", derived)
	}
	var exemptInFixture int
	for _, cs := range surface {
		if !cs.Runnable {
			continue
		}
		if _, exempt := CommandExempted(cs.Descriptor.Path); !exempt {
			continue
		}
		exemptInFixture++
		if name := commandToolName(cs.Descriptor.Path); containsStr(derived, name) {
			t.Errorf("%q is on the exemption list but was derived as %q, so the derivation is not reading the list",
				cs.Descriptor.Path, name)
		}
	}
	if exemptInFixture == 0 {
		t.Error("the fixture contains no exempt command, so the exclusion is untested")
	}

	// The exported reader is the one other packages consume, and it must not
	// hand out the slice the derivation reads.
	published := CommandExemptions()
	if len(published) != len(commandExemptions) {
		t.Errorf("CommandExemptions() published %d entries for %d declared", len(published), len(commandExemptions))
	}
	if len(published) > 0 {
		published[0] = CommandExemption{Path: "mutated", Reason: "mutated"}
		if commandExemptions[0].Path == "mutated" {
			t.Error("CommandExemptions() returned the live slice; a caller can alter what the derivation reads")
		}
	}
}

// TestCommandToolArgumentsFollowTheFlagWords asserts a derived property carries
// the flag's own words in snake_case, and that the derived shapes match the
// flags they came from.
//
// The load-bearing case is a multi-word flag: --output-dir is sent as
// output_dir, which is the spelling the hand-shaped gen-catalog tool already
// carries, so derivation and the shipped surface agree rather than diverging.
func TestCommandToolArgumentsFollowTheFlagWords(t *testing.T) {
	var work commandTool
	for _, cs := range fixtureCommandSurface() {
		if cs.Descriptor.Path == "aggrega work" {
			work = commandTool{
				name:       commandToolName(cs.Descriptor.Path),
				path:       commandArgvPath(cs.Descriptor.Path),
				arguments:  commandArguments(cs.Descriptor),
				positional: commandPositionalSummary(cs.Descriptor.Use),
			}
		}
	}
	if len(work.arguments) == 0 {
		t.Fatal("no arguments derived for the fixture work command")
	}

	byProperty := map[string]commandArgument{}
	for _, a := range work.arguments {
		byProperty[a.property] = a
	}
	for _, want := range []struct {
		property string
		flag     string
		jsonType string
		repeated bool
	}{
		{"output_dir", "output-dir", "string", false},
		{"dry_run", "dry-run", "boolean", false},
		{"depth", "depth", "integer", false},
		{"catalog", "catalog", "array", true},
		{"workspace_root", "workspace-root", "string", false},
	} {
		got, ok := byProperty[want.property]
		if !ok {
			t.Errorf("no property %q derived; got %v", want.property, work.acceptedProperties())
			continue
		}
		if got.flag != want.flag || got.jsonType != want.jsonType || got.repeated != want.repeated {
			t.Errorf("property %q = flag %q/%s/repeated=%v, want flag %q/%s/repeated=%v",
				want.property, got.flag, got.jsonType, got.repeated, want.flag, want.jsonType, want.repeated)
		}
	}

	// A hidden flag is not part of the documented surface and is not derived.
	if _, ok := byProperty["secret"]; ok {
		t.Errorf("a hidden flag was derived into the tool surface: %v", work.acceptedProperties())
	}

	// The shipped hand-shaped tool spells the same flag the same way, which is
	// the agreement this tier exists to make permanent.
	genCatalog, ok := lookupTool("gen-catalog")
	if !ok {
		t.Fatal("the curated gen-catalog tool is absent")
	}
	if !strings.Contains(genCatalog.inputSchema, `"output_dir"`) {
		t.Errorf("gen-catalog's schema no longer carries output_dir; derivation and the shipped surface have diverged")
	}
	if commandArgumentName("output-dir") != "output_dir" {
		t.Errorf("commandArgumentName(--output-dir) = %q, want output_dir", commandArgumentName("output-dir"))
	}
}

// TestGeneratedCommandToolIsInvocableByName drives a generated tool through the
// same tools/call path a curated tool uses and asserts the command actually ran:
// the argument vector it received, and its real output coming back as tool data.
func TestGeneratedCommandToolIsInvocableByName(t *testing.T) {
	var gotArgs []string
	s := newCommandToolServer(t, func(_ context.Context, args []string, stdout, stderr io.Writer) error {
		gotArgs = args
		_, _ = io.WriteString(stdout, "wrote 3 files\n")
		_, _ = io.WriteString(stderr, "notice: overlay applied\n")
		return nil
	})

	res := callTool(t, s, "cli-work", map[string]any{
		"output_dir": "/tmp/out",
		"dry_run":    true,
		"depth":      7,
		"catalog":    []string{"a", "b"},
	})
	if res.IsError {
		t.Fatalf("cli-work reported error: %s", res.Content[0].Text)
	}

	// The flags are emitted in the derived order, so one set of arguments always
	// produces one argument vector.
	want := []string{"work", "--catalog=a", "--catalog=b", "--depth=7", "--dry-run=true", "--output-dir=/tmp/out"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v, want %v", gotArgs, want)
	}
	// The command's real output is what comes back.
	if !strings.Contains(res.Content[0].Text, "wrote 3 files") {
		t.Errorf("tool result dropped the command's output: %q", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "overlay applied") {
		t.Errorf("tool result dropped the command's diagnostics: %q", res.Content[0].Text)
	}
	var payload commandToolResultPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal structured payload: %v", err)
	}
	if payload.Command != "aggrega work --catalog=a --catalog=b --depth=7 --dry-run=true --output-dir=/tmp/out" {
		t.Errorf("structured payload command = %q", payload.Command)
	}

	// A nested command's path becomes the argument vector that selects it.
	res2 := callTool(t, s, "cli-group-nested", map[string]any{})
	if res2.IsError {
		t.Fatalf("cli-group-nested reported error: %s", res2.Content[0].Text)
	}
	if strings.Join(gotArgs, " ") != "group nested" {
		t.Errorf("nested argv = %v, want [group nested]", gotArgs)
	}

	// Positional arguments go behind a bare double dash, so a value beginning
	// with a dash is read as a value rather than as a flag.
	res3 := callTool(t, s, "cli-inspect", map[string]any{"args": []string{"-weird-name"}})
	if res3.IsError {
		t.Fatalf("cli-inspect reported error: %s", res3.Content[0].Text)
	}
	if strings.Join(gotArgs, " ") != "inspect -- -weird-name" {
		t.Errorf("positional argv = %v, want [inspect -- -weird-name]", gotArgs)
	}
}

// TestGeneratedCommandToolSurfacesAFailure asserts a command that exits non-zero
// comes back as a tool-level failure carrying the command's own diagnostics,
// rather than as an opaque protocol error.
func TestGeneratedCommandToolSurfacesAFailure(t *testing.T) {
	s := newCommandToolServer(t, func(_ context.Context, _ []string, _, stderr io.Writer) error {
		_, _ = io.WriteString(stderr, "E0001: release.yaml: no such file")
		return errDummyExit
	})
	res := callTool(t, s, "cli-work", map[string]any{})
	if !res.IsError {
		t.Fatal("a non-zero exit was not reported as a tool-level failure")
	}
	if !strings.Contains(res.Content[0].Text, "E0001") {
		t.Errorf("failure result dropped the command's diagnostics: %q", res.Content[0].Text)
	}
}

// TestGeneratedCommandToolRefusesAnUnknownProperty asserts an argument the
// command does not declare is refused, naming what the tool accepts. A silently
// dropped argument reads to a caller as an argument that was honored.
func TestGeneratedCommandToolRefusesAnUnknownProperty(t *testing.T) {
	var ran bool
	s := newCommandToolServer(t, func(_ context.Context, _ []string, _, _ io.Writer) error {
		ran = true
		return nil
	})
	params, _ := json.Marshal(callToolParams{
		Name:      "cli-work",
		Arguments: mustJSON(t, map[string]any{"output-dir": "/tmp/out"}),
	})
	_, rpcErr := s.handleToolsCall(params)
	if rpcErr == nil || rpcErr.Code != codeInvalidParams {
		t.Fatalf("kebab-spelled property = %+v, want invalid-params", rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "output_dir") {
		t.Errorf("refusal does not name the accepted spelling: %q", rpcErr.Message)
	}
	if ran {
		t.Error("the command ran despite an unaccepted argument")
	}
}

// TestCommandToolsAreDiscoverableThroughTheCapabilityTool asserts the generated
// tools reach a client the way the long tail is meant to: absent from the
// default list, surfaced in full by the capability tool, and invocable by the
// name that listing gives.
func TestCommandToolsAreDiscoverableThroughTheCapabilityTool(t *testing.T) {
	s := newCommandToolServer(t, func(_ context.Context, _ []string, stdout, _ io.Writer) error {
		_, _ = io.WriteString(stdout, "ok")
		return nil
	})

	// The default list stays the curated set: the command surface never inflates
	// the up-front definition budget.
	res, rpcErr := s.handleToolsList()
	if rpcErr != nil {
		t.Fatalf("tools/list: %+v", rpcErr)
	}
	list := res.(listToolsResult)
	if len(list.Tools) != len(toolRegistry) {
		t.Errorf("default tools/list has %d tools, want the curated %d", len(list.Tools), len(toolRegistry))
	}
	for _, tl := range list.Tools {
		if strings.HasPrefix(tl.Name, commandToolPrefix) {
			t.Errorf("a generated command tool leaked into the default list: %q", tl.Name)
		}
	}

	// The capability tool surfaces every one of them, with a full definition.
	cap := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{})
	var payload describeCapabilitiesResult
	if err := json.Unmarshal(cap.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal capabilities: %v", err)
	}
	surfaced := capabilityNames(payload)
	want := toolSpecNames(deriveCommandTools(fixtureCommandSurface()))
	if missing := missingFrom(want, surfaced); len(missing) > 0 {
		t.Errorf("capability listing omits generated tools: %v", missing)
	}
	for _, c := range payload.Capabilities {
		if !strings.HasPrefix(c.Name, commandToolPrefix) {
			continue
		}
		if c.Description == "" || len(c.InputSchema) == 0 {
			t.Errorf("generated tool %q surfaced without a full definition", c.Name)
		}
		if !c.Deferred {
			t.Errorf("generated tool %q not marked deferred", c.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(c.InputSchema, &schema); err != nil {
			t.Errorf("generated tool %q has an unparseable input schema: %v", c.Name, err)
		}
	}

	// Every surfaced name is invocable by that name — discovery and dispatch
	// agree, which is what makes the listing actionable rather than decorative.
	for _, name := range want {
		if _, ok := s.lookupTool(name); !ok {
			t.Errorf("surfaced tool %q is not invocable by name", name)
		}
	}
}

// TestCommandToolDiscoveryIsByteStable asserts the discovery surface is
// byte-identical across independently constructed sessions, which is the
// property a diff-based reviewer and a caching client rely on.
//
// It compares the marshalled bytes rather than the order of a name list: a
// listing can hold its order while a description or a schema fragment moves.
func TestCommandToolDiscoveryIsByteStable(t *testing.T) {
	render := func() []byte {
		s := newCommandToolServer(t, nil)
		res := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{})
		return res.StructuredContent
	}
	first := render()
	for i := 0; i < 3; i++ {
		if got := render(); string(got) != string(first) {
			t.Fatalf("capability discovery is not byte-stable across sessions (run %d):\n first %s\n got   %s",
				i+2, first, got)
		}
	}

	// tools/list is stable for the same reason and must stay the curated set.
	renderList := func() []byte {
		s := newCommandToolServer(t, nil)
		res, _ := s.handleToolsList()
		b, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("marshal tools/list: %v", err)
		}
		return b
	}
	firstList := renderList()
	if got := renderList(); string(got) != string(firstList) {
		t.Errorf("tools/list is not byte-stable across sessions")
	}

	// The derived order is the catalog's, which is the command tree's own
	// depth-first pre-order, so it does not depend on map iteration.
	a := toolSpecNames(deriveCommandTools(fixtureCommandSurface()))
	for i := 0; i < 3; i++ {
		b := toolSpecNames(deriveCommandTools(fixtureCommandSurface()))
		if strings.Join(a, ",") != strings.Join(b, ",") {
			t.Fatalf("derived tool order is unstable: %v then %v", a, b)
		}
	}
}

// TestCommandToolDefinitionsStayModest records the deferred long tail's
// definition budget and fails if it balloons. The long tail is loaded on demand,
// so it is allowed to be larger than the curated set — but a client that asks
// for the whole tail receives all of it in one payload, so carrying each
// command's full long-form help here rather than serving it from cli:// would
// make that payload the largest thing the server emits.
func TestCommandToolDefinitionsStayModest(t *testing.T) {
	specs := deriveCommandTools(fixtureCommandSurface())
	perTool := 0
	for i := range specs {
		perTool += len(specs[i].name) + len(specs[i].description) + len(specs[i].inputSchema)
	}
	if len(specs) == 0 {
		t.Fatal("no generated tools to measure")
	}
	average := perTool / len(specs)
	t.Logf("generated command tools: %d tools, %d bytes, ~%d bytes each", len(specs), perTool, average)

	// A definition carrying a command's one-line help and its flag schema runs
	// a few hundred bytes; one carrying the full long-form help runs into the
	// thousands. This trips on the latter.
	const ceiling = 1800
	if average > ceiling {
		t.Errorf("generated tool definitions average %d bytes, over %d; the long-form help belongs in the cli:// resource, not in every definition", average, ceiling)
	}
}
