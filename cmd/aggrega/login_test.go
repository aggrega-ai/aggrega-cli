// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/credentials"
)

// freshDockerStore opens the credential store backed by the test's isolated
// DOCKER_CONFIG. A fresh handle each call avoids any in-memory caching masking a
// file-level write or delete.
func freshDockerStore(t *testing.T) credentials.Store {
	t.Helper()
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{AllowPlaintextPut: true})
	if err != nil {
		t.Fatalf("NewStoreFromDocker: %v", err)
	}
	return store
}

// TestLoginStoresCredentialAndLogoutRemoves: login writes a credential the
// shared store can read back (the exact store NewRemoteRepository consumes), and
// logout removes it. Isolated via DOCKER_CONFIG so no real config is touched.
// --password-stdin is the non-interactive path; SkipVerify avoids the network
// (the authenticated handshake is proven separately against an httptest server).
func TestLoginStoresCredentialAndLogoutRemoves(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	const reg = "registry.example.com"

	if err := loginRun(loginConfig{
		Registry: reg, Username: "alice", PasswordStdin: true, SkipVerify: true,
		In: strings.NewReader("s3cret\n"), Out: io.Discard,
	}); err != nil {
		t.Fatalf("loginRun: %v", err)
	}

	got, err := freshDockerStore(t).Get(context.Background(), reg)
	if err != nil {
		t.Fatalf("Get after login: %v", err)
	}
	if got.Username != "alice" || got.Password != "s3cret" {
		t.Errorf("stored credential = {user:%q pass-set:%v}; want alice/s3cret", got.Username, got.Password != "")
	}

	if err := logoutRun(logoutConfig{Registry: reg, Out: io.Discard}); err != nil {
		t.Fatalf("logoutRun: %v", err)
	}
	got, err = freshDockerStore(t).Get(context.Background(), reg)
	if err != nil {
		t.Fatalf("Get after logout: %v", err)
	}
	if got.Username != "" || got.Password != "" {
		t.Errorf("credential not removed after logout: {user:%q pass-set:%v}", got.Username, got.Password != "")
	}
}

// TestLogoutAbsentIsIdempotent: removing a credential that was never stored is
// not an error.
func TestLogoutAbsentIsIdempotent(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	if err := logoutRun(logoutConfig{Registry: "never.seen.example", Out: io.Discard}); err != nil {
		t.Errorf("logout of an absent credential should be a no-op; got %v", err)
	}
}

// TestLoginPasswordStdinRequiresUsername: with --password-stdin there is no room
// to prompt for a username, so one must be supplied explicitly.
func TestLoginPasswordStdinRequiresUsername(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	err := loginRun(loginConfig{
		Registry: "registry.example.com", PasswordStdin: true, SkipVerify: true,
		In: strings.NewReader("pw\n"), Out: io.Discard,
	})
	if err == nil {
		t.Fatal("expected an error when --password-stdin is used without --username")
	}
	if !strings.Contains(err.Error(), "username") {
		t.Errorf("error should mention the missing username; got %v", err)
	}
}
