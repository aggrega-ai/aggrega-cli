// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package hometest redirects the per-user homes a test binary would otherwise
// resolve, for the lifetime of that binary, so a suite never reads from or
// writes to the developer's own configuration or registry credentials.
//
// Two homes are resolved beneath every workspace tree, and a suite that leaves
// either of them ambient produces a verdict that depends on the machine it ran
// on rather than on the code under test.
//
// The configuration home. The workspaceconfig package resolves a global
// configuration layer beneath every workspace tree: it reads $AGGREGA_CONFIG
// when that is set, otherwise $XDG_CONFIG_HOME/aggrega/config.yaml, falling
// back to $HOME/.config/aggrega/config.yaml. That layer loads unconditionally,
// so a test that resolves a workspace would otherwise merge the developer's
// global configuration beneath its temporary fixtures. Pointing
// $XDG_CONFIG_HOME at an empty throwaway directory and clearing
// $AGGREGA_CONFIG makes the default global file absent (so reads stay clean)
// and confines any default-scoped write to the throwaway (so writes stay
// contained).
//
// The credential home. Every remote registry target authenticates through the
// container-registry credential file: $DOCKER_CONFIG/config.json when
// $DOCKER_CONFIG is set, otherwise $HOME/.docker/config.json. Left ambient, a
// suite reads whatever the developer is logged in to — and follows whatever
// credential helper that file names, executing a binary the suite never chose.
// Pointing $DOCKER_CONFIG at an empty throwaway directory makes the store empty
// and helper-free.
//
// $HOME itself is deliberately left untouched, and that is a decision rather
// than an omission. Each home above is reached through its own variable, which
// wins over the $HOME fallback in the resolution rule this mirrors, so
// redirecting the variables covers every read the redirect exists to cover.
// $HOME is also where the Go toolchain keeps its build cache, and a suite that
// builds a binary in a subprocess inherits the redirected value — so moving
// $HOME would silently turn a warm toolchain cache cold on every run, buying no
// isolation for a large and recurring cost.
package hometest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// envConfigHome is the variable carrying the redirected configuration home.
	envConfigHome = "XDG_CONFIG_HOME"

	// envConfigOverride is the variable that, when set, overrides the
	// configuration home outright with a single file path. The redirect clears
	// it so the redirected home is the only global layer in play.
	envConfigOverride = "AGGREGA_CONFIG"

	// envCredentialHome is the variable carrying the redirected
	// container-registry credential home.
	envCredentialHome = "DOCKER_CONFIG"
)

// RunHermetic isolates the per-user homes for a whole test binary and runs its
// suite. It creates one throwaway directory, points the configuration home and
// the credential home at separate subdirectories of it, clears the
// configuration-override variable, runs m.Run, removes the throwaway, and
// returns the suite's exit code so a caller can propagate it. Drive it from
// TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(hometest.RunHermetic(m)) }
//
// The redirect governs the ambient default only. A test that injects its own
// environment lookup, or that sets one of these variables for its own duration,
// is unaffected.
//
// It cannot cover a read that happens before it runs. Package-level variable
// initializers execute before TestMain, so a package variable whose initializer
// resolves a workspace or a credential store reads the developer's real
// environment no matter what this does — such an initializer has to become lazy
// instead.
// throwawayRoot is the directory RunHermetic created to hold this binary's
// redirected homes. It is recorded so the assertion below can check containment
// against the redirect's own answer rather than re-deriving where a throwaway
// ought to live — a second derivation could agree with this one today and
// disagree after either side moves. Empty when the redirect has not run.
var throwawayRoot string

