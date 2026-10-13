// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Config is the single-entity lint operation's input contract. A caller
// assembles it from whatever state it holds — flag globals, a tool request —
// and hands it to RunEntity. FetchTarget overrides registry construction for
// end-to-end tests (in-memory oras.ReadOnlyTarget).
type Config struct {
	EntityPath string
	PrePublish bool
	// Baseline optionally forces the pre-publish predecessor to a specific
	// already-published version instead of the auto-selected highest version
	// strictly below the candidate. Empty selects the default. Consulted only on
	// the pre-publish single-entity path.
	Baseline       string
	PluginPath     string
	PolicyResolver func(flagBase string, wsValue bool) bool
	FetchTarget    oras.ReadOnlyTarget

	// Workspace is the resolved workspace the caller threads in. Nil only when a
	// test invokes RunEntity directly; workspaceOrResolve then resolves from the
	// working directory.
	Workspace *workspaceconfig.ResolvedWorkspace

	// DiagOut is the sink for lint's diagnostic output (the first-publication
	// note, the pre-release-only warning, and the
	// findings report). Nil defaults to os.Stderr at the top of RunEntity;
	// callers pass a buffer to capture diagnostics without mutating a process
	// global.
	DiagOut io.Writer

	// Out is the sink for lint's success confirmation, kept separate from DiagOut
	// so the positive signal lands on stdout while diagnostics stay on stderr.
	// Nil defaults to os.Stdout at the top of RunEntity; callers pass a buffer to
	// capture the confirmation.
	Out io.Writer

	// Collect optionally gathers the run's findings as data alongside the human
	// report, for a caller that renders them structurally. Nil collects nothing
	// and leaves every other behaviour untouched.
	Collect *FindingCollector

	// ProposedBytes optionally supplies the entity document to lint, in place of
	// reading EntityPath off disk. Nil reads the file, which is what every
	// terminal invocation does.
	//
	// It exists for a caller holding a copy of the document the filesystem does
	// not have yet — an editor session linting the buffer someone is typing in.
	// EntityPath is still required and is still what the rules are told about,
	// because a rule that compares an entity's identity against the directory it
	// sits in is asking about the path rather than about the bytes; supplying the
	// bytes changes what is read, never where the document is said to be.
	//
	// A caller supplying an empty (but non-nil) document gets whatever the rules
	// make of an empty document, rather than a silent re-read: the two cases are
	// different questions and reading the file back would answer the wrong one.
	ProposedBytes []byte
}

// workspaceOrResolve returns ws when set — the production path, where a caller
// threads in the context-resolved workspace. When ws is nil it resolves from
// the current directory; that path is reachable only from tests that invoke the
// operation helpers directly.
func workspaceOrResolve(ws *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if ws != nil {
		return ws, nil
	}
	return workspaceconfig.Resolve("", "")
}

// stopped reports that the caller no longer wants the run, as an error that
// errors.Is matches against context.Canceled and context.DeadlineExceeded. It
// returns nil while the caller is still waiting, which is every ordinary run.
//
// It is one function rather than an inline check written at each interruption
// point so the sentence a stop produces is stated once. Two hand-written
// phrasings of one condition agree until either of them is edited, and nothing
// fails when they stop agreeing — the caller matching the wrapped cause keeps
// working while the reader is told two different things.
//
// The cause is wrapped rather than replaced because it is the part a caller acts
// on: a surface that distinguishes "the client withdrew the request" from "the
// budget ran out" reads it off the wrapped error, and a surface that does not
// care prints the sentence.
func stopped(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("aggrega lint: stopped before the check finished: %w", err)
	}
	return nil
}

// lintEntityHeader is the minimum YAML envelope lint parses out of the proposed
// entity file: kind, metadata.name, metadata.version, metadata.labels,
// spec.capability, and spec.implements. It extends the publishable-entity header
// shape for the core-plugin evolution-gate exemption and for resolving the
// Component a Recipe realizes.
type lintEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name       string            `yaml:"name"`
		PluginType string            `yaml:"pluginType"`
		Version    string            `yaml:"version"`
		Labels     map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Capability string `yaml:"capability"`
		// Implements is the Component reference a Recipe declares. It carries a
		// version pin in any of the three forms the reference accepts, so it is
		// resolved through the catalog lookup rather than compared as a string.
		Implements string `yaml:"implements"`
	} `yaml:"spec"`
}

// defaultRuleIDs returns the rule set to invoke per mode, in dispatch order.
// Default mode runs the structural rules; --pre-publish additionally runs the
// rule that needs the previously published bytes to compare against.
//
// The identifiers are read off the declared rule set rather than written out
// here — see publishingRules, which is that set's single home and carries the
// lockstep obligation against the guest's dispatch arms.
//
// A rule reading a whole Release is not in either set: it has no entity file to
// be dispatched over. See releaseRuleIDs.
func defaultRuleIDs(prePublish bool) []string {
	return entityRuleIDsOf(publishingRules, prePublish)
}

// releaseRuleIDs returns the rules a release-scoped run dispatches once over the
// Release it names, in dispatch order, read off the same declared rule set.
func releaseRuleIDs() []string {
	return releaseRuleIDsOf(publishingRules)
}

// entityRuleIDsOf selects, from rules, those dispatched per entity in the given
// mode. It takes the set as an argument so the partition between the two scopes
// is tested over a set holding both, whatever the shipped set holds today.
func entityRuleIDsOf(rules []publishingRule, prePublish bool) []string {
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.releaseScoped || (r.prePublish && !prePublish) {
			continue
		}
		ids = append(ids, ruleID(r.condition))
	}
	return ids
}

// releaseRuleIDsOf selects, from rules, those that read a whole Release.
func releaseRuleIDsOf(rules []publishingRule) []string {
	var ids []string
	for _, r := range rules {
		if r.releaseScoped {
			ids = append(ids, ruleID(r.condition))
		}
	}
	return ids
}

// pluginIsCore reports whether a Plugin's derived identity coordinate
// (<pluginType>/<name>) names one of the built-in plugins shipped inside the
// binary. The embedded set enumerates those coordinates the built-ins register
// under — derived from a Plugin's metadata.pluginType and bare metadata.name —
// so membership is an exact-string match against that set.
func pluginIsCore(coordinate string) bool {
	if coordinate == "" {
		return false
	}
	for _, uri := range embedded.URIs() {
		if uri == coordinate {
			return true
		}
	}
	return false
}

