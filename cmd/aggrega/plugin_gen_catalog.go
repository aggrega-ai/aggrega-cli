// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
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

Each entity's contract fields — pluginType, abiVersion, targets, exports, and
capabilities — are derived from the guest's describe() self-report, so the
entity can never promise a capability the binary does not provide. The entity's
identity (spec.plugin), runtime limits, and provider block come from an authored
per-plugin overlay; the entity version is the binary's own build version. The
plugin-level description, when the guest reports one, is emitted as
metadata.description and omitted cleanly when it does not.

Two selection modes:

  --from-embedded   generate an entity for every guest embedded in this binary.
                    The overlay path is a directory; each overlay is matched to
                    a guest by its spec.plugin identity.
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
// each guest to its overlay by the overlay's metadata.name identity — the same
// bare identity (<socket>/<name>) the embed tier keys the guest under. A guest
// with no matching overlay halts loudly: the overlay set is the authored input
// the generator merges, so a missing one is an authoring gap, not a value to invent.
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
		overlay, ok := overlays[uri]
		if !ok {
			return fmt.Errorf("aggrega plugin gen-catalog: no overlay in %q has metadata.name %q for embedded guest; author the overlay before generating", opts.overlayPath, uri)
		}
		if err := generateAndWrite(opts.outputDir, desc, description, overlay, version, uri, out); err != nil {
			return err
		}
	}
	return nil
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
	// A single .wasm has no repository identity of its own; the loader-returned
	// descriptor carries no plugin-level description field until the ABI grows
	// one, so the single-guest path emits a description only once that field
	// lands. The embedded path reads the description from the build-extracted
	// descriptor JSON.
	return generateAndWrite(opts.outputDir, desc, "", overlay, version, opts.pluginPath, out)
}