func RunHermetic(m *testing.M) int {
	dir, err := os.MkdirTemp("", "aggrega-home-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hometest: create throwaway home: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	throwawayRoot = dir

	for _, redirect := range []struct {
		name string
		path string
	}{
		{envConfigHome, filepath.Join(dir, "config")},
		{envCredentialHome, filepath.Join(dir, "credentials")},
	} {
		if err := os.MkdirAll(redirect.path, 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "hometest: create %s: %v\n", redirect.name, err)
			return 1
		}
		if err := os.Setenv(redirect.name, redirect.path); err != nil {
			fmt.Fprintf(os.Stderr, "hometest: redirect %s: %v\n", redirect.name, err)
			return 1
		}
	}
	if err := os.Unsetenv(envConfigOverride); err != nil {
		fmt.Fprintf(os.Stderr, "hometest: clear %s: %v\n", envConfigOverride, err)
		return 1
	}

	return m.Run()
}

// Isolate points the same homes at fresh directories for the duration of one
// test, and restores the surrounding values when it ends. It is the per-test
// form of the redirect above, for a case that constructs a whole command tree or
// writes configuration and wants its own scratch rather than the one the binary
// shares.
//
// It additionally points $HOME at the scratch directory, which RunHermetic
// deliberately does not do. The difference is scope: RunHermetic governs a whole
// binary including any subprocess it spawns, where moving $HOME would relocate
// the Go toolchain's caches; Isolate governs one in-process case, where nothing
// is spawned and covering the $HOME fallback costs nothing.
func Isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(envConfigHome, filepath.Join(home, "config"))
	t.Setenv(envCredentialHome, filepath.Join(home, "credentials"))
	t.Setenv(envConfigOverride, "")
}

// ConfigHome returns the redirected configuration home, so a test can plant a
// value where the production resolver will look for it. It is the accessor
// rather than a second spelling of the variable name, so a test that plants a
// fixture and the redirect that decides where fixtures live cannot disagree.
// Empty when the redirect has not run.
func ConfigHome() string { return os.Getenv(envConfigHome) }

// CredentialHome returns the redirected container-registry credential home, for
// the same reason and with the same guarantee as ConfigHome. Empty when the
// redirect has not run.
func CredentialHome() string { return os.Getenv(envCredentialHome) }

// AssertRedirected is the regression floor for the binary-wide redirect: it
// asserts that this test binary is running under it. Each redirected home must
// sit inside the throwaway the redirect created, and must not be the developer's
// own home or a directory inside it; the configuration-override variable must be
// empty. The second check is not implied by the first — it is what fails loudly
// if the throwaway is ever rooted somewhere it should not be.
//
// It is an environment-level check, so it proves the redirect ran — not that
// the code under test reads the redirected location. A suite that needs the
// stronger claim plants a distinguishable value in the redirected home and
// asserts a production read returns it; a redirect that is wired but ineffective
// passes this and fails that.
func AssertRedirected(t *testing.T) {
	t.Helper()

	if throwawayRoot == "" {
		t.Fatal("no throwaway home was created; the redirect did not run — drive it from TestMain")
	}
	realHome, homeErr := os.UserHomeDir()

	for _, redirect := range []struct {
		name string
		path string
	}{
		{envConfigHome, ConfigHome()},
		{envCredentialHome, CredentialHome()},
	} {
		if redirect.path == "" {
			t.Errorf("$%s is empty; the redirect did not run", redirect.name)
			continue
		}
		if !within(redirect.path, throwawayRoot) {
			t.Errorf("$%s = %q is not inside the throwaway home %q; the redirect is not isolating", redirect.name, redirect.path, throwawayRoot)
		}
		if homeErr == nil && realHome != "" && within(redirect.path, realHome) {
			t.Errorf("$%s = %q points inside the developer's own home %q; the redirect is not isolating", redirect.name, redirect.path, realHome)
		}
	}

	if v := os.Getenv(envConfigOverride); v != "" {
		t.Errorf("$%s = %q; the redirect must clear it", envConfigOverride, v)
	}
}

// within reports whether path is dir itself or sits beneath it. The separator is
// appended before the prefix test so a sibling whose name merely starts with
// dir's does not read as a descendant.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(os.PathSeparator))
}
