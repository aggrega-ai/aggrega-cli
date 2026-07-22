// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import "github.com/aggrega-ai/aggrega-cli/pkg/plugintype"

// Socket names one of the workspace-level Wasm plugin sockets. Every socket
// takes the unified PluginRef attachment shape; the socket determines which
// plugin type is expected and how an unconfigured socket behaves.
type Socket string

// socketName returns the workspace socket name a plugin type attaches at, read
// from the canonical plugin-type registry so the literal lives once. It panics
// if the type is absent from the registry or attaches at no socket — a static
// programming error, since the caller passes a known socket-bearing plugin-type
// constant.
func socketName(t plugintype.Type) string {
	e, ok := plugintype.Lookup(t)
	if !ok || e.Socket == "" {
		panic("workspaceconfig: no workspace socket for plugin type " + string(t))
	}
	return e.Socket
}

// The socket names read from the canonical plugin-type registry so each literal
// lives once — the registry is the single home of the socket-name vocabulary.
var (
	// SocketGovernance is the governance socket: one socket serving the pipeline
	// transform and verdict roles, each attachment naming the role it is bound to.
	SocketGovernance = Socket(socketName(plugintype.Governance))
	// SocketLinter is the publishing-linter socket (default export: lint).
	SocketLinter = Socket(socketName(plugintype.Linter))
	// SocketImporter is the import socket (default export: import).
	SocketImporter = Socket(socketName(plugintype.Importer))
	// SocketProvider is the provider-rendering socket that binds render-engine
	// capabilities to the plugins serving them.
	SocketProvider = Socket(socketName(plugintype.Provider))
)

// SocketPrefersCapabilityDefault reports whether an unconfigured socket prefers
// a capable embedded plugin (opt-in) rather than staying a no-op (opt-out). It
// is the single home for the per-socket policy so the choice is read from one
// table rather than re-decided at each socket's resolution site:
//
//   - Provider and linter opt IN: an unconfigured provider capability resolves
//     to the embedded plugin serving it, and an unconfigured linter socket
//     falls back to the embedded publishing-linter.
//   - Governance and importer opt OUT: an unconfigured socket runs no
//     plugin, so a build with the socket unset is byte-identical to a build
//     without the socket.
//
// The mechanism is uniform — one flag over one resolution path — rather than a
// bespoke fallback per socket.
func SocketPrefersCapabilityDefault(s Socket) bool {
	switch s {
	case SocketProvider, SocketLinter:
		return true
	default:
		return false
	}
}
