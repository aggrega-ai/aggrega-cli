// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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
// The entity is read first, through the parse a catalog load runs
// (catalog.ParsePluginEntity), so a document that fails its kind's schema — a
// metadata.version that is not a semantic version among them — is refused as a
// catalog load would refuse it, before the .wasm is read.
//
// Before any bytes land, the .wasm passes the single publication validation door
// (wasm.ValidatePlugin): its describe() self-report is cross-checked against the
// entity's authored capability claim, declared plugin type, command mirror,
// host-function record and recorded version. A mismatch halts loudly so a
// published entity can never promise a capability the binary does not provide.
// Publication also asks the door to refuse a .wasm whose self-reported version
// says it was built from a working tree with uncommitted changes, since that
// version names no commit holding the bytes being published.
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
func pushPlugin(ctx context.Context, out io.Writer, cfg PushConfig, dest registryhost.Location, wantSign bool, name, version, entityPath string, entityYAML []byte) (PushResult, error) {
	if cfg.PluginPath == "" {
		return PushResult{}, &ExecutableRequiredError{name: name}
	}

	// The entity is read the way a catalog load reads it, before its .wasm is
	// read: against its kind's schema, then the per-entity load checks. A
	// publication that decoded the document for itself would accept one its
	// consumers' first load refuses — a metadata.version that is not a semantic
	// version, a required field missing — and the version grammar has its one
	// statement in the schema, so it is never restated here.
	entity, err := catalog.ParsePluginEntity(entityYAML, entityPath)
	if err != nil {
		return PushResult{}, fmt.Errorf("parse Plugin at %q: %w", entityPath, err)
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
	//
	// Publication alone asks the door to refuse a guest built from a working tree
	// with uncommitted changes: its version names a commit that does not hold what
	// was built, so nothing could rebuild what this publishes from the version it
	// is published under.
	report, err := wasm.ValidatePlugin(ctx, wasmBytes, wasm.ValidateOptions{
		Identity:               entityIdentity,
		DeclaredType:           entity.Metadata.PluginType,
		MaxMemoryPages:         crossCheckMaxPages,
		Entity:                 resolver.PluginEntityInputs(entity),
		RefuseUncommittedBuild: true,
	})
	if errors.Is(err, wasm.ErrWasmCommandsMirrorMismatch()) {
		return PushResult{}, fmt.Errorf("Plugin %s: spec.commands in %s is out of sync with the .wasm at %s — re-run `aggrega plugin sync %s --plugin %s` and commit the result before publishing: %w",
			entityIdentity, entityPath, cfg.PluginPath, entityPath, cfg.PluginPath, err)
	}
	if errors.Is(err, wasm.ErrWasmHostFunctionsStampMismatch()) {
		return PushResult{}, fmt.Errorf("Plugin %s: spec.runtime.hostFunctions in %s is out of sync with the .wasm at %s — re-run `aggrega plugin sync %s --plugin %s` and commit the result before publishing: %w",
			entityIdentity, entityPath, cfg.PluginPath, entityPath, cfg.PluginPath, err)
	}
	if errors.Is(err, wasm.ErrPluginBuiltFromUncommittedChanges()) {
		return PushResult{}, fmt.Errorf("Plugin %s: the .wasm at %s was built from a working tree with uncommitted changes — %s: %w",
			entityIdentity, cfg.PluginPath, taggedBuildRemedy("metadata.version in "+entityPath), err)
	}
	if errors.Is(err, wasm.ErrWasmPluginVersionMismatch()) {
		return PushResult{}, fmt.Errorf("Plugin %s: metadata.version in %s does not match the .wasm describe() version at %s — %s: %w",
			entityIdentity, entityPath, cfg.PluginPath, taggedBuildRemedy("metadata.version"), err)
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

	// The manifest is published at <prefix>/plugin/<pluginType>/<name>,
	// addressed by the entity's version tag, prefix being the destination's
	// location prefix. The location derives from the plugin's pluginType scope
	// plus its bare name through the single OCILocation rule, so publish-location
	// == pull-location. The published entity bytes are the verified on-disk bytes
	// — publication records nothing into them.
	reference := BuildPushReference(dest.Prefix(), "Plugin", entity.Metadata.PluginType, name)

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

	// The digest this publication would land, derived from the whole manifest — the
	// config entity plus the inline .wasm layer — and computed once ahead of the
	// dry-run branch, so the digest a dry run states is the very value the
	// immutability guard compares and the signer signs below. It packs into an
	// in-memory store and contacts nothing.
	proposedDigest, err := oci.PluginManifestDigest(ctx, entityYAML, wasmBytes, annotations)
	if err != nil {
		return PushResult{}, fmt.Errorf("%s:%s: %w", reference, version, err)
	}

	if cfg.DryRun {
		// This account is spelled here rather than through the shared
		// continuation-line writer, and the duplication is deliberate: it reports
		// two more facts than a publication has to report — the layer's own media
		// type and the executable's content digest — and interleaves them among
		// the ones a publication shares. Routing it through a writer whose subject
		// is the result would either move these bytes or carry facts the result
		// does not hold. What must stay in step is the wording, and the two agree
		// on every label they share. The header line carries the manifest digest
		// exactly where the publication line carries it, and prints the result's own
		// field, so the line and the answer a caller reads are one value.
		result.Digest = proposedDigest
		fmt.Fprintf(out, "aggrega push (dry-run): %s:%s (%s)\n  kind: Plugin\n  entity mediatype: %s\n  wasm mediatype: %s\n  wasm content digest: %s\n  entity size: %d bytes\n  wasm size: %d bytes\n",
			reference, version, result.Digest, oci.MediaTypePlugin, oci.MediaTypeWasm, contentDigest, len(entityYAML), len(wasmBytes))
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
	// re-publication of a byte-identical manifest uploads nothing and completes what
	// that publication still lacks (completePublication); a tag already pointing at
	// different content is refused. The proposed digest is derived from the whole
	// manifest — the config entity plus the inline .wasm layer — so a changed
	// executable under an unchanged entity is caught as a conflict too.
	existingDigest, exists, err := oci.Resolve(ctx, target, version)
	if err != nil {
		return PushResult{}, fmt.Errorf("resolve %s:%s: %w", reference, version, err)
	}
	if exists {
		if existingDigest == proposedDigest {
			fmt.Fprintf(out, "Already published: %s:%s (%s)\n", reference, version, existingDigest)
			writePublicationFacts(out, "Plugin", oci.MediaTypePlugin, len(entityYAML), len(wasmBytes))
			result.Digest = existingDigest
			result.AlreadyPublished = true
			wsCfg := cfg.Workspace.Config()
			policy, _ := wsCfg.RegistryTrustPolicy(dest.Host)
			c := completion{
				target: target, reference: reference, tag: version, digest: existingDigest,
				wsCfg: wsCfg, policy: policy, now: cfg.Now,
			}
			if wantSign {
				req := cfg.signerRequest()
				c.sign = func(ctx context.Context, digest string) ([]byte, string, error) {
					return signPublishDigest(ctx, req, dest.Host, reference, version, digest)
				}
			}
			done, cerr := completePublication(ctx, out, c)
			if cerr != nil {
				return PushResult{}, cerr
			}
			result.Signed, result.Dated = done.signed, done.dated
			return result, nil
		}
		return PushResult{}, fmt.Errorf("%s:%s is already published as %s; refusing to overwrite with %s — publish a new version (aggrega lint --pre-publish verifies the bump): %w",
			reference, version, existingDigest, proposedDigest, oci.ErrImmutableTagConflict())
	}

	// Sign-before-publish: when signing is requested, sign the proposed manifest
	// digest before any bytes land, so a failed signing leaves no unsigned
	// content on the registry. The signature is attached after the manifest
	// lands, so an attach still failing after its retries leaves the Plugin
	// published without it, and the error says so. The digest is previewed via
	// oci.PluginManifestDigest (deterministic and equal to the digest the publication
	// will land), and because it commits to the inline .wasm layer, the one
	// signature transitively covers the executable — no separate .wasm signature
	// is produced.
	var (
		signatureBundle []byte
		signatureType   string
	)
	if wantSign {
		signer, serr := resolvePublishSigner(cfg.signerRequest(), dest.Host)
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
	// (if any) commits to. Each write it makes retries a transient failure
	// inside pkg/oci, so nothing here wraps it again.
	manifestDigest, err := oci.PushPluginManifest(ctx, target, version, entityYAML, wasmBytes, annotations)
	if err != nil {
		return PushResult{}, fmt.Errorf("push plugin %s:%s: %w", reference, version, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, version, manifestDigest)
	writePublicationFacts(out, "Plugin", oci.MediaTypePlugin, len(entityYAML), len(wasmBytes))
	result.Digest = manifestDigest

	if wantSign {
		if err := attachBundle(ctx, target, version, signatureType, signatureBundle); err != nil {
			return PushResult{}, fmt.Errorf("attach signature to %s:%s: %w", reference, version, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", reference, version)
	}
	dated, err := recordPublicationDate(ctx, out, target, reference, version, signatureBundle, cfg.Now)
	if err != nil {
		return PushResult{}, err
	}
	result.Dated = dated
	return result, nil
}

// taggedBuildRemedy is the repair publication names when the version a plugin
// reports about its own build cannot be published under its entity: a build from
// a working tree with uncommitted changes, and a build whose version differs from
// the entity's metadata.version. field names the entity field the sentence
// opens on, qualified by its file where the surrounding message has not already
// named it.
//
// Both refusals are cleared the same way, because a published version names a
// tagged build: the entity records the release, and the plugin is built from the
// commit that release's tag names, so it reports that version itself. For a new
// release the tag has to name a commit already carrying the entity, so the entity
// and the source it releases are committed and tagged before the build.
//
// It never directs writing the version the plugin reports into the entity. A
// build of any other commit reports a development version, and copying that into
// the entity would publish a development build under it. Nor does it need to know
// which tag versions the plugin — the compiler's for a plugin that ships with the
// compiler, one of its own for a plugin that ships outside it — because "that
// release's tag" is true of both.
func taggedBuildRemedy(field string) string {
	return "set " + field + " to the release you are publishing and build the plugin from a clean checkout of the commit that release's tag names, so it reports that version itself; for a new release, commit the entity with the source it releases and tag that commit first"
}
