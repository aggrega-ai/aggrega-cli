// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// manifestRelPath is the declarative shipped-plugin manifest, relative to the
// module root. The provider set is read from it rather than from a directory
// listing of the declarations, and that is the whole of what makes the published
// matrix complete: a provider that ships without a support claim still gets its
// rows, so an omitted declaration is visible on the page instead of silently
// absent from it.
const manifestRelPath = "internal/wasm/embedded/embedded-plugins.tsv"

// providerSocket is the manifest's socket value for a provider-rendering plugin.
// Rows for other sockets describe plugins that render nothing and read no
// placement annotations.
const providerSocket = "provider"

// overlayRelDir and catalogRelDir locate a provider's two declaration homes
// within the catalog tree, relative to its root.
const (
	overlayRelDir = "plugin-overlays/provider"
	catalogRelDir = "aggrega-catalog/plugin/provider"
)

// generatedEntityRelDir is the directory, relative to the module root, that holds
// the Plugin entity the build generates beside each embedded guest. It is read
// for a shipped provider the published catalog does not carry.
const generatedEntityRelDir = "internal/wasm/embedded/plugins"

// provider is one provider's published annotation support claim, assembled from
// the two homes that hold it.
type provider struct {
	// Name is the provider name a Recipe targets.
	Name string
	// InputKinds is the provider's own render-behavior claim, read from its
	// published entity or, where the catalog carries none, from the entity its
	// build generates. It fixes which rows are answerable at all: a key cannot
	// be applied on an input kind the provider does not render, and a cell for
	// one is not a gap but an absence of a question.
	InputKinds types.PluginInputKinds
	// Annotations is the support claim as authored, in author order.
	Annotations []types.WellKnownAnnotation
}

// scan reads the shipped-plugin manifest under moduleRoot and assembles the
// support claim of every provider it names, in manifest order.
//
// The two homes are read separately, and deliberately so. The support claim is
// authored by hand on the overlay, which is its one home; the render-behavior
// contract is machine-derived from the plugin's own self-report and lands on the
// published entity, which is where a catalog reader finds it. Reading each from
// where it is produced is what keeps this projection from becoming a third place
// either could be written. The published entity is looked for first; a provider
// the catalog does not carry has its contract read from the entity its build
// generates beside its guest (see providerEntity).
//
// Manifest order is author order and is preserved rather than sorted, so a row
// moves only when the shipped set moves.
func scan(moduleRoot, catalogRoot string) ([]provider, error) {
	names, err := providerNames(filepath.Join(moduleRoot, filepath.FromSlash(manifestRelPath)))
	if err != nil {
		return nil, err
	}

	out := make([]provider, 0, len(names))
	for _, name := range names {
		p := provider{Name: name}

		overlay, err := readPlugin(filepath.Join(catalogRoot, filepath.FromSlash(overlayRelDir), name+".yaml"))
		if err != nil {
			return nil, fmt.Errorf("provider %q overlay: %w", name, err)
		}
		p.Annotations = overlay.Spec.Provider.WellKnownAnnotations
		if err := requireScopes(p); err != nil {
			return nil, err
		}

		entityPath, err := providerEntity(moduleRoot, catalogRoot, name)
		if err != nil {
			return nil, err
		}
		entity, err := readPlugin(entityPath)
		if err != nil {
			return nil, fmt.Errorf("provider %q entity: %w", name, err)
		}
		p.InputKinds = entity.Spec.Provider.Contract.InputKinds

		out = append(out, p)
	}
	return out, nil
}

