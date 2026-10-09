// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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

	// PinSources is this invocation's answer to whether the publication runs the
	// entity author's pin step on the entity before bundling it: nil defers to
	// the workspace's pin_sources_at_push, and a value overrides that setting in
	// either direction. The step is the one every caller that pins reaches, so
	// the lock it writes is the lock `aggrega pin` writes. A publication that does
	// not run the step refuses an entity whose lock does not record every pin the
	// step would record.
	PinSources *bool

	// PinSourcesSupply is how this caller asks for the step on one invocation, in
	// its own vocabulary, carried into that refusal so it names something the
	// caller can actually do. Zero names nothing beyond what every caller shares.
	PinSourcesSupply PinSourcesSupply

	// Workspace is the resolved workspace the caller threads in. Required: Push
	// refuses a nil rather than resolving one of its own, because resolving here
	// would make the result depend on a directory no caller named.
	Workspace *workspaceconfig.ResolvedWorkspace

	// EnvLookup reads the environment for the identity-token secret resolution.
	// Nil means the live process environment.
	EnvLookup func(string) string

	// ConfiguredKeyNotConsulted withholds the signing key this workspace's own
	// configuration names, so only a KeyPath this caller supplied can select the
	// keyed branch. See SignerRequest.ConfiguredKeyNotConsulted for what it
	// declares; false is every caller that owns the machine the key sits on.
	ConfiguredKeyNotConsulted bool

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

	// Now supplies the publication date a publication states beside what it
	// published, where its signature carries no time a transparency log attests.
	// Nil reads the live clock; tests inject a fixed time.
	Now func() time.Time

	// Out is the sink for the operation's user-facing status output (the
	// dry-run line, the publication line, the already-published line, the
	// signature-attached note and the publication-date note). Nil defaults to
	// os.Stdout, so a caller that wants to capture the output passes a buffer
	// rather than mutating a process global.
	Out io.Writer
}

