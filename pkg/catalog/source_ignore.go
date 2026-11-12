// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ServingProvider is the provider plugin that serves a capability in a
// workspace: its name, which the pin step records beside an entity, its entity
// reference, which a reader is shown, and the ignore rules it declares for a
// source it renders.
type ServingProvider struct {
	// Name is the plugin's `<pluginType>/<name>`, with no version. It is what
	// the pin step records, so pinning again under another release of the same
	// plugin records the same name.
	Name string
	// Ref is the plugin's entity reference, `<pluginType>/<name>:<version>`.
	Ref string
	// Ignore is what the plugin declares its ecosystem never ships beside a
	// source, and nil where it declares nothing.
	Ignore *types.PluginSourceIgnore
}

// ServingProvider resolves the plugin that serves capability — a Recipe's or
// FeatureRecipe's `capability:`, optionally version-pinned — in this view,
// honouring bindings exactly as a build's own provider resolution does, and
// reports it in the form the pin step records.
//
// It is the provider resolution a build runs, read for a different fact: an
// explicit binding selects its plugin, and otherwise the one plugin serving the
// capability is used, which in an ordinary workspace is the plugin built into the
// binary. So the plugin whose rules an author records is the plugin their own
// build renders with. It reports false where no plugin serves the capability, and
// returns the resolution's own refusal for an ambiguous capability or a binding
// nothing satisfies.
func (l *LazyLookup) ServingProvider(capability string, bindings map[string]string) (ServingProvider, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().servingProvider(capability, bindings)
}

func (c *Catalog) servingProvider(capability string, bindings map[string]string) (ServingProvider, bool, error) {
	p, ok, err := c.ProviderPluginForTarget(capability, bindings)
	if err != nil || !ok {
		return ServingProvider{}, false, err
	}
	return ServingProvider{
		Name:   p.Coordinate(),
		Ref:    p.Coordinate() + ":" + p.Metadata.Version,
		Ignore: p.Spec.Provider.Contract.SourceIgnore,
	}, true, nil
}

// ComposeIgnore builds the ignore section the pin step records for the Recipe or
// FeatureRecipe document at path, from the plugin serving returns for its
// capability — nil where none serves it — and the author's .aggregaignore beside
// the document.
//
// It returns nil where the entity walks no local source directory, because only a
// walked directory has files the rules could leave out, and it asks serving only
// then, so an entity with nothing to walk is never held to its capability
// resolving. It returns nil too where there is neither a serving plugin nor a rule
// to record, which reads exactly as a lock recording no section. The composition
// itself, and the order that gives the author's file the last word, is the walk's
// own (sourcewalk.Compose); what this adds is which directory the entity walks and
// the plugin the rules came from.
func ComposeIgnore(path string, serving func() (*ServingProvider, error)) (*types.IgnoreRules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	walked, walks, err := LocalSourceDirectory(data, path)
	if err != nil || !walks {
		return nil, err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	var served *ServingProvider
	if serving != nil {
		if served, err = serving(); err != nil {
			return nil, err
		}
	}
	var declared *types.PluginSourceIgnore
	var plugin *types.IgnorePlugin
	if served != nil {
		declared = served.Ignore
		plugin = &types.IgnorePlugin{Name: served.Name}
		if declared != nil {
			plugin.Patterns = append([]string(nil), declared.Patterns...)
			plugin.Files = append([]string(nil), declared.Files...)
		}
	}
	rules, err := sourcewalk.Compose(base, walked, declared)
	if err != nil {
		return nil, err
	}
	if plugin == nil && len(rules) == 0 {
		return nil, nil
	}
	return &types.IgnoreRules{Plugin: plugin, Rules: rules}, nil
}