// requireScopes refuses a shipped provider that declares a key without saying
// where it reads it.
//
// This is the completeness rung, and it is deliberately narrower than the
// schema's. The scope is optional on the authored surface, because a provider
// published by anyone else is entitled to make the weaker claim "this engine
// reads this key" without being obliged to say on which path — and obliging it
// would be requiring an author to state something the format does not force. But
// a key with no scope contributes no cell here, so an unscoped declaration in
// what WE ship would silently shrink the published matrix. Holding our own set
// to the stronger claim is the difference between a matrix that is complete and
// one that merely looks it.
//
// What it cannot see, and no layer below the engine can: whether a declared key
// is read at the scope it claims. It sees only that a claim was made.
func requireScopes(p provider) error {
	for _, a := range p.Annotations {
		if len(a.Scope) == 0 {
			return fmt.Errorf("provider %q declares well-known annotation %q with no scope; every key a shipped provider declares states where it is read", p.Name, a.Key)
		}
	}
	return nil
}

// providerNames parses the tab-separated shipped-plugin manifest and returns the
// names of its provider rows, in manifest order.
//
// This reads the same manifest the plugin registry reads, and the duplication is
// deliberate rather than an oversight: that reader is unexported and panics on a
// malformed row, which is right for a compile-time embed and wrong for a
// generator that must report a bad row as an error. The manifest's own header
// records that it is read by several consumers by design.
func providerNames(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin manifest: %w", err)
	}

	var out []string
	for n, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimRight(raw, "\r")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s:%d: row has %d fields, need at least 2", path, n+1, len(fields))
		}
		if strings.TrimSpace(fields[0]) != providerSocket {
			continue
		}
		name := strings.TrimSpace(fields[1])
		if name == "" {
			return nil, fmt.Errorf("%s:%d: provider row missing a name", path, n+1)
		}
		out = append(out, name)
	}
	return out, nil
}

// providerEntity resolves the Plugin entity a provider's render-behavior contract
// is read from. The published entity wins where the catalog carries one. Where it
// carries none, the entity the build generates beside the guest is read, so a
// provider that is shipped but not yet released still gets its rows. With
// neither, the error names both places looked, because either one is what the
// author has to make present.
func providerEntity(moduleRoot, catalogRoot, name string) (string, error) {
	published, found, err := publishedEntity(catalogRoot, name)
	if err != nil {
		return "", err
	}
	if found {
		return published, nil
	}

	generated := filepath.Join(moduleRoot, filepath.FromSlash(generatedEntityRelDir), name+".plugin.yaml")
	_, err = os.Stat(generated)
	switch {
	case err == nil:
		return generated, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("provider %q generated entity: %w", name, err)
	}

	pattern := filepath.Join(catalogRoot, filepath.FromSlash(catalogRelDir), name, "*", "plugin.yaml")
	return "", fmt.Errorf("provider %q is shipped but has no published entity at %s and no generated entity at %s; "+
		"publish the provider's entity to the catalog, or build the provider so its entity is generated beside its guest",
		name, pattern, generated)
}

// publishedEntity resolves the one published entity of a provider within the
// catalog tree. found is false when the catalog carries none; providerEntity
// decides what that absence means.
//
// Exactly one version directory may be present. More than one means this
// projection cannot know which published claim the page describes, and picking
// either would publish a coin flip.
func publishedEntity(catalogRoot, name string) (path string, found bool, err error) {
	pattern := filepath.Join(catalogRoot, filepath.FromSlash(catalogRelDir), name, "*", "plugin.yaml")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", false, fmt.Errorf("provider %q: %w", name, err)
	}
	// Glob order is already lexical, but sorting explicitly says so: the error
	// message below names every candidate, and a message that varied between
	// runs would be a diagnostic nobody could diff.
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, fmt.Errorf("provider %q has %d published entities (%s); this projection describes one",
			name, len(matches), strings.Join(matches, ", "))
	}
}

// readPlugin decodes a Plugin document into the entity type the compiler itself
// uses, so this projection reads the declarations through the same field
// definitions the load path does and cannot acquire a private shape of its own.
func readPlugin(path string) (types.Plugin, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return types.Plugin{}, err
	}
	var p types.Plugin
	if err := yaml.Unmarshal(b, &p); err != nil {
		return types.Plugin{}, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return p, nil
}
