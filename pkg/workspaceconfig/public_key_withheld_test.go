// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// throwawayPrivateKeyPEM generates a fresh P-256 private key for one test and
// returns it as the PKCS#8 PEM block a key file holds. It is made here rather
// than read from a fixture so no key material is committed anywhere.
func throwawayPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate a throwaway key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encode the throwaway key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// nonEmptyLines returns the lines of s that carry any text, which are the
// fragments a refusal quoting s would reproduce.
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestLoadRefusesAValueInThePinnedPublicKeyWithoutQuotingIt reads a configuration
// file that already holds, in a registry's pinned public key, something that is not
// a public key — a private key picked by mistake above all — through the loader,
// the one path every layer of the configuration is read through.
//
// Each row asserts the refusal is the configuration gate's (ErrConfigInvalid), that
// it names the file and the key, that it states what the key expects from the
// schema, that it shows the placeholder where the value would be, and that no line
// of the value appears in it. The last is the property the key's schema
// declaration exists for: the sentence a schema library composes for a failed
// constraint quotes the value, and a private key quoted there reaches every
// terminal, editor notification and tool answer that carries the refusal.
//
// The loader is reached two ways: a file read on its own, and the workspace layer
// of a resolution, which is what every command does before it runs.
//
// A planted defect is visible: build the envelope gate's validator without the
// option that withholds sensitive values, and every row reports the value's lines.
func TestLoadRefusesAValueInThePinnedPublicKeyWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	privateKey := throwawayPrivateKeyPEM(t)
	for _, tc := range []struct {
		name string
		// value is what is written under public_key; its lines are what the
		// refusal must not reproduce.
		value string
		// scalar writes the value as a plain scalar rather than beneath a block
		// scalar, which is how a number reaches the key as a number.
		scalar bool
		// expects is what the refusal must say the key expects, read from the
		// schema rather than from the value.
		expects string
	}{
		{
			name:    "a private key picked by mistake",
			value:   privateKey,
			expects: `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "-----BEGIN [A-Z ]*PUBLIC KEY-----"`,
		},
		{
			name:    "text that is no key at all",
			value:   "token-a1b2c3d4e5f6-that-is-not-a-key\n",
			expects: `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "-----BEGIN [A-Z ]*PUBLIC KEY-----"`,
		},
		{
			name:    "a number",
			value:   "1234567890",
			scalar:  true,
			expects: `type: ` + redact.Placeholder() + ` does not satisfy type "string"`,
		},
	} {
		content := "workspace: {}\nregistries:\n  - host: registry.example.com\n    public_key: |\n" +
			indentBlock(tc.value, "      ")
		if tc.scalar {
			content = "workspace: {}\nregistries:\n  - host: registry.example.com\n    public_key: " + tc.value + "\n"
		}
		for _, reach := range []struct {
			name string
			read func(t *testing.T) (string, error)
		}{
			{"read on its own", func(t *testing.T) (string, error) {
				path := writeFixture(t, content)
				_, err := load(path)
				return path, err
			}},
			{"read as the workspace layer of a resolution", func(t *testing.T) (string, error) {
				// The global layer is left out, so nothing on the machine running the
				// test takes part; the layer under test is the workspace's own file.
				root := t.TempDir()
				path := filepath.Join(root, ConfigFilename)
				writeFile(t, path, content)
				_, err := ResolveWithoutGlobal(root, "", fakeEnv(nil))
				return path, err
			}},
		} {
			t.Run(tc.name+", "+reach.name, func(t *testing.T) {
				t.Parallel()
				path, err := reach.read(t)
				if err == nil {
					t.Fatal("expected the configuration to be refused; it loaded")
				}
				if !errors.Is(err, ErrConfigInvalid()) {
					t.Fatalf("the refusal must wrap ErrConfigInvalid; got %v", err)
				}
				msg := err.Error()
				if !strings.Contains(msg, path) {
					t.Errorf("the refusal does not name the file %s: %v", path, err)
				}
				if !strings.Contains(msg, ": registries[0].public_key: ") {
					t.Errorf("the refusal does not name the key: %v", err)
				}
				if !strings.Contains(msg, tc.expects) {
					t.Errorf("the refusal does not state what the key expects\n got: %s\nwant it to contain: %s", msg, tc.expects)
				}
				for i, line := range nonEmptyLines(tc.value) {
					if strings.Contains(msg, strings.TrimSpace(line)) {
						t.Errorf("the refusal quotes line %d of the value", i+1)
					}
				}
			})
		}
	}
}

// TestAnotherConfigurationRefusalReadsAsItDidBefore pins, word for word, refusals
// of configuration values nobody declared sensitive, so withholding the pinned
// public key's value cannot have moved them. Each row's expected sentence is the
// one the loader printed before the public key was declared sensitive.
//
// The rows are chosen for what they exclude. The first is a pattern refusal the
// document gate makes at the top level. The second sits in the same registry entry
// as the public key — a sibling of the one declared field — so a declaration that
// leaked sideways into its entry would reach it. The third is a type refusal that
// quotes its value, the family whose sentence the public key's now replaces.
//
// Each row's value is refused by the document gate and by nothing ahead of it:
// the host check the loader runs first reads registry hosts only.
func TestAnotherConfigurationRefusalReadsAsItDidBefore(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
		// refusal is the field path and sentence the loader reports, between the
		// file it names and the condition it wraps.
		refusal string
	}{
		{
			name:    "a certificate authority over plain http",
			content: "workspace: {}\nsigning:\n  fulcio_url: http://fulcio.example.test\n",
			refusal: `signing.fulcio_url: pattern: "http://fulcio.example.test" does not match regular expression "^https://[^\\s/?#]+(?:[/?#]\\S*)?$"`,
		},
		{
			name:    "a transparency log with no scheme, in the entry that pins a public key",
			content: "workspace: {}\nregistries:\n  - host: registry.example.test\n    signing:\n      rekor_url: rekor.example.test\n",
			refusal: `registries[0].signing.rekor_url: against /$defs/SigningConfig: pattern: "rekor.example.test" does not match regular expression "^https://[^\\s/?#]+(?:[/?#]\\S*)?$"`,
		},
		{
			name:    "a word where the offline posture takes a boolean",
			content: "workspace: {}\noffline: notabool\n",
			refusal: `offline: type: notabool has type "string", want "boolean"`,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeFixture(t, tc.content)
			_, err := load(path)
			if err == nil {
				t.Fatalf("expected the loader to refuse the file; it loaded:\n%s", tc.content)
			}
			if !errors.Is(err, ErrConfigInvalid()) {
				t.Fatalf("the refusal must wrap ErrConfigInvalid; got %v", err)
			}
			want := "workspaceconfig: " + path + ": " + tc.refusal + ": " + ErrConfigInvalid().Error()
			if got := err.Error(); got != want {
				t.Errorf("the refusal moved\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
