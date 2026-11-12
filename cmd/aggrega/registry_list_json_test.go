// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
)

// registryListJSONConfig declares three registries in an order that is neither
// ascending nor descending by host, so an assertion about the emitted order is
// about the resolution and not about the file happening to be sorted already.
// zed carries an alias and the advisory flag; hub is the default and pins keyless
// identities; mid pins a key.
const registryListJSONConfig = "workspace: {}\ndefault_registry: hub.aggrega.ai\n" +
	"registries:\n" +
	"  - host: zed.example.com\n    name: zed\n    advisory: true\n" +
	"  - host: hub.aggrega.ai\n    signing:\n      required: true\n" +
	"    trusted_identities:\n      - identity: a@example.com\n        issuer: https://accounts.google.com\n" +
	"      - identity: b@example.com\n        issuer: https://accounts.google.com\n" +
	"  - host: mid.example.com\n    public_key: |\n      -----BEGIN PUBLIC KEY-----\n      AAAA\n      -----END PUBLIC KEY-----\n"

// listRegistriesJSON runs the structured listing over cfgContent with the given
// credential store and returns the decoded document plus the raw bytes.
func listRegistriesJSON(t *testing.T, cfgContent string, store credentials.Store) (registryListDoc, string) {
	t.Helper()
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), cfgContent)
	var out bytes.Buffer
	if err := registryListRun(context.Background(), registryListConfig{ws: rw, Out: &out, JSON: true, store: store}); err != nil {
		t.Fatalf("registryListRun: %v", err)
	}
	var doc registryListDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\nstdout:\n%s", err, out.String())
	}
	return doc, out.String()
}

// TestRegistryListJSONDeterministicOrder pins that the structured listing emits
// registries in the same ascending host order the table uses, from a config that
// declares them in neither order. Both renderings read one resolved sequence, so
// this is also the guard against them drifting apart on order.
func TestRegistryListJSONDeterministicOrder(t *testing.T) {
	doc, raw := listRegistriesJSON(t, registryListJSONConfig, emptyCredentialStore{})

	got := make([]string, 0, len(doc.Registries))
	for _, r := range doc.Registries {
		got = append(got, r.Host)
	}
	want := []string{"hub.aggrega.ai", "mid.example.com", "zed.example.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("host order = %v; want %v\nstdout:\n%s", got, want, raw)
	}

	// Same input, same bytes: the emitted document is stable across runs.
	_, raw2 := listRegistriesJSON(t, registryListJSONConfig, emptyCredentialStore{})
	if raw != raw2 {
		t.Errorf("output not byte-stable across runs:\n--1--\n%s\n--2--\n%s", raw, raw2)
	}
}

// TestRegistryListJSONCarriesIdentityAndTrust pins the per-registry content: the
// host as identity, the alias when set, the default marker on exactly one entry,
// and the trust posture as discrete fields rather than the table's sentence.
func TestRegistryListJSONCarriesIdentityAndTrust(t *testing.T) {
	doc, raw := listRegistriesJSON(t, registryListJSONConfig, emptyCredentialStore{})

	if len(doc.Registries) != 3 {
		t.Fatalf("expected 3 registries, got %d\nstdout:\n%s", len(doc.Registries), raw)
	}
	byHost := map[string]registryops.Registry{}
	for _, r := range doc.Registries {
		byHost[r.Host] = r
	}

	hub := byHost["hub.aggrega.ai"]
	if !hub.Default {
		t.Error("hub is default_registry and must be marked default")
	}
	if !hub.Trust.SignaturesRequired {
		t.Error("hub requires signatures; the document must say so")
	}
	if hub.Trust.Anchor != "keyless" || hub.Trust.KeylessIdentities != 2 {
		t.Errorf("hub anchor = %q with %d identities; want keyless with 2",
			hub.Trust.Anchor, hub.Trust.KeylessIdentities)
	}

	mid := byHost["mid.example.com"]
	if mid.Trust.Anchor != "keyed" {
		t.Errorf("mid pins a public key; anchor = %q, want keyed", mid.Trust.Anchor)
	}

	zed := byHost["zed.example.com"]
	if zed.Name != "zed" {
		t.Errorf("zed alias = %q; want zed", zed.Name)
	}
	if !zed.Trust.Advisory {
		t.Error("zed is advisory; the document must say so")
	}
	if zed.Default {
		t.Error("zed is not the default registry and must not be marked one")
	}
}

