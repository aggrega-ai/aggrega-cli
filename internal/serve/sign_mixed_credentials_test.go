// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestTheDaemonRefusesAMixedCredentialBatch drives aggrega/signArtifacts and a
// batch aggrega/deprecateArtifact over the wire, against a workspace whose
// configuration names a held key for one registry and none for another, and
// holds each answer to the refusal the command line gives: the condition's own
// sentence, both registries named, its short code at the head of the message — this
// answer has no bracketed head of its own — and both of its codes in the data a
// client branches on.
//
// A request here names no key, so the configured key is the only one in play, which
// is exactly how one batch comes to span two credentials on this surface.
func TestTheDaemonRefusesAMixedCredentialBatch(t *testing.T) {
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
	srv, root := publishFixture(t, defaultRegistryWorkspace+
		"registries:\n  - host: "+keyedHost+"\n    signing:\n      key_path: "+keyPath+"\n")

	refs := []string{keyedHost + "/component/core/a:1.0.0", plainHost + "/component/core/b:1.0.0"}
	var batch strings.Builder
	for _, ref := range refs {
		batch.WriteString("- reference: " + ref + "\n  severity: warning\n  reason: superseded\n")
	}
	if err := os.WriteFile(filepath.Join(root, "batch.yaml"), []byte(batch.String()), 0o644); err != nil {
		t.Fatalf("write the batch file: %v", err)
	}

	wantCode, _ := cerrors.CodeOf(signing.ErrMixedSigningCredentials())
	wantShort, _ := cerrors.ShortCodeOf(signing.ErrMixedSigningCredentials())
	sentence, _ := cerrors.MessageOf(signing.ErrMixedSigningCredentials())
	if wantCode == "" || wantShort == "" || sentence == "" {
		t.Fatal("the condition carries no code, short code or sentence, so the assertions below would compare against nothing")
	}

	for _, tc := range []struct {
		name   string
		method string
		params map[string]any
	}{
		{"a signing batch", "aggrega/signArtifacts", map[string]any{"references": refs}},
		{"a deprecation batch", "aggrega/deprecateArtifact", map[string]any{"from": "batch.yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, tc.method, tc.params)
			if resp.Error == nil {
				t.Fatalf("a batch needing two credentials must be refused, not answered; got result %s", resp.Result)
			}
			msg := resp.Error.Message
			if !strings.HasPrefix(msg, wantShort+": ") {
				t.Errorf("the message does not open with the short code %s: %s", wantShort, msg)
			}
			if !strings.Contains(msg, sentence) {
				t.Errorf("the message does not carry the condition's sentence: %s", msg)
			}
			for _, host := range []string{keyedHost, plainHost} {
				if !strings.Contains(msg, host) {
					t.Errorf("the message does not name %s: %s", host, msg)
				}
			}
			if resp.Error.Data == nil || resp.Error.Data.Code != wantCode || resp.Error.Data.ShortCode != wantShort {
				t.Errorf("the data names the condition %+v, want code %q and short code %q", resp.Error.Data, wantCode, wantShort)
			}
		})
	}
}
