// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// runGetSchemaList executes get-schema --list on a freshly built command with
// separate stdout and stderr sinks, and returns both.
//
// It deliberately does NOT install a workspace resolver on the command context.
// Production wiring attaches one in the root command's PersistentPreRunE, so a
// command run without it is the strongest available statement that this path
// never reaches workspace resolution: the kinds come from the binary's own
// embedded copy, so the query answers identically outside a workspace, offline,
// and with no configuration — which is what makes it usable for wiring an
// editor's schema association.
func runGetSchemaList(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	c := newGetSchemaCmd()
	c.SetOut(&out)
	c.SetErr(&errBuf)
	c.SetArgs(args)
	err = c.Execute()
	return out.String(), errBuf.String(), err
}

// TestGetSchemaListEmitsTheAuthoritativeKindSet pins the emitted bytes against
// the schema enumeration itself rather than against a hand-written list of
// kinds.
//
// The derivation is the point. A test that restated the set would become a
// second home of what the binary can emit a schema for, and it would pass while
// the command and the embedded schema set disagreed, because both would be
// compared against the test's own third copy.
//
// Exact-byte equality against the joined enumeration proves every property a
// parser depends on at once: one kind per line, in the enumeration's order,
// terminated by a newline, with no header, count, or decoration around it.
func TestGetSchemaListEmitsTheAuthoritativeKindSet(t *testing.T) {
	t.Parallel()

	kinds := schemas.Kinds()
	// Calibration: an empty enumeration would make the comparison below
	// "" == "" and the test would pass while asserting nothing. A vacuous pass
	// reads exactly like a real one.
	if len(kinds) == 0 {
		t.Fatal("the binary embeds no schema kinds, so the comparison below asserts nothing")
	}

	stdout, stderr, err := runGetSchemaList(t, "--list")
	if err != nil {
		t.Fatalf("get-schema --list: %v (stderr: %s)", err, stderr)
	}

	want := strings.Join(kinds, "\n") + "\n"
	if stdout != want {
		t.Errorf("get-schema --list stdout = %q; want %q", stdout, want)
	}
	// The listing is the whole answer: a consumer reading stdout must not have
	// to know that something was also said elsewhere.
	if stderr != "" {
		t.Errorf("get-schema --list wrote to stderr: %q", stderr)
	}
}

// TestGetSchemaListOutputIsParseable checks the emitted shape by its properties
// rather than by re-deriving the expected bytes.
//
// This overlaps TestGetSchemaListEmitsTheAuthoritativeKindSet deliberately, and
// the duplication is kept rather than folded away: that test compares against a
// join of the enumeration, so a change to how the expectation is built could
// move both sides together. These assertions are stated in the terms a consuming
// script actually uses — split the output on newlines and read each line as one
// token — so they stay meaningful if the other test's derivation changes.
func TestGetSchemaListOutputIsParseable(t *testing.T) {
	t.Parallel()

	want := schemas.Kinds()
	if len(want) == 0 {
		t.Fatal("the binary embeds no schema kinds, so the assertions below assert nothing")
	}

	stdout, stderr, err := runGetSchemaList(t, "--list")
	if err != nil {
		t.Fatalf("get-schema --list: %v (stderr: %s)", err, stderr)
	}

	if !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("output must end in a newline so a line-reading consumer sees the last kind; got %q", stdout)
	}
	// One trailing newline terminates the final record rather than starting an
	// empty one, so the split drops exactly one empty tail element.
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d line(s) for %d kind(s): %q", len(lines), len(want), stdout)
	}
	for i, line := range lines {
		if line != want[i] {
			t.Errorf("line %d = %q; want %q", i, line, want[i])
		}
		if line == "" {
			t.Errorf("line %d is blank; a blank line is a record a consumer cannot read", i)
		}
		if line != strings.TrimSpace(line) {
			t.Errorf("line %d = %q carries surrounding whitespace; a consumer must not have to trim", i, line)
		}
		if strings.ContainsAny(line, " \t") {
			t.Errorf("line %d = %q is not a single token; a consumer reads the whole line as the kind", i, line)
		}
	}
}

// TestGetSchemaListIsSortedAscending pins the emitted order.
//
// The order is asserted on the OUTPUT rather than on the enumeration, because
// the order a consumer can rely on is the one it reads. A caller diffing two
// runs of this listing across binary versions sees only real additions and
// removals when the order is stable and total, and sees spurious churn the
// moment it is not.
func TestGetSchemaListIsSortedAscending(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runGetSchemaList(t, "--list")
	if err != nil {
		t.Fatalf("get-schema --list: %v (stderr: %s)", err, stderr)
	}
	emitted := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	// Calibration: a single-element listing is sorted for free, so the assertion
	// below would hold without measuring anything.
	if len(emitted) < 2 {
		t.Fatalf("the listing has %d entry/entries, so the order assertion below asserts nothing", len(emitted))
	}

	if !sort.StringsAreSorted(emitted) {
		t.Errorf("get-schema --list is not in ascending order: %v", emitted)
	}
}

// TestGetSchemaListReadsOneHome pins that the command holds no kind list of its
// own. The emitted set must be exactly what the binary embeds — no additions,
// and no exclusions.
//
// An exclusion is the failure mode worth guarding: it looks like a filter rather
// than a copy, but a filter names the kinds it drops, so it is a second home of
// the rule wearing a different shape. Checking both difference directions is what
// makes that visible; checking only that every emitted kind is embedded would
// pass an emission that silently dropped one.
//
// The addition direction carries a second guarantee: a kind this command names is
// one the same command can actually print a schema for, so a caller can walk the
// listing and fetch every entry in it without meeting an unknown-kind refusal.
func TestGetSchemaListReadsOneHome(t *testing.T) {
	t.Parallel()

	embedded := schemas.Kinds()
	if len(embedded) == 0 {
		t.Fatal("the binary embeds no schema kinds, so the comparison below asserts nothing")
	}

	stdout, stderr, err := runGetSchemaList(t, "--list")
	if err != nil {
		t.Fatalf("get-schema --list: %v (stderr: %s)", err, stderr)
	}
	emitted := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")

	inEmitted := make(map[string]bool, len(emitted))
	for _, k := range emitted {
		inEmitted[k] = true
	}
	inEmbedded := make(map[string]bool, len(embedded))
	for _, k := range embedded {
		inEmbedded[k] = true
	}
	for _, k := range embedded {
		if !inEmitted[k] {
			t.Errorf("kind %q is embedded in the binary but not emitted: the command excludes it", k)
		}
	}
	for _, k := range emitted {
		if !inEmbedded[k] {
			t.Errorf("kind %q is emitted but not embedded in the binary: the command adds it", k)
		}
		if _, ok := schemas.SchemaByKind(k); !ok {
			t.Errorf("kind %q is emitted but resolves to no schema, so fetching it would be refused", k)
		}
	}
}

// TestGetSchemaListRefusesAKindArgument pins the refusal, which is the direction
// the arity check moves in on this command: get-schema otherwise requires the
// kind positionally, so the conditional check relaxes that requirement for the
// query and must not also accept both at once.
//
// Naming a kind alongside the query is incoherent — the two ask for different
// output — and answering one of them silently would leave the caller unable to
// tell which it got.
func TestGetSchemaListRefusesAKindArgument(t *testing.T) {
	t.Parallel()

	_, _, err := runGetSchemaList(t, "--list", "component")
	if err == nil {
		t.Fatal("get-schema --list with a positional kind should be refused; got nil error")
	}
}
