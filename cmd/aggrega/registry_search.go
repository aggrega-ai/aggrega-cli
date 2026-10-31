// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"io"
	"path"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cliPluginSearchKind is the entity kind a search for a CLI plugin narrows to, in
// the spelling the search command's --kind flag takes. The index lookup below and
// the search command the hint prints both read it, so the hint never prints a
// search that asks something other than what the hint asked.
const cliPluginSearchKind = "plugin"

// cliPluginIndexAnswer is what the local registry index says about a name typed
// where a command was expected.
type cliPluginIndexAnswer int

const (
	// noLocalIndex: there is no index of the default registry to read — none has
	// been built, no default registry is configured, no workspace resolved, or the
	// index could not be read.
	noLocalIndex cliPluginIndexAnswer = iota
	// indexHoldsNoCLIPlugin: the index was read and holds no CLI plugin of that name
	// published through the default registry.
	indexHoldsNoCLIPlugin
	// indexHoldsCLIPlugin: the index holds a CLI plugin of that name published
	// through the default registry, which is where 'aggrega plugin add cli' looks.
	indexHoldsCLIPlugin
)

// workingDirectoryWorkspace resolves the workspace the working directory sits in,
// or returns nil where none resolves. It is the live form of the unknown-command
// hint's workspace seam, wired only in the process entry point.
func workingDirectoryWorkspace() *workspaceconfig.ResolvedWorkspace {
	rw, err := workspaceconfig.Resolve(".", "")
	if err != nil {
		return nil
	}
	return rw
}

// cliPluginInLocalIndex reports what the default registry's local index says about
// a CLI plugin named name.
//
// It reads the index through the search 'aggrega search' runs, asking it the
// question the printed search command asks — the name as the query, narrowed to
// the plugin kind and the CLI plugin type — restricted to the default registry,
// which is the registry a plugin addition enumerates. That search reads the
// snapshot 'aggrega registry update' wrote and contacts no network, so neither
// does this, whatever the workspace's offline setting.
//
// A record answers for the name when its repository sits beneath the repository
// path the default registry carries, if any, and its final path segment is the
// name: the same reading of a plugin's name a plugin addition prefers among
// several matches. A search that cannot be performed — no index built, or one
// that cannot be read — is reported as no index, so the hint offers to build one.
func cliPluginInLocalIndex(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, name string) cliPluginIndexAnswer {
	if rw == nil {
		return noLocalIndex
	}
	loc := rw.Config().DefaultRegistryLocation()
	if loc.Host == "" {
		return noLocalIndex
	}
	// The search's notices are commentary on withdrawn versions, which a hint about
	// whether a plugin exists has no use for.
	res, err := registryops.Search(ctx, rw, registryops.Query{
		Text:       name,
		Kinds:      []string{cliPluginSearchKind},
		PluginType: string(wasm.PluginTypeCLI),
		Registry:   loc.Host,
	}, io.Discard, searchNow())
	if err != nil {
		return noLocalIndex
	}
	beneath := loc.Repository("")
	for _, rec := range res.Records {
		repo, ok := strings.CutPrefix(registryindex.ReferenceForURI(rec.URI), loc.Host+"/")
		if ok && strings.HasPrefix(repo, beneath) && path.Base(repo) == name {
			return indexHoldsCLIPlugin
		}
	}
	return indexHoldsNoCLIPlugin
}
