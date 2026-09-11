// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// PushConfig is the input contract for the catalog-entity publication workflow.
// A caller assembles it from whatever state it holds — flag values, a request
// payload — and hands it to Push, which reads ONLY from cfg so concurrent
// callers cannot observe each other.
type PushConfig struct {
	EntityPath string
	Registry   string
	DryRun     bool

	// Sign is the tri-state signing request. nil means "follow the
	// destination registry's policy" (sign by default to a
	// signature-requiring registry, do not sign otherwise). A non-nil
	// value is the caller's explicit choice and overrides the
	// policy default — except that the strict-refuse guardrail below
	// always halts an unsigned publication to a signature-requiring registry,
	// regardless of this field. There is no way to bypass that refusal.
	Sign *bool

	// KeyPath, FulcioURL, RekorURL, TimestampURL, and IDToken are the
	// caller-supplied signing inputs. They override the workspace
	// config's signing block; an empty value falls through to the
	// per-registry then global config, then to the public-good endpoint
	// default. IDToken is never sourced from config — it is a short-lived
	// secret supplied only by the caller or the ambient OIDC environment.
	KeyPath      string
	FulcioURL    string
	RekorURL     string
	TimestampURL string
	IDToken      string

	// PluginPath is the local filesystem path to the .wasm artifact, required
	// when publishing a Plugin entity. Publication reads these bytes,
	// cross-checks them against the entity's authored claims, computes their
	// content digest, and publishes them as an OCI artifact addressed by that
	// digest before publishing the entity. Empty for every non-Plugin kind.
	PluginPath string

	// Workspace is the resolved workspace the caller threads in. Required: Push
	// refuses a nil rather than resolving one of its own, because resolving here
	// would make the result depend on a directory no caller named.
	Workspace *workspaceconfig.ResolvedWorkspace

	// EnvLookup reads the environment for the identity-token secret resolution.
	// Nil means the live process environment.
	EnvLookup func(string) string

	// TokenGetter acquires a keyless OIDC identity token interactively when no
	// explicit token is supplied. Nil is the unattended shape and is the default:
	// no acquisition is attempted at all and the shared identity ladder refuses,
	// naming what to supply. A caller that supplies one is declaring that a
	// person is watching and can complete a federated login.
	TokenGetter func() (string, error)

	// IsTTY reports whether a person is watching, gating the interactive rung.
	// Nil reports false, so the unattended default cannot reach a prompt.
	IsTTY func() bool

	// IdentitySupply and DestinationSupply carry the caller's own vocabulary into
	// the two refusals a caller can act on: no identity token was resolvable, and
	// no destination registry was named. Each condition is decided in one place;
	// what these carry is only how to say, to THIS caller, what it can supply.
	// Leaving either zero yields a refusal that names only what every caller
	// shares, which is shorter and still true.
	IdentitySupply    signing.IdentityTokenSupply
	DestinationSupply DestinationSupply

	// PushTarget overrides the production remote-repository
	// construction. A caller may inject an in-memory oras.Target via this
	// field; nil in production. When nil and DryRun is false,
	// Push constructs the target from the reference.
	PushTarget oras.Target

	// Signer produces the signature bundle when signing is requested.
	// When signing is requested and Signer is nil, Push builds a keyed or
	// keyless signer from the resolved signing inputs.
	Signer signing.Signer

	// Out is the sink for the operation's user-facing status output (the
	// dry-run line, the publication line, the already-published line, and the
	// signature-attached note). Nil defaults to os.Stdout, so a caller that
	// wants to capture the output passes a buffer rather than mutating a
	// process global.
	Out io.Writer
}

// signerRequest projects the publication's credential inputs onto the shared
// credential boundary's input contract, so the precedence chain and the identity
// ladder are consulted through one path rather than re-spelled per verb.
func (cfg PushConfig) signerRequest(registry string) SignerRequest {
	return SignerRequest{
		Workspace:            cfg.Workspace,
		Registry:             registry,
		Signer:               cfg.Signer,
		KeyPath:              cfg.KeyPath,
		FulcioURL:            cfg.FulcioURL,
		RekorURL:             cfg.RekorURL,
		TimestampURL:         cfg.TimestampURL,
		IDToken:              cfg.IDToken,
		EnvLookup:            cfg.EnvLookup,
		AcquireIdentityToken: interactiveAcquirer(cfg.TokenGetter, cfg.IsTTY),
		IdentitySupply:       cfg.IdentitySupply,
	}
}

