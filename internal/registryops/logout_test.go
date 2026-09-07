// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// recordingStore is a credential store that records what was asked of it and
// answers however the test needs.
//
// It exists so two properties can be asserted that a real store cannot show:
// that a removal asks for a delete and nothing else — no Get, so no credential
// is ever obtained to leak — and that a failing keychain produces an error
// naming the host.
type recordingStore struct {
	deleted   []string
	gets      []string
	puts      []string
	deleteErr error
	// held is what a Get would answer with, so a test can prove the material is
	// present in the store and still absent from everything the removal produces.
	held auth.Credential
}

func (s *recordingStore) Get(_ context.Context, addr string) (auth.Credential, error) {
	s.gets = append(s.gets, addr)
	return s.held, nil
}

func (s *recordingStore) Put(_ context.Context, addr string, _ auth.Credential) error {
	s.puts = append(s.puts, addr)
	return nil
}

func (s *recordingStore) Delete(_ context.Context, addr string) error {
	s.deleted = append(s.deleted, addr)
	return s.deleteErr
}

// dockerStoreIn opens the shared credential store against a directory of this
// test's own.
//
// The redirect is not tidiness. The store this operation addresses is
// machine-wide, so a test that did not redirect it would delete a credential the
// developer running the suite actually holds. A fresh handle per call also keeps
// any in-memory caching from masking a file-level delete.
func dockerStoreIn(t *testing.T, dir string) credentials.Store {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", dir)
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{AllowPlaintextPut: true})
	if err != nil {
		t.Fatalf("NewStoreFromDocker: %v", err)
	}
	return store
}

// TestLogoutRefusesAnEmptyRegistry pins the sentence the command line has always
// printed, because the command line's output must not move when the logic does.
func TestLogoutRefusesAnEmptyRegistry(t *testing.T) {
	_, err := Logout(context.Background(), LogoutOptions{Store: &recordingStore{}})
	if err == nil {
		t.Fatal("a removal naming no registry should be refused")
	}
	const want = "aggrega logout: registry argument required"
	if err.Error() != want {
		t.Errorf("refusal = %q; want %q verbatim, which is what the command line prints", err.Error(), want)
	}
}

// TestLogoutRefusesBeforeItOpensAStore asserts the ordering, which is what keeps
// a request naming nothing from causing a machine-wide side effect.
//
// A keychain is consulted by opening it, so an operation that validated after
// opening would touch the machine on a request it was going to refuse anyway.
func TestLogoutRefusesBeforeItOpensAStore(t *testing.T) {
	store := &recordingStore{}
	if _, err := Logout(context.Background(), LogoutOptions{Store: store}); err == nil {
		t.Fatal("expected a refusal")
	}
	if len(store.deleted) != 0 || len(store.gets) != 0 || len(store.puts) != 0 {
		t.Errorf("a refused removal touched the store: deleted=%v gets=%v puts=%v",
			store.deleted, store.gets, store.puts)
	}
}

// TestLogoutRemovesTheNamedHostAndLeavesItsNeighbour is the substance: the entry
// the request named is gone and the one beside it is not.
//
// The neighbour is the assertion that matters. A removal that wiped the store
// would satisfy "the credential is gone" while destroying every other
// credential on the machine, and that failure is invisible to a test holding one
// entry.
func TestLogoutRemovesTheNamedHostAndLeavesItsNeighbour(t *testing.T) {
	dir := t.TempDir()
	store := dockerStoreIn(t, dir)
	ctx := context.Background()

	const target = "doomed.example"
	const neighbour = "kept.example"
	for _, host := range []string{target, neighbour} {
		if err := store.Put(ctx, host, auth.Credential{Username: "u-" + host, Password: "p-" + host}); err != nil {
			t.Fatalf("seed %s: %v", host, err)
		}
	}

	res, err := Logout(ctx, LogoutOptions{Registry: target})
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if res.Registry != target {
		t.Errorf("result registry = %q; want the host addressed, %q", res.Registry, target)
	}

	// A fresh handle, so the read goes to the file rather than to a cached view.
	fresh := dockerStoreIn(t, dir)
	gone, err := fresh.Get(ctx, target)
	if err != nil {
		t.Fatalf("Get after the removal: %v", err)
	}
	if gone != auth.EmptyCredential {
		t.Errorf("the credential for %s survived the removal", target)
	}
	kept, err := fresh.Get(ctx, neighbour)
	if err != nil {
		t.Fatalf("Get the neighbour: %v", err)
	}
	if kept.Username != "u-"+neighbour {
		t.Errorf("the neighbour's credential did not survive: got username %q", kept.Username)
	}
}

