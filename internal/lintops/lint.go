// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
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

// lintEntityHeader is the minimum YAML envelope lint parses out of the proposed
// entity file: kind, metadata.name, metadata.version, metadata.labels, and
// spec.capability. It extends the publishable-entity header shape for the
// core-plugin evolution-gate exemption.
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
	} `yaml:"spec"`
}

// defaultRuleIDs returns the rule set to invoke per mode. Default mode runs all
// structural rules. --pre-publish prepends schema-evolution (which needs
// previous bytes).
//
// LOCKSTEP: the slice MUST match the plugin's lib.rs dispatcher arms. Adding a
// rule requires updating both this slice AND
// aggrega-cli/plugins/publishing-linter/src/lib.rs. The agreement is guarded by
// a test that reads the plugin's dispatch table, so a one-sided addition fails
// rather than producing a rule that silently never runs.
func defaultRuleIDs(prePublish bool) []string {
	rules := []string{
		"annotation-prefix",
		"name-shape",
		"dynamic-refs",
		"id-overrides",
		"category-matches-directory",
		"x-aggrega-discouragement",
		"plugin-default-limits",
		"secret-slot-declares-sensitive",
	}
	if prePublish {
		return append([]string{"schema-evolution"}, rules...)
	}
	return rules
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
func RunEntity(cfg Config) error {
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
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsCfg := rw.Config()

	// The workspace-config linter URI is a Plugin entity URI; the entity-front
	// derives the entity's artifact reference from its identity. A lazy lookup over
	// the workspace catalog dirs serves the entity by URI. The --plugin override
	// path is artifact-direct and never consults this, so the catalog is built only
	// for the URI path.
	var pluginByURI func(uri string) (types.Plugin, bool)
	var cacheResolvers []func(uri string) (pluginpath.ResolvedPlugin, error)
	if cfg.PluginPath == "" {
		dirs := workspaceconfig.ResolveCatalogDirs(nil, wsCfg, rw.Root())
		lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil)
		pluginByURI = lazyView.PluginByURI
		cacheResolvers = []func(uri string) (pluginpath.ResolvedPlugin, error){
			linterCacheResolve(rw.Root(), wsCfg, lazyView.PluginFromCacheByURI),
		}
	}
	resolvedPlugin, err := ResolveLinterPlugin(cfg.PluginPath, wsCfg, pluginByURI, cacheResolvers...)
	if err != nil {
		return err
	}
	pluginSpec := resolver.PluginSpec("publishing-linter", resolvedPlugin)

	proposedBytes, err := os.ReadFile(cfg.EntityPath)
	if err != nil {
		return fmt.Errorf("aggrega lint: read %s: %w", cfg.EntityPath, err)
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
		pluginUnderDeclarationAdvisory(proposedBytes, wsCfg, cfg.DiagOut)
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
			context.Background(), cfg.FetchTarget, wsCfg, header, cfg.Baseline, cfg.DiagOut,
		)
		if err != nil {
			return fmt.Errorf("aggrega lint: fetch previous: %w", err)
		}
		firstPublication = previousVersion == ""
	}

	inputs := buildRuleInputs(header.Kind, proposedJSON, previousJSON, policies, cfg.EntityPath, prePublish)
	export := ""
	if len(wsCfg.Plugins.Linter) > 0 {
		export = wsCfg.Plugins.Linter[0].Function
	}
	rawFindings, err := validator.DispatchPublishingLinterBatch(pluginSpec, export, inputs)
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

	// Bump-vs-classification check runs BEFORE printing findings so
	// the schema-evolution finding can be downgraded to warning when
	// the bump satisfies the classification. The rule body emits the
	// classification at Error severity (it's the rule's only output
	// channel); whether that classification IS a violation depends on
	// the proposed bump direction — which only the host knows. When
	// the bump satisfies, the finding is informational; when it
	// doesn't, the host surfaces its own explicit error.
	if prePublish && !firstPublication {
		verdict := classificationFromFindings(aggregated)
		if verdict != "" {
			proposedBump := bumpDirection(previousVersion, header.Metadata.Version)
			if !proposedBumpSatisfies(proposedBump, verdict) {
				cfg.Collect.Add(aggregated, cfg.EntityPath)
				cfg.Collect.Entities(1)
				printFindings(aggregated, cfg.DiagOut)
				return fmt.Errorf(
					"aggrega lint --pre-publish: schema-evolution requires %s bump; proposed %s -> %s is %s",
					verdict, previousVersion, header.Metadata.Version, proposedBump,
				)
			}
			// Bump satisfies the classification — downgrade the
			// schema-evolution finding to warning so it doesn't trip
			// the generic-error halt below.
			aggregated = downgradeRuleToWarning(aggregated, "schema-evolution")
		}
	}

	cfg.Collect.Add(aggregated, cfg.EntityPath)
	cfg.Collect.Entities(1)
	halted := printFindings(aggregated, cfg.DiagOut)
	if halted {
		return fmt.Errorf("aggrega lint: %d error finding(s)", countErrors(aggregated))
	}
	fmt.Fprintf(cfg.Out, "lint passed: %s\n", header.Metadata.Name)
	return nil
}