// RunEntity runs the publishing-discipline linter over one catalog entity. In
// default mode the rules run at warn severity over local content without
// consulting any registry. With PrePublish the previous registry version is
// fetched and the schema-evolution classification is compared against the
// proposed version bump; a mismatch halts. It writes the findings report to
// DiagOut and the success confirmation to Out, and returns a non-nil error when
// the run halts.
//
// The two modes differ in severity because they differ in consequence. Default
// mode is an authoring aid over content that has not been published and can
// still be changed freely, so a finding is advice; nothing about the local tree
// is a commitment to anyone. Pre-publish mode is the last point before content
// becomes immutable to every consumer, so the same finding becomes a refusal.
// The evolution check is confined to pre-publish for a structural reason rather
// than a policy one: classifying a change requires the previous published bytes
// to compare against, and in default mode there is no registry to read them from.
//
// A first publication has no predecessor and therefore nothing to classify. That
// case is distinguished from a genuine absence of history rather than assumed:
// an entity whose published tags are all pre-releases is not a first
// publication, and treating it as one would wave through a bump the check exists
// to verify. Where there is genuinely no prior version the bump check is skipped
// and the run says so, so a silent pass and an unverifiable one never look alike.
//
// ctx ends the run where the caller stops wanting it. It reaches the two places
// this operation spends real time — the previous-version fetch, which is the
// only step that leaves the machine, and the rule dispatch, which runs the
// linter guests — so a caller that gives up gets the work abandoned rather than
// only the answer discarded. A caller with nothing to cancel passes a background
// context and every check below is a no-op.
func RunEntity(ctx context.Context, cfg Config) error {
	if cfg.DiagOut == nil {
		cfg.DiagOut = os.Stderr
	}
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	if cfg.EntityPath == "" {
		return fmt.Errorf("aggrega lint: entity path required (pass as positional arg or via --entity)")
	}
	rw, err := workspaceOrResolve(cfg.Workspace)
	if err != nil {
		return err
	}
	wsCfg := rw.Config()

	// A lazy lookup over the workspace catalog dirs serves two unrelated reads:
	// the Plugin entity each configured linter URI names, and the Component a
	// linted Recipe realizes. It is constructed unconditionally because it indexes
	// nothing until it is asked something — the walk happens on the first query,
	// which for a non-Recipe entity on the --plugin path never comes.
	// This pass names no Release, so it reads the workspace's own stack.
	dirs := rw.ResolveCatalogDirs("", nil)
	lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil, rw.NamedCatalogDirs("", nil)...)

	// Every configured linter URI is a Plugin entity URI; the entity-front derives
	// each entity's artifact reference from its identity. The --plugin override
	// path is artifact-direct and never consults the catalog for the plugin, so
	// the feeders below stay nil there.
	var pluginByURI func(uri string) (types.Plugin, bool)
	var cacheResolvers []func(uri string) (pluginpath.ResolvedPlugin, error)
	// The single-entity pass resolves its linter attachments pin-free, and the
	// reason is structural rather than pending work. The record of the plugin
	// identities trusted for a workspace's verdict attachments sits beside a
	// Release, and this pass names no Release: it lints one proposed entity at
	// publish time, which is the same reason it reads no lockfile. The
	// release-scoped conventions pass is the lint path that names one, and it holds
	// its attachments to the record. Threading the empty set rather than branching
	// keeps one call shape.
	var trusted resolver.VerdictPluginTrust
	if cfg.PluginPath == "" {
		pluginByURI = lazyView.PluginByURI
		cacheResolvers = []func(uri string) (pluginpath.ResolvedPlugin, error){
			linterCacheResolve(rw.Root(), wsCfg, trusted, lazyView.PluginFromCacheByURI),
		}
	}
	attachments, err := ResolveLinterPlugins(cfg.PluginPath, wsCfg, trusted, pluginByURI, cacheResolvers...)
	if err != nil {
		return err
	}

	proposedBytes := cfg.ProposedBytes
	if proposedBytes == nil {
		proposedBytes, err = os.ReadFile(cfg.EntityPath)
		if err != nil {
			return fmt.Errorf("aggrega lint: read %s: %w", cfg.EntityPath, err)
		}
	}
	proposedJSON, header, err := yamlToLinterJSON(proposedBytes)
	if err != nil {
		return fmt.Errorf("aggrega lint: parse %s: %w", cfg.EntityPath, err)
	}

	// Advisory: when the linted target is a Plugin entity whose .wasm is
	// resolvable, report where the binary offers more than the entity curates
	// (binary ⊇ entity). It never halts and never changes the exit status; when the
	// .wasm is not resolvable it skips with a one-line note.
	if header.Kind == "Plugin" {
		pluginUnderDeclarationAdvisory(ctx, proposedBytes, wsCfg, cfg.DiagOut)
	}

	policyResolve := cfg.PolicyResolver
	if policyResolve == nil {
		policyResolve = func(_ string, ws bool) bool { return ws }
	}
	policies := effectivePolicies(wsCfg.Lint.PublishingDiscipline, policyResolve)

	// A core plugin's describe()-surface is version-locked to the binary and
	// publishes only on the compiler's own release, so the publish-time
	// evolution gate does not apply to it. Drop the schema-evolution rule and
	// skip the previous-version fetch a non-core plugin would perform. The
	// exemption short-circuits before any registry consultation.
	prePublish := cfg.PrePublish
	if cfg.PrePublish && header.Kind == "Plugin" && pluginIsCore(types.PluginCoordinate(header.Metadata.PluginType, header.Metadata.Name)) {
		fmt.Fprintf(cfg.DiagOut, "core plugin: publish evolution gate not applicable\n")
		prePublish = false
	}

	var (
		previousJSON     json.RawMessage
		previousVersion  string
		firstPublication bool
	)
	if prePublish {
		previousJSON, previousVersion, err = fetchPreviousRegistryVersion(
			ctx, cfg.FetchTarget, wsCfg, header, cfg.Baseline, cfg.DiagOut,
		)
		if err != nil {
			return fmt.Errorf("aggrega lint: fetch previous: %w", err)
		}
		firstPublication = previousVersion == ""
	}

	inputs := buildRuleInputs(ruleInputSources{
		Kind:                 header.Kind,
		Proposed:             proposedJSON,
		Previous:             previousJSON,
		ImplementedComponent: implementedComponentJSON(header, lazyView.Component),
		Policies:             policies,
		EntityPath:           cfg.EntityPath,
		PrePublish:           prePublish,
	})
	// Checked immediately before the guests run rather than only on entry,
	// because everything above it — the workspace resolution, the attachment
	// resolution, the previous-version fetch — is time in which a caller can give
	// up, and answering here names the withdrawal plainly instead of surfacing it
	// as a guest call that was unwound. The dispatch below carries ctx too, so a
	// caller who withdraws once the guests are running is not waiting on them.
	if err := stopped(ctx); err != nil {
		return err
	}
	rawFindings, err := dispatchLinterAttachments(ctx, attachments, inputs)
	if err != nil {
		return fmt.Errorf("aggrega lint: dispatch: %w", err)
	}

	aggregated := aggregateFindings(rawFindings)
	if !prePublish {
		aggregated = downgradeToWarning(aggregated)
	}

	if firstPublication {
		fmt.Fprintf(cfg.DiagOut,
			"first publication of %q; no version-bump verification performed\n",
			header.Metadata.Name)
		aggregated = downgradeFirstPublicationToWarning(aggregated)
	}

	// Bump-vs-classification check runs BEFORE reporting findings so
	// the schema-evolution finding can be downgraded to warning when
	// the bump satisfies the classification. The rule body emits the
	// classification at Error severity (it's the rule's only output
	// channel); whether that classification IS a violation depends on
	// the proposed bump direction — which only the host knows. When
	// the bump satisfies, the finding is informational; when it
	// doesn't, the host surfaces its own explicit refusal.
	if prePublish && !firstPublication {
		verdict := classificationFromFindings(aggregated)
		if verdict != "" {
			proposedBump := bumpDirection(previousVersion, header.Metadata.Version)
			if !proposedBumpSatisfies(proposedBump, verdict) {
				cfg.Collect.Add(aggregated, cfg.EntityPath)
				cfg.Collect.Entities(1)
				// The findings are reported first and the refusal joined after
				// them, so the bump refusal reads as the verdict over the
				// findings rather than as one more of them.
				return stderrors.Join(
					reportFindings(aggregated, header.Kind, cfg.EntityPath, cfg.DiagOut),
					bumpRefusal(header, previousVersion, proposedBump, verdict, cfg.EntityPath),
				)
			}
			// Bump satisfies the classification — downgrade the
			// schema-evolution finding to warning so it is reported as an
			// advisory rather than halting the run.
			aggregated = downgradeRuleToWarning(aggregated, ruleID(errSchemaEvolution))
		}
	}

	cfg.Collect.Add(aggregated, cfg.EntityPath)
	cfg.Collect.Entities(1)
	if err := reportFindings(aggregated, header.Kind, cfg.EntityPath, cfg.DiagOut); err != nil {
		return err
	}
	fmt.Fprintf(cfg.Out, "lint passed: %s\n", header.Metadata.Name)
	return nil
}

