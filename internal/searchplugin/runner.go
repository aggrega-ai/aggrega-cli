// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package searchplugin runs a search plugin as a runtime query: it resolves the
// plugin's bytes through the one resolution cascade, loads them under the
// capability-denied build instantiation profile, reads describe() through the
// shared load gate, invokes the guest's search export with a query payload, and
// decodes the ranked results the guest returns.
//
// The runner is a generic dispatcher: it holds zero search-specific logic. It
// does not know what any particular index contains, does not parse the query or
// the guest's index, and does not rank or reorder the results — the guest owns
// the index and the ranking, and the host carries the query in and the ranked
// results out verbatim. It differs from the provider, mutator, and CLI sockets
// only in the three generic socket parameters every socket names: the plugin
// type it accepts, the export it dispatches to, and the wire shapes it marshals.
package searchplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// ErrPluginNotSearch is returned when a resolved plugin is loaded to answer a
// query but its describe() reports a non-search plugin type — for example a
// provider or CLI plugin, which the runtime dispatches through their own sockets
// but which answers no ranked query. The error names the plugin and its actual
// type.
var ErrPluginNotSearch = errors.New("searchplugin: plugin is not a search plugin")

// ErrNoResolver is returned when Search is invoked on a Runner with no Resolve
// seam set, so there is no way to turn the plugin URI into bytes.
var ErrNoResolver = errors.New("searchplugin: no plugin resolver configured")

// Runner dispatches a search plugin. It composes plugin-URI resolution with a
// build-profile Wasm load and invoke; it carries no per-query state, so one
// Runner serves every query.
//
// It is the reusable host entry point a runtime search caller — the query
// command surface or the tool-server search tool — drives: construct a Runner
// with a resolver and call Search. The composition it wraps (resolve, load,
// describe, dispatch) is the same shared loader and gate every other socket goes
// through; the Runner adds no second load path of its own.
type Runner struct {
	// Resolve turns a plugin URI into its resolved bytes through the resolution
	// cascade (typically a build-configured pluginpath.Resolver's Resolve method
	// value). Required — a nil Resolve makes Search return ErrNoResolver.
	Resolve pluginpath.Lookup

	// Loader loads the resolved bytes under the capability-denied build profile.
	// A nil Loader defaults to one built through the shared caching constructor,
	// so it participates in the process-wide compilation cache. The build
	// profile denies the guest filesystem and network access, so the guest
	// answers every query from its embedded index alone — the query path is
	// offline by construction.
	Loader *wasm.Loader
}

// Search resolves the plugin URI, loads it under the capability-denied build
// profile, invokes its search export with in, and returns the guest's ranked
// results. The host preserves the guest's result order — the guest returns
// results already ranked — and interprets none of the fields.
//
// Search returns a non-nil error only for a host-side failure — the URI does not
// resolve, the bytes are not a loadable plugin, the resolved plugin is not of
// search type (ErrPluginNotSearch), the guest traps, or a payload fails to
// encode or decode. A query that matches nothing is not an error: it returns a
// SearchOutput with an empty Results list.
func (r Runner) Search(ctx context.Context, uri string, in abi.SearchInput) (abi.SearchOutput, error) {
	if r.Resolve == nil {
		return abi.SearchOutput{}, ErrNoResolver
	}
	loader := r.Loader
	if loader == nil {
		// Build the default loader through the sanctioned constructor so it
		// shares the process-wide compilation cache. A search query needs no
		// external-input service — the build profile denies the guest any host
		// data surface — so nil is passed for it.
		loader = wasm.NewCachingLoaderFromContext(ctx, nil)
	}

	resolved, err := r.Resolve(uri)
	if err != nil {
		return abi.SearchOutput{}, err
	}
	spec := resolver.PluginSpec(uri, resolved)

	// LoadAndDescribe Closes the plugin itself on any load or describe rejection
	// and returns no live plugin, so the deferred Close is registered only after
	// it succeeds and leaves the plugin live.
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return abi.SearchOutput{}, err
	}
	defer func() { _ = plugin.Close(ctx) }()

	if desc.PluginType != wasm.PluginTypeSearch {
		return abi.SearchOutput{}, fmt.Errorf("searchplugin: plugin %q reports type %q, not %q: %w",
			uri, desc.PluginType, wasm.PluginTypeSearch, ErrPluginNotSearch)
	}

	payload, err := json.Marshal(in)
	if err != nil {
		return abi.SearchOutput{}, fmt.Errorf("searchplugin: encode search input for plugin %q: %w", uri, err)
	}

	raw, err := wasm.InvokeExport(ctx, plugin, desc, wasm.SearchExportName, payload)
	if err != nil {
		return abi.SearchOutput{}, err
	}

	var out abi.SearchOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return abi.SearchOutput{}, fmt.Errorf("searchplugin: decode search output from plugin %q: %w", uri, err)
	}
	return out, nil
}