// downgradeRuleToWarning maps Error → Warning for findings whose RuleID matches
// the given id. Used for the schema-evolution rule when the proposed bump
// satisfies its classification verdict; the rule emits Error severity to convey
// the classification, but the halt-on-error logic requires downgrading once the
// bump is verified compatible.
func downgradeRuleToWarning(findings []abi.Diagnostic, ruleID string) []abi.Diagnostic {
	out := make([]abi.Diagnostic, len(findings))
	for i, f := range findings {
		if f.RuleID == ruleID && f.Severity == "halt" {
			f.Severity = "warn"
		}
		out[i] = f
	}
	return out
}

// ResolveLinterPlugin resolves the publishing-linter Wasm plugin, returning the
// resolved plugin (its .wasm bytes plus any entity capability claim and runtime
// limits) so the caller maps it onto the load spec. It is the linter's
// per-socket URI feeder. The --plugin flag is the explicit escape-hatch
// override: a direct artifact path read verbatim through the cascade's override,
// bypassing the entity model. The workspace config's plugins.linter[0].uri is a
// Plugin entity URI, resolved through the shared entity-front: entity URI →
// artifact reference → the cascade (embed → colocated → local → cache), so the
// linter sees the same entity model and the same tier model every plugin socket
// does, including the embedded built-ins.
//
// Only the entity-URI path carries a capability claim and runtime limits: the
// entity-front attaches them from the in-hand Plugin entity, so the linter load
// gains the entity's describe()-vs-entity cross-check and its explicit caps. The
// --plugin override path (artifact-direct) and the no-URI path carry a nil claim
// and nil limits — the cross-check is skipped and the loader applies the
// platform default caps.
//
// pluginByURI serves a Plugin entity by URI from the catalog, supplied by the
// caller (nil in a catalog-less path, where a configured entity URI then halts as
// not-an-entity); the entity-front derives the artifact reference from the
// entity's identity. The --plugin override path does not consult it — the escape
// hatch is artifact-direct.
//
// The linter passes NO digest pins: it lints a not-yet-pinned proposed entity
// at publish time, so there is no lockfile to verify the plugin against. It
// wires no puller — the linter resolves locally (embed / colocated / local).
//
// The linter socket is a verdict socket: a configured, non-embedded linter guest
// is cached at pull but not pinned (cache-not-pin), so the configured-URI path is
// wrapped in the shared verdict-lookup — the pinned / Tier-1 cascade is tried
// first, and an entity-URI that misses there falls through to the pull-populated
// content cache, read by URI and digest-checked against the cached manifest. This
// mirrors the policy socket exactly. The optional cacheResolve is the caller's
// workspace-root-anchored cache resolver, threaded from the caller so the cache
// read anchors at the resolved workspace root; a direct invocation that omits it
// falls back to a working-directory-anchored resolver built from wsCfg.
func ResolveLinterPlugin(cliFlag string, wsCfg workspaceconfig.Config, pluginByURI func(uri string) (types.Plugin, bool), cacheResolve ...func(uri string) (pluginpath.ResolvedPlugin, error)) (pluginpath.ResolvedPlugin, error) {
	cascade := pluginpath.NewResolver(pluginpath.ResolutionContext{
		Override: cliFlag,
	}, pluginpath.PinPostureNone, pluginpath.PinMaps{})
	// The --plugin override is the artifact-direct escape hatch: resolve it
	// verbatim through the cascade override, never the entity model. The cascade
	// override path sets no Contract/Limits, so the load gets a nil claim and
	// default caps — today's override behaviour.
	if cliFlag != "" {
		return cascade.Resolve("")
	}
	uri := ""
	if len(wsCfg.Plugins.Linter) > 0 {
		uri = wsCfg.Plugins.Linter[0].URI
	}
	if uri == "" {
		// Unconfigured socket. The per-socket capability-default policy decides
		// whether an unconfigured socket prefers a capable embedded plugin: the
		// linter opts in, so it falls back to the built-in publishing-linter
		// embedded in the binary, resolved through the cascade so its embed tier
		// serves it. Like the --plugin escape hatch, the built-in path carries a
		// nil capability claim and the platform default caps. A socket that opted
		// out would resolve no default here.
		if !workspaceconfig.SocketPrefersCapabilityDefault(workspaceconfig.SocketLinter) {
			return pluginpath.ResolvedPlugin{}, fmt.Errorf("no linter plugin configured and the linter socket does not fall back to an embedded default")
		}
		return cascade.Resolve(embedded.PublishingLinterURI)
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
	cr := linterCacheResolveOrDefault(wsCfg, cacheResolve)
	return resolver.VerdictLookup(front, cr)(uri)
}

// linterCacheResolve returns the verdict-socket cache-not-pin resolver the linter
// falls back to when the pinned / Tier-1 cascade does not surface a configured,
// non-embedded linter guest. The guest is cached at pull but never pinned, so it
// is read from the content cache by URI and its .wasm is digest-checked against
// the cached manifest. workspaceRoot and wsCfg anchor the content cache; cacheRead
// reads a Plugin entity from that cache by URI.
func linterCacheResolve(workspaceRoot string, wsCfg workspaceconfig.Config, cacheRead func(uri string) (types.Plugin, string, bool, error)) func(uri string) (pluginpath.ResolvedPlugin, error) {
	sources := resolver.VerdictPluginTierSources{
		WorkspaceRoot: workspaceRoot,
		CacheDir:      wsCfg.ResolvedArtifactCacheDir(),
	}
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		return resolver.ResolveVerdictPluginFromCache(sources, cacheRead, uri)
	}
}