// bumpRefusal builds the refusal raised when the version offered for
// publication is smaller than the evolution classification requires.
//
// It is the host's own verdict rather than a rule's finding: the rule reports
// what changed and how that classifies, and only the host knows which bump was
// offered — so the two are separate conditions and this one names the bump.
//
// The occurrence's values are the three facts a reader needs to choose a
// different version: the classification the change was given, the predecessor it
// was compared against, and the bump the offered version actually is.
func bumpRefusal(header lintEntityHeader, previousVersion, proposedBump, verdict, entityPath string) error {
	category, _ := cerrors.ClassifyEntityKind(header.Kind)
	return cerrors.Raise(errPublishBumpBelowClassification, category,
		cerrors.SourceLocation{File: entityPath},
		cerrors.ComposeMessage(errPublishBumpBelowClassification, []string{
			"classified " + verdict,
			previousVersion + " -> " + header.Metadata.Version,
			"which is a " + proposedBump + " bump",
		}, ""),
		nil)
}

// downgradeRuleToWarning re-severitizes findings whose RuleID matches the given
// id from the halting severity to the advisory one. Used for the
// schema-evolution rule when the proposed bump satisfies its classification
// verdict: the rule reports at the halting severity to convey the
// classification, and once the bump is verified compatible the finding is an
// advisory rather than a reason to stop.
func downgradeRuleToWarning(findings []abi.Diagnostic, ruleID string) []abi.Diagnostic {
	out := make([]abi.Diagnostic, len(findings))
	for i, f := range findings {
		if f.RuleID == ruleID && f.Severity == abi.SeverityHalt {
			f.Severity = abi.SeverityWarn
		}
		out[i] = f
	}
	return out
}

// LinterAttachment is one resolved linter attachment: the plugin the
// attachment's URI resolved to, paired with the export function that same
// attachment names. The pair travels together because each attachment names its
// own export, so a plugin taken from one attachment and an export taken from
// another would run the wrong entry point — and a slice of plugins beside a
// parallel slice of exports is precisely the shape that lets the two drift out
// of step.
//
// An empty Export means the attachment named no function, and the socket's own
// default export applies.
type LinterAttachment struct {
	Plugin pluginpath.ResolvedPlugin
	Export string
}

// linterAttachmentRefs returns the linter attachments the lint gate runs, in
// authored order. It is the one place that answers "which linter attachments do
// we run": it reads the workspace's verdict attachment set — the single spelling
// of that set — and keeps the entries attached at the linter socket. Reading the
// set here rather than indexing the socket's own list is what makes the gate's
// answer and every other consumer's answer the same answer by construction
// instead of by coincidence.
//
// An attachment carrying no plugin URI is not in the set: it identifies no
// plugin, so there is nothing for it to attach. Its export function names an
// entry point on a plugin that was never named and is not carried either — the
// unconfigured-socket fallback runs the built-in guest under that guest's own
// default export.
func linterAttachmentRefs(wsCfg workspaceconfig.Config) []workspaceconfig.PluginRef {
	var refs []workspaceconfig.PluginRef
	for _, attachment := range wsCfg.VerdictAttachments() {
		if attachment.Socket == workspaceconfig.SocketLinter {
			refs = append(refs, attachment.Ref)
		}
	}
	return refs
}

