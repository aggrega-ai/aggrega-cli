// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package egress is the single gate every network-egressing path crosses. It is
// the sole home of the offline decision and the sole constructor of the network
// clients and transports the rest of the compiler uses to reach a registry, a
// remote source, or an identity provider.
//
// The offline decision is DERIVED, never trusted from the caller. A caller does
// not hand the gate a boolean; it hands the gate the resolved workspace
// configuration, and the gate reads the offline field from it. The only
// legitimate source of a resolved configuration is the workspace-resolution
// choke-point, so a caller cannot fabricate an offline-false configuration to
// bypass the promise without first re-rolling workspace resolution — which a
// separate structural guard forbids. The two guards compose: resolution is
// single-sourced, and egress reads offline only from resolution's output.
//
// When the resolved configuration is offline, the constructors refuse at
// construction, before any dial, returning ErrOfflineEgress. Offline forbids the
// fetch, not the math: cryptographic verification of already-cached or pinned
// material against a pinned key or root performs no egress and does not pass
// through this gate.
//
// A structural guard-test asserts that no package outside this one constructs a
// network client or reaches for a network library directly, so a future consumer
// that forgets the gate cannot silently egress — deny-by-default over the whole
// module. The narrow exception is the OCI-semantics package, which owns the
// oras-specific repository and registry construction and receives its transport
// from this gate.
package egress

import (
	"errors"
	"net/http"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ErrOfflineEgress is returned by every egress constructor when the resolved
// workspace configuration is offline. It is refused at construction, before any
// network dial, so an offline operation fails early with a clear message rather
// than deep inside a transport with an opaque network error. The message carries
// its own remediation so callers that surface it need add none.
//
// It belongs to the fetch-error family: the source-resolution error classifier
// routes it into the same category as a remote-acquisition fault, since an
// offline refusal is, from the user's perspective, an inability to fetch.
var ErrOfflineEgress = errors.New("egress: network access refused because the workspace is offline; remediation: unset workspace.offline (or AGGREGA_OFFLINE) or run `aggrega pull` from a connected environment to populate the cache")

// RoundTripper returns the HTTP transport for network egress, derived from the
// resolved workspace configuration. When the configuration is offline it returns
// (nil, ErrOfflineEgress) before any dial; otherwise it returns the shared
// default transport. It is the transport source the OCI package threads into its
// repository and registry construction.
func RoundTripper(cfg workspaceconfig.Config) (http.RoundTripper, error) {
	if cfg.Offline {
		return nil, ErrOfflineEgress
	}
	return http.DefaultTransport, nil
}

// HTTPClient returns an HTTP client for network egress, derived from the resolved
// workspace configuration. When the configuration is offline it returns
// (nil, ErrOfflineEgress) before any dial. The returned client carries no
// timeout; a caller with a fetch deadline sets Timeout on the returned client.
func HTTPClient(cfg workspaceconfig.Config) (*http.Client, error) {
	rt, err := RoundTripper(cfg)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt}, nil
}
