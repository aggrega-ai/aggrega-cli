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

// pushPlugin publishes a Plugin entity and its .wasm artifact as two
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
// The .wasm artifact is signed INDEPENDENTLY by the SAME signer — its own
// manifest digest is signed and attached as a referrer of the .wasm — so the
// executable can be verified standalone by a signature-requiring registry,
// and pull enforces that the entity and the .wasm carry the same signer. A
// single signer is resolved once and used for both digests so the two
// signatures cannot diverge in identity.
func pushPlugin(out io.Writer, cfg pushConfig, registry string, wantSign bool, name, version string, entityYAML []byte) error {
	if cfg.PluginPath == "" {
		return fmt.Errorf("aggrega push: Plugin %s: --plugin <path-to-.wasm> is required to publish a Plugin (publish pushes the .wasm artifact, stamps spec.plugin, then pushes the entity)", name)
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
	// .wasm disagrees with the entity's authored capability claims.
	ctx := context.Background()
	entityIdentity := name + ":" + version
	if err := wasm.CrossCheckPublishedPlugin(ctx, entityIdentity, entityCapabilityClaim(entity), wasmBytes); err != nil {
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

	// The content digest is the pin the consume side verifies the .wasm bytes
	// against; it is computed once, through the single content-digest producer.
	contentDigest := digest.ContentDigest(wasmBytes)

	// The entity and the .wasm share the same repository location
	// (<registry>/wasm/<socket>/<name>); the entity is addressed by its version
	// tag, the .wasm by its content digest, so the two never collide on a tag.
	reference := buildPushReference(registry, "Plugin", "", name)
	wasmRepoRef := stripRegistryScheme(reference)

	stampedEntity, err := stampPluginDigest(entityYAML, wasmRepoRef, contentDigest)
	if err != nil {
		return fmt.Errorf("aggrega push: Plugin %s: %w", entityIdentity, err)
	}

	// Chain the host-function stamp onto the digest-stamped bytes so both
	// machine-owned fields land in the single published entity. Unlike the
	// digest (partly authored, so a divergent pin halts), the host-function
	// record is fully derived: a divergent on-disk value is overwritten with a
	// warning, never a halt.
	stampedEntity, err = stampHostFunctions(out, entityIdentity, stampedEntity, derivedHostFunctions)
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

	// Immutability guard on the entity tag (mirrors the catalog-kind path): a
	// re-push of byte-identical content is an idempotent no-op; a tag already
	// pointing at different content is refused. The .wasm is digest-addressed
	// and so carries no tag-conflict surface.
	existingDigest, exists, err := oci.Resolve(ctx, target, version)
	if err != nil {
		return fmt.Errorf("aggrega push: resolve %s:%s: %w", reference, version, err)
	}
	proposedEntityDigest, err := oci.ManifestDigest(ctx, oci.MediaTypePlugin, stampedEntity)
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

	// The .wasm is tagged by its content digest; both the push and the consume
	// side derive this tag from the one oci.ContentDigestTag function.
	wasmTag := oci.ContentDigestTag(contentDigest)

	// Sign-before-push: when signing is requested, sign BOTH the entity and the
	// .wasm manifest digests before any bytes land, so an unsigned artifact never
	// leaks onto a signature-requiring registry. The .wasm manifest digest is
	// previewed via oci.ManifestDigest (deterministic and equal to the digest
	// the push will land) so both signatures are produced ahead of any push,
	// mirroring the entity's preview at proposedEntityDigest. ONE signer is
	// resolved and used for both digests so the entity and the .wasm cannot be
	// signed by different identities — the same-signer guarantee pull enforces.
	var (
		entitySignatureBundle []byte
		entitySignatureType   string
		wasmSignatureBundle   []byte
		wasmSignatureType     string
		wasmManifestDigest    string
	)
	if wantSign {
		wasmManifestDigest, err = oci.ManifestDigest(ctx, oci.MediaTypeWasm, wasmBytes)
		if err != nil {
			return fmt.Errorf("aggrega push: %s@%s: %w", reference, contentDigest, err)
		}
		signer, serr := resolvePushSigner(cfg, registry)
		if serr != nil {
			return serr
		}
		entitySignatureBundle, entitySignatureType, err = signPushDigestWith(ctx, signer, reference, version, proposedEntityDigest)
		if err != nil {
			return err
		}
		wasmSignatureBundle, wasmSignatureType, err = signPushDigestWith(ctx, signer, reference, contentDigest, wasmManifestDigest)
		if err != nil {
			return err
		}
	}

	// Ordered push: the .wasm artifact FIRST, addressed by its content digest;
	// then the entity tagged by its version. A failure between the two leaves an
	// orphan .wasm (harmless, addressed only by digest) and no entity tag, so
	// the consume side fails at pull rather than loading a broken plugin.
	if _, err := oci.Push(ctx, target, wasmTag, oci.MediaTypeWasm, wasmBytes); err != nil {
		return fmt.Errorf("aggrega push: push .wasm %s@%s: %w", reference, contentDigest, err)
	}
	fmt.Fprintf(out, "Push complete (.wasm artifact): %s@%s\n", reference, contentDigest)

	entityManifestDigest, err := oci.Push(ctx, target, version, oci.MediaTypePlugin, stampedEntity)
	if err != nil {
		return fmt.Errorf("aggrega push: push entity %s:%s: %w", reference, version, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, version, entityManifestDigest)

	if wantSign {
		// Attach the .wasm signature as a referrer of the .wasm artifact (its
		// content-digest tag resolves the manifest the signature commits to),
		// and the entity signature as a referrer of the entity version tag —
		// two artifacts, two signature referrers.
		if err := attachBundle(ctx, target, wasmTag, wasmSignatureType, wasmSignatureBundle); err != nil {
			return fmt.Errorf("aggrega push: attach signature to .wasm %s@%s: %w", reference, contentDigest, err)
		}
		fmt.Fprintf(out, "Signature attached (.wasm artifact): %s@%s\n", reference, contentDigest)

		if err := attachBundle(ctx, target, version, entitySignatureType, entitySignatureBundle); err != nil {
			return fmt.Errorf("aggrega push: attach signature to %s:%s: %w", reference, version, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", reference, version)
	}
	return nil
}

// entityCapabilityClaim maps a Plugin entity's authored exports into the
// neutral capability-claim shape internal/wasm cross-checks against the
// plugin's describe() self-report. The mapping mirrors the load-side claim
// construction so the publish and load surfaces agree on what an authored
// export claim is.
func entityCapabilityClaim(entity types.Plugin) wasm.EntityCapabilityClaim {
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
	return nil, fmt.Errorf("Plugin entity has no spec.plugin field to stamp with the .wasm content digest")
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
