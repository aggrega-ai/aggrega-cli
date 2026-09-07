// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
)

// logoutParams is what aggrega/logout takes: the registry host whose stored
// credential to remove.
//
// The spelling is the one every other method here addresses a registry by, and
// the value is a host rather than a friendly alias: the credential store is
// keyed by the address a client authenticates to and holds no alias to resolve.
type logoutParams struct {
	Registry string `json:"registry"`
}

// credentialStoreScope states how far a change to the credential store reached.
//
// A session is opened over one workspace, so an answer with no scope on it reads
// as being about that workspace — and for the credential store that reading is
// always wrong, because the store is shared by every project on the machine and
// by every other tool that speaks to the same registries. MachineWide is
// therefore true on every one of these answers rather than derived from an
// override, since this operation has no workspace-scoped form to resolve to.
//
// It deliberately names no file, and the omission is a correctness matter rather
// than a reticence. The shared store delegates to a configured credential helper
// or OS keychain where one is present, so the removal is frequently served by
// something other than a file on disk — an answer naming a path would assert a
// location the removal did not necessarily touch. The configuration-write
// answers here carry a file because their write genuinely lands in one; this one
// does not, so it says what it knows.
type credentialStoreScope struct {
	// MachineWide says the change reached the store every project on this machine
	// shares. It is always true here.
	MachineWide bool `json:"machineWide"`
	// WorkspaceRoot is the workspace this session was opened over, so a caller
	// holding several sessions can tell which one answered.
	WorkspaceRoot string `json:"workspaceRoot"`
}

// logoutResult is the aggrega/logout response.
//
// Registry echoes the host addressed. There is deliberately no field reporting
// whether the store had held an entry: establishing that would take a second
// read of a machine-wide keychain nobody asked for, and the command line reports
// no such thing either. A caller that wants to know what the store holds asks
// aggrega/registryList, whose whole subject is presence.
type logoutResult struct {
	Registry string               `json:"registry"`
	Scope    credentialStoreScope `json:"scope"`
}

// dispatchLogout removes the stored credential for a registry.
//
// 🔴 It changes machine-wide state, and what admits it is that a caller sent it.
// Removing a credential is something a person does deliberately — the client
// puts a "log out" in front of whoever is driving it, and the request arrives
// because they chose it — so it runs on explicit invocation exactly as every
// other changing method here does, and never as a side effect of validating or
// inspecting anything. The response names the reach so a caller cannot read a
// machine-wide removal as a workspace-local one.
//
// Nothing is prompted for and nothing needs to be: the request carries the one
// value the operation takes. That is what separates this from acquiring a
// credential, which needs a secret typed at a terminal this surface does not
// have and is declined here for that reason.
//
// The credential is never read. The store is asked to delete the host's entry;
// no part of what that entry held reaches the response, an error, or a log line,
// because nothing here obtains it in the first place.
//
// It consults no workspace, so it answers in a session holding nothing prepared
// yet — the credential store is keyed by registry address rather than by
// project, and a host no workspace configures may still have an entry a caller
// wants gone.
func (s *Server) dispatchLogout(ctx context.Context, req *jsonRPCRequest) error {
	var params logoutParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	if params.Registry == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"logout: registry is required (the registry host whose stored credential to remove)")
	}

	// No store is supplied, so the shared one is opened per request — the same
	// store the command line addresses, honouring whatever credential helper or
	// keychain the machine has configured. A server-held handle would be a seam
	// nothing in production sets, and it would cache a store across the lifetime
	// of a session that may outlive the machine's credential configuration.
	res, err := registryops.Logout(ctx, registryops.LogoutOptions{
		Registry: params.Registry,
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
	}
	return s.writeResult(ctx, req.ID, logoutResult{
		Registry: res.Registry,
		Scope: credentialStoreScope{
			MachineWide:   true,
			WorkspaceRoot: s.workspaceRoot,
		},
	})
}
