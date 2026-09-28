// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltFromUncommittedChanges pins the predicate over each form a
// build-provenance version takes: only a version carrying the mark a build from
// an edited tree is stamped with reads as one. A clean development version and a
// prerelease are not marked, however much they resemble one.
func TestBuiltFromUncommittedChanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"1.0.0", false},
		{"1.0.0-dirty", true},
		{"1.0.0-3-g1a2b3c4", false},
		{"1.0.0-3-g1a2b3c4-dirty", true},
		{"0.7.3-19-gc7e21762-dirty", true},
		{"1a2b3c4", false},
		{"1a2b3c4-dirty", true},
		{"1.0.0-rc.1", false},
		{"1.0.0-dirty.1", false},
		{"dev", false},
		{"", false},
	} {
		if got := BuiltFromUncommittedChanges(tc.version); got != tc.want {
			t.Errorf("BuiltFromUncommittedChanges(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

// TestBuiltFromUncommittedChangesHoldsToTheVersionRecipe holds the predicate to
// the recipe that stamps the versions it reads, rather than to a copy of that
// recipe's form written here.
//
// The recipe is the module's shared build-version script, read from this
// module's own tree and run by bash over a scratch git repository in each state
// it distinguishes — before any tag, at a tag, past a tag — once clean and once
// with a tracked file edited. For every state the test asserts both the exact
// version the recipe stamps and that the predicate agrees with whether the tree
// was edited. So a recipe that stopped marking an edited tree, or marked it
// differently, fails here rather than leaving the predicate reading a form
// nothing produces; and a predicate that drifted from the recipe fails the same
// way.
//
// The script's bytes are read by this process and handed to bash as its program,
// so the file the assertion depends on is one this test reads itself. git and
// bash are required, not skipped for: the recipe is a git invocation, and a skip
// here would report a hold between the two that was never checked.
func TestBuiltFromUncommittedChangesHoldsToTheVersionRecipe(t *testing.T) {
	t.Parallel()
	recipe, err := os.ReadFile(filepath.Join("..", "..", "scripts", "lib", "build-version.sh"))
	if err != nil {
		t.Fatalf("read the version recipe: %v", err)
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required to drive the version recipe: %v", err)
	}
	bashBin, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is required to drive the version recipe: %v", err)
	}

	repo := t.TempDir()
	// The repository is isolated from every configuration the machine carries, so
	// a signing hook or a default branch setting cannot change what it records.
	env := append(os.Environ(),
		"HOME="+t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=aggrega-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=aggrega-test", "GIT_COMMITTER_EMAIL=test@example.invalid",
	)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(gitBin, append([]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	tracked := filepath.Join(repo, "tracked.txt")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(tracked, []byte(content), 0o644); err != nil {
			t.Fatalf("write tracked file: %v", err)
		}
	}
	stamp := func() string {
		t.Helper()
		cmd := exec.Command(bashBin, "-c", string(recipe)+"\nprintf '%s' \"$_ver\"\n")
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run the version recipe: %v\n%s", err, out)
		}
		return string(out)
	}
	check := func(state, want string, edited bool) {
		t.Helper()
		got := stamp()
		if got != want {
			t.Fatalf("%s: the recipe stamped %q, want %q", state, got, want)
		}
		if BuiltFromUncommittedChanges(got) != edited {
			t.Errorf("%s: BuiltFromUncommittedChanges(%q) = %v, but the tree the recipe described was edited=%v",
				state, got, !edited, edited)
		}
	}

	git("init", "-q")
	write("first\n")
	git("add", "tracked.txt")
	git("commit", "-q", "-m", "first")
	first := git("rev-parse", "--short", "HEAD")
	check("before any tag, clean", first, false)
	write("edited\n")
	check("before any tag, edited", first+"-dirty", true)
	git("checkout", "--", "tracked.txt")

	git("tag", "v1.0.0")
	check("at a tag, clean", "1.0.0", false)
	write("edited\n")
	check("at a tag, edited", "1.0.0-dirty", true)
	git("checkout", "--", "tracked.txt")

	write("second\n")
	git("commit", "-q", "-am", "second")
	second := git("rev-parse", "--short", "HEAD")
	check("past a tag, clean", "1.0.0-1-g"+second, false)
	write("edited\n")
	check("past a tag, edited", "1.0.0-1-g"+second+"-dirty", true)
}