// ResolveLinterPlugins resolves EVERY linter attachment the workspace
// configures, in authored order, returning each attachment's plugin (its .wasm
// bytes plus any entity capability claim and runtime limits) paired with the
// export function that attachment names, so the caller maps each pair onto its
// own load spec. It is the linter's per-socket URI feeder.
//
// The --plugin flag is the explicit escape-hatch override: a direct artifact
// path read verbatim through the cascade's override, bypassing the entity model.
// It resolves exactly ONE plugin however many attachments are configured — an
// escape hatch that fanned out over a configured set would be a different
// feature. It overrides the socket's uri and not its function, so the export
// stays the one the first configured attachment names.
//
// A configured attachment's uri is a Plugin entity URI, resolved through the
// shared entity-front: entity URI → artifact reference → the cascade (embed →
// colocated → local → cache), so the linter sees the same entity model and the
// same tier model every plugin socket does, including the embedded built-ins.
//
// Only the entity-URI path carries a capability claim and runtime limits: the
// entity-front attaches them from the in-hand Plugin entity, so the linter load
// gains the entity's describe()-vs-entity cross-check and its explicit caps. The
// --plugin override path (artifact-direct) and the unconfigured path carry a nil
// claim and nil limits — the cross-check is skipped and the loader applies the
// platform default caps.
//
// pluginByURI serves a Plugin entity by URI from the catalog, supplied by the
// caller (nil in a catalog-less path, where a configured entity URI then halts as
// not-an-entity); the entity-front derives the artifact reference from the
// entity's identity. The --plugin override path does not consult it — the escape
// hatch is artifact-direct.
//
// The linter reads no lockfile: it lints a not-yet-pinned proposed entity at
// publish time, so there is nothing in a lockfile to verify the plugin against.
// Plugin resolution fetches nothing, so it never reaches a registry: an attachment
// resolves from the bytes in the binary or from the already-populated content
// cache, and the --plugin escape hatch from the explicit path it names.
//
// The linter socket is a verdict socket: a configured, non-embedded linter guest
// is cached at pull but not pinned (cache-not-pin), so every configured-URI
// resolution is wrapped in the shared verdict-lookup — the pinned / Tier-1
// cascade is tried first, and an entity-URI that misses there falls through to
// the pull-populated content cache, read by URI and digest-checked against the
// cached manifest. This mirrors the policy socket exactly. The optional
// cacheResolve is the caller's workspace-root-anchored cache resolver, threaded
// from the caller so the cache read anchors at the resolved workspace root; a
// direct invocation that omits it falls back to a working-directory-anchored
// resolver built from wsCfg.
//
// # What a recorded trusted digest adds, per case
//
// trusted carries the digests recorded as trusted for the plugins that return an
// accept-or-reject verdict, keyed by plugin identity, the digests of the cached
// signature bundles their recorded signatures name, and the signature
// requirement a plugin the record carries no signature for is held to. Supplied
// and non-empty, it turns the first arm from a pin-free read into one that
// verifies the resolved bytes against the recorded digest; supplied empty — the
// ordinary case, and the case when nothing has recorded anything yet —
// resolution is pin-free exactly as before. An absent record is a first-run
// state rather than a fault, and never a pin of zero. The second arm, the
// content cache, re-hashes a recorded signature's bundle and puts a plugin whose
// record carries none to the requirement, as a build holds the same plugin. The
// cases differ, and none of them is "unverified until now":
//
//   - A configured attachment whose plugin is served from the pull-populated
//     content cache is already digest-checked today, against a pin synthesized
//     from that plugin's own cached manifest. What a recorded digest adds there is
//     the comparison against the recorded identity, which is what makes the pin an
//     authority instead of a restatement of the artifact it is checking.
//   - A configured attachment served by the embed tier is checked against nothing
//     today. That tier fronts the cache and consults no manifest, so a resolution
//     it satisfies has no digest to verify against — and this socket resolves an
//     attachment through exactly two tiers, embed and the content cache, because it
//     wires no co-located directories and an artifact reference is never a local
//     path. So the embed tier is where a recorded digest adds a
//     check that did not exist, and only for a plugin a record actually names.
//   - The --plugin override is out of reach, and by construction rather than by
//     policy: it is artifact-direct, resolving through the cascade's override by an
//     empty reference, while a recorded digest is addressed by a plugin identity.
//     The record-to-pin conversion refuses an empty address, so no record reaches
//     it.
//   - The unconfigured socket's built-in default is out of reach because an
//     unconfigured socket declares no attachment, so nothing resolves an identity
//     for it to be recorded under. Note what this is NOT: the built-in resolves by
//     its own identity, which is as addressable as any other, so a record standing
//     at that identity would govern the built-in's bytes. A hand-written record
//     could therefore still name it; what is guaranteed is narrower and is stated
//     below.
//
// The resolution that records these digests declines, deliberately, to record any
// coordinate the binary carries: a pull records what it ACQUIRED, and there is
// nothing to acquire for bytes that ship inside the binary. So no record this
// product writes ever stands at the built-in's identity, nor at the identity of
// any other plugin the binary carries — which makes the two cases above that turn
// on the embed tier reachable only by a record somebody wrote by hand.
//
// A registry-fetched plugin is separately signature- and host-function-verified
// when it is pulled. A recorded digest adds an identity check to resolution; it
// does not introduce integrity checking where integrity checking already existed.
//
// A resolution failure at ANY attachment halts the whole resolution: an
// attachment the operator configured and the gate cannot load is a loud
// guardrail, never an attachment to quietly drop.
func ResolveLinterPlugins(cliFlag string, wsCfg workspaceconfig.Config, trusted resolver.VerdictPluginTrust, pluginByURI func(uri string) (types.Plugin, bool), cacheResolve ...func(uri string) (pluginpath.ResolvedPlugin, error)) ([]LinterAttachment, error) {
	refs := linterAttachmentRefs(wsCfg)
	// The posture is named from what the caller actually supplied rather than
	// fixed at the construction: with recorded digests in hand the first arm
	// verifies resolved bytes against them, and with none it stays the pin-free
	// read it has always been. Naming a pinned posture over an empty set would
	// read as a pin nobody has, which is the confusion the posture axis exists to
	// prevent.
	posture := pluginpath.PinPostureNone
	if len(trusted.Pins) > 0 {
		posture = pluginpath.PinPostureVerifyOnRead
	}
	cascade := pluginpath.NewResolver(pluginpath.ResolutionContext{
		Override: cliFlag,
	}, posture, trusted.PinMaps)
	// The --plugin override is the artifact-direct escape hatch: resolve it
	// verbatim through the cascade override, never the entity model. The cascade
	// override path sets no Contract/Limits, so the load gets a nil claim and
	// default caps — today's override behaviour.
	if cliFlag != "" {
		resolved, err := cascade.Resolve("")
		if err != nil {
			return nil, err
		}
		export := ""
		if len(refs) > 0 {
			export = refs[0].Function
		}
		return []LinterAttachment{{Plugin: resolved, Export: export}}, nil
	}
	if len(refs) == 0 {
		// Unconfigured socket. The per-socket capability-default policy decides
		// whether an unconfigured socket prefers a capable embedded plugin: the
		// linter opts in, so it falls back to the built-in publishing-linter
		// embedded in the binary, resolved through the cascade so its embed tier
		// serves it. Like the --plugin escape hatch, the built-in path carries a
		// nil capability claim and the platform default caps. A socket that opted
		// out would resolve no default here.
		if !workspaceconfig.SocketPrefersCapabilityDefault(workspaceconfig.SocketLinter) {
			return nil, fmt.Errorf("no linter plugin configured and the linter socket does not fall back to an embedded default")
		}
		resolved, err := cascade.Resolve(embedded.PublishingLinterURI)
		if err != nil {
			return nil, err
		}
		return []LinterAttachment{{Plugin: resolved}}, nil
	}
	front := pluginpath.EntityLookup(func(entityURI string) (string, *pluginpath.PluginContractClaim, *pluginpath.PluginRuntimeLimits, *[]pluginpath.PluginCommandMirror, bool) {
		if pluginByURI == nil {
			return "", nil, nil, nil, false
		}
		entity, ok := pluginByURI(entityURI)
		if !ok {
			return "", nil, nil, nil, false
		}
		return entity.ArtifactReference(), resolver.PluginContractClaim(entity), resolver.PluginRuntimeLimits(entity), resolver.PluginCommandsMirror(entity), true
	}, cascade.Resolve, pluginpath.SurfacePermissive)

	// Verdict socket: an entity-URI that the pinned / Tier-1 cascade does not
	// surface falls through to the pull-populated content cache (cache-not-pin),
	// exactly as the policy socket does. The caller threads a workspace-root-
	// anchored resolver; a direct invocation falls back to one anchored at the
	// working directory.
	cr := linterCacheResolveOrDefault(wsCfg, trusted, cacheResolve)
	lookup := resolver.VerdictLookup(front, cr)
	attachments := make([]LinterAttachment, 0, len(refs))
	for _, ref := range refs {
		resolved, err := lookup(ref.URI)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, LinterAttachment{Plugin: resolved, Export: ref.Function})
	}
	return attachments, nil
}

