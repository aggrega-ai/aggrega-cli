// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// pushPlugin publishes a Plugin entity and its .wasm executable as ONE OCI
// manifest tagged by the entity's version: the entity YAML is the manifest's
// config blob and the .wasm is an inline layer addressed by its content digest.
// Binding the entity and the executable in a single manifest means the version
// tag resolves to both at once — there is one artifact to land and one tag to
// address it, and the entity's bare identity is its digest-free artifact
// identity. The resolved content digest is recorded by the consumer at pull, in
// the lockfile; it is never embedded in the published entity.
//
// Before any bytes land, the .wasm is loaded and its describe() self-report is
// cross-checked against the entity's authored exports and host-function
// declaration (wasm.CrossCheckPublishedPlugin); a mismatch halts loudly so a
// published entity can never promise a capability the binary does not provide.
//
// Signing signs the single manifest ONCE, reusing the entity-signing flow: the
// manifest digest is signed and attached as a referrer of the version tag
// exactly as every other entity is signed. Because that digest commits to the
// inline .wasm layer digest, the one signature transitively covers the
// executable and proves the entity↔.wasm pairing — there is no second artifact
// to sign or to verify independently.
//
// Publish is read-only over the entity file: the machine-owned
// spec.runtime.hostFunctions record is derived from the .wasm and compared to
// the record already committed on disk. When the two agree the on-disk bytes are
// published verbatim; when they diverge the publish HALTS and directs the author
// to re-record the set with `aggrega plugin sync`, so a published entity always
// carries the record its binary actually implies and publish never silently
// rewrites the committed file.
func pushPlugin(out io.Writer, cfg pushConfig, registry string, wantSign bool, name, version, entityPath string, entityYAML []byte) error {
	if cfg.PluginPath == "" {
		return fmt.Errorf("aggrega push: Plugin %s: --plugin <path-to-.wasm> is required to publish a Plugin (publish binds the .wasm into the entity's manifest)", name)
	}

	var entity types.Plugin
	if err := yaml.Unmarshal(entityYAML, &entity); err != nil {
		return fmt.Errorf("aggrega push: parse Plugin %s: %w", name, err)
	}

	wasmBytes, err := os.ReadFile(cfg.PluginPath)
	if err != nil {
		return fmt.Errorf("aggrega push: read .wasm %s: %w", cfg.PluginPath, err)
	}

	// Publish-time describe()-cross-check: refuse before any bytes land if the
	// .wasm disagrees with the entity's authored capability claims. The
	// cross-check loads the guest to invoke describe(); load it under the entity's
	// declared memory cap so a large guest whose working set exceeds the platform
	// default still instantiates, falling back to the describe headroom when the
	// entity declares none.
	ctx := context.Background()
	entityIdentity := name + ":" + version
	crossCheckMaxPages := describeMaxMemoryPages
	if entity.Spec.Runtime != nil && entity.Spec.Runtime.MaxMemoryPages != 0 {
		crossCheckMaxPages = entity.Spec.Runtime.MaxMemoryPages
	}
	// Build the authored capability claim through the resolver's single
	// entity → neutral → wasm mapping — the exact mapping the load gate
	// cross-checks against — so the publish and load surfaces can never disagree
	// on which authored fields (input kinds, boundary assembly, delimiters,
	// source filter, reusable-instance, cross-boundary-linking) reach the
	// describe() cross-check. An entity authoring neither exports nor a
	// capabilities block yields a nil claim, mapped to an empty claim that
	// cross-checks as a no-op.
	var capClaim wasm.EntityContractClaim
	if c := resolver.WasmContractClaim(resolver.PluginContractClaim(entity)); c != nil {
		capClaim = *c
	}
	desc, err := wasm.CrossCheckPublishedPlugin(ctx, entityIdentity, capClaim, wasmBytes, crossCheckMaxPages, entity.Spec.PluginType)
	if err != nil {
		return fmt.Errorf("aggrega push: Plugin %s: %w", entityIdentity, err)
	}

	// Verify the machine-owned command mirror against the .wasm describe()
	// self-report, read-only exactly as the host-function record is: the author
	// records it with `aggrega plugin sync`, and publish only checks it here — it
	// never rewrites the file. A divergence in name, short-help text, or order
	// HALTS before any bytes land, directing the author to re-record the set. The
	// describe() self-report is the one already loaded for the cross-check above,
	// so the guest is not instantiated a second time.
	committedCommands := entityCommandMirror(entity)
	if !wasm.CommandMirrorsEqual(committedCommands, desc.Commands) {
		return fmt.Errorf("aggrega push: Plugin %s: spec.commands in %s is out of sync with the .wasm at %s — the file records %s but the binary describes %s; re-run `aggrega plugin sync %s --plugin %s` and commit the result before publishing: %w",
			entityIdentity, entityPath, cfg.PluginPath, wasm.FormatCommandMirror(committedCommands), wasm.FormatCommandMirror(desc.Commands), entityPath, cfg.PluginPath, wasm.ErrWasmCommandsMirrorMismatch)
	}

	// Advisory only: report where the binary's describe() offers more than the
	// entity curates (binary ⊇ entity). Under-declaration of exports, capabilities,
	// or the render-behavior contract is legitimate, so these are notices — they
	// never halt the publish and never change its exit status.
	for _, notice := range wasm.UnderDeclaredNotices(entityCuratedSurface(entity), desc) {
		fmt.Fprintf(out, "  notice: %s: %s\n", entityIdentity, notice)
	}

	// Derive the host functions the .wasm actually imports, in canonical form,
	// and verify the entity's committed record matches. The set is machine-owned:
	// the author records it into the file with `aggrega plugin sync`, and publish
	// only checks it here — it never rewrites the file. The same derivation
	// primitive runs at load and at pull, so the committed record, the registered
	// surface, and the pull-time verification can never diverge.
	derivedHostFunctions, err := wasm.DeriveHostFunctions(entityIdentity, wasmBytes)
	if err != nil {
		return fmt.Errorf("aggrega push: Plugin %s: %w", entityIdentity, err)
	}
	var committedHostFunctions []string
	if entity.Spec.Runtime != nil {
		committedHostFunctions = entity.Spec.Runtime.HostFunctions
	}
	if !canonicalHostFunctionsEqual(committedHostFunctions, derivedHostFunctions) {
		return fmt.Errorf("aggrega push: Plugin %s: spec.runtime.hostFunctions in %s is out of sync with the .wasm at %s — the file records %v but the binary imports %v; re-run `aggrega plugin sync %s --plugin %s` and commit the result before publishing: %w",
			entityIdentity, entityPath, cfg.PluginPath, committedHostFunctions, derivedHostFunctions, entityPath, cfg.PluginPath, wasm.ErrWasmHostFunctionsStampMismatch)
	}

	// The content digest of the .wasm bytes equals the digest of the inline
	// .wasm layer the manifest carries; the consume side verifies the pulled
	// executable against it. Computed once, through the single content-digest
	// producer.
	contentDigest := digest.ContentDigest(wasmBytes)

	// The manifest is published at <registry>/plugin/<socket>/<name>, addressed
	// by the entity's version tag. The published entity bytes are the verified
	// on-disk bytes — publish records nothing into them.
	reference := buildPushReference(registry, "Plugin", "", name)

	// Searchable catalog annotations, sourced from the entity as pushed (never a
	// re-invoked describe()), built once and stamped identically onto the digest
	// preview and the real push. A plugin carries no category label, so Category
	// is left empty. Building them once is load-bearing: the previewed digest is
	// signed and compared by the immutability guard, so the preview must stamp
	// exactly the annotations the push will.
	annotations := oci.SearchableMetadata{
		Description:  entity.Metadata.Description,
		Kind:         "Plugin",
		PluginType:   entity.Spec.PluginType,
		Capabilities: entity.Spec.Capabilities,
	}.Annotations()

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push (dry-run): %s:%s\n  kind: Plugin\n  entity mediatype: %s\n  wasm mediatype: %s\n  wasm content digest: %s\n  entity size: %d bytes\n  wasm size: %d bytes\n",
			reference, version, oci.MediaTypePlugin, oci.MediaTypeWasm, contentDigest, len(entityYAML), len(wasmBytes))
		return nil
	}

	target := cfg.PushTarget
	if target == nil {
		repo, err := oci.NewRemoteRepository(reference)
		if err != nil {
			return fmt.Errorf("aggrega push: %s: %w", reference, err)
		}
		target = repo
	}

	// Immutability guard on the version tag (mirrors the catalog-kind path): a
	// re-push of a byte-identical manifest is an idempotent no-op; a tag already
	// pointing at different content is refused. The proposed digest is derived
	// from the whole manifest — the config entity plus the inline .wasm layer —
	// so a changed executable under an unchanged entity is caught as a conflict
	// too.
	existingDigest, exists, err := oci.Resolve(ctx, target, version)
	if err != nil {
		return fmt.Errorf("aggrega push: resolve %s:%s: %w", reference, version, err)
	}
	proposedDigest, err := oci.PluginManifestDigest(ctx, entityYAML, wasmBytes, annotations)
	if err != nil {
		return fmt.Errorf("aggrega push: %s:%s: %w", reference, version, err)
	}
	if exists {
		if existingDigest == proposedDigest {
			fmt.Fprintf(out, "Already published: %s:%s (%s)\n", reference, version, existingDigest)
			return nil
		}
		return fmt.Errorf("aggrega push: %s:%s is already published as %s; refusing to overwrite with %s — publish a new version (aggrega lint --pre-publish verifies the bump): %w",
			reference, version, existingDigest, proposedDigest, oci.ErrImmutableTagConflict)
	}

	// Sign-before-push: when signing is requested, sign the proposed manifest
	// digest before any bytes land, so an unsigned artifact never leaks onto a
	// signature-requiring registry. The digest is previewed via
	// oci.PluginManifestDigest (deterministic and equal to the digest the push
	// will land), and because it commits to the inline .wasm layer, the one
	// signature transitively covers the executable — no separate .wasm signature
	// is produced.
	var (
		signatureBundle []byte
		signatureType   string
	)
	if wantSign {
		signer, serr := resolvePushSigner(cfg, registry)
		if serr != nil {
			return serr
		}
		signatureBundle, signatureType, err = signPushDigestWith(ctx, signer, reference, version, proposedDigest)
		if err != nil {
			return err
		}
	}

	// Publish the one manifest — entity config plus inline .wasm layer — tagged
	// by the entity's version. The returned digest is the manifest the signature
	// (if any) commits to.
	manifestDigest, err := oci.PushPluginManifest(ctx, target, version, entityYAML, wasmBytes, annotations)
	if err != nil {
		return fmt.Errorf("aggrega push: push plugin %s:%s: %w", reference, version, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, version, manifestDigest)

	if wantSign {
		if err := attachBundle(ctx, target, version, signatureType, signatureBundle); err != nil {
			return fmt.Errorf("aggrega push: attach signature to %s:%s: %w", reference, version, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", reference, version)
	}
	return nil
}

// entityCommandMirror maps a Plugin entity's machine-owned command record into
// the neutral command shape the mirror comparison reads. An entity that records
// no commands field yields an empty slice, which the comparison treats as equal
// to a plugin that describes no commands.
func entityCommandMirror(entity types.Plugin) []wasm.PluginCLICommand {
	mirror := make([]wasm.PluginCLICommand, len(entity.Spec.Commands))
	for i, c := range entity.Spec.Commands {
		mirror[i] = wasm.PluginCLICommand{Name: c.Name, Description: c.Description}
	}
	return mirror
}

// entityCuratedSurface projects a Plugin entity's authored, curated surface — its
// export names, plugin-level capabilities, and whether it authors a
// render-behavior contract — into the shape the under-declaration advisory reads.
func entityCuratedSurface(entity types.Plugin) wasm.EntityCuratedSurface {
	exportNames := make([]string, len(entity.Spec.Exports))
	for i, e := range entity.Spec.Exports {
		exportNames[i] = e.Name
	}
	hasContract := entity.Spec.Provider != nil && entity.Spec.Provider.Contract != nil
	return wasm.EntityCuratedSurface{
		ExportNames:  exportNames,
		Capabilities: entity.Spec.Capabilities,
		HasContract:  hasContract,
	}
}

// canonicalHostFunctionsEqual reports whether two host-function sets are equal
// as sets (order and duplicates ignored). It treats an absent record (nil) and
// an explicit empty set as equal, matching the pull-side verify, so a plugin
// whose binary imports nothing verifies clean whether its file omits the record
// or carries an explicit empty one.
func canonicalHostFunctionsEqual(a, b []string) bool {
	seen := make(map[string]bool, len(a))
	for _, x := range a {
		seen[x] = true
	}
	other := make(map[string]bool, len(b))
	for _, x := range b {
		other[x] = true
	}
	if len(seen) != len(other) {
		return false
	}
	for x := range seen {
		if !other[x] {
			return false
		}
	}
	return true
}
