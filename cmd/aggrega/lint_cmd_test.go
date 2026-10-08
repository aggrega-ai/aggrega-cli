// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestLintReleasePrePublishMutuallyExclusive asserts that combining --release
// with --pre-publish halts with a clear different-scope OPS_ERROR — rather than
// silently taking the release-closure branch and ignoring --pre-publish — and
// that neither scope-flag on its own trips that guard (the three symmetric lint
// scopes stay intact).
func TestLintReleasePrePublishMutuallyExclusive(t *testing.T) {
	// The sentence alone, without the head. The category is composed by the
	// formatter from the value the refusal passes to its constructor, so an
	// assertion that also pinned the head would be pinning the renderer rather
	// than this guard.
	const guardMsg = "--pre-publish is mutually exclusive with --release"

	runLint := func(t *testing.T, args ...string) error {
		t.Helper()
		cmd := newLintCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetContext(withWorkspaceResolver(context.Background(), t.TempDir(), nil))
		cmd.SetArgs(args)
		return cmd.Execute()
	}

	t.Run("release plus pre-publish halts", func(t *testing.T) {
		err := runLint(t, "--release", filepath.Join(t.TempDir(), "release.yaml"), "--pre-publish")
		if err == nil {
			t.Fatal("--release with --pre-publish must halt")
		}
		if !strings.Contains(err.Error(), guardMsg) {
			t.Fatalf("expected the different-scope OPS_ERROR; got: %v", err)
		}
	})

	t.Run("release alone does not trip the guard", func(t *testing.T) {
		// --release alone is a valid scope. It may fail downstream (the Release
		// file does not exist), but it must never surface the pre-publish conflict.
		err := runLint(t, "--release", filepath.Join(t.TempDir(), "release.yaml"))
		if err != nil && strings.Contains(err.Error(), guardMsg) {
			t.Fatalf("--release alone must not trip the pre-publish guard: %v", err)
		}
	})

	t.Run("entity plus pre-publish does not trip the guard", func(t *testing.T) {
		// The single-entity + --pre-publish shape is exactly what --pre-publish is
		// for. It may fail downstream (the entity file does not exist), but it must
		// never surface the pre-publish/release conflict.
		err := runLint(t, filepath.Join(t.TempDir(), "component.yaml"), "--pre-publish")
		if err != nil && strings.Contains(err.Error(), guardMsg) {
			t.Fatalf("entity + --pre-publish must not trip the pre-publish guard: %v", err)
		}
	})
}

// TestMakeResolverFromCobraTriState pins the resolver's tri-state precedence:
// an explicit --no-X forces false, an explicit --X forces true, and with neither
// changed the workspace value passes through. This is the cobra-Changed() half of
// the policy-override surface the effective-policy JSON is built from.
func TestMakeResolverFromCobraTriState(t *testing.T) {
	const flag = "allow-dynamic-refs"

	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{}
		cmd.Flags().Bool(flag, false, "")
		cmd.Flags().Bool("no-"+flag, false, "")
		return cmd
	}

	t.Run("no override passes the workspace value through", func(t *testing.T) {
		resolve := makeResolverFromCobra(newCmd())
		if resolve(flag, true) != true {
			t.Errorf("expected workspace true to pass through")
		}
		if resolve(flag, false) != false {
			t.Errorf("expected workspace false to pass through")
		}
	})

	t.Run("--flag forces true despite workspace false", func(t *testing.T) {
		cmd := newCmd()
		_ = cmd.Flags().Set(flag, "true")
		if !makeResolverFromCobra(cmd)(flag, false) {
			t.Errorf("--%s should force true", flag)
		}
	})

	t.Run("--no-flag forces false despite workspace true", func(t *testing.T) {
		cmd := newCmd()
		_ = cmd.Flags().Set("no-"+flag, "true")
		if makeResolverFromCobra(cmd)(flag, true) {
			t.Errorf("--no-%s should force false", flag)
		}
	})

	t.Run("--no-flag beats --flag", func(t *testing.T) {
		cmd := newCmd()
		_ = cmd.Flags().Set(flag, "true")
		_ = cmd.Flags().Set("no-"+flag, "true")
		if makeResolverFromCobra(cmd)(flag, false) {
			t.Errorf("--no-%s should take precedence over --%s", flag, flag)
		}
	})
}

// TestLintCredentialMismatchFlagPairReachesTheResolver parses the real lint
// command's flags, so the pair is proven registered under the base name the
// policy assembly asks the resolver about — a pair registered under any other
// name would parse and still leave the suppression at its workspace value.
func TestLintCredentialMismatchFlagPairReachesTheResolver(t *testing.T) {
	const base = "allow-connection-credential-mismatches"
	resolved := func(t *testing.T, ws bool, args ...string) bool {
		t.Helper()
		cmd := newLintCmd()
		if err := cmd.Flags().Parse(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return makeResolverFromCobra(cmd)(base, ws)
	}
	if resolved(t, false) || !resolved(t, true) {
		t.Error("with neither flag given the workspace value passes through")
	}
	if !resolved(t, false, "--"+base) {
		t.Errorf("--%s must force the suppression on over the workspace", base)
	}
	if resolved(t, true, "--no-"+base) {
		t.Errorf("--no-%s must force the suppression off over the workspace", base)
	}
}
