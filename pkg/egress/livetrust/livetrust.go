// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package livetrust is the network-egress gate for the live Sigstore trust
// root. It is a sibling of the egress core rather than part of it: the live
// trust-root fetch pulls in the sigstore TUF machinery, which is a lock-time
// verification concern that must never enter the build/render path — and the
// build/render path imports the egress core for the offline decision. Keeping
// the TUF import in this separate leaf keeps the core, and therefore the build
// path, free of verification machinery while still single-sourcing the live
// fetch behind the offline gate.
//
// It is the sole home of the live-trust-root fetch: the TUF client
// construction lives only here, so no caller can build its own live root and
// egress around the offline decision. A pinned trust root loaded from a local
// file performs no egress and does not pass through here.
package livetrust

import (
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TrustedRoot fetches the live public-good Sigstore trust root over TUF,
// derived from the resolved workspace configuration. When the configuration is
// offline it returns (nil, egress.ErrOfflineEgress) before any dial, so an
// offline keyless sign or a no-pinned-root offline verify refuses early and
// clearly rather than failing deep inside a TUF request.
func TrustedRoot(cfg workspaceconfig.Config) (root.TrustedMaterial, error) {
	if cfg.Offline {
		return nil, egress.ErrOfflineEgress()
	}
	live, err := root.NewLiveTrustedRoot(tuf.DefaultOptions())
	if err != nil {
		return nil, fmt.Errorf("livetrust: live trust-root fetch: %w", err)
	}
	return live, nil
}