// TestRegistryListJSONReportsCredentialPresenceWithoutLeaking is the security
// assertion, and it is calibrated rather than merely negative.
//
// A planted credential with a distinctive password is stored for one host. The
// test first proves the store really serves it — otherwise "the secret is absent
// from the output" would be a claim about a dead store rather than about the
// output. It then requires the output to report presence for that host, absence
// for the others, and to contain no trace of the username, the password, or any
// substring of the password.
func TestRegistryListJSONReportsCredentialPresenceWithoutLeaking(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	const (
		host     = "hub.aggrega.ai"
		user     = "planted-user"
		password = "planted-Pa55word-NEEDLE"
	)
	store := freshDockerStore(t)
	if err := store.Put(context.Background(), host, auth.Credential{Username: user, Password: password}); err != nil {
		t.Fatalf("plant credential: %v", err)
	}

	// Calibration: the instrument fires. A store that served nothing would make
	// every assertion below pass vacuously.
	planted, err := store.Get(context.Background(), host)
	if err != nil {
		t.Fatalf("read back planted credential: %v", err)
	}
	if planted.Password != password {
		t.Fatalf("the store did not serve the planted credential (got %q); the leak assertion would be vacuous",
			planted.Password)
	}

	doc, raw := listRegistriesJSON(t, registryListJSONConfig, store)

	byHost := map[string]registryops.Registry{}
	for _, r := range doc.Registries {
		byHost[r.Host] = r
	}
	if got := byHost[host].AuthState; got != registryops.AuthConfigured {
		t.Errorf("%s has a stored credential; authState = %q, want %q", host, got, registryops.AuthConfigured)
	}
	for _, other := range []string{"mid.example.com", "zed.example.com"} {
		if got := byHost[other].AuthState; got != registryops.AuthNone {
			t.Errorf("%s has no stored credential; authState = %q, want %q", other, got, registryops.AuthNone)
		}
	}

	// No credential material in any form. The password's halves are checked too,
	// so a truncated or split rendering cannot slip through a whole-string check.
	for _, forbidden := range []string{
		password,
		user,
		password[:len(password)/2],
		password[len(password)/2:],
		"Pa55word",
		"NEEDLE",
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the output contains credential material %q:\n%s", forbidden, raw)
		}
	}
	// Nor any hint of its size.
	if strings.Contains(raw, "23") {
		t.Errorf("the output contains the credential's length; it must reveal no measurement:\n%s", raw)
	}
}

// TestRegistryListJSONUnreadableStoreIsUnknownNotNone pins that a credential
// store which cannot be consulted yields unknown rather than none. Conflating the
// two would report "not logged in" for a machine whose keychain is merely locked.
func TestRegistryListJSONUnreadableStoreIsUnknownNotNone(t *testing.T) {
	doc, raw := listRegistriesJSON(t, registryListJSONConfig, failingCredentialStore{})

	for _, r := range doc.Registries {
		if r.AuthState != registryops.AuthUnknown {
			t.Errorf("%s authState = %q; an unreadable store means %q\nstdout:\n%s",
				r.Host, r.AuthState, registryops.AuthUnknown, raw)
		}
	}
}

// TestRegistryListJSONEmptyEmitsEmptyList pins that no configured registries
// still yields a decodable document with an empty array, so a consumer iterates
// without special-casing null.
func TestRegistryListJSONEmptyEmitsEmptyList(t *testing.T) {
	doc, raw := listRegistriesJSON(t, "workspace: {}\n", emptyCredentialStore{})
	if len(doc.Registries) != 0 {
		t.Errorf("expected no registries; got %d", len(doc.Registries))
	}
	if !strings.Contains(raw, "\"registries\": []") {
		t.Errorf("expected an empty array rather than null; got:\n%s", raw)
	}
}

// TestRegistryListTableTouchesNoCredentialStore pins that the human table's
// behaviour is unchanged: it resolves no credential state, so an ordinary
// invocation opens no credential store, unlocks no keychain, and invokes no
// credential helper. The store below fails the test if it is consulted at all.
func TestRegistryListTableTouchesNoCredentialStore(t *testing.T) {
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), registryListJSONConfig)
	var out bytes.Buffer
	fatal := &recordingCredentialStore{}
	if err := registryListRun(context.Background(), registryListConfig{ws: rw, Out: &out, store: fatal}); err != nil {
		t.Fatalf("registryListRun: %v", err)
	}
	if fatal.gets != 0 {
		t.Errorf("the table path consulted the credential store %d time(s); it must consult it none", fatal.gets)
	}
	if !strings.Contains(out.String(), "hub.aggrega.ai") {
		t.Errorf("the table did not render; the assertion above would be vacuous:\n%s", out.String())
	}
}

// emptyCredentialStore serves no credential for any host.
type emptyCredentialStore struct{}

func (emptyCredentialStore) Get(context.Context, string) (auth.Credential, error) {
	return auth.EmptyCredential, nil
}
func (emptyCredentialStore) Put(context.Context, string, auth.Credential) error { return nil }
func (emptyCredentialStore) Delete(context.Context, string) error               { return nil }

// failingCredentialStore stands in for a store that cannot be consulted — a
// locked keychain, an unreadable config, a credential helper that is not
// installed.
type failingCredentialStore struct{}

func (failingCredentialStore) Get(context.Context, string) (auth.Credential, error) {
	return auth.EmptyCredential, errUnreadableStore
}
func (failingCredentialStore) Put(context.Context, string, auth.Credential) error { return nil }
func (failingCredentialStore) Delete(context.Context, string) error               { return nil }

// recordingCredentialStore counts the reads made against it.
type recordingCredentialStore struct{ gets int }

func (s *recordingCredentialStore) Get(context.Context, string) (auth.Credential, error) {
	s.gets++
	return auth.EmptyCredential, nil
}
func (s *recordingCredentialStore) Put(context.Context, string, auth.Credential) error { return nil }
func (s *recordingCredentialStore) Delete(context.Context, string) error               { return nil }

// errUnreadableStore is the failure a store surfaces when it cannot be consulted.
var errUnreadableStore = errStore("credential store unreadable")

type errStore string

func (e errStore) Error() string { return string(e) }
