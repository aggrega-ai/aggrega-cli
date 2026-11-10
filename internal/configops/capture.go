// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package configops

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// CaptureOptions is what freezing a configuration decides.
//
// AllPlugins widens the captured socket set from the sockets that determine the
// rendered output — the provider bindings and the label-transform governance
// attachments — to every plugin socket, adding the verdict governance and linter
// gates and the import socket. Those otherwise stay free to vary by environment,
// which is why the narrower set is the default.
//
// There is no write-target override, and that is the operation rather than an
// omission: freezing exists to make a workspace self-contained, so it always
// writes that workspace's own configuration file and never the machine-wide one.
type CaptureOptions struct {
	AllPlugins bool
}

// CapturedSocket is one plugin socket a capture froze, with the attachments it
// froze there.
//
// Field is the configuration key the attachments were written under, in the
// authored spelling a reader will find in the file — which is not always the
// socket's own name, because the provider socket is written under "providers".
// Socket is carried beside it rather than re-derived, so a consumer that needs to
// know which socket this is does not have to map the key back.
type CapturedSocket struct {
	Field  string                      `json:"field"`
	Socket workspaceconfig.Socket      `json:"socket"`
	Refs   []workspaceconfig.PluginRef `json:"refs"`
}

// CaptureResult is what a capture froze and where.
//
// Path is the configuration file that was written, so a caller holding a document
// model can open exactly what the operation produced. It is always this
// workspace's own file.
//
// Sockets and CatalogDirs are the captured content as values rather than as the
// lines a terminal prints, in the fixed order the write used, so a surface renders
// them its own way and two surfaces cannot disagree about what was frozen. Both
// are empty when the workspace had nothing build-critical, which is a real answer
// rather than a failure: the file is left as it stands and the emptiness is what
// says so.
type CaptureResult struct {
	Path        string           `json:"path"`
	Sockets     []CapturedSocket `json:"sockets"`
	CatalogDirs []string         `json:"catalogDirs"`
}

// Captured reports whether the capture froze anything at all.
//
// It is a method rather than a field so it cannot fall out of step with the two
// content fields it reads: a stored boolean would be a second statement of what
// those fields already say, and the copy is what goes stale.
func (r CaptureResult) Captured() bool {
	return len(r.Sockets) > 0 || len(r.CatalogDirs) > 0
}

// Capture freezes the effective build-critical configuration into the resolved
// workspace's own configuration file and reports what it froze and where.
//
// Each field is written at its effective value — the value that resolves with the
// global configuration and the workspace tree merged together — so a binding that
// today comes only from the global configuration is materialized into the
// workspace and travels with the project. Machine-wide tooling settings, a
// registry's trust policy among them, are not build-critical and are left where
// they are.
//
// Re-running it over an unchanged effective configuration rewrites the same file
// byte-for-byte, because the output is a pure function of the effective values in
// a fixed field order.
//
// A resolution that found no workspace and was handed none
// ([workspaceconfig.ResolvedWorkspace.FoundNoWorkspace]) is refused before
// anything is written. Its root is only the folder the resolution started from,
// and writing a configuration file there would make that folder a workspace as a
// side effect of asking what to freeze into one.
func Capture(rw *workspaceconfig.ResolvedWorkspace, opts CaptureOptions) (CaptureResult, error) {
	var res CaptureResult
	rw, err := workspaceOrRefuse(rw)
	if err != nil {
		return res, err
	}
	if rw.FoundNoWorkspace() {
		return res, fmt.Errorf("aggrega config capture: nothing was written, because %s is not inside an Aggrega "+
			"workspace and a capture writes only into a workspace's own %s", rw.Root(), workspaceconfig.ConfigFilename)
	}

	dest, err := rw.WorkspaceConfigDestination()
	if err != nil {
		return res, fmt.Errorf("aggrega config capture: %w", err)
	}
	effective := rw.Config()

	if err := workspaceconfig.CaptureBuildCritical(dest, effective, opts.AllPlugins); err != nil {
		return res, fmt.Errorf("aggrega config capture: write %s: %w", dest, err)
	}

	res.Path = dest
	res.Sockets = capturedSockets(effective, opts.AllPlugins)
	res.CatalogDirs = effective.Workspace.CatalogDirs
	return res, nil
}

// capturedSockets enumerates the plugin sockets a capture of cfg froze, in the
// fixed socket order, skipping a socket that contributed no attachment.
//
// The tier filter is what makes the default narrow: membership is per-attachment,
// so for the governance socket only the label-transform attachments are
// build-critical and its verdict attachments are excluded — they shape no output
// byte. Widening to every socket is the caller's explicit choice.
//
// 🔴 It is read as the answer to "what did the write freeze", so it must stay in
// step with the write rather than describe it independently: a socket named here
// that the write skipped, or skipped here that the write froze, is a report about
// a file that does not say what it claims. The two agree today because both drop a
// socket with no attachments after the same tier filter. Making that structural
// needs the enumeration itself to be reachable from outside the configuration
// package, which it is not — the classification lives there, unexported, and this
// is the third caller that has had to restate it.
func capturedSockets(cfg workspaceconfig.Config, allPlugins bool) []CapturedSocket {
	var out []CapturedSocket
	for _, ps := range cfg.PluginSockets() {
		refs := ps.Refs
		if !allPlugins {
			refs = ps.RefsByTier(workspaceconfig.SocketTierMutating)
		}
		if len(refs) == 0 {
			continue
		}
		field := string(ps.Socket)
		if ps.Socket == workspaceconfig.SocketProvider {
			field = "providers"
		}
		out = append(out, CapturedSocket{Field: field, Socket: ps.Socket, Refs: refs})
	}
	return out
}
