// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
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
	// OpConfigSocket attaches a plugin to a workspace config socket (governance,
	// linter, or importer). A socket attachment shapes a project's
	// build, so it defaults to the workspace config.
	OpConfigSocket
	// OpCLI installs a CLI-extension plugin. A CLI extension is machine-wide
	// tooling and defaults to the global scope, so an install naming neither
	// override registers the command for every project on the machine.
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
// every workspace-tree config, on the platform this program runs on: the
// explicit AGGREGA_CONFIG path when that variable is set, otherwise the
// platform's default location (machinedirs.GlobalConfigFile). It reports false
// only when neither resolves, so a caller writing the global layer surfaces an
// actionable error instead of writing to a bogus relative path. Every
// environment read goes through lookup so resolution is deterministic and
// parallel-safe; pass nil to read the live process environment.
//
// A caller holding a resolution asks it instead ([ResolvedWorkspace.GlobalConfigFile]),
// which answers for the platform and the environment that resolution was taken
// against.
func GlobalConfigPath(lookup func(string) string) (string, bool) {
	if lookup == nil {
		lookup = os.Getenv
	}
	return globalConfigPath(machinedirs.Host(), lookup)
}

// globalConfigPath is the single home of the global file's location: the
// AGGREGA_CONFIG path when lookup carries one, otherwise the default location p
// keeps it in.
func globalConfigPath(p machinedirs.Platform, lookup func(string) string) (string, bool) {
	if v := lookup(EnvConfigOverride); v != "" {
		return v, true
	}
	return machinedirs.GlobalConfigFile(p, lookup)
}

// errNoGlobalConfigLocation is the answer when no location resolves for the
// global configuration file. It names the variables in words that read the same
// on every platform — no shell's variable syntax — because the reader may be at
// a Windows prompt or a Unix one.
var errNoGlobalConfigLocation = errors.New("no location resolves for the global configuration file: " +
	"pass --config, or set the " + EnvConfigOverride + " environment variable, or APPDATA on Windows / " +
	"XDG_CONFIG_HOME or HOME elsewhere")

// WriteDestination resolves the config file a mutating operation writes to. The
// operation's nature sets the default (tool → global, build → workspace); the
// global and workspace overrides, when set, force the destination regardless of
// nature. Setting both overrides is a usage error. The global destination is
// GlobalConfigPath (the AGGREGA_CONFIG path or the default location of the
// platform this program runs on); the workspace destination is ConfigFilename at
// root, the resolved workspace root. lookup reads the environment for the
// global-path resolution; pass nil for the live process environment.
//
// This is the write-target POLICY, and it is not the form a command reaches for:
// taking the root and the environment reader as parameters, it lets a caller
// resolve a destination against a different environment view than the one its
// configuration was read through. The method below binds both to one resolution
// and is the form every config-mutating command uses.
func WriteDestination(op ConfigOperation, global, workspace bool, root string, lookup func(string) string) (string, error) {
	if lookup == nil {
		lookup = os.Getenv
	}
	return writeDestination(op, global, workspace, root, machinedirs.Host(), lookup)
}

// writeDestination is the write-target policy for a stated platform; see
// WriteDestination.
func writeDestination(op ConfigOperation, global, workspace bool, root string, p machinedirs.Platform, lookup func(string) string) (string, error) {
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
		path, ok := globalConfigPath(p, lookup)
		if !ok {
			return "", errNoGlobalConfigLocation
		}
		return path, nil
	}
	return workspaceConfigDestination(root)
}

// workspaceConfigDestination is the workspace configuration file at root as a
// place a configuration write goes: the file, held to the workspace before
// anything is written to it.
//
// The file is one the product composes inside the workspace, so a link standing
// in its place and leading out of the workspace would turn a configuration write
// into rewriting a file elsewhere on the machine; such a destination is refused
// rather than returned. The global file is not held to anything, because it is
// the machine's own configuration rather than the workspace's.
func workspaceConfigDestination(root string) (string, error) {
	path := filepath.Join(root, ConfigFilename)
	if err := writescope.CheckWrite(root, path); err != nil {
		return "", err
	}
	return path, nil
}

// WorkspaceConfigDestination is the workspace configuration file of THIS
// resolution as a place a configuration write goes, for an operation that writes
// that file whatever the operation's nature — the capture of the build-critical
// configuration. It is held to the workspace exactly as [ResolvedWorkspace.WriteDestination]
// holds a workspace destination.
func (r *ResolvedWorkspace) WorkspaceConfigDestination() (string, error) {
	return workspaceConfigDestination(r.root)
}

// WriteDestination resolves the config file a mutating operation writes to,
// against THIS resolution's own root and its own view of the environment.
//
// This is the single write-target site: every config-mutating command routes
// through it, so the write lands in the file the configuration was read from and
// the write sites cannot drift. The reason is a correctness one rather than
// convenience. The free function above takes the environment reader as a
// parameter, so a caller holding a resolution can pass a different one — or nil,
// which reads the live process environment — and then write to a global file the
// resolution never read. The two come apart whenever the resolution was directed
// at a particular global file rather than at the ambient one, which is exactly
// what a caller does when it points the global layer somewhere explicit.
//
// Reading a configuration from one file and writing the change to another is a
// silent wrong answer: the operation reports success, names the file it wrote,
// and the setting the caller asked to change is not the one the next read will
// see.
func (r *ResolvedWorkspace) WriteDestination(op ConfigOperation, global, workspace bool) (string, error) {
	return writeDestination(op, global, workspace, r.root, r.machinePlatform(), r.environment())
}

// GlobalConfigFile returns the global configuration file THIS resolution reads
// beneath its cascade and a global write goes to: the file --config or
// AGGREGA_CONFIG names, otherwise the default location on the platform the
// resolution was taken for. When no location resolves the error says, in words
// valid on every platform, what to set.
//
// It answers whether or not the file exists, because a caller about to create
// the file needs its location as much as one about to read it.
func (r *ResolvedWorkspace) GlobalConfigFile() (string, error) {
	path, ok := globalConfigPath(r.machinePlatform(), r.environment())
	if !ok {
		return "", errNoGlobalConfigLocation
	}
	return path, nil
}

// MachineCacheRoot returns the root the machine-wide caches are kept under, on
// the platform and in the environment THIS resolution was taken against
// (machinedirs.CacheRoot). When no root resolves the error names what is
// missing, and the caches are then kept in memory for the run alone.
func (r *ResolvedWorkspace) MachineCacheRoot() (string, error) {
	return machinedirs.CacheRoot(r.machinePlatform(), r.environment())
}

// environment is the lookup this resolution read the environment through, or
// the process's own when it carries none.
func (r *ResolvedWorkspace) environment() func(string) string {
	if r.lookup == nil {
		return os.Getenv
	}
	return r.lookup
}

// machinePlatform is the platform this resolution was taken for, or the host's
// when it records none.
func (r *ResolvedWorkspace) machinePlatform() machinedirs.Platform {
	if r.platform == "" {
		return machinedirs.Host()
	}
	return r.platform
}
