// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

// Socket names one of the workspace-level Wasm plugin sockets. Every socket
// takes the unified PluginRef attachment shape; the socket determines which
// plugin type is expected and how an unconfigured socket behaves.
type Socket string

const (
	// SocketMutator is the label-mutation socket (default export: mutate).
	SocketMutator Socket = "mutator"
	// SocketPolicy is the policy-validation socket (default export: validate).
	SocketPolicy Socket = "policy"
	// SocketLinter is the publishing-linter socket (default export: lint).
	SocketLinter Socket = "linter"
	// SocketImporter is the import socket (default export: import).
	SocketImporter Socket = "importer"
	// SocketProvider is the provider-rendering socket that binds render-engine
	// capabilities to the plugins serving them.
	SocketProvider Socket = "provider"
)

// SocketPrefersCapabilityDefault reports whether an unconfigured socket prefers
// a capable embedded plugin (opt-in) rather than staying a no-op (opt-out). It
// is the single home for the per-socket policy so the choice is read from one
// table rather than re-decided at each socket's resolution site:
//
//   - Provider and linter opt IN: an unconfigured provider capability resolves
//     to the embedded plugin serving it, and an unconfigured linter socket
//     falls back to the embedded publishing-linter.
//   - Mutator, policy, and importer opt OUT: an unconfigured socket runs no
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
