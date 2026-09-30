// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// throwawayKeyPEMs generates a fresh P-256 key pair for one test and returns its
// private and public halves as the PEM blocks key files hold. Nothing is read from
// or committed to a fixture.
func throwawayKeyPEMs(t *testing.T) (privatePEM, publicPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate a throwaway key: %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encode the private half: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatalf("encode the public half: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
}

// linesOf returns the non-blank lines of s, trimmed: the fragments a message
// quoting s would reproduce.
func linesOf(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// withheldPatternSentence is what a refusal of the pinned public key says in
// place of the value: the pattern the key had to match, with the placeholder
// where the value would be.
func withheldPatternSentence() string {
	return `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "-----BEGIN [A-Z ]*PUBLIC KEY-----"`
}

// TestRegistryAddOnTheDaemonRefusesAKeyFileThatIsNotAPublicKeyWithoutQuotingIt
// drives a registration whose key file is not a public key through the daemon's
// own protocol, and holds it to the registration every surface shares.
//
// Each row registers one registry twice, each into a workspace of its own: once as
// an aggrega/registryAdd request, and once through the shared registration directly
// with the options the command line hands it. A refusal must be the configuration
// gate's (ErrConfigInvalid), and the daemon must answer with the shared sentence
// behind the condition's short code — the form it answers every registration
// refusal naming a condition. The refusal names the file it refused, so each
// sentence names its own workspace's configuration file; that path is the one place
// the two differ, and every other byte must match. Neither the sentence nor the
// answer may quote any line of the key file, while both name the key and the
// pattern a public key must match. Neither registration writes anything. The
// public-key row is the positive control: both register, and the two written
// configurations are byte-identical and hold the key as read.
//
// Planted defect: build the configuration gate's validator without the option that
// withholds sensitive values, and both refusal rows see the file's lines in the
// daemon's answer.
func TestRegistryAddOnTheDaemonRefusesAKeyFileThatIsNotAPublicKeyWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	privatePEM, publicPEM := throwawayKeyPEMs(t)
	const notAKeyText = "registry token a1b2c3d4e5f6, keep this safe\n"
	const seed = "workspace: {}\n"

	for _, tc := range []struct {
		name        string
		content     string
		wantRefused bool
	}{
		{name: "a private key picked by mistake", content: privatePEM, wantRefused: true},
		{name: "a file that holds no key at all", content: notAKeyText, wantRefused: true},
		{name: "a public key registers on both surfaces alike", content: publicPEM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			keyPath := filepath.Join(t.TempDir(), "key.pem")
			if err := os.WriteFile(keyPath, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("write the key file: %v", err)
			}
			const host = "registry.example.test"

			srv, _, wsPath := registryWriteFixture(t, seed)
			resp := callDaemonMethod(t, srv, "aggrega/registryAdd", map[string]any{
				"host": host, "keyPath": keyPath, "workspace": true,
			})

			ref, _, refPath := registryWriteFixture(t, seed)
			_, refErr := registryops.Add(t.Context(), ref.resolvedWorkspace, registryops.AddOptions{
				Host:       host,
				KeyPath:    keyPath,
				FetchTrust: true,
				Workspace:  true,
			})

			daemonBytes, err := os.ReadFile(wsPath)
			if err != nil {
				t.Fatalf("read the daemon's configuration: %v", err)
			}
			sharedBytes, err := os.ReadFile(refPath)
			if err != nil {
				t.Fatalf("read the shared registration's configuration: %v", err)
			}

			if !tc.wantRefused {
				if refErr != nil {
					t.Fatalf("the shared registration: %v", refErr)
				}
				var res registryAddResult
				decodeDaemonResult(t, resp, &res)
				if !bytes.Equal(daemonBytes, sharedBytes) {
					t.Errorf("the daemon wrote a different configuration from the shared registration\n--- daemon ---\n%s\n--- shared ---\n%s",
						daemonBytes, sharedBytes)
				}
				for _, line := range linesOf(tc.content) {
					if !strings.Contains(string(daemonBytes), line) {
						t.Errorf("the written entry does not hold the key as read:\n%s", daemonBytes)
						break
					}
				}
				return
			}

			if !errors.Is(refErr, workspaceconfig.ErrConfigInvalid()) {
				t.Fatalf("the shared registration returned %v; want the configuration gate's refusal", refErr)
			}
			short, ok := cerrors.ShortCodeOf(refErr)
			want, wantOK := cerrors.ShortCodeOf(workspaceconfig.ErrConfigInvalid())
			if !ok || !wantOK || short != want {
				t.Errorf("the refusal carries short code %q (present %t); want the condition's own %q", short, ok, want)
			}
			if resp.Error == nil {
				t.Fatalf("the daemon answered with a result where the shared layer refused: %s", resp.Result)
			}
			// Each sentence names its own workspace's file, so each path is replaced by
			// the same placeholder before the two are compared; the loop below holds
			// each sentence to naming its own file, so the replacement cannot be vacuous.
			const configFile = "<the workspace configuration file>"
			answer := strings.ReplaceAll(resp.Error.Message, wsPath, configFile)
			shared := short + ": " + strings.ReplaceAll(refErr.Error(), refPath, configFile)
			if answer != shared {
				t.Errorf("the daemon's refusal is not the shared sentence behind its short code\n got: %q\nwant: %q", answer, shared)
			}
			if string(daemonBytes) != seed || string(sharedBytes) != seed {
				t.Errorf("a refused registration changed the configuration:\n--- daemon ---\n%s\n--- shared ---\n%s", daemonBytes, sharedBytes)
			}
			for _, s := range []struct{ surface, text, configPath string }{
				{"the shared refusal", refErr.Error(), refPath},
				{"the daemon's answer", resp.Error.Message, wsPath},
			} {
				if !strings.Contains(s.text, s.configPath) {
					t.Errorf("%s does not name the configuration file it refused, %s: %s", s.surface, s.configPath, s.text)
				}
				if !strings.Contains(s.text, ": registries[0].public_key: ") || !strings.Contains(s.text, withheldPatternSentence()) {
					t.Errorf("%s does not name the key and the pattern it expects: %s", s.surface, s.text)
				}
				for i, line := range linesOf(tc.content) {
					if strings.Contains(s.text, line) {
						t.Errorf("%s quotes line %d of the key file", s.surface, i+1)
					}
				}
			}
		})
	}
}

