// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigNature classifies a config-mutating operation by the kind of setting it
// writes, which sets the file the write defaults to. A tool-scoped setting is
// shared across every workspace on the machine and defaults to the global
// config; a build-scoped setting is tied to one project's build and defaults to
// that project's workspace config, so it travels with the project.
type ConfigNature int

const (
	// NatureTool is a setting shared across every workspace on the machine.
	// It defaults to the global config file.
	NatureTool ConfigNature = iota
	// NatureBuild is a setting scoped to one project's build. It defaults to
	// the workspace config file so it travels with the project.
	NatureBuild
)

// ConfigOperation names a config-mutating operation. Its nature — read via
// Nature — sets which file the operation writes to by default. This is the
// single classification home: every config-mutating command maps its operation
// to one of these values rather than re-deciding tool-vs-build locally.
type ConfigOperation int

const (
	// OpRegistry registers a registry and its trust policy (the registries
	// list). A registry is machine-wide tooling, so it defaults to the global
	// config.
	OpRegistry ConfigOperation = iota
	// OpProviderBinding binds a capability to a provider-rendering plugin (the
	// plugins.providers list). A provider binding is part of how a project
	// builds, so it defaults to the workspace config.
	OpProviderBinding
	// OpConfigSocket attaches a plugin to a workspace config socket (mutator,
	// policy, linter, or importer). A socket attachment shapes a project's
	// build, so it defaults to the workspace config.
	OpConfigSocket
	// OpCLI installs a CLI-extension plugin. A CLI extension is machine-wide
	// tooling and defaults to the global scope. No config-file write serves
	// this operation today — it is recorded here so its nature is classified in
	// one place with the rest.
	OpCLI
)

// Nature returns the nature that sets op's default write file. Tool-scoped
// operations default to the global config; build-scoped operations default to
// the workspace config.
func (op ConfigOperation) Nature() ConfigNature {
	switch op {
	case OpRegistry, OpCLI:
		return NatureTool
	default:
		return NatureBuild
	}
}

// GlobalConfigPath resolves the path of the global config file that sits beneath
// every workspace-tree config: the explicit $AGGREGA_CONFIG path when that
// variable is set, otherwise the XDG default
// ($XDG_CONFIG_HOME/aggrega/config.yaml, falling back to
// $HOME/.config/aggrega/config.yaml). It reports false only when neither
// $AGGREGA_CONFIG nor a home base directory can be resolved, so a caller writing
// the global layer surfaces an actionable error instead of writing to a bogus
// relative path. Every environment read goes through lookup so resolution is
// deterministic and parallel-safe; pass nil to read the live process
// environment. This is the single home for the global config path — the
// $AGGREGA_CONFIG-else-XDG rule lives here and reuses the XDG path primitive.
func GlobalConfigPath(lookup func(string) string) (string, bool) {
	if lookup == nil {
		lookup = os.Getenv
	}
	if v := lookup(EnvConfigOverride); v != "" {
		return v, true
	}
	return xdgConfigPath(lookup)
}

// WriteDestination resolves the config file a mutating operation writes to. The
// operation's nature sets the default (tool → global, build → workspace); the
// global and workspace overrides, when set, force the destination regardless of
// nature. Setting both overrides is a usage error. The global destination is
// GlobalConfigPath (the $AGGREGA_CONFIG path or the XDG default); the workspace
// destination is ConfigFilename at root, the resolved workspace root. lookup
// reads the environment for the global-path resolution; pass nil for the live
// process environment. This is the single write-target policy: every
// config-mutating command routes through it so the write sites cannot drift.
func WriteDestination(op ConfigOperation, global, workspace bool, root string, lookup func(string) string) (string, error) {
	if global && workspace {
		return "", fmt.Errorf("workspaceconfig: --global and --workspace cannot be combined; choose at most one")
	}

	toGlobal := op.Nature() == NatureTool
	if global {
		toGlobal = true
	}
	if workspace {
		toGlobal = false
	}

	if toGlobal {
		path, ok := GlobalConfigPath(lookup)
		if !ok {
			return "", fmt.Errorf("workspaceconfig: cannot resolve a global config path; set $AGGREGA_CONFIG or a home directory ($HOME / $XDG_CONFIG_HOME)")
		}
		return path, nil
	}
	return filepath.Join(root, ConfigFilename), nil
}

// WriteDestination resolves the config file a mutating operation writes to,
// against THIS resolution's own root and its own view of the environment.
//
// It is the form to reach for wherever a resolved workspace is already in hand,
// and the reason is a correctness one rather than convenience. The free function
// above takes the environment reader as a parameter, so a caller holding a
// resolution can pass a different one — or nil, which reads the live process
// environment — and then write to a global file the resolution never read. The
// two come apart whenever the resolution was directed at a particular global
// file rather than at the ambient one, which is exactly what a caller does when
// it points the global layer somewhere explicit.
//
// Reading a configuration from one file and writing the change to another is a
// silent wrong answer: the operation reports success, names the file it wrote,
// and the setting the caller asked to change is not the one the next read will
// see.
func (r *ResolvedWorkspace) WriteDestination(op ConfigOperation, global, workspace bool) (string, error) {
	return WriteDestination(op, global, workspace, r.root, r.lookup)
}
