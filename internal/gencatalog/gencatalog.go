// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package gencatalog generates deterministic, idempotent Plugin catalog
// entities from Wasm guests. Each entity's contract fields are derived from the
// guest's describe() self-report and merged with an authored per-plugin overlay
// (identity, runtime limits, provider block); re-running over the same inputs
// produces byte-identical output. It is the single home for the generation
// logic, driven both by the CLI command and by the in-process tool surface.
package gencatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
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

// Options carries the inputs to a catalog-generation run: the selection mode
// (FromEmbedded or a single PluginPath), the output directory, the authored
// overlay path (a directory for FromEmbedded, a file for a single plugin), and
// the delivery.
//
// Delivery selects whether the generated entities are written under OutputDir or
// reported as content for the caller to place. The zero value writes, so a
// caller that says nothing about delivery generates exactly as before.
//
// WorkspaceRoot is the workspace the generation runs in. Every entity is composed
// beneath OutputDir, and is held to the root that directory picks
// ([writescope.RootFor]): the workspace root where OutputDir lies inside the
// workspace, and OutputDir itself where it lies outside it — the directory the
// caller named, honoured where it was named. An empty WorkspaceRoot holds every
// entity to OutputDir. The overlays read beneath an overlay directory are held the
// same way, to the root OverlayPath picks.
type Options struct {
	FromEmbedded  bool
	PluginPath    string
	OutputDir     string
	OverlayPath   string
	Delivery      authoredcontent.Delivery
	WorkspaceRoot string
}

// Run validates the option combination, composes an entity for every selected
// guest, then delivers the result: under a writing delivery each entity is
// written beneath OutputDir and a per-entity listing goes to out; under a
// reporting delivery the entities are reported to out and nothing is written.
//
// Composition finishes before any delivery begins. A generated Plugin entity is
// content a catalog author will own and go on editing, so the reporting delivery
// hands it back rather than putting it on disk behind their back; composing the
// whole set first is also what lets an authoring halt on the fifth guest leave
// none of the first four on disk.
//
// Every entity's place is then held to the root the output directory picks (see
// [Options]) before either delivery: a link beneath the output directory leading
// out of that root halts the generation with nothing written, and the reporting
// delivery halts where the write would.
func Run(ctx context.Context, opts Options, out io.Writer) error {
	switch {
	case opts.FromEmbedded && opts.PluginPath != "":
		return fmt.Errorf("aggrega plugin gen-catalog: --from-embedded and --plugin are mutually exclusive")
	case !opts.FromEmbedded && opts.PluginPath == "":
		return fmt.Errorf("aggrega plugin gen-catalog: one of --from-embedded or --plugin <path> is required")
	}
	if opts.OutputDir == "" {
		return fmt.Errorf("aggrega plugin gen-catalog: --output-dir <dir> is required")
	}
	if opts.OverlayPath == "" {
		return fmt.Errorf("aggrega plugin gen-catalog: --overlay is required (a directory for --from-embedded, a file for --plugin)")
	}

	var docs []authoredcontent.Document
	var err error
	if opts.FromEmbedded {
		docs, err = composeFromEmbedded(ctx, opts)
	} else {
		docs, err = composeFromPlugin(ctx, opts)
	}
	if err != nil {
		return err
	}
	if err := checkEntities(writescope.RootFor(opts.WorkspaceRoot, opts.OutputDir), docs); err != nil {
		return fmt.Errorf("aggrega plugin gen-catalog: %w", err)
	}

	if opts.Delivery.Reports() {
		return authoredcontent.Report(out, docs)
	}
	return writeEntities(docs, out)
}

// checkEntities holds the place of every composed entity to root before any is
// written, so a set with one place leading out of root writes none of it.
func checkEntities(root string, docs []authoredcontent.Document) error {
	for _, doc := range docs {
		if err := writescope.CheckWrite(root, doc.Path); err != nil {
			return err
		}
	}
	return nil
}

