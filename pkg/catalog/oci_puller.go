// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// NewDefaultOCIPuller returns the production OCIPuller backed by
// pkg/oci.NewRemoteRepository per Pull-call. Returns nil when the
// workspace is configured offline so closure-walker and lazy-lookup
// Tier-3 paths surface as NotFound without attempting any network
// access.
//
// The exported function lives next to the OCIPuller interface
// definition so both CLI verbs (pull, build) and long-running
// consumers (the compiler-as-service daemon) construct pullers via
// the same canonical helper. Local copies would drift; one
// implementation prevents that.
func NewDefaultOCIPuller(wsCfg workspaceconfig.Config) OCIPuller {
	if wsCfg.Workspace.Offline {
		return nil
	}
	return DefaultOCIPuller{}
}

// DefaultOCIPuller is the production OCIPuller implementation. The
// type is stateless; instances are interchangeable. Pull constructs
// a fresh oci.RemoteRepository per call so per-call authentication
// + retry behavior matches pkg/oci's documented surface exactly.
type DefaultOCIPuller struct{}

func (DefaultOCIPuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, err
	}
	return oci.Pull(ctx, repo, tag)
}