// TestLogoutOfAnAbsentHostSucceedsAndRewritesNothing pins the behaviour the
// command line has, measured rather than assumed: a host the store holds nothing
// for is removed successfully and the store file is left byte-identical.
//
// Both halves are asserted. The success alone would pass on an implementation
// that rewrote the store on every call, which would touch a machine-wide file
// for a request that changed nothing.
func TestLogoutOfAnAbsentHostSucceedsAndRewritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const seeded = `{"auths":{"kept.example":{"auth":"dTpw"}}}`
	if err := os.WriteFile(path, []byte(seeded), 0o600); err != nil {
		t.Fatalf("seed the store file: %v", err)
	}
	t.Setenv("DOCKER_CONFIG", dir)

	if _, err := Logout(context.Background(), LogoutOptions{Registry: "never-stored.example"}); err != nil {
		t.Fatalf("removing an absent credential should succeed; got %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the store file: %v", err)
	}
	if string(after) != seeded {
		t.Errorf("the store file was rewritten for a removal that changed nothing:\n got %s\nwant %s", after, seeded)
	}
}

// TestLogoutNeverObtainsTheCredentialItRemoves is the absence claim, with its
// instrument named: the store records every call made to it, and a removal makes
// exactly one Delete and no Get.
//
// This is what makes "the response carries no credential material" a structural
// property rather than an inspection of one response. Nothing can leak a value
// the operation never asks for.
func TestLogoutNeverObtainsTheCredentialItRemoves(t *testing.T) {
	store := &recordingStore{held: auth.Credential{Username: "alice", Password: "s3cret-marker"}}
	res, err := Logout(context.Background(), LogoutOptions{Registry: "target.example", Store: store})
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if len(store.gets) != 0 {
		t.Errorf("the removal read the credential store for content: gets=%v", store.gets)
	}
	if len(store.puts) != 0 {
		t.Errorf("the removal wrote a credential: puts=%v", store.puts)
	}
	if want := []string{"target.example"}; len(store.deleted) != 1 || store.deleted[0] != want[0] {
		t.Errorf("deleted = %v; want exactly %v", store.deleted, want)
	}
	if rendered := fmt.Sprintf("%+v", res); strings.Contains(rendered, "s3cret-marker") || strings.Contains(rendered, "alice") {
		t.Errorf("the result rendered credential material: %s", rendered)
	}
}

// TestLogoutReportsAFailingStoreWithTheHostNamed keeps a keychain fault
// distinguishable from an absent credential, which the honest-outcome contract
// depends on: if a fault were reported as success, "there is no credential here"
// and "the keychain could not be reached" would be one answer.
func TestLogoutReportsAFailingStoreWithTheHostNamed(t *testing.T) {
	sentinel := errors.New("keychain unavailable")
	store := &recordingStore{deleteErr: sentinel, held: auth.Credential{Password: "s3cret-marker"}}
	_, err := Logout(context.Background(), LogoutOptions{Registry: "broken.example", Store: store})
	if err == nil {
		t.Fatal("a failing store should be reported rather than answered as a success")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("the store's own fault should be wrapped rather than replaced; got %v", err)
	}
	if !strings.Contains(err.Error(), "broken.example") {
		t.Errorf("the refusal should name the host it could not remove; got %v", err)
	}
	if strings.Contains(err.Error(), "s3cret-marker") {
		t.Errorf("the refusal rendered credential material: %v", err)
	}
}
