// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// The per-host credential-state lookup read the shared store on a background
// context. All three surfaces that build one — the registry listing command, the
// daemon's inventory handler and the tool protocol's registry tool — hold a
// cancellable context, and none of them could reach the read.
//
// # The bound, stated because it is real and narrow
//
// The shared credential store honors a context only where it delegates to a
// credential HELPER, which is a subprocess exchange. Its plaintext
// configuration-file path declares the parameter and ignores it, so on a machine
// with no helper configured the read completes whatever the caller asked for.
// That is why the check sits immediately before the read as well as being carried
// into it: the caller's withdrawal is answered here even where the store would
// not answer it.

// credentialReadRecorder reports whether it was asked for anything and what context it
// was handed, so the proof can distinguish "the read was declined" from "the read
// happened on somebody else's context".
type credentialReadRecorder struct {
	asked   int
	gotCtx  context.Context
	entries map[string]auth.Credential
}

func (s *credentialReadRecorder) Get(ctx context.Context, host string) (auth.Credential, error) {
	s.asked++
	s.gotCtx = ctx
	if c, ok := s.entries[host]; ok {
		return c, nil
	}
	return auth.EmptyCredential, nil
}

func (s *credentialReadRecorder) Put(context.Context, string, auth.Credential) error { return nil }
func (s *credentialReadRecorder) Delete(context.Context, string) error               { return nil }

var _ credentials.Store = (*credentialReadRecorder)(nil)

// TestAWithdrawnRunReadsNoCredential is the load-bearing proof.
//
// The control is what discriminates: a live run must read the store and report
// the host configured, and a withdrawn run must not read it at all. A run where
// both reported unknown would be satisfied by a resolver that never reads
// anything.
//
// A planted defect makes this fail: drop the check before the read, and the
// withdrawn run asks the store exactly as the control does — which on the
// plaintext path succeeds, because that path ignores the context it is handed.
func TestAWithdrawnRunReadsNoCredential(t *testing.T) {
	const host = "registry.example.com"
	entries := map[string]auth.Credential{host: {Username: "u", Password: "p"}}

	live := &credentialReadRecorder{entries: entries}
	if got := AuthResolver(context.Background(), live)(host); got != AuthConfigured {
		t.Fatalf("the live control reported %q rather than %q, so the withdrawn arm below would "+
			"pass for the wrong reason", got, AuthConfigured)
	}
	if live.asked != 1 {
		t.Fatalf("the live control asked the store %d times rather than once", live.asked)
	}
	if live.gotCtx == nil || live.gotCtx.Err() != nil {
		t.Errorf("the live control handed the store a context that was already ended (%v); the "+
			"caller's context is supposed to arrive intact", live.gotCtx)
	}

	withdrawn, stop := context.WithCancel(context.Background())
	stop()

	stopped := &credentialReadRecorder{entries: entries}
	if got := AuthResolver(withdrawn, stopped)(host); got != AuthUnknown {
		t.Errorf("a withdrawn run reported the credential state as %q; it was supposed to report "+
			"%q rather than reading the store", got, AuthUnknown)
	}
	if stopped.asked != 0 {
		t.Errorf("a withdrawn run asked the credential store %d time(s); the withdrawal was "+
			"supposed to reach the read", stopped.asked)
	}
}

// TestTheCallersContextReachesTheCredentialRead pins the other half: the context
// the store receives is the CALLER'S, not one the resolver minted. Without this a
// resolver that checked the caller's context and then read on a fresh one would
// pass the proof above.
func TestTheCallersContextReachesTheCredentialRead(t *testing.T) {
	type marker struct{}
	want := "the caller's own"
	ctx := context.WithValue(context.Background(), marker{}, want)

	store := &credentialReadRecorder{entries: map[string]auth.Credential{}}
	_ = AuthResolver(ctx, store)("registry.example.com")

	if store.asked != 1 {
		t.Fatalf("the store was asked %d times rather than once", store.asked)
	}
	if got, _ := store.gotCtx.Value(marker{}).(string); got != want {
		t.Errorf("the store was handed a context carrying %q; the caller's own carries %q, so "+
			"the read is running under a context the caller cannot end", got, want)
	}
}