// interactiveAcquirer builds the identity ladder's optional interactive rung from
// a caller's terminal check and token getter. A nil getter yields a nil acquirer,
// which is the unattended shape: the ladder then has no rung that needs a person
// and produces its own refusal instead of blocking on a flow nobody is watching.
//
// Declining, rather than failing, when there is no terminal is what lets the
// shared ladder produce that refusal: there is nobody to ask, which is not an
// acquisition failure.
func interactiveAcquirer(getter func() (string, error), tty func() bool) signing.InteractiveIdentityTokenAcquirer {
	if getter == nil {
		return nil
	}
	return func() (string, bool, error) {
		if tty == nil || !tty() {
			return "", false, nil
		}
		t, err := getter()
		if err != nil {
			return "", false, err
		}
		return t, true, nil
	}
}

// componentCategoryLabel is the metadata.labels key whose value the standard
// catalog convention sets to an entity's category. Its value is stamped as the
// searchable category annotation at publication so a registry index can filter by
// category without fetching the entity body.
const componentCategoryLabel = "component.aggrega.ai/category"

// pushEntityHeader is the minimal YAML envelope Push parses out
// of the entity file: kind, metadata.name, metadata.version,
// metadata.description, and metadata.labels. Description and labels feed
// the searchable annotations stamped onto the published manifest.
//
// A second decode of the same document happens inside pkg/oci, where
// the per-version path check reads metadata.version to compare it against
// the entity's per-version directory. The two are deliberately separate, and the
// reason is worth stating so nobody folds them together on the strength of the
// one field they share:
//
//   - They are not the same shape. That decode reads exactly one field; this one
//     reads six. Five of the six are meaningless to the version check, so a
//     shared struct would carry them into a package with no use for them.
//   - They cannot be shared in the direction it would have to run. This package
//     consumes pkg/oci; pkg/oci does not consume this one, so the shared type
//     would have to live on the pkg/oci side — which is the side that needs
//     least of it.
//   - Their requirements on the one shared field differ, and only on this side.
//     Labels here must survive as an ordered map because the whole map is
//     stamped onto the published manifest and identical input must produce
//     byte-identical published bytes. The version check has no such constraint,
//     reads no labels, and would gain nothing from the ordering.
//
// So this is one document read twice for two unrelated questions, not one
// mechanism written twice. What must stay in step is only the field NAME the two
// agree on, and that name comes from the published entity schema rather than
// from either struct.
type pushEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string `yaml:"name"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
		// Labels is decoded into an ordered map rather than a Go map because the
		// whole map is stamped onto the published manifest, and identical input must
		// produce byte-identical published bytes. A Go map would lose the author's
		// order at the decode, before the publication path could preserve it.
		Labels types.OrderedMap[string] `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		// Capability is the render-engine capability for Recipe / FeatureRecipe.
		// It is part of those kinds' identity, so the OCI location must encode it —
		// two providers may ship a recipe of the same name.
		Capability string `yaml:"capability"`
	} `yaml:"spec"`
}

// ListPublishableKinds writes each publishable entity kind on its own line, in
// the lexical order oci.MediaTypeKinds already returns them in.
//
// That enumeration is the single home of the publishable-kind set, so this
// helper neither restates it nor filters it. A restated copy agrees on the day
// it is written and silently disagrees the day a kind is added — and a filter
// would be such a copy expressed as an exclusion. The order is not re-sorted
// here for the same reason: sorting twice invites the two orderings to drift.
//
// The emitted shape is a parsed contract, not display text. One kind per line
// with no header, count, or decoration is what lets a caller read the set
// without a parser, so the shape is held stable rather than adjusted for
// readability.
func ListPublishableKinds(out io.Writer) error {
	for _, kind := range oci.MediaTypeKinds() {
		if _, err := fmt.Fprintln(out, kind); err != nil {
			return err
		}
	}
	return nil
}

