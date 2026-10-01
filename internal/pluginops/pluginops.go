// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pluginops holds the plugin operations every caller-facing surface
// reaches: adding a plugin of a named type, removing one, aligning a plugin
// entity's machine-owned records with its module, and generating Plugin catalog
// entities from guest metadata plus an authored overlay.
//
// The package exists because the logic these operations carry used to sit in the
// command-line package, which is package main and therefore unimportable by any
// other package — a language rule rather than a convention. A surface that is not
// the command line could not reach the code even though the code was right there.
//
// One package carries all four rather than one package per operation, because the
// socket vocabulary is genuinely shared: the type argument every add and every
// removal accepts, the provider socket that binds a capability rather than
// attaching to a socket list, and the ordered hint an unknown type is reported
// against. Four packages would either duplicate that vocabulary — the
// dis-unification a second copy always is, invisible for exactly as long as the
// copies agree — or force a shared package anyway.
//
// Each operation takes an Options value and returns a Result, and takes the
// resolved workspace as an explicit parameter rather than as a field. The
// workspace is at a named parameter so a caller that has not resolved one cannot
// omit it silently: every call site names a value there, and a nil is refused
// below rather than repaired, because resolving one here from the current
// directory would make the answer depend on a directory no caller named.
//
// A Result carries what the operation did rather than the prose describing it.
// The command line renders its own sentence from those fields and a protocol
// surface serializes them, so neither reads the other's presentation, and the
// operation itself has no opinion about which surface asked.
package pluginops

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ProviderSocket is the type argument that binds a capability to a
// provider-rendering plugin (the plugins.providers list), as opposed to the
// config sockets that take a bare plugin attachment.
const ProviderSocket = "provider"

// configSocketKinds is the set of workspace-level config sockets a plugin
// attaches to by its bare entity identity. It is the plugin-type registry's
// socket-attached subset minus the provider socket, which is handled separately
// because it binds a capability rather than attaching a plugin to a socket list.
var configSocketKinds = buildConfigSocketKinds()

// buildConfigSocketKinds derives the config-socket membership set from the
// canonical plugin-type registry so the socket vocabulary lives once: every
// plugin type that names a workspace socket, except the provider socket. The
// result is a membership set read by key, never range-iterated for output.
func buildConfigSocketKinds() map[string]bool {
	kinds := map[string]bool{}
	for _, e := range plugintype.Entries() {
		if e.Socket != "" && e.Socket != ProviderSocket {
			kinds[e.Socket] = true
		}
	}
	return kinds
}

// IsConfigSocket reports whether the type argument names a workspace-level config
// socket a plugin attaches to by its bare entity identity.
func IsConfigSocket(socket string) bool { return configSocketKinds[socket] }

// KindsHint is the ordered list of accepted type arguments an unknown type is
// reported against: the provider socket, the config sockets, then the cli type.
// The kind names are sourced from the plugin-type registry so the vocabulary
// lives once; the order is the fixed presentation order the surfaces show, which
// differs from the registry's author order.
func KindsHint() string {
	kinds := []string{
		ProviderSocket,
		string(workspaceconfig.SocketGovernance),
		string(workspaceconfig.SocketLinter),
		string(workspaceconfig.SocketImporter),
		string(wasm.PluginTypeCLI),
	}
	return strings.Join(kinds, ", ")
}

// GovernanceRolesHint names every governance attachment role, in the order the
// role vocabulary declares them, as one English alternative: the wording the
// command line and the daemon state the accepted roles in, whether offering them
// in help or refusing a request that named none of them. It reads that
// vocabulary rather than listing the roles, because a list written into a
// message keeps reading as complete after a role is added, so the message would
// stop offering a value the configuration accepts and nothing would fail.
func GovernanceRolesHint() string {
	roles := plugintype.GovernanceRoles()
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = string(r)
	}
	return choiceList(names)
}

// choiceList joins choices as an English alternative: "a", "a or b", and
// "a, b, or c" from three or more.
func choiceList(choices []string) string {
	switch len(choices) {
	case 0:
		return ""
	case 1:
		return choices[0]
	case 2:
		return choices[0] + " or " + choices[1]
	}
	last := len(choices) - 1
	return strings.Join(choices[:last], ", ") + ", or " + choices[last]
}

// workspaceOrRefuse returns the resolved workspace the caller threaded in.
//
// A nil workspace is a wiring error — the call was reached without any caller
// having resolved one — so it is reported rather than repaired. Resolving here
// from the current directory would make the result depend on a directory no
// caller named, which is exactly the dependency routing every surface through one
// resolution point exists to remove.
func workspaceOrRefuse(rw *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if rw == nil {
		return nil, fmt.Errorf("internal: no resolved workspace supplied — the caller must resolve one and pass it in")
	}
	return rw, nil
}

// classifySocket reports what kind of attachment the type argument names, or an
// error naming the accepted set when it names none. The three-way answer is what
// the add and remove paths both branch on, so the vocabulary is read once here
// rather than re-tested at each site.
func classifySocket(command, socket string) (isProvider, isCLI bool, err error) {
	isProvider = socket == ProviderSocket
	isCLI = socket == string(wasm.PluginTypeCLI)
	if !isProvider && !isCLI && !configSocketKinds[socket] {
		return false, false, fmt.Errorf("%s: unknown type %q; expected one of %s", command, socket, KindsHint())
	}
	return isProvider, isCLI, nil
}