// dispatchLinterAttachments runs inputs at every resolved linter attachment, in
// authored order, returning the per-dispatch findings concatenated in that same
// order — the first attachment's per-rule results, then the second's, and so on.
// Each attachment is dispatched with ITS OWN export function, so one
// attachment's entry point is never read off another.
//
// The concatenation is what the existing per-rule aggregation then flattens, so
// no aggregation, severity or reporting rule changes because more than one
// attachment ran. A dispatch failure at any attachment halts the run rather than
// dropping that attachment's verdict: a gate the operator configured and the
// host could not run is a loud failure, never a silent skip.
//
// ctx reaches each attachment's guest, so a caller who gives up part way through
// a multi-attachment run ends the evaluation that is running rather than waiting
// for the remaining attachments to be asked and answered.
func dispatchLinterAttachments(ctx context.Context, attachments []LinterAttachment, inputs []validator.LinterInput) ([][]abi.Diagnostic, error) {
	var raw [][]abi.Diagnostic
	for _, attachment := range attachments {
		spec := resolver.PluginSpec("publishing-linter", attachment.Plugin)
		findings, err := validator.DispatchPublishingLinterBatch(ctx, spec, attachment.Export, inputs)
		if err != nil {
			return nil, err
		}
		raw = append(raw, findings...)
	}
	return raw, nil
}

// linterCacheResolve returns the verdict-socket cache-not-pin resolver the linter
// falls back to when the pinned / Tier-1 cascade does not surface a configured,
// non-embedded linter guest. The guest is cached at pull but never pinned, so it
// is read from the content cache by URI and its .wasm is digest-checked against
// the cached manifest. workspaceRoot and wsCfg anchor the content cache; cacheRead
// reads a Plugin entity from that cache by URI.
//
// trusted is threaded on so the fallback arm compares the same recorded digests
// the first arm verifies against. The two arms are the same socket resolving the
// same attachment through different tiers, so a record honoured by one and
// ignored by the other would make the check depend on which tier happened to
// serve the bytes. It also carries what the record says of the plugin's signature,
// which this arm holds the plugin to before its bytes are read.
func linterCacheResolve(workspaceRoot string, wsCfg workspaceconfig.Config, trusted resolver.VerdictPluginTrust, cacheRead func(uri string) (types.Plugin, string, bool, error)) func(uri string) (pluginpath.ResolvedPlugin, error) {
	sources := resolver.VerdictPluginTierSources{
		WorkspaceRoot: workspaceRoot,
		CacheDir:      wsCfg.ResolvedArtifactCacheDir(),
	}
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		return resolver.ResolveVerdictPluginFromCache(sources, trusted, cacheRead, uri)
	}
}

// linterCacheResolveOrDefault picks the caller-threaded cache resolver when one
// was supplied — the caller threads a workspace-root-anchored resolver over the
// same catalog view it already built — and otherwise constructs a fallback for a
// direct invocation, anchored at the working directory and reading the content
// cache the workspace config names.
func linterCacheResolveOrDefault(wsCfg workspaceconfig.Config, trusted resolver.VerdictPluginTrust, cacheResolve []func(uri string) (pluginpath.ResolvedPlugin, error)) func(uri string) (pluginpath.ResolvedPlugin, error) {
	if len(cacheResolve) > 0 && cacheResolve[0] != nil {
		return cacheResolve[0]
	}
	lazyView := catalog.NewLazyLookup("", wsCfg.ResolvedArtifactCacheDir(), nil, nil)
	return linterCacheResolve("", wsCfg, trusted, lazyView.PluginFromCacheByURI)
}

// pluginUnderDeclarationAdvisory reports, as advisory notices, where a Plugin
// entity's .wasm describe() self-report offers more than the entity curates
// (binary ⊇ entity — the inverse of the load-gate cross-check). It resolves the
// entity's .wasm through the plugin cascade by the entity's identity (embed,
// colocated, cache, or a local path); a .wasm that does not resolve is skipped with
// a one-line note rather than treated as an error. It never halts and never
// changes the exit status — under-declaration of a curated surface is legitimate.
// The derivation is single-sourced with the publish advisory (wasm.UnderDeclaredNotices).
//
// ctx reaches the describe() call, which instantiates the entity's own guest and
// is the only step here that runs anything. An advisory the caller has stopped
// waiting for is still work, so it is abandoned on the same signal the run it
// belongs to is.
func pluginUnderDeclarationAdvisory(ctx context.Context, entityBytes []byte, wsCfg workspaceconfig.Config, diagOut io.Writer) {
	var entity types.Plugin
	if err := yaml.Unmarshal(entityBytes, &entity); err != nil {
		fmt.Fprintf(diagOut, "under-declaration advisory: skipped (entity does not parse as a Plugin)\n")
		return
	}
	// Deliberately pin-free, and it stays that way even where the gate plugins
	// around it verify against recorded digests. The artifact resolved here belongs
	// to the entity being LINTED — the audited subject, not the gate doing the
	// auditing — so a record of the identities trusted for the gates holds nothing
	// addressed to it. And this advisory never halts and never changes the exit
	// status, so a mismatch would have nothing it could do about one.
	cascade := pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureNone, pluginpath.PinMaps{})
	resolved, err := cascade.Resolve(entity.ArtifactReference())
	if err != nil {
		fmt.Fprintf(diagOut, "under-declaration advisory: skipped for %q (its .wasm is not resolvable locally: %v)\n",
			entity.Metadata.Name, err)
		return
	}
	desc, err := wasm.DescribePluginBytes(ctx, entity.Metadata.Name, resolved.Bytes, wasm.DescribeMaxMemoryPages, entity.Metadata.PluginType)
	if err != nil {
		fmt.Fprintf(diagOut, "under-declaration advisory: skipped for %q (its .wasm did not describe: %v)\n",
			entity.Metadata.Name, err)
		return
	}
	for _, notice := range wasm.UnderDeclaredNotices(resolver.ProjectCuratedSurface(entity), desc) {
		fmt.Fprintf(diagOut, "  notice: %s: %s\n", entity.Metadata.Name, notice)
	}
}

