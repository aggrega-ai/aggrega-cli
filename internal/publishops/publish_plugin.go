// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"errors"
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
// Before any bytes land, the .wasm passes the single publication validation door
// (wasm.ValidatePlugin): its describe() self-report is cross-checked against the
// entity's authored capability claim, declared plugin type, command mirror, and
// host-function record. A mismatch halts loudly so a published entity can never
// promise a capability the binary does not provide.
//
// Signing signs the single manifest ONCE, reusing the entity-signing flow: the
// manifest digest is signed and attached as a referrer of the version tag
// exactly as every other entity is signed. Because that digest commits to the
// inline .wasm layer digest, the one signature transitively covers the
// executable and proves the entity↔.wasm pairing — there is no second artifact
// to sign or to verify independently.
//
// Publication is read-only over the entity file: the machine-owned
// spec.runtime.hostFunctions record is derived from the .wasm and compared to
// the record already committed on disk. When the two agree the on-disk bytes are
// published verbatim; when they diverge the publication HALTS and directs the author
// to re-record the set with `aggrega plugin sync`, so a published entity always
// carries the record its binary actually implies and publication never silently
// rewrites the committed file. Refusing rather than repairing is what keeps the
// record reviewable: a machine-owned field that publication rewrote would change
// without ever appearing in a diff anyone read.
//
// The .wasm bytes are read once and the same slice is threaded through
// validation, the content-digest computation, and the publication. There is therefore no
// window in which the verified record, the digest, and the bytes that ship could
// describe different content — an edit landing on disk after the read cannot
// reach any of the three. The loader applies the same read-once discipline
// between import inspection, derivation, and instantiation.
func pushPlugin(out io.Writer, cfg PushConfig, registry string, wantSign bool, name, version, entityPath string, entityYAML []byte) (PushResult, error) {
	if cfg.PluginPath == "" {
		return PushResult{}, fmt.Errorf("Plugin %s: --plugin <path-to-.wasm> is required to publish a Plugin (publish binds the .wasm into the entity's manifest)", name)
	}

	var entity types.Plugin
	if err := yaml.Unmarshal(entityYAML, &entity); err != nil {
		return PushResult{}, fmt.Errorf("parse Plugin %s: %w", name, err)
	}

	wasmBytes, err := os.ReadFile(cfg.PluginPath)
	if err != nil {
		return PushResult{}, fmt.Errorf("read .wasm %s: %w", cfg.PluginPath, err)
	}

	// Publication-time describe()-cross-check: refuse before any bytes land if the
	// .wasm disagrees with the entity's authored capability claims. The
	// cross-check loads the guest to invoke describe(); load it under the entity's
	// declared memory cap so a large guest whose working set exceeds the platform
	// default still instantiates, falling back to the describe headroom when the
	// entity declares none.
	ctx := context.Background()
	entityIdentity := name + ":" + version
	crossCheckMaxPages := wasm.DescribeMaxMemoryPages
	if entity.Spec.Runtime != nil && entity.Spec.Runtime.MaxMemoryPages != 0 {
		crossCheckMaxPages = entity.Spec.Runtime.MaxMemoryPages
	}
	// One publication validation door: describe()-cross-check the authored capability
	// claim, the declared-vs-reported plugin type, the machine-owned command
	// mirror, and the machine-owned host-function record. The entity surface is
	// assembled through the resolver's single entity → EntityInputs mapping — the
	// exact fields the load gate cross-checks against — so publication, validation, and
	// the test harness can never apply a divergent cross-check. The machine-owned
	// records (commands, host functions) are checked read-only: the author records
	// them with `aggrega plugin sync`, and publication only verifies them — it never
	// rewrites the file. A divergence HALTS before any bytes land. The location
	// context — which file is out of sync and the sync command that re-records it —
	// is re-wrapped here because the neutral validation layer names neither.
	report, err := wasm.ValidatePlugin(ctx, wasmBytes, wasm.ValidateOptions{
		Identity:       entityIdentity,
		DeclaredType:   entity.Metadata.PluginType,
		MaxMemoryPages: crossCheckMaxPages,
		Entity:         resolver.PluginEntityInputs(entity),
	})
	if errors.Is(err, wasm.ErrWasmCommandsMirrorMismatch()) {
		return PushResult{}, fmt.Errorf("Plugin %s: spec.commands in %s is out of sync with the .wasm at %s — re-run `aggrega plugin sync %s --plugin %s` and commit the result before publishing: %w",
			entityIdentity, entityPath, cfg.PluginPath, entityPath, cfg.PluginPath, err)
	}
	if errors.Is(err, wasm.ErrWasmHostFunctionsStampMismatch()) {
		return PushResult{}, fmt.Errorf("Plugin %s: spec.runtime.hostFunctions in %s is out of sync with the .wasm at %s — re-run `aggrega plugin sync %s --plugin %s` and commit the result before publishing: %w",
			entityIdentity, entityPath, cfg.PluginPath, entityPath, cfg.PluginPath, err)
	}
	if errors.Is(err, wasm.ErrWasmPluginVersionMismatch()) {
		return PushResult{}, fmt.Errorf("Plugin %s: metadata.version in %s does not match the .wasm describe() version at %s — set metadata.version to the guest's build version (regenerate with `aggrega plugin gen-catalog --plugin`) before publishing: %w",
			entityIdentity, entityPath, cfg.PluginPath, err)
	}
	if err != nil {
		return PushResult{}, fmt.Errorf("Plugin %s: %w", entityIdentity, err)
	}

	// Advisory only: report where the binary's describe() offers more than the
	// entity curates (binary ⊇ entity). Under-declaration of exports, capabilities,
	// or the render-behavior contract is legitimate, so these are notices — they
	// never halt the publication and never change its exit status.
	for _, notice := range report.Notices {
		fmt.Fprintf(out, "  notice: %s: %s\n", entityIdentity, notice)
	}

	// The content digest of the .wasm bytes equals the digest of the inline
	// .wasm layer the manifest carries; the consume side verifies the pulled
	// executable against it. Computed once, through the single content-digest
	// producer.
	contentDigest := digest.ContentDigest(wasmBytes)

	// The manifest is published at <registry>/plugin/<pluginType>/<name>,
	// addressed by the entity's version tag. The location derives from the
	// plugin's pluginType scope plus its bare name through the single OCILocation
	// rule, so publish-location == pull-location. The published entity bytes are the
	// verified on-disk bytes — publication records nothing into them.
	reference := BuildPushReference(registry, "Plugin", entity.Metadata.PluginType, name)

	// Searchable catalog annotations, sourced from the entity as published (never a
	// re-invoked describe()) through the single plugin facet projection, built once
	// and stamped identically onto the digest preview and the real publication. Building
	// them once is load-bearing: the previewed digest is signed and compared by the
	// immutability guard, so the preview must stamp exactly the annotations the
	// publication will. Routing through the projection is load-bearing for a second
	// reason: which facets a plugin publication stamps is then a single statement, so a
	// check that the artifact a binary carries is the artifact a publication of it
	// produces can assert against what this publication really sends rather than against a
	// restatement of it.
	annotations, err := oci.PluginSearchableMetadata(entity).Annotations()
	if err != nil {
		return PushResult{}, fmt.Errorf("Plugin %s: %w", name, err)
	}

	result := PushResult{
		Reference:  reference,
		Tag:        version,
		Kind:       "Plugin",
		MediaType:  oci.MediaTypePlugin,
		Size:       len(entityYAML),
		PluginSize: len(wasmBytes),
		Signed:     wantSign,
	}

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push (dry-run): %s:%s\n  kind: Plugin\n  entity mediatype: %s\n  wasm mediatype: %s\n  wasm content digest: %s\n  entity size: %d bytes\n  wasm size: %d bytes\n",
			reference, version, oci.MediaTypePlugin, oci.MediaTypeWasm, contentDigest, len(entityYAML), len(wasmBytes))
		result.DryRun = true
		result.Signed = false
		return result, nil
	}

	target := cfg.PushTarget
	if target == nil {
		httpClient, err := egressClient(cfg.Workspace.Config())
		if err != nil {
			return PushResult{}, fmt.Errorf("%s: %w", reference, err)
		}
		repo, err := oci.NewRemoteRepository(reference, httpClient)
		if err != nil {
			return PushResult{}, fmt.Errorf("%s: %w", reference, err)
		}
		target = repo
	}

	// Immutability guard on the version tag (mirrors the catalog-kind path): a
	// re-publication of a byte-identical manifest is an idempotent no-op; a tag already
	// pointing at different content is refused. The proposed digest is derived
	// from the whole manifest — the config entity plus the inline .wasm layer —
	// so a changed executable under an unchanged entity is caught as a conflict
	// too.
	existingDigest, exists, err := oci.Resolve(ctx, target, version)
	if err != nil {
		return PushResult{}, fmt.Errorf("resolve %s:%s: %w", reference, version, err)
	}
	proposedDigest, err := oci.PluginManifestDigest(ctx, entityYAML, wasmBytes, annotations)
	if err != nil {
		return PushResult{}, fmt.Errorf("%s:%s: %w", reference, version, err)
	}
	if exists {
		if existingDigest == proposedDigest {
			fmt.Fprintf(out, "Already published: %s:%s (%s)\n", reference, version, existingDigest)
			result.Digest = existingDigest
			result.AlreadyPublished = true
			result.Signed = false
			return result, nil
		}
		return PushResult{}, fmt.Errorf("%s:%s is already published as %s; refusing to overwrite with %s — publish a new version (aggrega lint --pre-publish verifies the bump): %w",
			reference, version, existingDigest, proposedDigest, oci.ErrImmutableTagConflict())
	}

	// Sign-before-publish: when signing is requested, sign the proposed manifest
	// digest before any bytes land, so an unsigned artifact never leaks onto a
	// signature-requiring registry. The digest is previewed via
	// oci.PluginManifestDigest (deterministic and equal to the digest the publication
	// will land), and because it commits to the inline .wasm layer, the one
	// signature transitively covers the executable — no separate .wasm signature
	// is produced.
	var (
		signatureBundle []byte
		signatureType   string
	)
	if wantSign {
		signer, serr := resolvePublishSigner(cfg.signerRequest(registry))
		if serr != nil {
			return PushResult{}, serr
		}
		signatureBundle, signatureType, err = signPublishDigestWith(ctx, signer, reference, version, proposedDigest)
		if err != nil {
			return PushResult{}, err
		}
	}

	// Publish the one manifest — entity config plus inline .wasm layer — tagged
	// by the entity's version. The returned digest is the manifest the signature
	// (if any) commits to.
	manifestDigest, err := RetryTransient(ctx, func() (string, error) {
		return oci.PushPluginManifest(ctx, target, version, entityYAML, wasmBytes, annotations)
	}, IsRetryablePublishError)
	if err != nil {
		return PushResult{}, fmt.Errorf("push plugin %s:%s: %w", reference, version, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, version, manifestDigest)
	result.Digest = manifestDigest

	if wantSign {
		if err := attachBundle(ctx, target, version, signatureType, signatureBundle); err != nil {
			return PushResult{}, fmt.Errorf("attach signature to %s:%s: %w", reference, version, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", reference, version)
	}
	return result, nil
}
