// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestTheSigningToolsRefuseAMixedCredentialBatch drives sign-artifacts and a
// batch deprecate-artifact through the tool dispatch over a workspace whose
// configuration names a held key for one registry and none for another.
//
// Over the process's own streams the configured key is the caller's to spend, so
// the batch spans two credentials and is refused with the sentence the command line
// gives, naming both registries. Over a listener the configured key is withheld
// from the caller, so the same workspace is one keyless credential to that caller
// and is NOT refused for mixing — it is refused for want of an identity, which
// nothing in the call sent. That row is the control: a refusal that fired on the
// configuration alone, whatever the caller may spend, would fail it.
func TestTheSigningToolsRefuseAMixedCredentialBatch(t *testing.T) {
	t.Setenv(signing.EnvIdentityToken, "")
	t.Setenv(signing.EnvSigstoreIDToken, "")
	if signing.GitHubActionsAmbientAvailable(os.Getenv) {
		t.Skip("the ambient OIDC endpoint is present in this environment, so an absent identity cannot be arranged here")
	}
	const keyedHost, plainHost = "keyed.example.test", "plain.example.test"
	keyPath := heldKeyForAWorkspace(t)
	refs := []string{keyedHost + "/component/core/a:1.0.0", plainHost + "/component/core/b:1.0.0"}
	sentence, _ := cerrors.MessageOf(signing.ErrMixedSigningCredentials())
	if sentence == "" {
		t.Fatal("the condition carries no sentence, so the assertions below would compare against nothing")
	}

	session := func(t *testing.T, transport transportMode) *server {
		t.Helper()
		s := keySessionOn(t, transport, defaultRegistryWorkspace+
			"registries:\n  - host: "+keyedHost+"\n    signing:\n      key_path: "+keyPath+"\n")
		var batch strings.Builder
		for _, ref := range refs {
			batch.WriteString("- reference: " + ref + "\n  severity: warning\n  reason: superseded\n")
		}
		if err := os.WriteFile(filepath.Join(s.startDir, "batch.yaml"), []byte(batch.String()), 0o644); err != nil {
			t.Fatalf("write the batch file: %v", err)
		}
		return s
	}

	// run drives one tool through its own argument projection and the operation it
	// runs, which is the whole path a call takes below the transport's framing.
	type tool struct {
		name string
		run  func(t *testing.T, s *server) error
	}
	tools := []tool{
		{signArtifactsToolName, func(t *testing.T, s *server) error {
			cfg, refusal := s.signArtifactsConfig(signArtifactsArgs{References: refs})
			if refusal != "" {
				t.Fatalf("signArtifactsConfig refused legal arguments: %s", refusal)
			}
			_, err := runSignArtifacts(context.Background(), cfg)
			return err
		}},
		{deprecateArtifactToolName, func(t *testing.T, s *server) error {
			cfg, refusal := s.deprecateArtifactConfig(deprecateArtifactArgs{From: "batch.yaml"})
			if refusal != "" {
				t.Fatalf("deprecateArtifactConfig refused legal arguments: %s", refusal)
			}
			_, err := runDeprecateArtifact(context.Background(), cfg)
			return err
		}},
	}

	for _, tl := range tools {
		t.Run(tl.name, func(t *testing.T) {
			t.Run("over the process's own streams the batch is refused for mixing", func(t *testing.T) {
				s := session(t, transportStdio)
				err := tl.run(t, s)
				if !errors.Is(err, signing.ErrMixedSigningCredentials()) {
					t.Fatalf("want the batch refused for needing different credentials, got %v", err)
				}
				text := explainToolErrorAt("The signing did not happen.", err, docServerReference)
				if !strings.Contains(text, sentence) {
					t.Errorf("the tool's answer does not carry the condition's sentence: %s", text)
				}
				for _, host := range []string{keyedHost, plainHost} {
					if !strings.Contains(text, host) {
						t.Errorf("the tool's answer does not name %s: %s", host, text)
					}
				}
			})

			t.Run("over a listener the withheld key leaves one credential, so it is not", func(t *testing.T) {
				s := session(t, transportHTTP)
				err := tl.run(t, s)
				if err == nil {
					t.Fatal("a call carrying no identity over a listener must be refused")
				}
				if errors.Is(err, signing.ErrMixedSigningCredentials()) {
					t.Fatalf("a caller that may not spend the configured key was refused for mixing it: %v", err)
				}
				if !strings.Contains(err.Error(), "identity token") {
					t.Errorf("want the refusal for want of an identity, got %v", err)
				}
			})
		})
	}
}
