// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestMain isolates the per-user homes for this test binary. Constructing any
// remote target reads the container-registry credential file, so without this
// the suite would read whatever the developer is logged in to — and would follow
// whatever credential helper that file names, executing a binary the suite never
// chose.
func TestMain(m *testing.M) {
	os.Exit(hometest.RunHermetic(m))
}

// TestCredentialHomeRedirectIsInEffect is the passing control for the credential
// half of the redirect: it proves the production construction path reads the
// redirected credential home, not merely that the environment points somewhere
// temporary. A redirect that is wired but ineffective passes an
// environment-level assertion and fails this one.
//
// It plants a credential for a host no other case uses, builds a repository
// through the production constructor, and asserts the credential the transport
// resolves for that host is the planted one. The host is deliberately unique, so
// the plant answers only this case and leaves every other host resolving empty.
func TestCredentialHomeRedirectIsInEffect(t *testing.T) {
	// Not parallel: it writes to the credential home the whole binary shares.
	hometest.AssertRedirected(t)

	const (
		plantedHost     = "redirect-control.invalid"
		plantedUsername = "planted-user"
		plantedPassword = "planted-password"
	)

	credentialPath := filepath.Join(hometest.CredentialHome(), "config.json")
	basic := base64.StdEncoding.EncodeToString([]byte(plantedUsername + ":" + plantedPassword))
	body := `{"auths":{"` + plantedHost + `":{"auth":"` + basic + `"}}}`
	if err := os.WriteFile(credentialPath, []byte(body), 0o600); err != nil {
		t.Fatalf("plant the distinguishable credential: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(credentialPath) })

	repo, err := oci.NewRemoteRepository(plantedHost+"/control/target", &http.Client{})
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	client, ok := repo.Client.(*auth.Client)
	if !ok {
		t.Fatalf("repository transport is %T, not the authenticated client the constructor wires", repo.Client)
	}
	if client.Credential == nil {
		t.Fatal("the authenticated client carries no credential resolver, so nothing reads the credential home at all")
	}

	got, err := client.Credential(context.Background(), plantedHost)
	if err != nil {
		t.Fatalf("resolve the planted credential: %v", err)
	}
	if got.Username != plantedUsername || got.Password != plantedPassword {
		t.Fatalf("credential for %s = %q/%q, want the planted %q/%q; the production path did not read the redirected credential home at %s",
			plantedHost, got.Username, got.Password, plantedUsername, plantedPassword, credentialPath)
	}

	// The negative half: a host the planted file says nothing about resolves
	// empty. Without it, a resolver returning the same credential for every host
	// would pass the assertion above.
	other, err := client.Credential(context.Background(), "unplanted-control.invalid")
	if err != nil {
		t.Fatalf("resolve an unplanted host: %v", err)
	}
	if other.Username != "" || other.Password != "" {
		t.Fatalf("an unplanted host resolved to %q/%q; the assertion above does not show the planted entry was read by host", other.Username, other.Password)
	}
}
