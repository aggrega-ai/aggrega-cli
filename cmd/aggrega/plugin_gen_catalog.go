// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// genCatalogHeader is the two-part banner every generated Plugin entity
// carries: the do-not-edit marker (the entity is machine-derived from the
// guest's describe() self-report and an authored overlay, so hand edits are
// overwritten on the next generation) followed by the standard license header.
// The editable input is the overlay, not this output.
const genCatalogHeader = "# generated; DO NOT EDIT\n" +
	"# SPDX-License-Identifier: Apache-2.0\n" +
	"# Copyright 2026 Cognitio S.r.l.\n"

// genCatalogPluginFilename is the kind-conventional filename for a Plugin
// entity within its per-version directory.
const genCatalogPluginFilename = "plugin.yaml"

// genCatalogOptions carries the parsed gen-catalog flags.
type genCatalogOptions struct {
	fromEmbedded bool
	pluginPath   string
	outputDir    string
	overlayPath  string
}

// newPluginGenCatalogCmd builds the `plugin gen-catalog` subcommand. It emits a
// deterministic, idempotent Plugin entity for each selected guest by merging the
// machine-derived contract fields from the guest's describe() self-report with
// an authored per-plugin overlay (identity, runtime limits, provider block) and
// the binary's own build version. Re-running over the same inputs produces
// byte-identical output.
func newPluginGenCatalogCmd() *cobra.Command {
	opts := genCatalogOptions{}
	c := &cobra.Command{
		Use:   "gen-catalog",
		Short: "Generate Plugin catalog entities from guest describe() plus an authored overlay",
		Long: `Generate one Plugin entity per selected Wasm guest.

Each entity's contract fields — pluginType, abiVersion, capabilities, exports,
and contract — are derived from the guest's describe() self-report, so the
entity can never promise a capability the binary does not provide. The entity's
identity (metadata.name), runtime limits, and provider block come from an authored
per-plugin overlay; the entity version is the binary's own build version. The
plugin-level description, when the guest reports one, is emitted as
metadata.description and omitted cleanly when it does not.

Two selection modes:

  --from-embedded   generate an entity for every guest embedded in this binary.
                    The overlay path is a directory; each overlay is matched to
                    a guest by its metadata.name identity.
  --plugin <f.wasm> generate a single entity for one non-embedded guest. The
                    overlay path is a single overlay file.

The output is written under <output>/<name>/<version>/plugin.yaml and carries a
generated-output banner. Regenerating over unchanged inputs yields byte-identical
files.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return genCatalogRun(cmd.Context(), opts, cmd.OutOrStdout())
		},
	}
	c.Flags().BoolVar(&opts.fromEmbedded, "from-embedded", false, "generate an entity for every guest embedded in this binary")
	c.Flags().StringVar(&opts.pluginPath, "plugin", "", "generate a single entity for the .wasm guest at this path")
	c.Flags().StringVarP(&opts.outputDir, "output", "o", "", "directory the generated entities are written under (required)")
	c.Flags().StringVar(&opts.overlayPath, "overlay", "", "authored overlay directory (--from-embedded) or file (--plugin) (required)")
	return c
}

// genCatalogRun validates the flag combination and dispatches to the embedded or
// single-guest generation path.
func genCatalogRun(ctx context.Context, opts genCatalogOptions, out io.Writer) error {
	switch {
	case opts.fromEmbedded && opts.pluginPath != "":
		return fmt.Errorf("aggrega plugin gen-catalog: --from-embedded and --plugin are mutually exclusive")
	case !opts.fromEmbedded && opts.pluginPath == "":
		return fmt.Errorf("aggrega plugin gen-catalog: one of --from-embedded or --plugin <path> is required")
	}
	if opts.outputDir == "" {
		return fmt.Errorf("aggrega plugin gen-catalog: --output <dir> is required")
	}
	if opts.overlayPath == "" {
		return fmt.Errorf("aggrega plugin gen-catalog: --overlay is required (a directory for --from-embedded, a file for --plugin)")
	}

	version := collectVersionInfo().Version

	if opts.fromEmbedded {
		return genCatalogFromEmbedded(ctx, opts, version, out)
	}
	return genCatalogFromPlugin(ctx, opts, version, out)
}

// genCatalogFromEmbedded generates an entity for every embedded guest, matching
// each guest to its overlay by the guest's bare name — the final segment of the
// embed-tier identity coordinate <pluginType>/<name>, which is the identity an
// overlay authors as metadata.name. The coordinate the guest's own pluginType
// derives is verified against the embed-tier identity, so an overlay filed under
// the wrong socket halts rather than generating under a mismatched identity. A
// guest with no matching overlay halts loudly: the overlay set is the authored
// input the generator merges, so a missing one is an authoring gap, not a value
// to invent.
func genCatalogFromEmbedded(ctx context.Context, opts genCatalogOptions, version string, out io.Writer) error {
	overlays, err := loadOverlaysByPlugin(opts.overlayPath)
	if err != nil {
		return err
	}
	for _, uri := range embedded.URIs() {
		desc, description, err := embeddedDescriptor(ctx, uri)
		if err != nil {
			return err
		}
		// The host-function set is derived from the guest's own .wasm import
		// section — the same primitive publish and pull verify against — so the
		// generated entity carries the record its binary implies and needs no
		// separate recording step before it is publish-ready. The bytes are
		// available instantiation-free from the embedded set.
		wasmBytes, ok := embedded.Lookup(uri)
		if !ok {
			return fmt.Errorf("aggrega plugin gen-catalog: embedded guest %q has no artifact", uri)
		}
		hostFunctions, err := wasm.DeriveHostFunctions(uri, wasmBytes)
		if err != nil {
			return fmt.Errorf("aggrega plugin gen-catalog: %w", err)
		}
		bareName := coordinateLeaf(uri)
		overlay, ok := overlays[bareName]
		if !ok {
			return fmt.Errorf("aggrega plugin gen-catalog: no overlay in %q has metadata.name %q for embedded guest %q; author the overlay before generating", opts.overlayPath, bareName, uri)
		}
		if got := types.PluginCoordinate(string(desc.PluginType), overlay.Metadata.Name); got != uri {
			return fmt.Errorf("aggrega plugin gen-catalog: overlay for %q derives identity coordinate %q, which does not match embedded guest %q; fix the overlay's metadata.name or socket", bareName, got, uri)
		}
		if err := generateAndWrite(opts.outputDir, desc, description, hostFunctions, overlay, version, uri, out); err != nil {
			return err
		}
	}
	return nil
}

// coordinateLeaf returns the bare name segment of an identity coordinate — the
// text after the final "/". The embed tier keys guests under the derived
// coordinate <pluginType>/<name>; an overlay authors the bare <name>, so the leaf
// is the join key between them.
func coordinateLeaf(coordinate string) string {
	if i := strings.LastIndex(coordinate, "/"); i >= 0 {
		return coordinate[i+1:]
	}
	return coordinate
}

// genCatalogFromPlugin generates a single entity for one non-embedded guest read
// from disk, described through the Wasm loader, and merged with the single
// overlay file.
func genCatalogFromPlugin(ctx context.Context, opts genCatalogOptions, version string, out io.Writer) error {
	wasmBytes, err := os.ReadFile(opts.pluginPath)
	if err != nil {
		return fmt.Errorf("aggrega plugin gen-catalog: read .wasm %s: %w", opts.pluginPath, err)
	}
	desc, err := describeWasmBytes(ctx, opts.pluginPath, wasmBytes)
	if err != nil {
		return err
	}
	overlay, err := loadOverlayFile(opts.overlayPath)
	if err != nil {
		return err
	}
	// The host-function set is derived from the guest's own .wasm import section
	// — the same primitive publish and pull verify against — so the generated
	// entity is publish-ready with no separate recording step.
	hostFunctions, err := wasm.DeriveHostFunctions(opts.pluginPath, wasmBytes)
	if err != nil {
		return fmt.Errorf("aggrega plugin gen-catalog: %w", err)
	}
	// A single .wasm has no repository identity of its own; the loader-returned
	// descriptor carries no plugin-level description field until the ABI grows
	// one, so the single-guest path emits a description only once that field
	// lands. The embedded path reads the description from the build-extracted
	// descriptor JSON.
	return generateAndWrite(opts.outputDir, desc, "", hostFunctions, overlay, version, opts.pluginPath, out)
}

// generateAndWrite merges one descriptor with its overlay and writes the entity
// to <outputDir>/<pluginType>/<name>/<version>/plugin.yaml — the plugin
// coordinate path that mirrors the catalog filesystem layout.
func generateAndWrite(outputDir string, desc wasm.PluginDescriptor, description string, hostFunctions []string, overlay types.Plugin, version, identity string, out io.Writer) error {
	entity, err := entitysynth.MergeForPublish(desc, description, hostFunctions, overlay, version, identity)
	if err != nil {
		return fmt.Errorf("aggrega plugin gen-catalog: %w", err)
	}
	coordinate := entity.Coordinate()
	dir := filepath.Join(outputDir, filepath.FromSlash(coordinate), entity.Metadata.Version)
	path := filepath.Join(dir, genCatalogPluginFilename)
	// The assembled entity's spec.provider.workloadSchema must satisfy the same
	// structural contract the catalog loader enforces at load: a provider block
	// with a non-empty workloadSchema must parse as YAML and compile to JSON
	// Schema 2020-12. Reusing the loader's own validator keeps this single-sourced
	// with the load-time gate, so the generator is structurally incapable of
	// shipping a workloadSchema the loader would later reject — an authoring
	// defect in the overlay halts generation loudly instead of riding through.
	if errs := validator.ValidatePluginProviderSchemas(entity.Metadata.Name, path, &entity.Spec); len(errs) > 0 {
		return fmt.Errorf("aggrega plugin gen-catalog: %s has an invalid spec.provider.workloadSchema; fix the overlay: %w", entity.Metadata.Name, errors.Join(errs...))
	}
	body, err := emitGenCatalogEntity(entity)
	if err != nil {
		return fmt.Errorf("aggrega plugin gen-catalog: encode %s: %w", entity.Metadata.Name, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("aggrega plugin gen-catalog: create %s: %w", dir, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("aggrega plugin gen-catalog: write %s: %w", path, err)
	}
	fmt.Fprintf(out, "generated %s\n", path)
	return nil
}

// emitGenCatalogEntity marshals a Plugin entity to deterministic YAML with the
// generated-output banner. The struct field order fixes the key order and the
// encoder's fixed indent fixes the layout, so the same entity marshals to the
// same bytes on every run.
func emitGenCatalogEntity(entity types.Plugin) ([]byte, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(entity); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(genCatalogHeader + b.String()), nil
}

// embeddedDescriptor returns an embedded guest's validated describe() descriptor
// and its plugin-level description. It reads the build-extracted descriptor JSON
// when present — the plugin-level description surfaces through that JSON, so the
// generated entity's description lights up once the guest reports one — and falls
// back to loading and describing the guest when the static descriptor is absent.
func embeddedDescriptor(ctx context.Context, uri string) (wasm.PluginDescriptor, string, error) {
	if raw, ok := embedded.LookupDescriptor(uri); ok {
		desc, description, err := entitysynth.ParseDescriptorJSON(raw)
		if err == nil {
			return desc, description, nil
		}
		// A malformed static descriptor falls through to load-and-describe rather
		// than emitting from a corrupt self-report.
	}
	bytesForGuest, ok := embedded.Lookup(uri)
	if !ok {
		return wasm.PluginDescriptor{}, "", fmt.Errorf("aggrega plugin gen-catalog: embedded guest %q has no artifact", uri)
	}
	desc, err := describeWasmBytes(ctx, uri, bytesForGuest)
	if err != nil {
		return wasm.PluginDescriptor{}, "", err
	}
	return desc, "", nil
}

// describeWasmBytes loads a guest from bytes and returns its validated describe()
// descriptor. The instance is closed before the descriptor is returned; the
// generator needs the self-report, not a running guest.
func describeWasmBytes(ctx context.Context, name string, wasmBytes []byte) (wasm.PluginDescriptor, error) {
	spec := wasm.PluginSpec{Name: genCatalogDiagName(name), Bytes: wasmBytes, MaxMemoryPages: describeMaxMemoryPages}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		return wasm.PluginDescriptor{}, fmt.Errorf("aggrega plugin gen-catalog: describe %s: %w", name, err)
	}
	_ = plugin.Close(ctx)
	return desc, nil
}

// genCatalogDiagName derives a short diagnostic name for a guest reference — the
// final path segment — used only in describe-error messages.
func genCatalogDiagName(ref string) string {
	s := ref
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return ref
	}
	return s
}

// loadOverlaysByPlugin reads every *.yaml overlay under dir and indexes them by
// their metadata.name identity — the join key the embedded path matches a guest
// against. Two overlays declaring the same identity halt loudly: the identity
// must be unique, and silently keeping one would drop the other.
func loadOverlaysByPlugin(dir string) (map[string]types.Plugin, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("aggrega plugin gen-catalog: overlay directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("aggrega plugin gen-catalog: --overlay must be a directory for --from-embedded, got file %s", dir)
	}
	var paths []string
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".yaml" || ext == ".yml" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("aggrega plugin gen-catalog: walk overlay directory %s: %w", dir, err)
	}
	// Sort so a duplicate-identity halt names paths deterministically.
	sort.Strings(paths)

	out := make(map[string]types.Plugin, len(paths))
	for _, path := range paths {
		overlay, err := loadOverlayFile(path)
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(overlay.Metadata.Name)
		if key == "" {
			return nil, fmt.Errorf("aggrega plugin gen-catalog: overlay %s has no metadata.name identity", path)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("aggrega plugin gen-catalog: two overlays declare metadata.name %q; identity must be unique", key)
		}
		out[key] = overlay
	}
	return out, nil
}

// loadOverlayFile reads and parses one overlay file into a Plugin value.
//
// This overlay walk is deliberately independent of the catalog loaders and the
// validate/lint entity readers — do NOT "unify" it into them. It is a
// generator: it unmarshals the full typed Plugin an overlay author wrote, then
// merges it with the describe() contract to emit a catalog entity. The
// validate reader peeks only a document's kind to dispatch validation tiers,
// and the lint reader parses a metadata envelope and re-encodes the whole
// document to JSON for the linter plugin — different projections of the YAML
// for different jobs, not a shared kind-peek. The one correctness primitive
// these paths do share — the provider workloadSchema check — is already
// single-sourced through validator.ValidatePluginProviderSchemas in
// generateAndWrite, so nothing here re-implements a validator.
func loadOverlayFile(path string) (types.Plugin, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return types.Plugin{}, fmt.Errorf("aggrega plugin gen-catalog: read overlay %s: %w", path, err)
	}
	var overlay types.Plugin
	if err := yaml.Unmarshal(raw, &overlay); err != nil {
		return types.Plugin{}, fmt.Errorf("aggrega plugin gen-catalog: parse overlay %s: %w", path, err)
	}
	return overlay, nil
}
