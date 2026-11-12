// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestLoginAndLogoutReportAnUnreadableCredentialFileWithoutItsContents pins what
// the two commands that open the credential store themselves say when its file
// cannot be read: the fixed sentence naming the registry and the credential file,
// and no character of the file. Each row drives one command against a fabricated
// credential file that is not valid JSON, in this test's own credential home.
//
// The store's own text for such a file quotes the character its decoder stopped
// at, which here is the first character of the planted text, so a command
// relaying that text fails the absence assertions. They are checked on the
// error's text and on its one-line cause chain, because a frame left beneath the
// sentence would print through the chain even with the sentence on top. The file
// is also read back unchanged: a command that could not read it must not have
// rewritten it.
func TestLoginAndLogoutReportAnUnreadableCredentialFileWithoutItsContents(t *testing.T) {
	const (
		registry  = "registry.example.com"
		planted   = "probe-placeholder-token-no-colon"
		storeText = "invalid character"
	)
	cases := []struct {
		name string
		run  func(t *testing.T) error
	}{
		{
			name: "aggrega login",
			run: func(t *testing.T) error {
				return loginRun(context.Background(), loginConfig{
					ws:       resolveTempWorkspace(t, tempWorkspaceOptions{}),
					Registry: registry, Username: "probe-user", PasswordStdin: true, SkipVerify: true,
					In: strings.NewReader("probe-placeholder-password\n"), Out: io.Discard,
				})
			},
		},
		{
			name: "aggrega logout",
			run: func(*testing.T) error {
				return logoutRun(context.Background(), logoutConfig{Registry: registry, Out: io.Discard})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("DOCKER_CONFIG", dir)
			path := filepath.Join(dir, "config.json")
			body := []byte(`{"auths":{"` + registry + `":{"auth":` + planted + `}}}`)
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatalf("plant the credential file: %v", err)
			}

			err := tc.run(t)
			if err == nil {
				t.Fatal("the command succeeded over a credential file that is not valid JSON")
			}
			for _, rendered := range []struct{ how, text string }{
				{"the error's text", err.Error()},
				{"the one-line cause chain", cerrors.CauseChain(err)},
			} {
				if strings.Contains(rendered.text, storeText) {
					t.Errorf("%s carries the store's own text (%q):\n%s", rendered.how, storeText, rendered.text)
				}
				if strings.Contains(rendered.text, planted) {
					t.Errorf("%s carries the planted text:\n%s", rendered.how, rendered.text)
				}
			}
			msg := err.Error()
			for _, want := range []string{
				`the credential for "` + registry + `" cannot be read`,
				"the credential file (config.json",
				"is not shown",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the error does not carry %q:\n%s", want, msg)
				}
			}
			after, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("read the credential file back: %v", rerr)
			}
			if !bytes.Equal(after, body) {
				t.Errorf("the command rewrote a credential file it could not read:\n%s", after)
			}
		})
	}
}
