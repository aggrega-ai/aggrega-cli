// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// runPushListKinds executes push --list-kinds on a freshly built command with
// separate stdout and stderr sinks, and returns both.
//
// It deliberately does NOT install a workspace resolver on the command context.
// Production wiring attaches one in the root command's PersistentPreRunE, so a
// command run without it is the strongest available statement that this path
// never reaches workspace resolution: were the flag handled after that
// resolution, the run would fail with the "resolver not attached" programming
// error instead of emitting the set. That subsumes "works outside a workspace"
// without a directory change, which would mutate process-wide state and race
// every parallel test in this package.
func runPushListKinds(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	c := newPushCmd()
	c.SetOut(&out)
	c.SetErr(&errBuf)
	c.SetArgs(args)
	err = c.Execute()
	return out.String(), errBuf.String(), err
}

// TestPushListKindsEmitsTheAuthoritativeKindSet pins the emitted bytes against
// the kind enumeration itself rather than against a hand-written list of kinds.
//
// The derivation is the point. A test that restated the set would become another
// home of the publishable-kind rule — the very duplication the flag exists to
// remove — and it would pass while the command and the transport disagreed,
// because both would be compared against the test's own third copy.
//
// Exact-byte equality against the joined enumeration proves every property a
// parser depends on at once: one kind per line, in the enumeration's order,
// terminated by a newline, with no header, count, or decoration around it.
func TestPushListKindsEmitsTheAuthoritativeKindSet(t *testing.T) {
	t.Parallel()

	kinds := oci.MediaTypeKinds()
	// Calibration: an empty enumeration would make the comparison below
	// "" == "" and the test would pass while asserting nothing. A vacuous pass
	// reads exactly like a real one.
	if len(kinds) == 0 {
		t.Fatal("the transport enumerates no kinds, so the comparison below asserts nothing")
	}

	stdout, stderr, err := runPushListKinds(t, "--list-kinds")
	if err != nil {
		t.Fatalf("push --list-kinds: %v (stderr: %s)", err, stderr)
	}

	want := strings.Join(kinds, "\n") + "\n"
	if stdout != want {
		t.Errorf("push --list-kinds stdout = %q; want %q", stdout, want)
	}
}

// TestPushListKindsOutputIsParseable checks the emitted shape by its properties
// rather than by re-deriving the expected bytes.
//
// This overlaps TestPushListKindsEmitsTheAuthoritativeKindSet deliberately, and
// the duplication is kept rather than folded away: that test compares against a
// join of the enumeration, so a change to how the expectation is built could
// move both sides together. These assertions are stated in the terms a consuming
// script actually uses — split the output on newlines and read each line as one
// token — so they stay meaningful if the other test's derivation changes.
func TestPushListKindsOutputIsParseable(t *testing.T) {
	t.Parallel()

	want := oci.MediaTypeKinds()
	if len(want) == 0 {
		t.Fatal("the transport enumerates no kinds, so the assertions below assert nothing")
	}

	stdout, stderr, err := runPushListKinds(t, "--list-kinds")
	if err != nil {
		t.Fatalf("push --list-kinds: %v (stderr: %s)", err, stderr)
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

// TestPushListKindsNeedsNoPositionalArgument pins that the query is callable
// bare. push otherwise takes the entity path positionally, and a caller asking
// what the binary can publish has no entity to name.
func TestPushListKindsNeedsNoPositionalArgument(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runPushListKinds(t, "--list-kinds")
	if err != nil {
		t.Fatalf("push --list-kinds with no positional arg: %v (stderr: %s)", err, stderr)
	}
	if stdout == "" {
		t.Error("push --list-kinds with no positional arg produced no output")
	}
}

// TestPushListKindsRefusesAnEntityPath pins the refusal, which is the direction
// the arity check moves in on this command: push already accepted zero
// positional arguments, so the conditional check tightens rather than relaxes.
//
// Naming an entity alongside the query is incoherent — the run publishes
// nothing — and accepting it silently would read as a push that happened.
func TestPushListKindsRefusesAnEntityPath(t *testing.T) {
	t.Parallel()

	_, _, err := runPushListKinds(t, "--list-kinds", "some/entity.yaml")
	if err == nil {
		t.Fatal("push --list-kinds with a positional entity path should be refused; got nil error")
	}
}

// TestPushListKindsReadsOneHome pins that the command holds no kind list of its
// own. The emitted set must be exactly what the transport enumerates — no
// additions, and no exclusions.
//
// An exclusion is the failure mode worth guarding: it looks like a filter rather
// than a copy, but a filter names the kinds it drops, so it is a second home of
// the rule wearing a different shape. Checking both difference directions is what
// makes that visible; checking only that every emitted kind is enumerated would
// pass an emission that silently dropped one.
func TestPushListKindsReadsOneHome(t *testing.T) {
	t.Parallel()

	enumerated := oci.MediaTypeKinds()
	if len(enumerated) == 0 {
		t.Fatal("the transport enumerates no kinds, so the comparison below asserts nothing")
	}

	stdout, stderr, err := runPushListKinds(t, "--list-kinds")
	if err != nil {
		t.Fatalf("push --list-kinds: %v (stderr: %s)", err, stderr)
	}
	emitted := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")

	inEmitted := make(map[string]bool, len(emitted))
	for _, k := range emitted {
		inEmitted[k] = true
	}
	inEnumerated := make(map[string]bool, len(enumerated))
	for _, k := range enumerated {
		inEnumerated[k] = true
	}
	for _, k := range enumerated {
		if !inEmitted[k] {
			t.Errorf("kind %q is enumerated by the transport but not emitted: the command excludes it", k)
		}
	}
	for _, k := range emitted {
		if !inEnumerated[k] {
			t.Errorf("kind %q is emitted but not enumerated by the transport: the command adds it", k)
		}
	}
}