// linterCacheResolveOrDefault picks the caller-threaded cache resolver when one
// was supplied — the caller threads a workspace-root-anchored resolver over the
// same catalog view it already built — and otherwise constructs a fallback for a
// direct invocation, anchored at the working directory and reading the content
// cache the workspace config names.
func linterCacheResolveOrDefault(wsCfg workspaceconfig.Config, cacheResolve []func(uri string) (pluginpath.ResolvedPlugin, error)) func(uri string) (pluginpath.ResolvedPlugin, error) {
	if len(cacheResolve) > 0 && cacheResolve[0] != nil {
		return cacheResolve[0]
	}
	lazyView := catalog.NewLazyLookup("", wsCfg.ResolvedArtifactCacheDir(), nil, nil)
	return linterCacheResolve("", wsCfg, lazyView.PluginFromCacheByURI)
}

// pluginUnderDeclarationAdvisory reports, as advisory notices, where a Plugin
// entity's .wasm describe() self-report offers more than the entity curates
// (binary ⊇ entity — the inverse of the load-gate cross-check). It resolves the
// entity's .wasm through the plugin cascade by the entity's identity (embed,
// colocated, cache, or a local path); a .wasm that does not resolve is skipped with
// a one-line note rather than treated as an error. It never halts and never
// changes the exit status — under-declaration of a curated surface is legitimate.
// The derivation is single-sourced with the publish advisory (wasm.UnderDeclaredNotices).
func pluginUnderDeclarationAdvisory(entityBytes []byte, wsCfg workspaceconfig.Config, diagOut io.Writer) {
	var entity types.Plugin
	if err := yaml.Unmarshal(entityBytes, &entity); err != nil {
		fmt.Fprintf(diagOut, "under-declaration advisory: skipped (entity does not parse as a Plugin)\n")
		return
	}
	cascade := pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureNone, pluginpath.PinMaps{})
	resolved, err := cascade.Resolve(entity.ArtifactReference())
	if err != nil {
		fmt.Fprintf(diagOut, "under-declaration advisory: skipped for %q (its .wasm is not resolvable locally: %v)\n",
			entity.Metadata.Name, err)
		return
	}
	desc, err := wasm.DescribePluginBytes(context.Background(), entity.Metadata.Name, resolved.Bytes, wasm.DescribeMaxMemoryPages, entity.Metadata.PluginType)
	if err != nil {
		fmt.Fprintf(diagOut, "under-declaration advisory: skipped for %q (its .wasm did not describe: %v)\n",
			entity.Metadata.Name, err)
		return
	}
	for _, notice := range wasm.UnderDeclaredNotices(resolver.ProjectCuratedSurface(entity), desc) {
		fmt.Fprintf(diagOut, "  notice: %s: %s\n", entity.Metadata.Name, notice)
	}
}