// PushResult reports what a catalog-entity publication did, as the facts
// themselves rather than as the status lines Push also writes to cfg.Out.
//
// The two are not alternatives. The prose is for a person watching a terminal; a
// surface that answers a caller needs the values, and re-deriving them by parsing
// the prose would make a display format into a contract. Every field is filled on
// every path the operation can take, so a caller reads one shape whichever branch
// ran, and the two booleans say WHICH branch ran: a dry run computed the digest
// and published nothing, and an already-published re-publication landed nothing
// because the bytes were already there.
type PushResult struct {
	// Reference and Tag address the published artifact: the repository reference
	// and the entity's own metadata.version.
	Reference string
	Tag       string
	// Kind is the entity kind published, and MediaType the transport media type
	// its bundle was published under.
	Kind      string
	MediaType string
	// Size is the published bundle's byte count. For a Plugin it is the entity
	// document's size; the executable travels as an inline layer and its size is
	// reported separately.
	Size int
	// PluginSize is the .wasm executable's byte count for a Plugin publication,
	// and zero for every other kind. A Plugin publishes entity and executable as
	// one manifest, so a single size would describe neither.
	PluginSize int
	// Digest is the published manifest's digest, and it is EMPTY on a dry run.
	// A dry run stops before the immutability guard, which is where the proposed
	// digest is first computed, and computing one here to fill the field would add
	// work to a path whose whole purpose is not to do any.
	Digest string
	// Signed reports that a signature bundle was produced and attached.
	Signed bool
	// AlreadyPublished reports that the tag already pointed at byte-identical
	// content, so the publication was an idempotent no-op.
	AlreadyPublished bool
	// DryRun reports that nothing was published: the bundle was computed and the
	// destination was never contacted.
	DryRun bool
}

// writePublicationFacts writes the transport facts of a publication — what kind
// of thing landed, under which media type, and how many bytes — as the indented
// continuation lines beneath the header line that announced it.
//
// It is the single home for those lines, so the account a dry run gives and the
// account a real publication gives cannot come to describe the same three facts
// in two shapes. Every one of them is a fact the operation computes and reports
// in its result, and a publication states them for the same reason a dry run
// does: a caller that has just landed bytes has no other way to learn what media
// type they went under or how many of them there were.
//
// kind is omitted when empty. A standalone data artifact carries opaque bytes
// and has no entity kind to name, so a line naming one would be a blank fact.
//
// The two byte counts are distinguished as the entity's and the executable's
// only where there are two to tell apart, which is exactly when the executable
// travels as its own layer inside one manifest. A lone count is just the size.
//
// The lines are indented and follow the header rather than joining it, and that
// is load-bearing rather than cosmetic: output like this is read back by scripts
// that match the header anchored at the start of its line and take the reference
// from a fixed field, so a fact inserted into the header would move what they
// read while an indented line beneath it is invisible to them.
func writePublicationFacts(out io.Writer, kind, mediaType string, size, pluginSize int) {
	if kind != "" {
		fmt.Fprintf(out, "  kind: %s\n", kind)
	}
	if pluginSize > 0 {
		fmt.Fprintf(out, "  entity mediatype: %s\n  entity size: %d bytes\n  wasm size: %d bytes\n",
			mediaType, size, pluginSize)
		return
	}
	fmt.Fprintf(out, "  mediatype: %s\n  size: %d bytes\n", mediaType, size)
}

