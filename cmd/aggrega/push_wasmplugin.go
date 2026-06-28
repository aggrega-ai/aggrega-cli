// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// pushWasmPlugin publishes a WasmPlugin entity and its .wasm artifact as two
// ordered OCI artifacts: the .wasm first (addressed by its content digest),
// then the entity referencing it by content-digest pin in spec.plugin. The
// ordering plus the existing content-digest-pin makes partial-push safe — a
// failed entity push leaves only an orphan .wasm addressed by digest, and the
// consume side fails at pull on the absent entity tag rather than loading a
// silently-broken plugin.
//
// Before any bytes land, the .wasm is loaded and its describe() self-report is
// cross-checked against the entity's authored exports and host-function
// declaration (wasm.CrossCheckPublishedPlugin); a mismatch halts loudly so a
// published entity can never promise a capability the binary does not provide.
//
// spec.plugin handling (digest stamping): the .wasm content digest is computed
// once via the canonical content-digest spelling. If the on-disk spec.plugin
// already pins a digest, it MUST equal the computed one or the publish halts; a
// tag-form or placeholder spec.plugin is stamped to
// oci://<wasm-repo>@sha256:<content-digest> in the published entity bytes.
//
// Signing reuses the entity-signing flow: the entity manifest's digest is
// signed and attached as a referrer exactly as every other entity is signed.
// The .wasm artifact is not signed separately — the signed entity commits to
// the .wasm content digest through spec.plugin, so tampering with the .wasm
// breaks the pin and fails at pull.
func pushWasmPlugin(out io.Writer, cfg pushConfig, registry string, wantSign bool, name, version string, entityYAML []byte) error {
	if cfg.PluginPath == "" {
		return fmt.Errorf("aggrega push: WasmPlugin %s: --plugin <path-to-.wasm> is required to publish a WasmPlugin (publish pushes the .wasm artifact, stamps spec.plugin, then pushes the entity)", name)
	}

	var entity types.WasmPlugin
	if err := yaml.Unmarshal(entityYAML, &entity); err != nil {
		return fmt.Errorf("aggrega push: parse WasmPlugin %s: %w", name, err)
	}

	wasmBytes, err := os.ReadFile(cfg.PluginPath)
	if err != nil {
		return fmt.Errorf("aggrega push: read .wasm %s: %w", cfg.PluginPath, err)
	}

	// Publish-time describe()-cross-check: refuse before any bytes land if the
	// .wasm disagrees with the entity's authored claims.
	ctx := context.Background()
	entityIdentity := name + ":" + version
	if err := wasm.CrossCheckPublishedPlugin(ctx, entityIdentity, entityCapabilityClaim(entity), authoredHostFunctions(entity), wasmBytes); err != nil {
		return fmt.Errorf("aggrega push: WasmPlugin %s: %w", entityIdentity, err)
	}

	// The content digest is the pin the consume side verifies the .wasm bytes
	// against; it is computed once, through the single content-digest producer.
	contentDigest := digest.ContentDigest(wasmBytes)

	// The entity and the .wasm share the same repository location
	// (<registry>/wasm/<socket>/<name>); the entity is addressed by its version
	// tag, the .wasm by its content digest, so the two never collide on a tag.
	reference := buildPushReference(registry, "WasmPlugin", "", name)
	wasmRepoRef := stripRegistryScheme(reference)

	stampedEntity, err := stampPluginDigest(entityYAML, wasmRepoRef, contentDigest)
	if err != nil {
		return fmt.Errorf("aggrega push: WasmPlugin %s: %w", entityIdentity, err)
	}

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push (dry-run): %s:%s\n  kind: WasmPlugin\n  entity mediatype: %s\n  wasm mediatype: %s\n  wasm content digest: %s\n  entity size: %d bytes\n  wasm size: %d bytes\n",
			reference, version, oci.MediaTypeWasmPlugin, oci.MediaTypeWasm, contentDigest, len(stampedEntity), len(wasmBytes))
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

	// Immutability guard on the entity tag (mirrors the catalog-kind path): a
	// re-push of byte-identical content is an idempotent no-op; a tag already
	// pointing at different content is refused. The .wasm is digest-addressed
	// and so carries no tag-conflict surface.
	existingDigest, exists, err := oci.Resolve(ctx, target, version)
	if err != nil {
		return fmt.Errorf("aggrega push: resolve %s:%s: %w", reference, version, err)
	}
	proposedEntityDigest, err := oci.ManifestDigest(ctx, oci.MediaTypeWasmPlugin, stampedEntity)
	if err != nil {
		return fmt.Errorf("aggrega push: %s:%s: %w", reference, version, err)
	}
	if exists {
		if existingDigest == proposedEntityDigest {
			fmt.Fprintf(out, "Already published: %s:%s (%s)\n", reference, version, existingDigest)
			return nil
		}
		return fmt.Errorf("aggrega push: %s:%s is already published as %s; refusing to overwrite with %s — publish a new version (aggrega lint --pre-publish verifies the bump): %w",
			reference, version, existingDigest, proposedEntityDigest, oci.ErrImmutableTagConflict)
	}

	// Sign-before-push: sign the proposed entity manifest digest before any
	// bytes land, so an unsigned artifact never leaks onto a signature-requiring
	// registry. Reuses the shared signer construction and sign atom.
	var (
		signatureBundle []byte
		signatureType   string
	)
	if wantSign {
		signatureBundle, signatureType, err = signPushDigest(ctx, cfg, registry, reference, version, proposedEntityDigest)
		if err != nil {
			return err
		}
	}

	// Ordered push: the .wasm artifact FIRST, addressed by its content digest;
	// then the entity tagged by its version. A failure between the two leaves an
	// orphan .wasm (harmless, addressed only by digest) and no entity tag, so
	// the consume side fails at pull rather than loading a broken plugin.
	wasmTag := oci.ContentDigestTag(contentDigest)
	if _, err := oci.Push(ctx, target, wasmTag, oci.MediaTypeWasm, wasmBytes); err != nil {
		return fmt.Errorf("aggrega push: push .wasm %s@%s: %w", reference, contentDigest, err)
	}
	fmt.Fprintf(out, "Push complete (.wasm artifact): %s@%s\n", reference, contentDigest)

	entityManifestDigest, err := oci.Push(ctx, target, version, oci.MediaTypeWasmPlugin, stampedEntity)
	if err != nil {
		return fmt.Errorf("aggrega push: push entity %s:%s: %w", reference, version, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, version, entityManifestDigest)

	if wantSign {
		if err := attachBundle(ctx, target, version, signatureType, signatureBundle); err != nil {
			return fmt.Errorf("aggrega push: attach signature to %s:%s: %w", reference, version, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", reference, version)
	}
	return nil
}

// entityCapabilityClaim maps a WasmPlugin entity's authored exports into the
// neutral capability-claim shape internal/wasm cross-checks against the
// plugin's describe() self-report. The mapping mirrors the load-side claim
// construction so the publish and load surfaces agree on what an authored
// export claim is.
func entityCapabilityClaim(entity types.WasmPlugin) wasm.EntityCapabilityClaim {
	exports := make([]wasm.EntityExportClaim, 0, len(entity.Spec.Exports))
	for _, e := range entity.Spec.Exports {
		exports = append(exports, wasm.EntityExportClaim{
			Name:     e.Name,
			Operates: e.Operates,
			Scope:    e.Scope,
		})
	}
	return wasm.EntityCapabilityClaim{Exports: exports}
}

// authoredHostFunctions returns the entity's authored host-function declaration
// for the publish-time authored-vs-derived cross-check, or nil when the entity
// authored no runtime block or no declaration (which skips that cross-check).
func authoredHostFunctions(entity types.WasmPlugin) []string {
	if entity.Spec.Runtime == nil {
		return nil
	}
	return entity.Spec.Runtime.DeclaredHostFunctions
}

// stripRegistryScheme returns the registry repository path without any leading
// "oci://" — buildPushReference yields a scheme-less <registry>/<location>, and
// the stamped spec.plugin adds the oci:// scheme back, so this guards against a
// double scheme if the input ever carries one.
func stripRegistryScheme(reference string) string {
	return strings.TrimPrefix(reference, "oci://")
}

// stampPluginDigest returns the entity YAML with spec.plugin set to the
// canonical content-addressed reference oci://<wasmRepoRef>@<contentDigest>.
//
// Digest-stamp discipline: when the on-disk spec.plugin already pins an
// @sha256: digest, it MUST equal the computed content digest or the publish
// halts loudly (the authored pin disagrees with the bytes being published).
// When spec.plugin carries a tag-form or placeholder reference (or is empty),
// it is stamped to the canonical pinned form. The edit is a targeted YAML-node
// value replacement so the rest of the entity bytes are preserved.
func stampPluginDigest(entityYAML []byte, wasmRepoRef, contentDigest string) ([]byte, error) {
	canonical := "oci://" + wasmRepoRef + "@" + contentDigest

	var doc yaml.Node
	if err := yaml.Unmarshal(entityYAML, &doc); err != nil {
		return nil, fmt.Errorf("parse entity for digest stamping: %w", err)
	}
	pluginNode := findMappingValue(specNode(&doc), "plugin")
	if pluginNode != nil {
		if existing := pluginNode.Value; existing != "" {
			if _, _, pinned := strings.Cut(existing, "@sha256:"); pinned {
				if existing != canonical {
					return nil, fmt.Errorf("on-disk spec.plugin %q does not match the published .wasm content digest %q (publish a matching .wasm or update spec.plugin): %w",
						existing, canonical, oci.ErrFetchIntegrity)
				}
				// Already pinned to the exact computed digest: bytes are
				// stamp-stable, return verbatim.
				return entityYAML, nil
			}
		}
		pluginNode.Value = canonical
		pluginNode.Tag = "!!str"
		pluginNode.Style = yaml.DoubleQuotedStyle
		stamped, err := marshalNode(&doc)
		if err != nil {
			return nil, err
		}
		return stamped, nil
	}
	return nil, fmt.Errorf("WasmPlugin entity has no spec.plugin field to stamp with the .wasm content digest")
}

// specNode returns the "spec" mapping node of a parsed document, or nil.
func specNode(doc *yaml.Node) *yaml.Node {
	if doc == nil || len(doc.Content) == 0 {
		return nil
	}
	return findMappingValue(doc.Content[0], "spec")
}

// findMappingValue returns the value node for key in a YAML mapping node, or
// nil when the node is not a mapping or has no such key.
func findMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// marshalNode re-encodes a YAML node with the package's fixed two-space indent,
// so the stamped entity bytes are deterministic across runs of the same input.
func marshalNode(doc *yaml.Node) ([]byte, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("re-encode stamped entity: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("re-encode stamped entity: %w", err)
	}
	return []byte(b.String()), nil
}
