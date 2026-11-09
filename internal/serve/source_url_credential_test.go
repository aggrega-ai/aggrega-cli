// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// credentialURLUser and credentialURLSecret are the user and password the
// refused buffer writes into its source URL. Nothing the daemon answers may carry
// either.
const (
	credentialURLUser   = "deploy-bot"
	credentialURLSecret = "s3cr3t-in-an-editor-buffer"
)

// credentialURLRecipe is a Recipe sourcing a classic Helm chart repository at url
// over https, with credential written before the host when it is not empty.
func credentialURLRecipe(credential string) string {
	host := "charts.example.com/repo"
	if credential != "" {
		host = credential + "@" + host
	}
	return `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  source:
    kind: helm-https-chart
    url: https://` + host + `
    chart: svc
    version: "1.0.0"
  valuesMapping: |
    {}
`
}

// writeCredentialFixture writes body at path beneath root, creating its
// directories.
func writeCredentialFixture(t *testing.T, root, path, body string) string {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

// credentialURLWorkspace writes a workspace whose Release deploys one component
// rendered by the Recipe at the returned path, which holds recipe.
func credentialURLWorkspace(t *testing.T, recipe string) (root, recipePath string) {
	t.Helper()
	root = t.TempDir()
	writeCredentialFixture(t, root, workspaceconfig.ConfigFilename, "workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\n")
	writeCredentialFixture(t, root, "catalog/component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      replicas: { type: integer, default: 1 }
`)
	recipePath = writeCredentialFixture(t, root, "catalog/recipe/helm/svc/1.0.0/recipe.yaml", recipe)
	writeCredentialFixture(t, root, "aggregate.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeCredentialFixture(t, root, "manifest.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc: helm/svc:1.0.0
`)
	writeCredentialFixture(t, root, "release.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`)
	return root, recipePath
}

// TestTheDaemonRefusesASourceURLCarryingACredentialWithoutPrintingIt drives the
// editor's validation of a Recipe buffer the way the editor sends it while a
// Release is selected — an aggrega/validate request naming that Release — over a
// buffer whose source URL carries a credential. The answer carries the source-url
// condition, with the identifiers the command's single-entity check of the same
// file carries, naming the host and the netrc file; and nothing in the whole
// answer — message, advice, codes or positions — repeats the user or the
// password. The same buffer with the credential removed is the control: the
// daemon answers it with no such entry.
//
// The daemon also answers a request naming no Release, which the editor sends when
// nothing is selected, from the document alone; that answer is checked here only
// for what it must never do, which is repeat the credential.
//
// A planted defect makes this fail: have the source-url rule's predicate answer no
// credential for every URL.
func TestTheDaemonRefusesASourceURLCarryingACredentialWithoutPrintingIt(t *testing.T) {
	t.Parallel()
	wantCode, _ := cerrors.CodeOf(netrc.ErrSourceURLCarriesCredential())
	wantShort, _ := cerrors.ShortCodeOf(netrc.ErrSourceURLCarriesCredential())

	t.Run("no credential in the URL (control)", func(t *testing.T) {
		t.Parallel()
		doc := credentialURLRecipe("")
		root, path := credentialURLWorkspace(t, doc)
		srv := newTestServer(t, root, nil, nil)
		for _, d := range validateContentOverTheWire(t, srv, path, doc, "release.yaml").Diagnostics {
			if d.Code == wantCode {
				t.Errorf("a source URL carrying no credential was refused for one: %+v", d)
			}
		}
	})

	t.Run("a credential in the URL", func(t *testing.T) {
		t.Parallel()
		doc := credentialURLRecipe(credentialURLUser + ":" + credentialURLSecret)
		root, path := credentialURLWorkspace(t, doc)
		srv := newTestServer(t, root, nil, nil)

		_, commandErr := validateops.ValidateEntity(context.Background(), root, t.TempDir(),
			[]string{filepath.Join(root, "catalog")}, nil, path, false)
		if !errors.Is(commandErr, netrc.ErrSourceURLCarriesCredential()) {
			t.Fatalf("the command's check did not refuse the buffer under the source-url condition: %v", commandErr)
		}

		result := validateContentOverTheWire(t, srv, path, doc, "release.yaml")
		var found bool
		for _, d := range result.Diagnostics {
			if d.Code == wantCode && d.ShortCode == wantShort {
				found = true
				if !strings.Contains(d.Message, "host charts.example.com") || !strings.Contains(d.Message, "netrc") {
					t.Errorf("the refusal does not name the host and the netrc file: %q", d.Message)
				}
			}
		}
		if !found {
			t.Fatalf("the daemon's answer carries no %s / %s entry: %+v", wantCode, wantShort, result.Diagnostics)
		}

		noRelease := validateContentOverTheWire(t, srv, path, doc, "")
		for _, answer := range []validateResult{result, noRelease} {
			encoded, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("encode the daemon's answer: %v", err)
			}
			for _, secret := range []string{credentialURLSecret, credentialURLUser} {
				if strings.Contains(string(encoded), secret) {
					t.Errorf("the daemon's answer repeats %q from the source URL:\n%s", secret, encoded)
				}
			}
		}
		for _, secret := range []string{credentialURLSecret, credentialURLUser} {
			if strings.Contains(commandErr.Error(), secret) {
				t.Errorf("the command's refusal repeats %q from the source URL: %v", secret, commandErr)
			}
		}
	})
}
