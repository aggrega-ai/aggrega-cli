// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"net/http"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// workspaceEgressClient derives the network-egress HTTP client for a resolved
// workspace configuration, refusing with egress.ErrOfflineEgress when the
// workspace is offline. It is the command-level convenience over
// egress.HTTPClient for the many command paths that construct a remote OCI
// target or fetch over HTTP: the offline decision lives once at the gate, and
// each command reacts to the refusal in its own loud-or-silent way.
func workspaceEgressClient(cfg workspaceconfig.Config) (*http.Client, error) {
	return egress.HTTPClient(cfg)
}
