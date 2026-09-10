// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// RemoveOptions is what a removal decides, independently of who asked for it.
//
// Socket is the plugin type the entry was attached under and Query identifies
// the entry to remove: the plugin's short name, its full pinned reference, or —
// for the provider type — the capability the binding serves.
//
// Global and Workspace are the write-target overrides. A provider or socket
// attachment defaults to this project's workspace configuration and a cli
// command defaults to the global one; leaving both unset takes the operation's
// own default. Setting both is a usage error the write-target policy refuses.
type RemoveOptions struct {
	Socket string
	Query  string

	Global    bool
	Workspace bool
}

// RemoveResult is what a removal did: the type it removed, the query it matched,
// and the configuration file it wrote.
//
// ConfigIsMachineWide is stated rather than left to be inferred. A cli command
// defaults to the global configuration file every workspace on the machine
// resolves through, so a caller that opened a session over one workspace would
// otherwise read the removal as being about that workspace, and for that default
// the reading is wrong.
type RemoveResult struct {
	Socket              string `json:"socket"`
	Query               string `json:"query"`
	ConfigPath          string `json:"configPath"`
	ConfigIsMachineWide bool   `json:"configIsMachineWide"`
}

// Remove drops a configured plugin of the named type from a configuration file so
// it no longer participates: a plugins.<type> list entry, a plugins.providers
// capability binding, or a plugins.cli command.
//
// No registry is reached and nothing is fetched: a removal operates on the
// configuration text alone.
//
// It halts loudly when the type is unknown or when the query matches no entry, so
// a removal is never a silent no-op and a typo never removes the wrong plugin. The
// write destination is resolved off the supplied resolution, so the removal is
// applied to the configuration file that resolution read rather than to whichever
// file the ambient environment names.
func Remove(rw *workspaceconfig.ResolvedWorkspace, opts RemoveOptions) (RemoveResult, error) {
	const command = "aggrega plugin remove"

	var res RemoveResult
	rw, err := workspaceOrRefuse(rw)
	if err != nil {
		return res, err
	}

	socket := opts.Socket
	isProvider, isCLI, err := classifySocket(command, socket)
	if err != nil {
		return res, err
	}

	op := workspaceconfig.OpConfigSocket
	switch {
	case isProvider:
		op = workspaceconfig.OpProviderBinding
	case isCLI:
		op = workspaceconfig.OpCLI
	}
	configPath, machineWide, err := writeTarget(rw, op, opts.Global, opts.Workspace)
	if err != nil {
		return res, err
	}

	var removed bool
	switch {
	case isProvider:
		removed, err = workspaceconfig.RemoveProviderBinding(configPath, opts.Query)
	case isCLI:
		removed, err = workspaceconfig.RemoveCLIPluginRef(configPath, opts.Query)
	default:
		removed, err = workspaceconfig.RemovePluginSocketRef(configPath, socket, opts.Query)
	}
	if err != nil {
		return res, fmt.Errorf("%s: write %s: %w", command, configPath, err)
	}
	if !removed {
		return res, fmt.Errorf("%s: no %s plugin matching %q in %s", command, socket, opts.Query, configPath)
	}

	return RemoveResult{
		Socket:              socket,
		Query:               opts.Query,
		ConfigPath:          configPath,
		ConfigIsMachineWide: machineWide,
	}, nil
}

// writeTarget resolves the configuration file a plugin mutation writes to and
// reports whether that file is the machine-wide one.
//
// The path comes from the single write-target site, so the write lands in the file
// the supplied resolution read. The reach is then read OFF that resolved path
// rather than re-derived from the override flags: the flags are only two of the
// policy's three inputs — the operation's own nature is the third — so a
// classification computed from the flags alone would be right until any one of
// them moved. Comparing the resolved path against this resolution's own workspace
// file cannot come apart from the policy, because it observes the policy's answer.
func writeTarget(rw *workspaceconfig.ResolvedWorkspace, op workspaceconfig.ConfigOperation, global, workspace bool) (path string, machineWide bool, err error) {
	path, err = rw.WriteDestination(op, global, workspace)
	if err != nil {
		return "", false, err
	}
	return path, path != filepath.Join(rw.Root(), workspaceconfig.ConfigFilename), nil
}