// yamlToLinterJSON converts the proposed entity YAML to JSON and extracts the
// metadata envelope. Returns the JSON bytes the plugin consumes plus the header
// struct the host inspects for kind, name, version, labels, extends.
func yamlToLinterJSON(yamlBytes []byte) (json.RawMessage, lintEntityHeader, error) {
	var raw interface{}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("yaml unmarshal: %w", err)
	}
	cleaned := convertYAMLMaps(raw)
	jsonBytes, err := json.Marshal(cleaned)
	if err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("json marshal: %w", err)
	}
	var header lintEntityHeader
	if err := yaml.Unmarshal(yamlBytes, &header); err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("yaml header unmarshal: %w", err)
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
func effectivePolicies(ws workspaceconfig.PublishingDisciplineConfig, resolve func(string, bool) bool) json.RawMessage {
	policies := map[string]any{
		"allow_dynamic_refs":                resolve("allow-dynamic-refs", ws.AllowDynamicRefs),
		"allow_id_overrides":                resolve("allow-id-overrides", ws.AllowIDOverrides),
		"allow_direct_x_aggrega_extensions": resolve("allow-direct-x-aggrega-extensions", ws.AllowDirectXAggregaExtensions),
		"allow_aggrega_ai_prefix":           resolve("allow-aggrega-ai-prefix", ws.AllowAggregaAIPrefix),
		"allow_nonstandard_names":           resolve("allow-nonstandard-names", ws.AllowNonstandardNames),
		"allow_category_mismatches":         resolve("allow-category-mismatches", ws.AllowCategoryMismatches),
	}
	out, _ := json.Marshal(policies)
	return out
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
		reference := wsCfg.DefaultRegistry + "/" + catalog.OCILocation(header.Kind, header.Spec.Capability, header.Metadata.Name)
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
	previousJSON, _, err := yamlToLinterJSON(pull.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("parse previous %s: %w", latest, err)
	}
	return previousJSON, latest, nil
}

// buildRuleInputs builds one LinterInput per rule the host dispatches to. The
// schema-evolution rule (only in --pre-publish) gets the previous bytes;
// category-matches-directory gets source_path. Other rules get the same shared
// proposed bytes + policies.
func buildRuleInputs(
	kind string,
	proposed, previous, policies json.RawMessage,
	entityPath string,
	prePublish bool,
) []validator.LinterInput {
	rules := defaultRuleIDs(prePublish)
	inputs := make([]validator.LinterInput, 0, len(rules))
	for _, r := range rules {
		input := validator.LinterInput{
			RuleID:     r,
			EntityKind: kind,
			Proposed:   proposed,
			Policies:   policies,
		}
		switch r {
		case "schema-evolution":
			input.Previous = previous
		case "category-matches-directory":
			input.SourcePath = entityPath
		}
		inputs = append(inputs, input)
	}
	return inputs
}

// aggregateFindings flattens per-rule [][]LinterFinding slices into a single
// ordered list, preserving rule-iteration order.
func aggregateFindings(perRule [][]abi.Diagnostic) []abi.Diagnostic {
	var all []abi.Diagnostic
	for _, findings := range perRule {
		all = append(all, findings...)
	}
	return all
}

// downgradeToWarning maps every error finding to a warning. Default mode (no
// --pre-publish) treats every rule's output as informational.
func downgradeToWarning(findings []abi.Diagnostic) []abi.Diagnostic {
	out := make([]abi.Diagnostic, len(findings))
	for i, f := range findings {
		if f.Severity == "halt" {
			f.Severity = "warn"
		}
		out[i] = f
	}
	return out
}

// downgradeFirstPublicationToWarning maps the schema-evolution rule's
// classification finding to warn severity for the first-publication case.
// Other rules' findings stay at whatever severity they had.
func downgradeFirstPublicationToWarning(findings []abi.Diagnostic) []abi.Diagnostic {
	out := make([]abi.Diagnostic, len(findings))
	for i, f := range findings {
		if f.RuleID == "schema-evolution" && f.Severity == "halt" {
			f.Severity = "warn"
		}
		out[i] = f
	}
	return out
}

// printFindings writes each finding to diagOut (os.Stderr in production) in a
// stable format and returns true when at least one finding is severity=halt.
func printFindings(findings []abi.Diagnostic, diagOut io.Writer) bool {
	halted := false
	for _, f := range findings {
		if f.Severity == "halt" {
			halted = true
		}
		fmt.Fprintf(diagOut, "[%s] %s: %s\n", f.Severity, f.RuleID, f.Message)
	}
	return halted
}

func countErrors(findings []abi.Diagnostic) int {
	n := 0
	for _, f := range findings {
		if f.Severity == "halt" {
			n++
		}
	}
	return n
}

// classificationFromFindings extracts the schema-evolution rule's classification
// verdict. Returns "" when no schema-evolution finding exists. Multi-finding edge
// case: HIGHEST classification wins (MAJOR > MINOR > PATCH).
func classificationFromFindings(findings []abi.Diagnostic) string {
	rank := map[string]int{"PATCH": 0, "MINOR": 1, "MAJOR": 2}
	highest := ""
	for _, f := range findings {
		if f.RuleID != "schema-evolution" {
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
