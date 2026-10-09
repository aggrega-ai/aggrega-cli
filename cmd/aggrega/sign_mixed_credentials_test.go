// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheCommandLineRefusesAMixedCredentialBatch drives `aggrega sign` and
// `aggrega deprecate --from` through their command-line adapters over references
// whose registries need different signing credentials — a held key the
// configuration names for one registry, and none for the other — and holds each
// to what a reader of the terminal is given: the condition, matched through its
// accessor; its short code, which the bracketed head of the printed diagnostic
// carries; the command named; and both registries named.
//
// Both adapters are driven with nobody watching and an interactive rung that fails
// the test if reached: the refusal comes before any identity is asked for, so a
// prompt here would be the defect. No target is injected, and none is needed — the
// refusal comes before any registry is read.
func TestTheCommandLineRefusesAMixedCredentialBatch(t *testing.T) {
	t.Parallel()
	const keyedHost, plainHost = "keyed.example.test", "plain.example.test"

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "held-key.pem")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatalf("write the held key: %v", err)
	}
	root := t.TempDir()
	configYAML := "workspace: {}\nregistries:\n  - host: " + keyedHost + "\n    signing:\n      key_path: " + keyPath + "\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	refs := []string{keyedHost + "/component/core/a:1.0.0", plainHost + "/component/core/b:1.0.0"}
	batchPath := filepath.Join(root, "batch.yaml")
	var batch strings.Builder
	for _, ref := range refs {
		batch.WriteString("- reference: " + ref + "\n  severity: warning\n  reason: superseded\n")
	}
	if err := os.WriteFile(batchPath, []byte(batch.String()), 0o644); err != nil {
		t.Fatalf("write the batch file: %v", err)
	}

	wantShort, ok := cerrors.ShortCodeOf(signing.ErrMixedSigningCredentials())
	if !ok || wantShort == "" {
		t.Fatal("the condition carries no short code, so the head assertion below would compare against nothing")
	}
	noPrompt := func() (string, error) {
		t.Error("an identity was asked for; the refusal must come before it")
		return "", nil
	}
	noEnv := func(string) string { return "" }
	notWatching := func() bool { return false }

	for _, row := range []struct {
		command string
		run     func(rw *workspaceconfig.ResolvedWorkspace) error
	}{
		{"aggrega sign", func(rw *workspaceconfig.ResolvedWorkspace) error {
			return signRun(t.Context(), signConfig{
				References: refs, Workspace: rw, EnvLookup: noEnv, IsTTY: notWatching, TokenGetter: noPrompt,
			})
		}},
		{"aggrega deprecate", func(rw *workspaceconfig.ResolvedWorkspace) error {
			return deprecateRun(t.Context(), deprecateConfig{
				From: batchPath, Workspace: rw, EnvLookup: noEnv, IsTTY: notWatching, TokenGetter: noPrompt,
			})
		}},
	} {
		t.Run(row.command, func(t *testing.T) {
			err := row.run(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}))
			if !errors.Is(err, signing.ErrMixedSigningCredentials()) {
				t.Fatalf("want the batch refused for needing different credentials, got %v", err)
			}
			if short, ok := cerrors.ShortCodeOf(err); !ok || short != wantShort {
				t.Errorf("the refusal carries short code %q (present %t), want %q", short, ok, wantShort)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, row.command+": ") {
				t.Errorf("the refusal does not name the command it refused: %s", msg)
			}
			for _, host := range []string{keyedHost, plainHost} {
				if !strings.Contains(msg, host) {
					t.Errorf("the refusal does not name %s: %s", host, msg)
				}
			}
			if strings.Contains(msg, keyPath) {
				t.Errorf("the refusal names where the key sits on this machine: %s", msg)
			}
		})
	}
}
