// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// lint command flags. Cobra populates these globals from the
// invocation; lintRun reads them through lintConfig for testability.
var (
	lintEntityPath  string
	lintReleasePath string
	lintPrePublish  bool
	lintPluginPath  string
	lintCatalogDirs []string

	// Tri-state policy override flags. Both
	// `allow_*` (6) and `enforce_*` (2) families get paired CLI
	// flags. resolvePolicy decides effective value per cobra
	// Flags().Changed() state.
	lintAllowDynamicRefs          bool
	lintNoAllowDynamicRefs        bool
	lintAllowIDOverrides          bool
	lintNoAllowIDOverrides        bool
	lintAllowDirectXAggregaExt    bool
	lintNoAllowDirectXAggregaExt  bool
	lintAllowAggregaAIPrefix      bool
	lintNoAllowAggregaAIPrefix    bool
	lintAllowNonstandardNames     bool
	lintNoAllowNonstandardNames   bool
	lintAllowCategoryMismatches   bool
	lintNoAllowCategoryMismatches bool
	lintEnforceMonotonicNarrowing bool
	lintNoEnforceMonotonic        bool
	lintEnforceImmutability       bool
	lintNoEnforceImmutability     bool
)

func newLintCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "lint [entity-path]",
		Short: "Check catalog publishing conventions across the catalog, a Release, or one entity",
		Long: `aggrega lint checks publishing CONVENTIONS — naming, category-directory
alignment, annotation prefixes, and the rest of the publishing-linter
discipline. It does not check correctness (schema, pillars, resolution,
render): that is aggrega validate.

Three invocation shapes, mirroring validate:

  aggrega lint                    conventions across every catalog entity
  aggrega lint --release <path>   conventions across a Release's dependency closure
  aggrega lint <entity>           publishing discipline for one entity

Without an entity argument, lint runs the structural conventions rules
over every catalog entity found under the catalog directories. With
--release it runs them over the entities that Release's closure reaches.
In both, findings are advisory (warning severity) and never halt.

With an entity path, lint runs the publishing-linter rule set against
that one entity. In default mode the rules run at warning severity over
local content without consulting any registry. With --pre-publish the
previous registry version is fetched and the schema-evolution
classification is compared against the proposed version bump; mismatches
halt. --pre-publish applies to the single-entity shape only. Per-workspace
policy flags from .aggrega-config.yaml's lint.publishing_discipline block
can be overridden per invocation via the --allow-*/--no-allow-* and
--enforce-*/--no-enforce-* flag pairs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := lintEntityPath
			if path == "" && len(args) > 0 {
				path = args[0]
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			if lintReleasePath != "" {
				// Release-closure mode is mutually exclusive with a positional /
				// --entity target — they select different closures.
				if path != "" {
					return fmt.Errorf("OPS_ERROR: --release is mutually exclusive with an entity argument; pass one or the other")
				}
				// --pre-publish drives the single-entity registry-bump check; the
				// release-closure pass never consults it. Halt loudly rather than
				// silently ignore the flag.
				if lintPrePublish {
					return fmt.Errorf("OPS_ERROR: --pre-publish is mutually exclusive with --release; --pre-publish applies to a single entity, --release checks a Release closure")
				}
				return lintConventionsRelease(rw, lintReleasePath, lintCatalogDirs, cmd.OutOrStdout(), os.Stderr)
			}
			if path == "" {
				// No entity argument → whole-catalog conventions.
				return lintConventionsCatalogWide(rw, lintCatalogDirs, cmd.OutOrStdout(), os.Stderr)
			}
			return lintRun(lintConfig{
				EntityPath:     path,
				PrePublish:     lintPrePublish,
				PluginPath:     lintPluginPath,
				PolicyResolver: makeResolverFromCobra(cmd),
				ws:             rw,
				Out:            cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringArrayVar(&lintCatalogDirs, "catalog", nil,
		"Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Used by the whole-catalog and --release conventions passes.")
	c.Flags().StringVar(&lintReleasePath, "release", "", "Path to a Release YAML to check conventions over its resolved dependency closure (mutually exclusive with an entity argument)")
	c.Flags().StringVar(&lintEntityPath, "entity", "", "Path to the entity YAML to lint (alternatively pass as the positional arg)")
	c.Flags().BoolVar(&lintPrePublish, "pre-publish", false, "Fetch the previous registry version and verify the proposed bump against the schema-evolution classification")
	c.Flags().StringVar(&lintPluginPath, "plugin", "", "Filesystem path to the publishing-linter .wasm artifact (overrides workspace plugins.linter[0].uri)")

	// Tri-state allow_* flags
	c.Flags().BoolVar(&lintAllowDynamicRefs, "allow-dynamic-refs", false, "Force allow_dynamic_refs=true for this invocation")
	c.Flags().BoolVar(&lintNoAllowDynamicRefs, "no-allow-dynamic-refs", false, "Force allow_dynamic_refs=false for this invocation")
	c.Flags().BoolVar(&lintAllowIDOverrides, "allow-id-overrides", false, "Force allow_id_overrides=true for this invocation")
	c.Flags().BoolVar(&lintNoAllowIDOverrides, "no-allow-id-overrides", false, "Force allow_id_overrides=false for this invocation")
	c.Flags().BoolVar(&lintAllowDirectXAggregaExt, "allow-direct-x-aggrega-extensions", false, "Force allow_direct_x_aggrega_extensions=true")
	c.Flags().BoolVar(&lintNoAllowDirectXAggregaExt, "no-allow-direct-x-aggrega-extensions", false, "Force allow_direct_x_aggrega_extensions=false")
	c.Flags().BoolVar(&lintAllowAggregaAIPrefix, "allow-aggrega-ai-prefix", false, "Force allow_aggrega_ai_prefix=true")
	c.Flags().BoolVar(&lintNoAllowAggregaAIPrefix, "no-allow-aggrega-ai-prefix", false, "Force allow_aggrega_ai_prefix=false")
	c.Flags().BoolVar(&lintAllowNonstandardNames, "allow-nonstandard-names", false, "Force allow_nonstandard_names=true")
	c.Flags().BoolVar(&lintNoAllowNonstandardNames, "no-allow-nonstandard-names", false, "Force allow_nonstandard_names=false")
	c.Flags().BoolVar(&lintAllowCategoryMismatches, "allow-category-mismatches", false, "Force allow_category_mismatches=true")
	c.Flags().BoolVar(&lintNoAllowCategoryMismatches, "no-allow-category-mismatches", false, "Force allow_category_mismatches=false")

	// Tri-state enforce_* flags
	c.Flags().BoolVar(&lintEnforceMonotonicNarrowing, "enforce-monotonic-narrowing", false, "Force enforce_monotonic_narrowing=true")
	c.Flags().BoolVar(&lintNoEnforceMonotonic, "no-enforce-monotonic-narrowing", false, "Force enforce_monotonic_narrowing=false")
	c.Flags().BoolVar(&lintEnforceImmutability, "enforce-immutability", false, "Force enforce_immutability=true")
	c.Flags().BoolVar(&lintNoEnforceImmutability, "no-enforce-immutability", false, "Force enforce_immutability=false")

	return c
}

// lintConfig is the test-injectable input contract. Tests construct
// it directly; production cobra populates it from the flag globals
// + a PolicyResolver closure built from the cobra Flags().Changed
// state. FetchTarget overrides registry construction for end-to-end
// tests (in-memory oras.ReadOnlyTarget), mirroring push's PushTarget.
type lintConfig struct {
	EntityPath     string
	PrePublish     bool
	PluginPath     string
	PolicyResolver func(flagBase string, wsValue bool) bool
	FetchTarget    oras.ReadOnlyTarget

	// ws is the resolved workspace, threaded in by the command's RunE. Nil only
	// when a test invokes lintRun directly; workspaceOrResolve then resolves
	// from the working directory.
	ws *workspaceconfig.ResolvedWorkspace

	// DiagOut is the sink for lint's diagnostic output (the first-publication
	// note, the pre-release-only warning, the not-resolvable-parent note, and the
	// findings report). Nil defaults to os.Stderr at the top of lintRun
	// (production); tests pass a buffer to capture diagnostics without mutating a
	// process global.
	DiagOut io.Writer

	// Out is the sink for lint's success confirmation (stdout in production),
	// kept separate from DiagOut so the positive signal lands on stdout while
	// diagnostics stay on stderr. Nil defaults to os.Stdout at the top of
	// lintRun; tests pass a buffer to capture the confirmation.
	Out io.Writer
}

// makeResolverFromCobra returns a closure that decides effective
// policy values per cobra Flags().Changed() state. Precedence:
// explicit --no-X > explicit --X > workspace value.
func makeResolverFromCobra(cmd *cobra.Command) func(string, bool) bool {
	return func(flagBase string, wsValue bool) bool {
		if cmd.Flags().Changed("no-" + flagBase) {
			return false
		}
		if cmd.Flags().Changed(flagBase) {
			return true
		}
		return wsValue
	}
}

// lintEntityHeader is the minimum YAML envelope lint parses out of
// the proposed entity file: kind, metadata.name, metadata.version,
// metadata.labels, spec.extends, and spec.capability. Mirrors
// pushEntityHeader's shape and extends it for narrowing-rule parent
// resolution and the core-plugin evolution-gate exemption.
type lintEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string            `yaml:"name"`
		Version string            `yaml:"version"`
		Labels  map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Extends    string `yaml:"extends"`
		Capability string `yaml:"capability"`
	} `yaml:"spec"`
}

// defaultRuleIDs returns the rule set to invoke per mode. Default
// mode runs all structural rules. --pre-publish prepends
// schema-evolution (which needs previous bytes).
//
// LOCKSTEP: the slice MUST match the plugin's lib.rs dispatcher
// arms. Adding a rule requires updating both this
// slice AND aggrega-cli/plugins/publishing-linter/src/lib.rs.
func defaultRuleIDs(prePublish bool) []string {
	rules := []string{
		"annotation-prefix",
		"name-shape",
		"narrowing",
		"dynamic-refs",
		"id-overrides",
		"category-matches-directory",
		"x-aggrega-discouragement",
		"plugin-default-limits",
	}
	if prePublish {
		return append([]string{"schema-evolution"}, rules...)
	}
	return rules
}

// pluginIsCore reports whether a Plugin entity's identity names one of the
// built-in plugins shipped inside the binary. The embedded set enumerates the
// bare identities (<socket>/<name>) the built-ins register under — the same
// identity shape a Plugin entity's metadata.name carries — so membership is an
// exact-string match against that set.
func pluginIsCore(identity string) bool {
	if identity == "" {
		return false
	}
	for _, uri := range embedded.URIs() {
		if uri == identity {
			return true
		}
	}
	return false
}

func lintRun(cfg lintConfig) error {
	if cfg.DiagOut == nil {
		cfg.DiagOut = os.Stderr
	}
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	if cfg.EntityPath == "" {
		return fmt.Errorf("aggrega lint: entity path required (pass as positional arg or via --entity)")
	}
	rw, err := workspaceOrResolve(cfg.ws)
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
	if cfg.PluginPath == "" {
		dirs := resolveCatalogDirs(nil, wsCfg, rw.Root())
		lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil)
		pluginByURI = lazyView.PluginByURI
	}
	resolvedPlugin, err := resolveLinterPlugin(cfg.PluginPath, wsCfg, pluginByURI)
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

	resolver := cfg.PolicyResolver
	if resolver == nil {
		resolver = func(_ string, ws bool) bool { return ws }
	}
	policies := effectivePolicies(wsCfg.Lint.PublishingDiscipline, resolver)

	// A core plugin's describe()-surface is version-locked to the binary and
	// publishes only on the compiler's own release, so the publish-time
	// evolution gate does not apply to it. Drop the schema-evolution rule and
	// skip the previous-version fetch a non-core plugin would perform. The
	// exemption short-circuits before any registry consultation.
	prePublish := cfg.PrePublish
	if cfg.PrePublish && header.Kind == "Plugin" && pluginIsCore(header.Metadata.Name) {
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
			context.Background(), cfg.FetchTarget, wsCfg, header, cfg.DiagOut,
		)
		if err != nil {
			return fmt.Errorf("aggrega lint: fetch previous: %w", err)
		}
		firstPublication = previousVersion == ""
	}

	parentJSON, _ := resolveParent(header, wsCfg, cfg.EntityPath, cfg.DiagOut)

	inputs := buildRuleInputs(header.Kind, proposedJSON, previousJSON, parentJSON, policies, cfg.EntityPath, prePublish)
	rawFindings, err := validator.DispatchPublishingLinterBatch(pluginSpec, inputs)
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
	// the proposed bump direction — which only the CLI knows. When
	// the bump satisfies, the finding is informational; when it
	// doesn't, the CLI surfaces its own explicit error.
	if prePublish && !firstPublication {
		verdict := classificationFromFindings(aggregated)
		if verdict != "" {
			proposedBump := bumpDirection(previousVersion, header.Metadata.Version)
			if !proposedBumpSatisfies(proposedBump, verdict) {
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

	halted := printFindings(aggregated, cfg.DiagOut)
	if halted {
		return fmt.Errorf("aggrega lint: %d error finding(s)", countErrors(aggregated))
	}
	fmt.Fprintf(cfg.Out, "lint passed: %s\n", header.Metadata.Name)
	return nil
}

// downgradeRuleToWarning maps Error → Warning for findings whose
// RuleID matches the given id. Used for the schema-evolution rule
// when the proposed bump satisfies its classification verdict; the
// rule emits Error severity to convey the classification, but the
// CLI's halt-on-error logic requires downgrading once the bump is
// verified compatible.
func downgradeRuleToWarning(findings []validator.LinterFinding, ruleID string) []validator.LinterFinding {
	out := make([]validator.LinterFinding, len(findings))
	for i, f := range findings {
		if f.RuleID == ruleID && f.Severity == "error" {
			f.Severity = "warning"
		}
		out[i] = f
	}
	return out
}

// resolveLinterPlugin resolves the publishing-linter Wasm plugin, returning the
// resolved plugin (its .wasm bytes plus any entity capability claim and runtime
// limits) so the caller maps it onto the load spec. It is the linter's
// per-socket URI feeder. The --plugin flag is the explicit escape-hatch
// override: a direct artifact path read verbatim through the cascade's override,
// bypassing the entity model. The workspace config's plugins.linter[0].uri is a
// Plugin entity URI, resolved through the shared entity-front: entity URI →
// artifact reference → the cascade (embed → sidecar → local → cache), so the
// linter sees the same entity model and the same tier model every plugin socket
// does, including the embedded built-ins and the operator's sidecar directory.
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
// wires no puller — the linter resolves locally (embed / sidecar / local); an
// OCI artifact reference with nothing cached halts with the cascade's
// not-resolvable message.
func resolveLinterPlugin(cliFlag string, wsCfg workspaceconfig.Config, pluginByURI func(uri string) (types.Plugin, bool)) (pluginpath.ResolvedPlugin, error) {
	cascade := pluginpath.Resolver{
		SidecarDir:  wsCfg.PluginSidecarDir,
		Override:    cliFlag,
		OverrideLog: func(msg string) { fmt.Fprintf(os.Stderr, "  plugin: %s\n", msg) },
	}
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
	front := pluginpath.EntityLookup(func(entityURI string) (string, *pluginpath.PluginContractClaim, *pluginpath.PluginRuntimeLimits, bool) {
		if pluginByURI == nil {
			return "", nil, nil, false
		}
		entity, ok := pluginByURI(entityURI)
		if !ok {
			return "", nil, nil, false
		}
		return entity.ArtifactReference(), resolver.PluginContractClaim(entity), resolver.PluginRuntimeLimits(entity), true
	}, cascade.Resolve)
	return front(uri)
}

// looksLikeOCIURI returns true when the URI looks like an OCI
// reference. Heuristic: oci:// scheme, or hub.aggrega.ai host, or
// <host>/<path>:<tag> shape with a slash before the colon.
// yamlToLinterJSON converts the proposed entity YAML to JSON and
// extracts the metadata envelope. Returns the JSON bytes the plugin
// consumes plus the header struct the host inspects for kind, name,
// version, labels, extends.
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

// effectivePolicies merges workspace policy values with CLI tri-state
// overrides. resolver is called per flag-base name to determine
// effective value.
func effectivePolicies(ws workspaceconfig.PublishingDisciplineConfig, resolve func(string, bool) bool) json.RawMessage {
	exemptions := ws.NarrowingExemptions
	if exemptions == nil {
		exemptions = []workspaceconfig.NarrowingExemption{}
	}
	policies := map[string]any{
		"enforce_monotonic_narrowing":       resolve("enforce-monotonic-narrowing", ws.EnforceMonotonicNarrowing),
		"enforce_immutability":              resolve("enforce-immutability", ws.EnforceImmutability),
		"narrowing_exemptions":              exemptions,
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

// fetchPreviousRegistryVersion lists tags on the entity's registry
// reference, picks the latest non-pre-release, and pulls its bytes.
// Returns ("", "", nil) when no qualifying tag exists (first
// publication. The pre-release-only warning is written to diagOut (os.Stderr
// in production).
func fetchPreviousRegistryVersion(
	ctx context.Context,
	overrideTarget oras.ReadOnlyTarget,
	wsCfg workspaceconfig.Config,
	header lintEntityHeader,
	diagOut io.Writer,
) (json.RawMessage, string, error) {
	target := overrideTarget
	if target == nil {
		reference := buildPushReference(wsCfg.DefaultRegistry, header.Kind, header.Spec.Capability, header.Metadata.Name)
		repo, err := oci.NewRemoteRepository(reference)
		if err != nil {
			return nil, "", fmt.Errorf("construct repo: %w", err)
		}
		target = repo
	}
	tags, err := oci.ListTags(ctx, target)
	if err != nil {
		return nil, "", fmt.Errorf("list tags: %w", err)
	}
	latest := oci.SelectLatestVersion(tags, false /* includePreRelease */)
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

// resolveParent walks workspace catalog_dirs to find the parent
// entity named in header.Spec.Extends. Only catalog-dir
// resolution is performed today; OCI parent fetch defers. Returns nil
// silently when extends is empty or the parent isn't locally
// resolvable — the narrowing rule short-circuits on nil parent. The
// not-locally-resolvable note is written to diagOut (os.Stderr in production).
func resolveParent(header lintEntityHeader, wsCfg workspaceconfig.Config, entityPath string, diagOut io.Writer) (json.RawMessage, error) {
	if header.Spec.Extends == "" {
		return nil, nil
	}
	candidates := wsCfg.Workspace.CatalogDirs
	if len(candidates) == 0 {
		// No catalog dirs configured; fall back to the entity file's
		// own directory (development convenience for testing).
		candidates = []string{filepath.Dir(entityPath)}
	}
	// Strip any version suffix from the extends URI; resolution is
	// path-based, not version-pinned at this layer.
	name := header.Spec.Extends
	if idx := strings.LastIndex(name, ":"); idx >= 0 {
		// Only strip if the bit after the colon looks like a version.
		if _, err := semver.NewVersion(name[idx+1:]); err == nil {
			name = name[:idx]
		}
	}
	kindLower := strings.ToLower(header.Kind)
	for _, dir := range candidates {
		candidate := filepath.Join(dir, kindLower, name+".yaml")
		bytes, err := os.ReadFile(candidate)
		if err == nil {
			parentJSON, _, err := yamlToLinterJSON(bytes)
			if err != nil {
				return nil, fmt.Errorf("parse parent %s: %w", candidate, err)
			}
			return parentJSON, nil
		}
	}
	fmt.Fprintf(diagOut, "narrowing rule: parent %q not locally resolvable; rule skipped\n", header.Spec.Extends)
	return nil, nil
}

// buildRuleInputs builds one LinterInput per rule the CLI dispatches
// to. The schema-evolution rule (only in --pre-publish) gets the
// previous bytes; the narrowing rule gets parent; category-matches-
// directory gets source_path. Other rules get the same shared
// proposed bytes + policies.
func buildRuleInputs(
	kind string,
	proposed, previous, parent, policies json.RawMessage,
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
		case "narrowing":
			input.Parent = parent
		case "category-matches-directory":
			input.SourcePath = entityPath
		}
		inputs = append(inputs, input)
	}
	return inputs
}

// aggregateFindings flattens per-rule [][]LinterFinding slices into
// a single ordered list, preserving rule-iteration order.
func aggregateFindings(perRule [][]validator.LinterFinding) []validator.LinterFinding {
	var all []validator.LinterFinding
	for _, findings := range perRule {
		all = append(all, findings...)
	}
	return all
}

// downgradeToWarning maps every error finding to a warning. Default
// mode (no --pre-publish) treats every rule's output as informational.
func downgradeToWarning(findings []validator.LinterFinding) []validator.LinterFinding {
	out := make([]validator.LinterFinding, len(findings))
	for i, f := range findings {
		if f.Severity == "error" {
			f.Severity = "warning"
		}
		out[i] = f
	}
	return out
}

// downgradeFirstPublicationToWarning maps the schema-evolution
// rule's classification finding to warning severity for the first-
// publication case. Other rules' findings stay at
// whatever severity they had.
func downgradeFirstPublicationToWarning(findings []validator.LinterFinding) []validator.LinterFinding {
	out := make([]validator.LinterFinding, len(findings))
	for i, f := range findings {
		if f.RuleID == "schema-evolution" && f.Severity == "error" {
			f.Severity = "warning"
		}
		out[i] = f
	}
	return out
}

// printFindings writes each finding to diagOut (os.Stderr in production) in a
// stable format and returns true when at least one finding is severity=error.
func printFindings(findings []validator.LinterFinding, diagOut io.Writer) bool {
	halted := false
	for _, f := range findings {
		if f.Severity == "error" {
			halted = true
		}
		fmt.Fprintf(diagOut, "[%s] %s: %s\n", f.Severity, f.RuleID, f.Message)
	}
	return halted
}

func countErrors(findings []validator.LinterFinding) int {
	n := 0
	for _, f := range findings {
		if f.Severity == "error" {
			n++
		}
	}
	return n
}

// classificationFromFindings extracts the schema-evolution rule's
// classification verdict. Returns "" when no schema-evolution
// finding exists. Multi-finding edge case: HIGHEST classification
// wins (MAJOR > MINOR > PATCH).
func classificationFromFindings(findings []validator.LinterFinding) string {
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

// proposedBumpSatisfies returns true when proposed >= required.
// Over-bump is allowed: MAJOR-required can satisfy with MAJOR;
// MINOR-required can satisfy with MINOR or MAJOR; PATCH-required
// can satisfy with any.
func proposedBumpSatisfies(proposed, required string) bool {
	rank := map[string]int{"PATCH": 0, "MINOR": 1, "MAJOR": 2}
	return rank[proposed] >= rank[required]
}

// bumpDirection compares two semver versions and returns PATCH,
// MINOR, or MAJOR. When the versions are equal or the previous is
// unparseable, returns PATCH (best-effort).
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