// signerRequest projects the publication's credential inputs onto the shared
// credential boundary's input contract, so the precedence chain and the identity
// ladder are consulted through one path rather than re-spelled per verb.
func (cfg PushConfig) signerRequest(registry string) SignerRequest {
	return SignerRequest{
		Workspace:                 cfg.Workspace,
		Registry:                  registry,
		Signer:                    cfg.Signer,
		KeyPath:                   cfg.KeyPath,
		FulcioURL:                 cfg.FulcioURL,
		RekorURL:                  cfg.RekorURL,
		TimestampURL:              cfg.TimestampURL,
		IDToken:                   cfg.IDToken,
		EnvLookup:                 cfg.EnvLookup,
		ConfiguredKeyNotConsulted: cfg.ConfiguredKeyNotConsulted,
		AcquireIdentityToken:      interactiveAcquirer(cfg.TokenGetter, cfg.IsTTY),
		IdentitySupply:            cfg.IdentitySupply,
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
// The shared-directory predicate Push reaches decodes the same document's kind
// and name a second time, and the two are deliberately separate so nobody folds
// them together on the strength of the fields they share:
//
//   - The predicate is shared with the pin step, which is handed a path and
//     nothing else, so it reads the kind it judges from the document itself
//     rather than trusting a caller to pass one — a kind handed in by one of its
//     two callers would be a way for the two to disagree about one document.
//   - They are not the same shape. The predicate reads two fields; this header
//     reads six, and the labels among them must survive as an ordered map
//     because the whole map is stamped onto the published manifest, which the
//     predicate has no use for.
//
// So this is one document read twice for two unrelated questions, not one
// mechanism written twice. What must stay in step is only the field NAMES the two
// agree on, and those come from the published entity schema rather than from
// either struct.
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

// PublicationBindsExecutable reports whether publishing an entity of kind also
// binds an executable the caller names beside the entity's document: a Plugin,
// whose .wasm is published inside the entity's own manifest.
//
// It is the declaration Push routes on, so the route and every surface that
// reports it are one fact rather than two. A publication of such a kind that
// names no executable is refused with an [ExecutableRequiredError], because the
// publication binds that executable into the entity's manifest. A surface
// answering which kinds a publication naming the document alone accepts reads
// this rather than keeping a kind of its own.
//
// kind is the value a document carries in its top-level `kind:` field.
func PublicationBindsExecutable(kind string) bool {
	return kind == string(entitykind.Plugin)
}

// ExecutableRequiredError refuses the publication of an entity whose kind binds
// an executable — see [PublicationBindsExecutable] — when the caller named none.
//
// It is a type rather than a plain error so a caller can recognise the refusal
// without reading its text. The text names the command line's flag even where
// another surface raised it, so it is prose that may be reworded rather than a
// contract to match against: recognise the refusal with errors.As.
type ExecutableRequiredError struct {
	name string
}

// Error renders the refusal naming the entity whose executable is missing.
func (e *ExecutableRequiredError) Error() string {
	return fmt.Sprintf("Plugin %s: --plugin <path-to-.wasm> is required to publish a Plugin (publish binds the .wasm into the entity's manifest)", e.name)
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
// and published nothing, and an already-published re-publication uploaded no
// content because the bytes were already there, and attached only what that
// publication still lacked.
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
	// Signed reports that this call attached a signature: on a publication, the
	// one it made before publishing; on a version already published, the one it
	// attached because the version carried none that counts as this
	// publication's. It is false on a dry run and wherever nothing was signed.
	Signed bool
	// AlreadyPublished reports that the tag already pointed at byte-identical
	// content, so no content was uploaded. What that publication still lacked —
	// its publication date, and its signature where this one signs — was attached
	// beside it, and nothing was where it lacked neither; Signed says whether a
	// signature was, and the status output names each attachment.
	AlreadyPublished bool
	// DryRun reports that nothing was published: the bundle was computed and the
	// destination was never contacted.
	DryRun bool

	// Pin is what the pin step did to the entity's lock before the bundle was
	// built, and nil where this publication did not run it. On a dry run it
	// reports what the step would have written, and wrote nothing.
	Pin *entitypin.Result

	// WholeDirectory is the authored source reference that made the publication
	// carry every file in the entity's directory, and empty where it carries only
	// the files the entity names. A Recipe or FeatureRecipe whose local source is
	// written as a template picks its directory only once a Release fills the
	// template in, so the publication cannot tell which directory that is and
	// carries all of them; this is how a caller learns that it did.
	WholeDirectory string
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

// writeWholeDirectoryNote writes, beneath the facts above, the one case in which a
// publication carries more than the entity names: a local source written as a
// template, whose directory no publication can know, so every file in the
// entity's directory travels. It writes nothing when source is empty. The
// over-inclusion is the accepted price of keeping such a source publishable, and
// it is said out loud because a file travelling that nothing named is the kind of
// thing an author should not have to discover from a consumer.
func writeWholeDirectoryNote(out io.Writer, source string) {
	if source == "" {
		return
	}
	fmt.Fprintf(out, "  carries: every file in the entity's directory, because its source %q is a template "+
		"and the directory it names is decided by each Release that uses it\n", source)
}

// Push bundles the entity at cfg.EntityPath and publishes it.
// Reads workspace config; parses the entity envelope (kind,
// metadata.name, metadata.version); refuses an entity that shares its
// directory with another entity where its kind has a directory of its own;
// routes a Plugin to the one-manifest packer and every other kind through
// pkg/oci.Bundle, after reading the entity through the catalog's per-entity
// parse for the files it declares beside its document — which the bundle
// carries, and which decide whether it publishes as that document or as an
// archive; constructs the OCI repository reference; publishes via
// pkg/oci.Push.
//
// Signing is sign-before-publish: when signing is requested, Push computes
// the proposed manifest digest, constructs the signer, and signs the digest
// BEFORE any bytes reach the registry. If the signer cannot be constructed or
// signing fails, Push refuses before publishing — zero bytes land, so a failed
// signing never leaves unsigned content on the registry. The signature bundle
// is attached as a referrer only after the publication succeeds; the bundle
// commits to the digest string, so the pre-publication signature is valid for
// the published bytes. An attach that still fails after its retries leaves the
// content published without its signature: Push returns an error naming the
// signature it could not attach, and a consumer of a signature-requiring registry
// refuses the version until one is attached. A second Push of the same content
// that signs attaches it, and Sign attaches one by reference.
//
// A Push that finds the same content already published uploads nothing and
// completes the publication instead (completePublication): it attaches the
// signature, where it signs, if the version carries none that counts as its own
// under the destination's trust policy, and the publication date, if one is owed
// and the version carries none. A publication that is already complete gets
// nothing attached.
//
// Every write to the registry — the content, the manifest, the tag, the
// signature and the date — retries a transient failure inside pkg/oci (see
// oci.IsTransientRegistryError), each attempt a new upload of the same bytes,
// so Push itself wraps none of them; a write still failing fails Push with an
// error naming the step.
//
// A publication then states when it landed, as a published-at referrer, unless
// the signature it attached carries a transparency log's attested time — a
// keyless one does, a keyed one does not, and an unsigned publication has none
// (recordPublicationDate). Neither the entity's manifest nor its tag is touched,
// so the date moves no digest, and a re-publication of the same bytes states a
// date only where the version carries none, stating its own clock: the time the
// publication was completed.
//
// Returns wrapped errors on workspace-config validation failure,
// entity-file read / parse failure, the shared-directory halt
// (entitypin.ErrEntitySharesItsDirectory) for an entity of a kind that has a
// directory of its own with another entity beside it, the unpinned-entity halt
// (recipepin.ErrEntityNotPinned) for a Recipe or FeatureRecipe whose lock does
// not record every pin the pin step would record, where this invocation does not
// run the step, the catalog parse's own refusal for a document it cannot read —
// reached for a Recipe or FeatureRecipe through the pin step or the hold, for
// a Component or Feature before the bundle, and for a Plugin before its .wasm is
// read — bundle failure (including ErrUnknownEntityKind for
// non-publishable kinds), signer-construction / signing failure (refused before
// publishing), and publication failure. The directory's name is not judged: an
// entity alone in its directory publishes wherever that directory sits.
//
// When cfg.DryRun is true, Push prints the proposed
// <reference>:<tag> + mediatype + bundle byte count and returns
// without constructing a remote target. Useful for authoring and
// for verifying the reference-shape construction without binding to
// a registry. Everything a dry run reaches, it judges as a publication would,
// the unpinned-entity halt included.
func Push(ctx context.Context, cfg PushConfig) (PushResult, error) {
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
	// returns below without constructing a target, so it never reaches the
	// registry. It is not network-free for every entity — the pin step, and the
	// hold that stands in for it, acquire a remote source the entity writes out in
	// full, and an offline workspace refuses that acquisition in its own words.
	if !cfg.DryRun {
		if err := refuseIfOffline(rw, "publishing a catalog entity is a network round trip"); err != nil {
			return PushResult{}, err
		}
	}

	dest, err := destination(wsCfg, cfg.Registry, cfg.DestinationSupply)
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

	// An entity of a kind that has a directory of its own is refused here when
	// another entity sits beside it — whether or not this publication pins, and
	// for every kind, a Plugin included. It comes ahead of the pin step because the
	// step writes the one lock the directory has room for, and ahead of every other
	// judgement of the entity because it costs one directory listing and fetches
	// nothing. It is the predicate the pin step asks, reached rather than restated,
	// so a publication refuses exactly the directories `aggrega pin` refuses, in the
	// same words. What it reads beside the document is held to the root the step
	// holds the lock beside it to.
	if err := entitypin.RefuseSharedDirectory(entitypin.NamedDocumentRoot(rw.Root(), cfg.EntityPath), cfg.EntityPath); err != nil {
		return PushResult{}, err
	}

	// The pin step runs here, ahead of every refusal that judges the entity and
	// ahead of the bundle, and both positions are the point. The bundle carries
	// the lock beside the entity, so a lock written after it would be missing
	// from what is published and the published digest would describe a lock
	// that no longer exists. And a publication that pins on the author's behalf
	// pins before the entity is judged, so what is judged is the entity as
	// pinned. The refusals above it are about the invocation — the offline
	// posture, a destination nobody named, a document that is not an entity — or
	// about a directory the entity shares, and no pin could answer any of them.
	//
	// It runs where the entity has a writable home, which a publication always
	// has: the entity is named by its document, and the step reads it from that
	// document's own directory. So the restriction a dependency resolution meets
	// on a cache-resident entity is the same predicate here, satisfied rather
	// than skipped.
	//
	// Whether this publication runs the step is decided once, here, and read
	// again at the hold below: where it runs, the lock it writes is exactly what
	// the hold would compare against, so the hold is satisfied by construction;
	// where it does not, the hold is what stands in for it.
	pinning := wsCfg.PushPinsSources(cfg.PinSources)
	var pinned *entitypin.Result
	if pinning {
		pinned, err = pinBeforePublishing(ctx, out, cfg, rw)
		if err != nil {
			return PushResult{}, err
		}
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
	requiresSignatures := wsCfg.RegistrySigningRequiredForKind(dest.Host, header.Kind)
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
				dest.Host, signing.ErrUnsignedPushRefused())
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
	//
	// The route reads the one declaration of which kinds take it, so a surface
	// reporting which kinds a publication naming the document alone accepts reads
	// the same fact this route acts on.
	if PublicationBindsExecutable(header.Kind) {
		res, perr := pushPlugin(ctx, out, cfg, dest, wantSign, header.Metadata.Name, header.Metadata.Version, cfg.EntityPath, data)
		if perr == nil {
			res.Pin = pinned
		}
		return res, perr
	}

	// The lock is held to the pin step here, immediately before the bundle and
	// after every refusal that costs nothing, because it is the one judgement on
	// this path that may acquire a source: a refusal here builds no archive and
	// reaches no registry, and an invocation a cheaper refusal would have stopped
	// never pays for the acquisition. A Plugin never reaches it — its publication
	// holds its own machine-owned records to its binary above and does not route
	// through the bundler — and a dry run reaches it exactly as a publication does,
	// so a dry run that reports a bundle is one this would not have refused.
	if !pinning {
		if err := holdBeforeBundling(ctx, cfg, rw); err != nil {
			return PushResult{}, err
		}
	}

	// The entity is read here the way a build reads it, and for the same reason as
	// the hold above sits where it does: immediately before the bundle, after every
	// refusal that is about the invocation rather than the entity. It judges the
	// entity, so it follows the pin step rather than preceding it — and it must,
	// because the lock the step writes is one of the files the entity carries. It
	// reads only files beside the entity, so it costs less than any fetch a later
	// step makes.
	declared, err := declaredBeforeBundling(header.Kind, cfg.EntityPath, data)
	if err != nil {
		return PushResult{}, err
	}

	bundleBytes, mediaType, err := oci.Bundle(header.Kind, cfg.EntityPath, declared.Files())
	if err != nil {
		return PushResult{}, fmt.Errorf("bundle %s: %w", cfg.EntityPath, err)
	}

	reference := BuildPushReference(dest.Prefix(), header.Kind, header.Spec.Capability, header.Metadata.Name)
	tag := header.Metadata.Version
	result := PushResult{
		Reference:      reference,
		Tag:            tag,
		Kind:           header.Kind,
		MediaType:      mediaType,
		Size:           len(bundleBytes),
		Signed:         wantSign,
		Pin:            pinned,
		WholeDirectory: declared.WholeDirectory(),
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
		writeWholeDirectoryNote(out, result.WholeDirectory)
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

	// The last point before the publication becomes visible to other readers.
	// Everything above is local — reading the entity, evaluating its signing
	// policy, building the bundle — and a caller that withdrew discards all of it;
	// what follows reaches the registry and cannot be taken back, because published
	// bytes are frozen and the registry offers no overwrite path.
	if serr := stopped(ctx); serr != nil {
		return PushResult{}, serr
	}

	// Immutability guard: published bytes are frozen. HEAD the target tag
	// before publishing and compare the digest it points at with the digest
	// this content would produce. Re-publishing byte-identical content uploads
	// nothing and completes what that publication still lacks; overwriting an
	// existing tag with different content is refused. Changed content is
	// published under a new version — the pre-publication lint gates that bump
	// against the previous release. There is no force escape: the registry has no
	// overwrite path the operator can opt into, by design.
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
			writeWholeDirectoryNote(out, result.WholeDirectory)
			result.Digest = existingDigest
			result.AlreadyPublished = true
			policy, _ := wsCfg.RegistryTrustPolicy(dest.Host)
			c := completion{
				target: target, reference: reference, tag: tag, digest: existingDigest,
				wsCfg: wsCfg, policy: policy, now: cfg.Now,
			}
			if wantSign {
				req := cfg.signerRequest(dest.Host)
				c.sign = func(ctx context.Context, digest string) ([]byte, string, error) {
					return signPublishDigest(ctx, req, reference, tag, digest)
				}
			}
			signed, cerr := completePublication(ctx, out, c)
			if cerr != nil {
				return PushResult{}, cerr
			}
			result.Signed = signed
			return result, nil
		}
		return PushResult{}, fmt.Errorf("%s:%s is already published as %s; refusing to overwrite with %s — publish a new version (aggrega lint --pre-publish verifies the bump): %w",
			reference, tag, existingDigest, proposedDigest, oci.ErrImmutableTagConflict())
	}

	// Sign-before-publish: produce the signature bundle BEFORE any bytes reach
	// the registry. The manifest digest is known in advance, and the bundle
	// commits to the digest string, so the signature is valid for the bytes
	// that will land. If the signer cannot be constructed or signing fails,
	// refuse here — nothing is published, so a failed signing leaves no
	// unsigned content on the registry. The bundle is held and attached
	// as a referrer only after the publication succeeds.
	var (
		signatureBundle []byte
		signatureType   string
	)
	if wantSign {
		signatureBundle, signatureType, err = signPublishDigest(ctx, cfg.signerRequest(dest.Host), reference, tag, proposedDigest)
		if err != nil {
			return PushResult{}, err
		}
	}

	digest, err := oci.Push(ctx, target, tag, mediaType, bundleBytes, annotations)
	if err != nil {
		return PushResult{}, fmt.Errorf("push %s:%s: %w", reference, tag, err)
	}
	fmt.Fprintf(out, "Push complete: %s:%s (%s)\n", reference, tag, digest)
	writePublicationFacts(out, header.Kind, mediaType, len(bundleBytes), 0)
	writeWholeDirectoryNote(out, result.WholeDirectory)
	result.Digest = digest

	if wantSign {
		if err := attachBundle(ctx, target, tag, signatureType, signatureBundle); err != nil {
			return PushResult{}, fmt.Errorf("attach signature to %s:%s: %w", reference, tag, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", reference, tag)
	}
	if err := recordPublicationDate(ctx, out, target, reference, tag, signatureBundle, cfg.Now); err != nil {
		return PushResult{}, err
	}
	return result, nil
}

// declaredBeforeBundling reads the entity through the catalog's own per-entity
// parse and returns the files its declarations name beside its document, which
// are the files the bundle carries.
//
// Reading through the parse is what makes a publication refuse exactly the
// documents a catalog load of them refuses, in the same words: one that fails its
// kind's schema, one naming content outside the entity's own directory, and a
// Component or Feature whose imported schema file is missing or unusable.
// Publishing such a document would ship an entity whose consumer's first load
// refuses it. It is the parse a build reaches, not a check of its own: a
// publication that judged these documents by a reading of its own would refuse a
// different set, and the two would disagree the first time either was changed. A
// Recipe or FeatureRecipe has already been read through the same parse by the pin
// step or the hold that stands in for it, so for those two kinds this reading
// finds nothing that one did not.
//
// It is also the one reading of which files travel, and so of the shape the
// publication takes: an entity carrying nothing beside its document publishes as
// that document. A Plugin never reaches this point, and every other kind declares
// no file of its own and publishes as its document.
func declaredBeforeBundling(kind, path string, data []byte) (catalog.DeclaredFiles, error) {
	declared, err := catalog.DeclaredFilesOf(data, path)
	if err != nil {
		return catalog.DeclaredFiles{}, fmt.Errorf("parse %s at %q: %w", kind, path, err)
	}
	return declared, nil
}

// BuildPushReference composes the canonical OCI repository reference
// for a catalog entity: <registry>/<kindPathSegment>/<metadata.name>,
// registry being the destination's location prefix — its host, followed by
// the repository path a default registry carries, if any — joined through
// catalog.RegistryReference, the one join every pull tier binds through.
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
	return catalog.RegistryReference(registry, kind, provider, metadataName)
}