// Push bundles the entity at cfg.EntityPath and publishes it.
// Reads workspace config; parses the entity envelope (kind,
// metadata.name, metadata.version); bundles via pkg/oci.Bundle (which
// handles per-kind dispatch including per-version subdir enforcement for
// every publishable kind and Recipe / FeatureRecipe tar.gz directory
// walking); constructs the OCI repository reference; publishes via pkg/oci.Push.
//
// Signing is sign-before-publish: when signing is requested, Push computes
// the proposed manifest digest, constructs the signer, and signs the digest
// BEFORE any bytes reach the registry. If the signer cannot be constructed or
// signing fails, Push refuses before publishing — zero bytes land, so an
// unsigned artifact can never leak onto a signature-requiring registry. The
// signature bundle is attached as a referrer only after the publication
// succeeds; the bundle commits to the digest string, so the pre-publication
// signature is valid for the published bytes.
//
// Returns wrapped errors on workspace-config validation failure,
// entity-file read / parse failure, bundle failure (including
// ErrUnknownEntityKind for non-publishable kinds and
// ErrEntityVersionPathMismatch for an entity whose metadata.version
// disagrees with its per-version subdirectory), signer-construction /
// signing failure (refused before publishing), and publication failure.
//
// When cfg.DryRun is true, Push prints the proposed
// <reference>:<tag> + mediatype + bundle byte count and returns
// without constructing a remote target. Useful for authoring and
// for verifying the reference-shape construction without binding to
// a registry.
func Push(cfg PushConfig) (PushResult, error) {
	if cfg.EntityPath == "" {
		return PushResult{}, fmt.Errorf("entity path required (pass as positional arg or via --entity)")
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	rw, err := requireWorkspace(cfg.Workspace)
	if err != nil {
		return PushResult{}, err
	}
	wsCfg := rw.Config()
	// Carry the resolved workspace forward so downstream helpers source the
	// egress client from the same resolution.
	cfg.Workspace = rw

	// Publishing an entity is a network round trip and has no form that is not,
	// so an offline workspace refuses it here rather than after the entity has
	// been read, its signing policy evaluated and its bundle built. A dry run is
	// the exception and is excluded by what it does rather than by its name: it
	// returns below without constructing a target, so it reaches no network and
	// is the offline-safe form of this command.
	if !cfg.DryRun {
		if err := refuseIfOffline(rw, "publishing a catalog entity is a network round trip"); err != nil {
			return PushResult{}, err
		}
	}

	token, err := resolveDestination(cfg.Registry, wsCfg.DefaultRegistry, cfg.DestinationSupply)
	if err != nil {
		return PushResult{}, err
	}
	registry, err := wsCfg.ResolveRegistry(token)
	if err != nil {
		return PushResult{}, err
	}

	data, err := os.ReadFile(cfg.EntityPath)
	if err != nil {
		return PushResult{}, fmt.Errorf("read %s: %w", cfg.EntityPath, err)
	}
	var header pushEntityHeader
	if err := yaml.Unmarshal(data, &header); err != nil {
		return PushResult{}, fmt.Errorf("parse %s: %w", cfg.EntityPath, err)
	}
	if header.Kind == "" {
		return PushResult{}, fmt.Errorf("%s: missing kind field", cfg.EntityPath)
	}
	if header.Metadata.Name == "" {
		return PushResult{}, fmt.Errorf("%s: missing metadata.name", cfg.EntityPath)
	}
	if header.Metadata.Version == "" {
		return PushResult{}, fmt.Errorf("%s: missing metadata.version", cfg.EntityPath)
	}

	// Signing policy. A publication is refused unsigned when EITHER the destination
	// registry requires signatures OR the workspace signing policy requires a
	// signature for this artifact kind. Signing is on by default whenever
	// either condition holds. An explicit caller-supplied value overrides the default
	// for whether to sign, but never relaxes the refusal: there is no way to
	// publish unsigned content when a signature is required. The kind is read from
	// the entity header (parsed above) so the org policy's kind scope can be
	// evaluated before any bytes reach the registry — the refusal, like the
	// per-registry one, halts before the network call so an unsigned
	// artifact never leaks.
	requiresSignatures := wsCfg.RegistrySigningRequiredForKind(registryhost.Of(registry), header.Kind)
	orgRequired := wsCfg.SigningRequiredForKind(header.Kind)
	wantSign := requiresSignatures || orgRequired
	if cfg.Sign != nil {
		wantSign = *cfg.Sign
	}
	if (requiresSignatures || orgRequired) && !wantSign {
		// Distinguish the cause: the org-policy refusal names the kind and
		// wraps its own sentinel; the per-registry refusal keeps the
		// registry-specific message and sentinel. When both conditions hold the
		// registry requirement is reported (it is the more specific
		// destination-scoped cause).
		if requiresSignatures {
			return PushResult{}, fmt.Errorf("registry %q requires signatures; re-run signing the artifact (there is no flag to push unsigned content here): %w",
				registry, signing.ErrUnsignedPushRefused())
		}
		return PushResult{}, fmt.Errorf("the signing policy requires a signature for kind %q; re-run signing the artifact (there is no flag to push unsigned content here): %w",
			header.Kind, signing.ErrSigningPolicyRequiresSignature())
	}

	// A Plugin publishes as ONE manifest — the entity YAML as the config blob,
	// the .wasm as an inline layer addressed by its content digest — with a
	// publication-time describe()-cross-check against the .wasm bytes. The entity
	// body still carries a machine-derived host-function record; that plus the
	// entity-and-executable-in-one-manifest packing differs enough from the
	// single-artifact catalog-kind path that it has its own helper; signing,
	// the immutability guard, and the content-digest spelling are reused, not
	// re-spelled.
	if header.Kind == "Plugin" {
		return pushPlugin(out, cfg, registry, wantSign, header.Metadata.Name, header.Metadata.Version, cfg.EntityPath, data)
	}

	bundleBytes, mediaType, err := oci.Bundle(header.Kind, cfg.EntityPath)
	if err != nil {
		return PushResult{}, fmt.Errorf("bundle %s: %w", cfg.EntityPath, err)
	}

	reference := BuildPushReference(registry, header.Kind, header.Spec.Capability, header.Metadata.Name)
	tag := header.Metadata.Version
	result := PushResult{
		Reference: reference,
		Tag:       tag,
		Kind:      header.Kind,
		MediaType: mediaType,
		Size:      len(bundleBytes),
		Signed:    wantSign,
	}

	// Searchable catalog annotations, built once and stamped identically onto
	// the digest preview and the real publication. Building them once is
	// load-bearing: the previewed digest is signed and compared by the
	// immutability guard, so the preview must stamp exactly the annotations the
	// publication will.
	//
	// The whole authored label map travels, not just the category: a label
	// selector filters on every label an author wrote, and it can only match what
	// the manifest carries.
	category, _ := header.Metadata.Labels.Get(componentCategoryLabel)
	annotations, err := oci.SearchableMetadata{
		Description: header.Metadata.Description,
		Category:    category,
		Kind:        header.Kind,
		Labels:      &header.Metadata.Labels,
	}.Annotations()
	if err != nil {
		return PushResult{}, fmt.Errorf("%s: %w", cfg.EntityPath, err)
	}

	if cfg.DryRun {
		fmt.Fprintf(out, "aggrega push (dry-run): %s:%s\n", reference, tag)
		writePublicationFacts(out, header.Kind, mediaType, len(bundleBytes), 0)
		result.DryRun = true
		// A dry run reports no signature: nothing was signed, because signing
		// happens against the digest a publication is about to land and this one
		// lands nothing.
		result.Signed = false
		return result, nil
	}

	target := cfg.PushTarget
	if target == nil {
		httpClient, err := egressClient(wsCfg)
		if err != nil {
			return PushResult{}, fmt.Errorf("%s: %w", reference, err)
		}
		repo, err := oci.NewRemoteRepository(reference, httpClient)
		if err != nil {
			return PushResult{}, fmt.Errorf("%s: %w", reference, err)
		}
		target = repo
	}

	ctx := context.Background()

	// Immutability guard: published bytes are frozen. HEAD the target tag
	// before publishing and compare the digest it points at with the digest
	// this content would produce. Re-publishing byte-identical content is an
	// idempotent no-op; overwriting an existing tag with different content is
	// refused. Changed content is published under a new version — the
	// pre-publication lint gates that bump against the previous release. There is no
	// force escape: the registry has no overwrite path the operator can opt
	// into, by design.
	existingDigest, exists, err := oci.Resolve(ctx, target, tag)
	if err != nil {
		return PushResult{}, fmt.Errorf("resolve %s:%s: %w", reference, tag, err)
	}
	proposedDigest, err := oci.ManifestDigest(ctx, mediaType, bundleBytes, annotations)
	if err != nil {
		return PushResult{}, fmt.Errorf("%s:%s: %w", reference, tag, err)
	}
	if exists {
		if existingDigest == proposedDigest {
			fmt.Fprintf(out, "Already published: %s:%s (%s)\n", reference, tag, existingDigest)
			writePublicationFacts(out, header.Kind, mediaType, len(bundleBytes), 0)
			result.Digest = existingDigest
			result.AlreadyPublished = true
			// The bytes were already there, so no signature was produced on this
			// call whatever the policy asked for.
			result.Signed = false
			return result, nil
		}
		return PushResult{}, fmt.Errorf("%s:%s is already published as %s; refusing to overwrite with %s — publish a new version (aggrega lint --pre-publish verifies the bump): %w",
			reference, tag, existingDigest, proposedDigest, oci.ErrImmutableTagConflict())
	}

	// Sign-before-publish: produce the signature bundle BEFORE any bytes reach
	// the registry. The manifest digest is known in advance, and the bundle
	// commits to the digest string, so the signature is valid for the bytes
	// that will land. If the signer cannot be constructed or signing fails,
	// refuse here — nothing is published, so an unsigned artifact never leaks
	// onto a signature-requiring registry. The bundle is held and attached
	// as a referrer only after the publication succeeds.
	var (
		signatureBundle []byte
		signatureType   string
	)
	if wantSign {
		signatureBundle, signatureType, err = signPublishDigest(ctx, cfg.signerRequest(registry), reference, tag, proposedDigest)
		if err != nil {
			return PushResult{}, err
		}
	}

	digest, err := RetryTransient(ctx, func() (string, error) {
		return oci.Push(ctx, target, tag, mediaType, bundleBytes, annotations)
	}, IsRetryablePublishError)
	if err != nil {
		return PushResult{}, fmt.Errorf("push %s:%s: %w", reference, tag, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, tag, digest)
	writePublicationFacts(out, header.Kind, mediaType, len(bundleBytes), 0)
	result.Digest = digest

	if wantSign {
		if err := attachBundle(ctx, target, tag, signatureType, signatureBundle); err != nil {
			return PushResult{}, fmt.Errorf("attach signature to %s:%s: %w", reference, tag, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", reference, tag)
	}
	return result, nil
}

// BuildPushReference composes the canonical OCI repository reference
// for a catalog entity: <registry>/<kindPathSegment>/<metadata.name>.
// For most kinds the path segment is the lowercased kind name
// (Component → component, Feature → feature, etc.) per the mediatype
// convention (application/vnd.aggrega.<kindLower>.v1...);
// metadata.name may itself contain "/" separators (e.g.,
// "core/server", "core/compute/api-server") and is preserved verbatim.
//
// Plugin kinds carry a pluginType scope in their identity: the path
// segment is `plugin`, and the caller passes the plugin's
// metadata.pluginType as the scope — e.g., pluginType="linter" with a
// bare metadata.name="aggrega-publishing" produces
// <registry>/plugin/linter/aggrega-publishing. The pluginType segment
// is part of the plugin's identity (same name in different sockets is a
// different plugin); deriving the location from pluginType plus the bare
// name keeps the URI symmetric with the catalog filesystem layout.
//
// The publish-side construction here MUST agree with the consume-side
// catalog.SplitURIForOCI which parses URIs back into their
// (reference, tag) pair at the closure walker's registry dispatch. The
// round-trip symmetry is pinned by a test across all publishable catalog kinds.
func BuildPushReference(registry, kind, provider, metadataName string) string {
	return registry + "/" + catalog.OCILocation(kind, provider, metadataName)
}