// generateAndWrite merges one descriptor with its overlay and writes the entity
// to <outputDir>/<name>/<version>/plugin.yaml.
func generateAndWrite(outputDir string, desc wasm.PluginDescriptor, description string, overlay types.Plugin, version, identity string, out io.Writer) error {
	entity, err := mergeGenCatalogEntity(desc, description, overlay, version, identity)
	if err != nil {
		return err
	}
	dir := filepath.Join(outputDir, filepath.FromSlash(entity.Metadata.Name), entity.Metadata.Version)
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

// mergeGenCatalogEntity assembles a Plugin entity from three sources: the
// describe() contract fields (machine-derived — the compiler-checked truth of
// what the guest does), the authored overlay (identity, runtime limits, provider
// block, and any authored metadata), and the binary's own build version. The
// plugin-level description, when present, becomes metadata.description. The
// contract fields always come from describe(), overriding any the overlay
// carries, so describe() is the single source for the guest's declared behavior;
// the overlay supplies only what describe() does not report.
func mergeGenCatalogEntity(desc wasm.PluginDescriptor, description string, overlay types.Plugin, version, identity string) (types.Plugin, error) {
	if err := desc.Validate(identity); err != nil {
		return types.Plugin{}, fmt.Errorf("aggrega plugin gen-catalog: %w", err)
	}
	if strings.TrimSpace(overlay.Metadata.Name) == "" {
		return types.Plugin{}, fmt.Errorf("aggrega plugin gen-catalog: overlay for %q has no metadata.name identity", identity)
	}

	entity := types.Plugin{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Plugin",
		Metadata:   overlay.Metadata,
		Spec:       types.PluginSpec{},
	}
	// Version is the binary's own build identity; description is the guest's
	// plugin-level self-report. Everything else in metadata is authored.
	entity.Metadata.Version = version
	entity.Metadata.Description = description

	// Identity is metadata.name (copied from the overlay above); runtime limits
	// and the provider block are authored. The machine-owned host-function set and
	// the signature pointer are not the generator's to write — publish derives and
	// stamps the host functions from the .wasm, and signing attaches the signature
	// at push.
	entity.Spec.Runtime = sanitizeOverlayRuntime(overlay.Spec.Runtime)
	entity.Spec.Provider = overlay.Spec.Provider

	// Contract fields are machine-derived from describe().
	entity.Spec.PluginType = string(desc.PluginType)
	entity.Spec.AbiVersion = desc.ABIVersion
	entity.Spec.Targets = desc.CompatibleProviders
	entity.Spec.Exports = descriptorExports(desc.Exports)
	entity.Spec.Capabilities = descriptorCapabilities(desc.Capabilities)

	return entity, nil
}

// sanitizeOverlayRuntime returns the overlay's runtime block with the
// machine-owned host-function set cleared: publish derives it from the .wasm and
// stamps it into the published entity, so the generator never authors it.
func sanitizeOverlayRuntime(rt *types.PluginRuntime) *types.PluginRuntime {
	if rt == nil {
		return nil
	}
	out := *rt
	out.HostFunctions = nil
	return &out
}

// descriptorExports maps the describe() export declarations onto the entity's
// authored-export shape, preserving declared order and carrying the four fields
// the entity records (name, operates, scope, selector).
func descriptorExports(exports []wasm.PluginExport) []types.PluginExport {
	if len(exports) == 0 {
		return nil
	}
	out := make([]types.PluginExport, 0, len(exports))
	for _, e := range exports {
		out = append(out, types.PluginExport{
			Name:     e.Name,
			Operates: e.Operates,
			Scope:    e.Scope,
			Selector: e.Selector,
		})
	}
	return out
}

// isZeroCapabilities reports whether a describe() capability self-report carries
// no plugin-level claim — every field at its zero value — so the generator emits
// no capabilities block rather than an all-empty one. The cross-cutting sockets
// (linter, mutator, policy-validator) report no plugin-level capabilities.
func isZeroCapabilities(c wasm.Capabilities) bool {
	return c.InputKinds == (wasm.InputKinds{}) &&
		c.BoundaryAssembly == "" &&
		c.ArtifactDelimiters == nil &&
		len(c.SourceFilter) == 0 &&
		c.AssemblyNameAnnotation == nil &&
		!c.ReusableInstance &&
		!c.CrossBoundaryLinking
}

// descriptorCapabilities maps the describe() plugin-level capability self-report
// onto the entity's authored-capability shape, or returns nil when the guest
// reports none.
func descriptorCapabilities(c wasm.Capabilities) *types.PluginCapabilities {
	if isZeroCapabilities(c) {
		return nil
	}
	out := &types.PluginCapabilities{
		InputKinds: types.PluginInputKinds{
			Sourced:             c.InputKinds.Sourced,
			Synthesized:         c.InputKinds.Synthesized,
			ArtifactsStandalone: c.InputKinds.ArtifactsStandalone,
		},
		BoundaryAssembly:     c.BoundaryAssembly,
		SourceFilter:         c.SourceFilter,
		ReusableInstance:     c.ReusableInstance,
		CrossBoundaryLinking: c.CrossBoundaryLinking,
	}
	if c.ArtifactDelimiters != nil {
		out.ArtifactDelimiters = &types.PluginArtifactDelimiters{
			Left:  c.ArtifactDelimiters.Left,
			Right: c.ArtifactDelimiters.Right,
		}
	}
	if c.AssemblyNameAnnotation != nil {
		out.AssemblyNameAnnotation = &types.PluginAssemblyNameAnnotation{
			Annotation:    c.AssemblyNameAnnotation.Annotation,
			RequireUnique: c.AssemblyNameAnnotation.RequireUnique,
		}
	}
	return out
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
		desc, description, err := parseDescriptorJSON(raw)
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

// parseDescriptorJSON unmarshals a describe() descriptor JSON payload into the
// validated descriptor shape and, separately, reads the optional plugin-level
// description field. The description is read through a forward-compatible view so
// the generator picks it up as soon as guests emit it, without this surface
// declaring an ABI field of its own.
func parseDescriptorJSON(raw []byte) (wasm.PluginDescriptor, string, error) {
	var desc wasm.PluginDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return wasm.PluginDescriptor{}, "", err
	}
	var extra struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &extra); err != nil {
		return wasm.PluginDescriptor{}, "", err
	}
	return desc, extra.Description, nil
}

// describeWasmBytes loads a guest from bytes and returns its validated describe()
// descriptor. The instance is closed before the descriptor is returned; the
// generator needs the self-report, not a running guest.
func describeWasmBytes(ctx context.Context, name string, wasmBytes []byte) (wasm.PluginDescriptor, error) {
	spec := wasm.PluginSpec{Name: genCatalogDiagName(name), Bytes: wasmBytes, MaxMemoryPages: describeMaxMemoryPages}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
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
// their spec.plugin identity — the join key the embedded path matches a guest
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
