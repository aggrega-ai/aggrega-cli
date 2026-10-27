// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// toolCredentialUser and toolCredentialSecret are the user and password the
// refused rows write into a source URL. No tool answer may carry either.
const (
	toolCredentialUser   = "deploy-bot"
	toolCredentialSecret = "s3cr3t-in-a-tool-call"
)

// credentialRecipe is a Recipe whose source is the chart archive at url, written
// with credential before the host when credential is not empty.
func credentialRecipe(url, credential string) string {
	if credential != "" {
		url = strings.Replace(url, "://", "://"+credential+"@", 1)
	}
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\n" +
		"metadata:\n  name: server\n  version: \"1.0.0\"\nspec:\n  capability: helm\n  implements: server\n" +
		"  source:\n    kind: helm-https-chart\n    url: " + url + "\n    version: \"1.0.0\"\n"
}

// assertNoCredentialIn fails where any text of a tool's whole answer carries the
// user or the password.
func assertNoCredentialIn(t *testing.T, tool string, res *callToolResult) {
	t.Helper()
	answer, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("encode the %s tool's answer: %v", tool, err)
	}
	for _, secret := range []string{toolCredentialSecret, toolCredentialUser} {
		if strings.Contains(string(answer), secret) {
			t.Errorf("the %s tool's answer repeats %q from the source URL:\n%s", tool, secret, answer)
		}
	}
}

// TestTheValidateToolRefusesASourceURLCarryingACredentialWithoutPrintingIt calls
// the validate tool's single-entity scope on a Recipe whose source URL carries a
// credential. The tool answers invalid, with the refusal the shared core gives the
// same file — the source-url condition, naming the host and the netrc file — and
// nothing in its whole answer repeats the user or the password.
//
// A planted defect makes this fail: have the source-url rule's predicate answer no
// credential for every URL.
func TestTheValidateToolRefusesASourceURLCarryingACredentialWithoutPrintingIt(t *testing.T) {
	s := newTestServer(t)
	root := t.TempDir()
	writeNoticeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	entity := filepath.Join(root, "catalog", "recipe", "helm", "server", "1.0.0", "recipe.yaml")
	writeNoticeFixture(t, entity, credentialRecipe("https://charts.example.com/server-1.0.0.tgz",
		toolCredentialUser+":"+toolCredentialSecret))

	res := callTool(t, s, "validate", validateArgs{Entity: entity, WorkspaceRoot: root})
	payload := unmarshalValidatePayload(t, res)
	if payload.Valid {
		t.Fatalf("the validate tool accepted a Recipe whose source URL carries a credential: %+v", payload)
	}
	sentence, _ := cerrors.MessageOf(netrc.ErrSourceURLCarriesCredential())
	joined := strings.Join(payload.Diagnostics, "\n")
	for _, want := range []string{sentence, "host charts.example.com", "netrc"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the tool's refusal does not say %q:\n%s", want, joined)
		}
	}
	assertNoCredentialIn(t, "validate", res)

	_, coreErr := validateops.ValidateEntity(context.Background(), root, t.TempDir(), nil, nil, entity, false)
	if !errors.Is(coreErr, netrc.ErrSourceURLCarriesCredential()) {
		t.Fatalf("the shared core did not refuse the same file under the source-url condition: %v", coreErr)
	}
}

// TestThePinToolRefusesASourceURLCarryingACredentialBeforeFetching calls the pin
// tool, in a workspace, on a Recipe whose remote source the counting chart server
// serves. With a credential in the URL the call is refused under the source-url
// condition, the server answers nothing, no lock is written, and nothing in the
// whole answer repeats the user or the password. The control is the same Recipe
// with the credential removed: the step fetches the source and writes the lock,
// so the refused row's empty fetch count is the refusal's and not the fixture's.
//
// Not parallel: the server resolves from the process working directory, and the
// rows set the offline posture's variable so the control may reach the server.
//
// A planted defect makes this fail: have the source-url rule's predicate answer no
// credential for every URL.
func TestThePinToolRefusesASourceURLCarryingACredentialBeforeFetching(t *testing.T) {
	for _, tc := range []struct {
		name       string
		credential string
	}{
		{name: "no credential in the URL (control)"},
		{name: "a credential in the URL", credential: toolCredentialUser + ":" + toolCredentialSecret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, hits := countingChartArchiveServer(t)
			folder := t.TempDir()
			writeNoticeFixture(t, filepath.Join(folder, workspaceconfig.ConfigFilename), "workspace: {}\n")
			entity := filepath.Join("catalog", "recipe", "helm", "server", "1.0.0", "recipe.yaml")
			writeNoticeFixture(t, filepath.Join(folder, entity), credentialRecipe(srv.URL+"/pinchart.tgz", tc.credential))
			t.Setenv("AGGREGA_OFFLINE", "false")
			t.Chdir(folder)

			res := callTool(t, unlaunchedServer(""), "pin", pinArgs{Entity: entity})
			lock := recipepin.LockPath(filepath.Join(folder, entity))

			if tc.credential == "" {
				if res.IsError {
					t.Fatalf("the pin tool refused a Recipe whose source URL carries no credential: %s", res.Content[0].Text)
				}
				if hits.Load() == 0 {
					t.Fatal("the control fetched nothing, so the refused row's empty fetch count proves nothing")
				}
				if _, err := os.Stat(lock); err != nil {
					t.Fatalf("the control wrote no lock (%v), so the refused row's missing lock proves nothing", err)
				}
				return
			}
			if !res.IsError {
				t.Fatalf("the pin tool pinned a Recipe whose source URL carries a credential: %s", res.Content[0].Text)
			}
			sentence, _ := cerrors.MessageOf(netrc.ErrSourceURLCarriesCredential())
			if text := res.Content[0].Text; !strings.Contains(text, sentence) {
				t.Errorf("the refusal does not carry the source-url condition's sentence %q:\n%s", sentence, text)
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("the refused call fetched the source %d time(s)", n)
			}
			if _, err := os.Stat(lock); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the refused call wrote the lock (stat: %v)", err)
			}
			assertNoCredentialIn(t, "pin", res)
		})
	}
}
