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
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// pushPlugin publishes a Plugin entity and its .wasm executable as ONE OCI
// manifest tagged by the entity's version: the entity YAML is the manifest's
// config blob and the .wasm is an inline layer addressed by its content digest.
// Binding the entity and the executable in a single manifest means the version
// tag resolves to both at once — there is one artifact to land and one tag to
// address it, and spec.plugin stays the digest-free artifact identity. The
// resolved content digest is recorded by the consumer at pull, in the lockfile;
// it is never embedded in the published entity.
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
func pushPlugin(out io.Writer, cfg pushConfig, registry string, wantSign bool, name, version string, entityYAML []byte) error {
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
	var capClaim wasm.EntityCapabilityClaim
	if c := resolver.WasmCapabilityClaim(resolver.PluginCapabilityClaim(entity)); c != nil {
		capClaim = *c
	}
	if err := wasm.CrossCheckPublishedPlugin(ctx, entityIdentity, capClaim, wasmBytes, crossCheckMaxPages); err != nil {
		return fmt.Errorf("aggrega push: Plugin %s: %w", entityIdentity, err)
	}

	// Derive the host functions the .wasm actually imports, in canonical form.
	// The set is machine-owned: it is stamped into the published entity below,
	// not authored. The same primitive runs at load and at pull, so the stamp,
	// the registered surface, and the pull-time verification can never diverge.
	derivedHostFunctions, err := wasm.DeriveHostFunctions(entityIdentity, wasmBytes)
	if err != nil {
		return fmt.Errorf("aggrega push: Plugin %s: %w", entityIdentity, err)
	}

	// The content digest of the .wasm bytes equals the digest of the inline
	// .wasm layer the manifest carries; the consume side verifies the pulled
	// executable against it. Computed once, through the single content-digest
	// producer.
	contentDigest := digest.ContentDigest(wasmBytes)

	// The manifest is published at <registry>/plugin/<socket>/<name>, addressed
	// by the entity's version tag.
	reference := buildPushReference(registry, "Plugin", "", name)

	// Stamp the machine-owned host-function record into the published entity.
	// The set is fully derived from the .wasm, so a divergent on-disk value is
	// overwritten with a warning, never a halt. spec.plugin is left as the
	// authored digest-free identity.
	stampedEntity, err := stampHostFunctions(out, entityIdentity, entityYAML, derivedHostFunctions)
	if err != nil {
		return fmt.Errorf("aggrega push: Plugin %s: %w", entityIdentity, err)
	}

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push (dry-run): %s:%s\n  kind: Plugin\n  entity mediatype: %s\n  wasm mediatype: %s\n  wasm content digest: %s\n  entity size: %d bytes\n  wasm size: %d bytes\n",
			reference, version, oci.MediaTypePlugin, oci.MediaTypeWasm, contentDigest, len(stampedEntity), len(wasmBytes))
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
	proposedDigest, err := oci.PluginManifestDigest(ctx, stampedEntity, wasmBytes)
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
	manifestDigest, err := oci.PushPluginManifest(ctx, target, version, stampedEntity, wasmBytes)
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

// stampHostFunctions returns the entity YAML with spec.runtime.hostFunctions set
// to the canonical (sorted, de-duplicated) host-function set derived from the
// .wasm. The field is machine-owned: unlike the content-digest stamp, a
// divergent on-disk value does NOT halt — it is overwritten, with a warning that
// shows the on-disk-vs-derived sets, because the binary is the sole source of
// truth for the field. A spec.runtime block absent on disk is created so the
// stamp always lands. The edit is a targeted YAML-node replacement so the rest
// of the entity bytes are preserved, and the canonical ordering makes a
// re-publish of the same .wasm byte-identical.
func stampHostFunctions(out io.Writer, entityIdentity string, entityYAML []byte, derived []string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(entityYAML, &doc); err != nil {
		return nil, fmt.Errorf("parse entity for host-function stamping: %w", err)
	}
	spec := specNode(&doc)
	if spec == nil {
		return nil, fmt.Errorf("Plugin entity has no spec block to stamp with the derived host functions")
	}

	runtime := findMappingValue(spec, "runtime")
	if runtime == nil || runtime.Kind != yaml.MappingNode {
		runtime = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		spec.Content = append(spec.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "runtime"},
			runtime)
	}

	if existing := findMappingValue(runtime, "hostFunctions"); existing != nil {
		if onDisk := sequenceValues(existing); !canonicalHostFunctionsEqual(onDisk, derived) {
			fmt.Fprintf(out, "  warning: Plugin %s spec.runtime.hostFunctions is machine-derived; overwriting on-disk %v with %v derived from the .wasm\n",
				entityIdentity, onDisk, derived)
		}
		*existing = *hostFunctionsSequenceNode(derived)
	} else {
		runtime.Content = append(runtime.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "hostFunctions"},
			hostFunctionsSequenceNode(derived))
	}

	return marshalNode(&doc)
}

// hostFunctionsSequenceNode builds the YAML sequence node for a derived
// host-function set. An empty set yields an explicit empty flow sequence ([]),
// so the machine-owned record is present and visible rather than absent.
func hostFunctionsSequenceNode(names []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if len(names) == 0 {
		seq.Style = yaml.FlowStyle
		return seq
	}
	for _, n := range names {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: n})
	}
	return seq
}

// sequenceValues returns the scalar values of a YAML sequence node, or nil when
// the node is not a sequence.
func sequenceValues(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(node.Content))
	for _, c := range node.Content {
		out = append(out, c.Value)
	}
	return out
}

// canonicalHostFunctionsEqual reports whether two host-function sets are equal
// as sets (order and duplicates ignored), so a re-published-identical .wasm whose
// on-disk stamp is already canonical is recognised as a no-change overwrite and
// emits no warning.
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