// writeEntities puts each composed entity at its own path, creating the
// per-coordinate directory, and names what it wrote.
//
// It consumes the composed documents rather than composing anything itself,
// which is what makes the reported content and the bytes on disk one
// computation: there is a single producer of an entity's bytes and both
// deliveries read its output.
func writeEntities(docs []authoredcontent.Document, out io.Writer) error {
	for _, doc := range docs {
		if err := os.MkdirAll(filepath.Dir(doc.Path), 0o755); err != nil {
			return fmt.Errorf("aggrega plugin gen-catalog: create %s: %w", filepath.Dir(doc.Path), err)
		}
		if err := os.WriteFile(doc.Path, []byte(doc.Content), 0o644); err != nil {
			return fmt.Errorf("aggrega plugin gen-catalog: write %s: %w", doc.Path, err)
		}
		fmt.Fprintf(out, "generated %s\n", doc.Path)
	}
	return nil
}

// composeFromEmbedded composes an entity for every embedded guest, matching
// each guest to its overlay by the guest's bare name — the final segment of the
// embed-tier identity coordinate <pluginType>/<name>, which is the identity an
// overlay authors as metadata.name. The coordinate the guest's own pluginType
// derives is verified against the embed-tier identity, so an overlay filed under
// the wrong socket halts rather than generating under a mismatched identity. A
// guest with no matching overlay halts loudly: the overlay set is the authored
// input the generator merges, so a missing one is an authoring gap, not a value
// to invent.
//
// The documents come back in the embedded set's own order, so a report and a
// write name the entities in the same sequence on every run.
func composeFromEmbedded(ctx context.Context, opts Options) ([]authoredcontent.Document, error) {
	overlays, err := loadOverlaysByPlugin(writescope.RootFor(opts.WorkspaceRoot, opts.OverlayPath), opts.OverlayPath)
	if err != nil {
		return nil, err
	}
	var docs []authoredcontent.Document
	for _, uri := range embedded.URIs() {
		desc, description, err := embeddedDescriptor(ctx, uri)
		if err != nil {
			return nil, err
		}
		// The host-function set is derived from the guest's own .wasm import
		// section — the same primitive publish and pull verify against — so the
		// generated entity carries the record its binary implies and needs no
		// separate recording step before it is publish-ready. The bytes are
		// available instantiation-free from the embedded set.
		wasmBytes, ok := embedded.Lookup(uri)
		if !ok {
			return nil, fmt.Errorf("aggrega plugin gen-catalog: embedded guest %q has no artifact", uri)
		}
		hostFunctions, err := wasm.DeriveHostFunctions(uri, wasmBytes)
		if err != nil {
			return nil, fmt.Errorf("aggrega plugin gen-catalog: %w", err)
		}
		bareName := coordinateLeaf(uri)
		overlay, ok := overlays[bareName]
		if !ok {
			return nil, fmt.Errorf("aggrega plugin gen-catalog: no overlay in %q has metadata.name %q for embedded guest %q; author the overlay before generating", opts.OverlayPath, bareName, uri)
		}
		if got := types.PluginCoordinate(string(desc.PluginType), overlay.Metadata.Name); got != uri {
			return nil, fmt.Errorf("aggrega plugin gen-catalog: overlay for %q derives identity coordinate %q, which does not match embedded guest %q; fix the overlay's metadata.name or socket", bareName, got, uri)
		}
		doc, err := composeEntity(opts.OutputDir, desc, description, hostFunctions, overlay, uri)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, nil
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

// composeFromPlugin composes a single entity for one non-embedded guest read
// from disk, described through the Wasm loader, and merged with the single
// overlay file.
func composeFromPlugin(ctx context.Context, opts Options) ([]authoredcontent.Document, error) {
	wasmBytes, err := os.ReadFile(opts.PluginPath)
	if err != nil {
		return nil, fmt.Errorf("aggrega plugin gen-catalog: read .wasm %s: %w", opts.PluginPath, err)
	}
	desc, err := describeWasmBytes(ctx, opts.PluginPath, wasmBytes)
	if err != nil {
		return nil, err
	}
	overlay, err := loadOverlayFile(opts.OverlayPath)
	if err != nil {
		return nil, err
	}
	// The host-function set is derived from the guest's own .wasm import section
	// — the same primitive publish and pull verify against — so the generated
	// entity is publish-ready with no separate recording step.
	hostFunctions, err := wasm.DeriveHostFunctions(opts.PluginPath, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("aggrega plugin gen-catalog: %w", err)
	}
	// A single .wasm has no repository identity of its own; the loader-returned
	// descriptor carries no plugin-level description field until the ABI grows
	// one, so the single-guest path emits a description only once that field
	// lands. The embedded path reads the description from the build-extracted
	// descriptor JSON.
	doc, err := composeEntity(opts.OutputDir, desc, "", hostFunctions, overlay, opts.PluginPath)
	if err != nil {
		return nil, err
	}
	return []authoredcontent.Document{doc}, nil
}

// composeEntity merges one descriptor with its overlay and returns the entity as
// content addressed to <outputDir>/<pluginType>/<name>/<version>/plugin.yaml —
// the plugin coordinate path that mirrors the catalog filesystem layout.
//
// It is the only producer of a generated entity's bytes and of the path those
// bytes belong at. Both deliveries read what it returns, so a reported entity
// and a written one cannot differ: there is no second composition to drift from,
// and a caller cannot be handed content addressed to a path the write would not
// have used.
func composeEntity(outputDir string, desc wasm.PluginDescriptor, description string, hostFunctions []string, overlay types.Plugin, identity string) (authoredcontent.Document, error) {
	entity, err := entitysynth.MergeForPublish(desc, description, hostFunctions, overlay, identity)
	if err != nil {
		return authoredcontent.Document{}, fmt.Errorf("aggrega plugin gen-catalog: %w", err)
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
	// The gate runs before either delivery, so content reported to a caller has
	// cleared the same validation content written to disk has.
	if errs := validator.ValidatePluginProviderSchemas(entity.Metadata.Name, path, &entity.Spec); len(errs) > 0 {
		return authoredcontent.Document{}, fmt.Errorf("aggrega plugin gen-catalog: %s has an invalid spec.provider.workloadSchema; fix the overlay: %w", entity.Metadata.Name, errors.Join(errs...))
	}
	body, err := emitGenCatalogEntity(entity)
	if err != nil {
		return authoredcontent.Document{}, fmt.Errorf("aggrega plugin gen-catalog: encode %s: %w", entity.Metadata.Name, err)
	}
	return authoredcontent.Document{Path: path, Content: string(body)}, nil
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
	spec := wasm.PluginSpec{Name: genCatalogDiagName(name), Bytes: wasmBytes, MaxMemoryPages: wasm.DescribeMaxMemoryPages}
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
//
// dir is the overlay directory the caller named, and a directory named through a
// link is the directory it leads to, so it is walked there, each overlay still
// spelled beneath dir as named (pathscope.WalkDir).
//
// root is the directory every read beneath dir is held to — the one dir picks
// ([writescope.RootFor]): the workspace for an overlay directory inside it, and dir
// itself for one named outside it. A link met beneath dir that the walk would read
// something through — one named like an overlay, or one leading to a directory — is
// judged where the walk meets it ([readscope.CheckOverlayRead]). One leading out of
// root is not read and halts the generation, naming the link and where it leads:
// passing it over would generate without the overlay it stands for, or fail later
// asking for an overlay that was authored. One leading elsewhere inside root is met
// as any entry is: a link to an overlay is read where it leads, and a link to a
// directory is not entered.
func loadOverlaysByPlugin(root, dir string) (map[string]types.Plugin, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("aggrega plugin gen-catalog: overlay directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("aggrega plugin gen-catalog: --overlay must be a directory for --from-embedded, got file %s", dir)
	}
	var paths []string
	var held error
	err = pathscope.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		overlay := isOverlayFile(path)
		if d.Type()&fs.ModeSymlink != 0 && (overlay || leadsToDirectory(path)) {
			if held = readscope.CheckOverlayRead(root, path); held != nil {
				return fs.SkipAll
			}
		}
		if overlay {
			paths = append(paths, path)
		}
		return nil
	})
	if held != nil {
		return nil, fmt.Errorf("aggrega plugin gen-catalog: %w", held)
	}
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

// isOverlayFile reports whether path is named like an overlay, the one shape of
// file the overlay walk reads.
func isOverlayFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// leadsToDirectory reports whether the link at path leads to a directory, which may
// hold overlays of its own.
func leadsToDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