// TestTheEditorsDiagnosticOfAConfigurationBufferWithholdsTheKeyItHolds validates an
// editor buffer of the workspace configuration that holds, in a registry's pinned
// public key, something other than a public key — the buffer an editor sends while
// a person is still typing, before anything is saved.
//
// The diagnostic is the document gate's, drawn on the key's line, and it states the
// pattern a public key must match with the placeholder where the value would be;
// no line of the value appears in any diagnostic. The public-key row is the
// positive control: a buffer holding a real public key draws no diagnostic at all.
//
// Planted defect: build the document gate's validator without the option that
// withholds sensitive values, and the refusal rows see the value's lines in the
// diagnostic message.
func TestTheEditorsDiagnosticOfAConfigurationBufferWithholdsTheKeyItHolds(t *testing.T) {
	t.Parallel()
	privatePEM, publicPEM := throwawayKeyPEMs(t)
	srv := newTestServer(t, t.TempDir(), nil, nil)
	for _, tc := range []struct {
		name      string
		value     string
		wantClean bool
	}{
		{name: "a private key picked by mistake", value: privatePEM},
		{name: "text that is no key at all", value: "registry token a1b2c3d4e5f6, keep this safe\n"},
		{name: "a public key", value: publicPEM, wantClean: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var b strings.Builder
			b.WriteString("workspace: {}\nregistries:\n  - host: registry.example.com\n    public_key: |\n")
			for _, line := range strings.Split(strings.TrimSuffix(tc.value, "\n"), "\n") {
				b.WriteString("      ")
				b.WriteString(line)
				b.WriteString("\n")
			}
			content := b.String()
			diags, has := srv.envelopeDiagnosticsForBuffer(workspaceconfig.ConfigFilename, "", []byte(content), parseDoc(t, content))
			if tc.wantClean {
				if has || len(diags) != 0 {
					t.Fatalf("a buffer holding a public key drew diagnostics: %+v", diags)
				}
				return
			}
			if !has || len(diags) == 0 {
				t.Fatalf("expected a diagnostic for the buffer; has=%v diags=%d", has, len(diags))
			}
			found := false
			for _, d := range diags {
				if d.Line == 4 && strings.Contains(d.Message, withheldPatternSentence()) {
					found = true
				}
				for i, line := range linesOf(tc.value) {
					if strings.Contains(d.Message, line) {
						t.Errorf("a diagnostic quotes line %d of the value: line %d", i+1, d.Line)
					}
				}
			}
			if !found {
				t.Errorf("no diagnostic on the key's line states the pattern it expects; got %+v", diags)
			}
		})
	}
}