// yamlToLinterJSON converts an entity document's YAML to the JSON a linter plugin
// receives and extracts the metadata envelope. Returns the JSON bytes the plugin
// consumes plus the header struct the host inspects for kind, name, version,
// labels, extends.
//
// It is the one conversion every document handed to a linter plugin goes
// through, on every lint scope: the entity being linted and its previously
// published version, each entity a whole-catalog or release-scoped pass walks,
// and the Aggregate, Release and Components a rule reading a whole Release is
// handed. The JSON it returns carries no parameter value — see
// withoutParameterValues, keyed on the kind the document declares — so a path
// that converted a document any other way would hand a plugin whatever that
// second conversion did not remove.
func yamlToLinterJSON(yamlBytes []byte) (json.RawMessage, lintEntityHeader, error) {
	var raw interface{}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("yaml unmarshal: %w", err)
	}
	var header lintEntityHeader
	if err := yaml.Unmarshal(yamlBytes, &header); err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("yaml header unmarshal: %w", err)
	}
	cleaned := convertYAMLMaps(raw)
	withoutParameterValues(cleaned, header.Kind)
	jsonBytes, err := json.Marshal(cleaned)
	if err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("json marshal: %w", err)
	}
	return jsonBytes, header, nil
}

// convertYAMLMaps recursively converts map[interface{}]interface{}
// (go.yaml.in/yaml/v3 default for nested maps) to map[string]interface{}
// so json.Marshal accepts the value.
func convertYAMLMaps(v interface{}) interface{} {
	switch x := v.(type) {
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, val := range x {
			out[fmt.Sprintf("%v", k)] = convertYAMLMaps(val)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, val := range x {
			out[k] = convertYAMLMaps(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, val := range x {
			out[i] = convertYAMLMaps(val)
		}
		return out
	default:
		return v
	}
}

// effectivePolicies merges workspace policy values with CLI tri-state overrides.
// resolve is called per flag-base name to determine the effective value.
func effectivePolicies(ws workspaceconfig.PublishingDisciplineConfig, resolve func(string, bool) bool) validator.Policies {
	return validator.Policies{
		AllowDynamicRefs:              resolve("allow-dynamic-refs", ws.AllowDynamicRefs),
		AllowIDOverrides:              resolve("allow-id-overrides", ws.AllowIDOverrides),
		AllowDirectXAggregaExtensions: resolve("allow-direct-x-aggrega-extensions", ws.AllowDirectXAggregaExtensions),
		AllowAggregaAiPrefix:          resolve("allow-aggrega-ai-prefix", ws.AllowAggregaAIPrefix),
		AllowNonstandardNames:         resolve("allow-nonstandard-names", ws.AllowNonstandardNames),
		AllowCategoryMismatches:       resolve("allow-category-mismatches", ws.AllowCategoryMismatches),
		AllowUndeclaredRecipeProvides: resolve("allow-undeclared-recipe-provides", ws.AllowUndeclaredRecipeProvides),
		AllowConnectionCredentialMismatches: resolve("allow-connection-credential-mismatches",
			ws.AllowConnectionCredentialMismatches),
	}
}

// fetchPreviousRegistryVersion lists tags on the entity's registry reference,
// selects the predecessor to diff against, and pulls its bytes. The default
// predecessor is the highest version strictly below the candidate
// (header.Metadata.Version) — the version just beneath the one being published,
// which is the correct comparison target for a back-patch where the highest tag
// overall may sit far above the candidate. When baseline is non-empty it forces
// that specific version as the predecessor instead: the value must be a
// strict-SemVer tag present in the registry, else a loud error. Returns
// ("", "", nil) when no qualifying predecessor exists (first publication, or a
// candidate below every existing stable tag). The pre-release-only warning is
// written to diagOut (os.Stderr in production).
func fetchPreviousRegistryVersion(
	ctx context.Context,
	overrideTarget oras.ReadOnlyTarget,
	wsCfg workspaceconfig.Config,
	header lintEntityHeader,
	baseline string,
	diagOut io.Writer,
) (json.RawMessage, string, error) {
	target := overrideTarget
	if target == nil {
		httpClient, err := egress.HTTPClient(wsCfg)
		if err != nil {
			// The pre-publish evolution comparison fetches the previous registry
			// version; an offline workspace cannot, and silently skipping the
			// comparison would let a version publish without the bump-vs-change
			// check, so the refusal is loud.
			return nil, "", fmt.Errorf("fetch previous registry version: %w", err)
		}
		// The predecessor is read from where a publication through the default
		// registry lands, under the default's location prefix and through the one
		// join publication composes its reference with.
		reference := catalog.RegistryReference(wsCfg.DefaultRegistryLocation().Prefix(), header.Kind, header.Spec.Capability, header.Metadata.Name)
		repo, err := oci.NewRemoteRepository(reference, httpClient)
		if err != nil {
			return nil, "", fmt.Errorf("construct repo: %w", err)
		}
		target = repo
	}
	tags, err := oci.ListTags(ctx, target)
	if err != nil {
		return nil, "", fmt.Errorf("list tags: %w", err)
	}
	var latest string
	if baseline != "" {
		// Operator-forced predecessor. It must be a real strict-SemVer version
		// the registry actually holds; HasAnySemverTag reuses the one selection
		// parse so conformance cannot disagree with the default path.
		if !oci.HasAnySemverTag([]string{baseline}) || !slices.Contains(tags, baseline) {
			return nil, "", fmt.Errorf(
				"--baseline %q is not a strict-SemVer version present in the registry for this entity",
				baseline)
		}
		latest = baseline
	} else {
		latest = oci.SelectHighestBelow(tags, header.Metadata.Version)
		if latest == "" {
			// Distinguish "no previous version exists" (true first publication)
			// from "tags exist but all are pre-release" (different shape;
			// operator should know).
			if oci.HasAnySemverTag(tags) {
				fmt.Fprintf(diagOut,
					"warning: registry has only pre-release tags for this entity; "+
						"treating as first publication. Promote a pre-release "+
						"to a stable version to enable bump-vs-classification verification.\n")
			}
			return nil, "", nil
		}
	}
	pull, err := oci.Pull(ctx, target, latest)
	if err != nil {
		return nil, "", fmt.Errorf("pull %s: %w", latest, err)
	}
	// The predecessor arrives in whatever shape its kind publishes as. Two of them
	// publish as an archive, so the comparison reads the entity document out of
	// the fetched artifact before converting it — without that step the evolution
	// gate cannot compare a Recipe or a FeatureRecipe against its predecessor at
	// all, and a publish that should have been checked is refused instead.
	previousDocument, derr := oci.EntityYAML(pull.Bytes)
	if derr != nil {
		return nil, "", fmt.Errorf("read previous %s: %w", latest, derr)
	}
	previousJSON, _, err := yamlToLinterJSON(previousDocument)
	if err != nil {
		return nil, "", fmt.Errorf("parse previous %s: %w", latest, err)
	}
	return previousJSON, latest, nil
}

// ruleInputSources is the per-entity material buildRuleInputs distributes over
// the rule set.
//
// The fields are grouped rather than passed positionally because the entity
// documents share one type: a transposed pair of JSON documents compiles cleanly
// and hands a rule the wrong entity, which then reports about something it was
// never looking at. A keyed literal at each call site cannot make that mistake.
type ruleInputSources struct {
	// Kind is the linted entity's own kind, which scopes each rule.
	Kind string
	// Proposed is the entity being linted, parsed to JSON.
	Proposed json.RawMessage
	// Previous is the same entity at its previously published version, present
	// only in --pre-publish mode.
	Previous json.RawMessage
	// ImplementedComponent is the Component a linted Recipe realizes; absent for
	// every other kind and whenever the reference does not resolve.
	ImplementedComponent json.RawMessage
	// Policies is the effective per-workspace suppression set.
	Policies validator.Policies
	// EntityPath is where the linted entity was read from.
	EntityPath string
	// PrePublish selects the rule set: the pre-publish set additionally carries
	// the rule that needs previously published bytes.
	PrePublish bool
}

// buildRuleInputs builds one LinterInput per rule the host dispatches to. The
// schema-evolution rule (only in --pre-publish) gets the previous bytes;
// category-matches-directory gets source_path; provides-declared-by-component
// gets the implemented Component. Other rules get the same shared proposed bytes
// + policies.
//
// A per-rule field is assigned only on the arm that consumes it, so the payload
// a rule receives names exactly what that rule reads.
func buildRuleInputs(src ruleInputSources) []validator.LinterInput {
	rules := defaultRuleIDs(src.PrePublish)
	inputs := make([]validator.LinterInput, 0, len(rules))
	for _, r := range rules {
		input := validator.LinterInput{
			RuleID:     r,
			EntityKind: src.Kind,
			Proposed:   src.Proposed,
			Policies:   src.Policies,
		}
		switch r {
		case ruleID(errSchemaEvolution):
			input.Previous = src.Previous
		case ruleID(errCategoryMatchesDirectory):
			input.SourcePath = src.EntityPath
		case ruleID(errProvidesDeclaredByComponent):
			input.ImplementedComponent = src.ImplementedComponent
		}
		inputs = append(inputs, input)
	}
	return inputs
}

// implementedComponentJSON resolves the Component a linted Recipe names in
// spec.implements and returns it as the JSON the payload carries.
//
// What the guest receives is the RESOLVED Component — the entity the catalog
// lookup produces, with any extends chain already folded in — rather than the
// bytes on disk. An extending Component writes an inheritance marker where its
// pillar content would be, so its own file does not carry the provides contract
// the entity actually declares; marshalling the resolved entity back through the
// same YAML-to-JSON conversion the proposed entity goes through hands the guest
// one shape for both.
//
// Four cases return nil, and the field is then omitted from the payload: an
// entity that is not a Recipe, a Recipe naming no Component, a caller with no
// catalog to resolve against, and a reference the lookup does not surface. A
// rule reading the field treats its absence as an unavailable comparison rather
// than as an empty contract.
func implementedComponentJSON(header lintEntityHeader, componentByURI func(uri string) (types.Component, bool)) json.RawMessage {
	if header.Kind != "Recipe" || header.Spec.Implements == "" || componentByURI == nil {
		return nil
	}
	comp, ok := componentByURI(header.Spec.Implements)
	if !ok {
		return nil
	}
	return componentLinterJSON(comp)
}

// componentLinterJSON converts a resolved Component to the JSON a linter payload
// carries, through the one conversion every document a linter plugin receives
// goes through, so a rule reads one shape whichever field a Component arrives in.
// It returns nil for a Component that does not convert, which a payload then
// omits.
func componentLinterJSON(comp types.Component) json.RawMessage {
	entityYAML, err := yaml.Marshal(comp)
	if err != nil {
		return nil
	}
	entityJSON, _, err := yamlToLinterJSON(entityYAML)
	if err != nil {
		return nil
	}
	return entityJSON
}

// aggregateFindings flattens the per-rule finding slices into a single ordered
// list, preserving rule-iteration order.
func aggregateFindings(perRule [][]abi.Diagnostic) []abi.Diagnostic {
	var all []abi.Diagnostic
	for _, findings := range perRule {
		all = append(all, findings...)
	}
	return all
}

// downgradeToWarning re-severitizes every halting finding to the advisory
// severity. Default mode (no --pre-publish) treats every rule's output as
// informational.
func downgradeToWarning(findings []abi.Diagnostic) []abi.Diagnostic {
	out := make([]abi.Diagnostic, len(findings))
	for i, f := range findings {
		if f.Severity == abi.SeverityHalt {
			f.Severity = abi.SeverityWarn
		}
		out[i] = f
	}
	return out
}

// downgradeFirstPublicationToWarning re-severitizes the schema-evolution rule's
// classification finding to the advisory severity for the first-publication
// case. Other rules' findings stay at whatever severity they had.
func downgradeFirstPublicationToWarning(findings []abi.Diagnostic) []abi.Diagnostic {
	evolution := ruleID(errSchemaEvolution)
	out := make([]abi.Diagnostic, len(findings))
	for i, f := range findings {
		if f.RuleID == evolution && f.Severity == abi.SeverityHalt {
			f.Severity = abi.SeverityWarn
		}
		out[i] = f
	}
	return out
}

// reportFindings writes the run's advisory findings to diagOut and returns its
// halting findings as the run's error, one raised condition per finding, or nil
// when nothing halted.
//
// # The two severities reach a reader by two different routes, deliberately
//
// A halting finding is a diagnostic in its own right, so it becomes an
// occurrence of the condition that names its rule and travels up as the error
// the run returns. The terminal formatter then composes its head the way it
// composes every other halting diagnostic's — the concern in brackets beside the
// condition's short code — so a lint halt and a compiler halt are told apart by
// what the head SAYS rather than being told apart not at all.
//
// An advisory is a row in a report of findings rather than a diagnostic, so it
// is written here as it is produced, in a shape that opens with its severity and
// carries no brackets. Borrowing the bracketed head for an advisory is the thing
// this function exists to stop: the brackets are the framework's, and a row that
// wears them while meaning something else is indistinguishable from a halt at a
// glance.
//
// entityKind is the linted document's kind, which is what decides the concern a
// halting finding is filed under. A kind the classifier does not cover is filed
// under none, and the head then carries the condition alone — the classifier
// reports rather than guesses, and a concern guessed for a kind nobody mapped
// would send its reader to a surface nobody chose.
//
// entityPath locates every finding from this run, and a finding that located
// itself more precisely carries its own line, column and authored key on top.
func reportFindings(findings []abi.Diagnostic, entityKind, entityPath string, diagOut io.Writer) error {
	var halts []error
	for _, f := range FindingsFrom(findings, entityPath) {
		if f.Severity != abi.SeverityHalt {
			fmt.Fprintln(diagOut, advisoryLine(f))
			continue
		}
		halts = append(halts, raiseFinding(f, entityKind))
	}
	return stderrors.Join(halts...)
}

// advisoryLine renders one non-halting finding as a report row: the severity it
// carries, the short code of the condition naming its rule, the finding's
// identity — its rule, or the code its producer assigned where it names no
// rule — and the rule's message.
//
// It opens with the severity rather than with brackets, and that is the whole
// point of the shape. The bracketed head belongs to a halting diagnostic and
// says which concern the reader must act on; an advisory asks the reader to act
// on nothing, so it must not be able to be mistaken for one.
//
// Both identifiers are carried because the row is scanned rather than read: a
// report lists many findings and a reader picks out the rule, while the short
// code is what they quote and look up. A finding whose rule this build does not
// know by name carries no short code; neither does a finding naming no rule,
// which the row names by its own code instead; and a finding carrying neither a
// rule nor a code of its own carries no identifier at all — each absent token is
// omitted rather than stood in for.
//
// The colon after the identifiers closes them, so it is written only where at
// least one was: a finding carrying neither reads as its severity and then its
// message, and no row holds an empty segment between two colons for a reader to
// wonder about.
//
// The short code is the finding's own, which [FindingFrom] read off the
// condition the rule names, so the row and the structured report print the same
// short code because they print the same field. The identity is
// [Finding.Identity], which the language server's entry reads too, so the row and
// the editor name a finding alike because they read the same function.
func advisoryLine(f Finding) string {
	identity := f.Identity()
	var b strings.Builder
	b.WriteString(f.Severity)
	b.WriteString(":")
	if f.ShortCode != "" {
		b.WriteString(" ")
		b.WriteString(f.ShortCode)
	}
	if identity != "" {
		b.WriteString(" ")
		b.WriteString(identity)
	}
	if f.ShortCode != "" || identity != "" {
		b.WriteString(":")
	}
	b.WriteString(" ")
	b.WriteString(f.Message)
	return b.String()
}

// raiseFinding turns one halting finding into the occurrence of the condition
// that names its rule.
//
// The message is composed rather than assembled here, so the condition's own
// sentence leads and the rule's text follows it — the composition every other
// halting diagnostic's message follows. The rule's text is carried through
// unchanged, so the structured report and the terminal render one finding's
// words rather than two.
//
// A finding whose rule this build does not know by name names no condition, and
// the occurrence says so rather than borrowing one: the linter socket runs
// whatever guest the workspace attaches, and a guest of somebody else's reports
// rules this build has never heard of. Such a finding leads with its identity —
// the rule's own identifier, or the code its producer assigned where it names no
// rule, read through [Finding.Identity] as the advisory row reads it — which is
// then the only thing identifying it.
func raiseFinding(f Finding, entityKind string) error {
	category, _ := cerrors.ClassifyEntityKind(entityKind)
	source := findingSource(f)
	condition, ok := ConditionForRule(f.RuleID)
	if !ok {
		message := f.Message
		if identity := f.Identity(); identity != "" {
			message = identity + ": " + f.Message
		}
		return cerrors.Unidentified(category, source, message, nil, nil)
	}
	return cerrors.Raise(condition, category, source,
		cerrors.ComposeMessage(condition, nil, f.Message), nil)
}

// findingSource projects a finding's location onto the authoring locator a
// diagnostic points at. The file is always carried; the position and the
// authored key are carried only where the rule that produced the finding
// supplied them, so a rule that located nothing is not reported as having
// located line zero.
func findingSource(f Finding) cerrors.SourceLocation {
	if f.Location == nil {
		return cerrors.SourceLocation{}
	}
	return cerrors.SourceLocation{
		File:    f.Location.File,
		Locator: f.Location.KeyPath,
		Line:    f.Location.Line,
		Column:  f.Location.Column,
	}
}

// classificationFromFindings extracts the schema-evolution rule's classification
// verdict. Returns "" when no schema-evolution finding exists. Multi-finding edge
// case: HIGHEST classification wins (MAJOR > MINOR > PATCH).
func classificationFromFindings(findings []abi.Diagnostic) string {
	evolution := ruleID(errSchemaEvolution)
	rank := map[string]int{"PATCH": 0, "MINOR": 1, "MAJOR": 2}
	highest := ""
	for _, f := range findings {
		if f.RuleID != evolution {
			continue
		}
		if f.Classification == "" {
			continue
		}
		if highest == "" || rank[f.Classification] > rank[highest] {
			highest = f.Classification
		}
	}
	return highest
}

// proposedBumpSatisfies returns true when proposed >= required. Over-bump is
// allowed: MAJOR-required can satisfy with MAJOR; MINOR-required can satisfy with
// MINOR or MAJOR; PATCH-required can satisfy with any.
func proposedBumpSatisfies(proposed, required string) bool {
	rank := map[string]int{"PATCH": 0, "MINOR": 1, "MAJOR": 2}
	return rank[proposed] >= rank[required]
}

// bumpDirection compares two semver versions and returns PATCH, MINOR, or MAJOR.
// When the versions are equal or the previous is unparseable, returns PATCH
// (best-effort).
func bumpDirection(previous, proposed string) string {
	pv, _ := semver.NewVersion(previous)
	sv, _ := semver.NewVersion(proposed)
	if pv == nil || sv == nil {
		return "PATCH"
	}
	if sv.Major() > pv.Major() {
		return "MAJOR"
	}
	if sv.Minor() > pv.Minor() {
		return "MINOR"
	}
	return "PATCH"
}
