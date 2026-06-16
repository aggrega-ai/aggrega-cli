// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// lint command flags. Cobra populates these globals from the
// invocation; lintRun reads them through lintConfig for testability.
var (
	lintEntityPath string
	lintPrePublish bool
	lintPluginPath string

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
		Short: "Validate the whole catalog, or run the publishing-linter over one entity",
		Long: `aggrega lint has two modes, selected by whether an entity path is given.

Without an entity argument, lint validates the WHOLE catalog: it loads
every catalog entity and runs the schema-pillar (Pass-2) and library
resolution/rejection checks — the same correctness validation a build
applies, but over the entire catalog rather than one Release's closure.
This is the place a broken catalog entity is meant to surface (CI,
pre-publish); a build validates only the Release it processes.

With an entity path, lint runs the publishing-linter rule set against
that entity. In default mode the rules run at warning severity over
local content without consulting any registry. With --pre-publish the
previous registry version is fetched and the schema-evolution
classification is compared against the proposed version bump; mismatches
halt. Per-workspace policy flags from .aggrega-config.yaml's
lint.publishing_discipline block can be overridden per invocation via
the --allow-*/--no-allow-* and --enforce-*/--no-enforce-* flag pairs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := lintEntityPath
			if path == "" && len(args) > 0 {
				path = args[0]
			}
			if path == "" {
				// No entity argument → whole-catalog validation.
				return lintCatalogWideFromWorkspace()
			}
			return lintRun(lintConfig{
				EntityPath:     path,
				PrePublish:     lintPrePublish,
				PluginPath:     lintPluginPath,
				PolicyResolver: makeResolverFromCobra(cmd),
			})
		},
	}
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

// lintCatalogWideFromWorkspace resolves the workspace's catalog directories and
// cache from .aggrega-config.yaml, then runs the whole-catalog validation pass.
// Catalog dirs default to the workspace root when none are configured (mirroring
// the daemon's catalog resolution).
func lintCatalogWideFromWorkspace() error {
	wsRoot, _ := os.Getwd()
	wsCfg, err := workspaceconfig.Load(workspaceConfigPath())
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	cacheDir := wsCfg.Workspace.ResolvedCacheDir()
	// Whole-catalog lint has no --catalog flag, so the flag list is empty; the
	// shared resolver reproduces the prior behavior — config catalog_dirs, or the
	// workspace root when none are configured.
	dirs := resolveCatalogDirs(nil, wsCfg, wsRoot)
	return lintCatalogWide(wsRoot, cacheDir, dirs)
}

// lintCatalogWide runs the whole-catalog correctness validation: it loads every
// catalog entity under dirs and applies the schema-pillar (Pass-2) and library
// resolution/rejection checks (LoadAllWithCacheDir), then resolves the extends
// chains. This is the catalog-wide counterpart to a build's per-Release closure
// validation — the home where a broken catalog entity is meant to surface (CI,
// pre-publish). It halts loudly on any schema-pillar violation, unresolvable or
// invalid library, or extends-resolution failure.
func lintCatalogWide(wsRoot, cacheDir string, dirs []string) error {
	cat, err := catalog.LoadAllWithCacheDir(wsRoot, cacheDir, dirs...)
	if err != nil {
		return fmt.Errorf("catalog validation failed: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(os.Stderr, "  extends: %s\n", e)
		}
		return fmt.Errorf("catalog validation failed: %d extends resolution error(s)", len(extErrs))
	}
	if err := verifyNoOCILocationCollision(cat); err != nil {
		return fmt.Errorf("catalog validation failed: %w", err)
	}
	// Surface the whole-catalog advisory notices accumulated during the eager
	// passes above — schema-pillar overlap (Pass-2) and narrowing-on-extend (from
	// ResolveAllExtends). Informational only: advisories never escalate the exit
	// code, which stays 0 here (errors already returned above). Emission order is
	// deterministic — lintWarnings is accumulated over sorted-key walks.
	for _, w := range cat.LintWarnings() {
		fmt.Fprintf(os.Stderr, "  notice: %s\n", w)
	}
	fmt.Println("catalog validation passed (schema-pillar + library + extends + oci-location)")
	return nil
}

// verifyNoOCILocationCollision halts when two DISTINCT catalog identities map to
// the same OCI repository location (the lowercased, identity-derived coordinate
// from ociLocation). Such a collision would silently overwrite one entity's
// published bytes with another's at population time — the loud counterpart to the
// publish-path location encoding. Grouping is by version-independent identity
// (provider/name for recipes, metadata.name otherwise), so multiple versions of
// one identity sharing a location — distinguished by tag — are not a collision.
func verifyNoOCILocationCollision(cat *catalog.Catalog) error {
	byLoc := map[string]map[string]struct{}{}
	add := func(kind, provider, name string) {
		loc := catalog.OCILocation(kind, provider, name)
		ident := name
		if provider != "" {
			ident = provider + "/" + name
		}
		if byLoc[loc] == nil {
			byLoc[loc] = map[string]struct{}{}
		}
		byLoc[loc][ident] = struct{}{}
	}
	for _, e := range cat.Components {
		add("Component", "", e.Metadata.Name)
	}
	for _, e := range cat.Features {
		add("Feature", "", e.Metadata.Name)
	}
	for _, e := range cat.Providers {
		add("Provider", "", e.Metadata.Name)
	}
	for _, e := range cat.Aggregates {
		add("Aggregate", "", e.Metadata.Name)
	}
	for _, e := range cat.Recipes {
		add("Recipe", e.Spec.Provider, e.Metadata.Name)
	}
	for _, e := range cat.FeatureRecipes {
		add("FeatureRecipe", e.Spec.Provider, e.Metadata.Name)
	}
	for _, e := range cat.SchemaLibraries {
		add("SchemaLibrary", "", e.Metadata.Name)
	}
	for _, e := range cat.MappingLibraries {
		add("MappingLibrary", "", e.Metadata.Name)
	}

	var collisions []string
	for loc, idents := range byLoc {
		if len(idents) <= 1 {
			continue
		}
		names := make([]string, 0, len(idents))
		for id := range idents {
			names = append(names, id)
		}
		sort.Strings(names)
		collisions = append(collisions, fmt.Sprintf("%s <- %s", loc, strings.Join(names, ", ")))
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Strings(collisions)
	return fmt.Errorf("%w:\n  - %s", oci.ErrOCILocationCollision, strings.Join(collisions, "\n  - "))
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
// metadata.labels, and spec.extends. Mirrors pushEntityHeader's
// shape and extends it for narrowing-rule parent resolution.
type lintEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string            `yaml:"name"`
		Version string            `yaml:"version"`
		Labels  map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Extends  string `yaml:"extends"`
		Provider string `yaml:"provider"`
	} `yaml:"spec"`
}

// defaultRuleIDs returns the rule set to invoke per mode. Default
// mode runs all structural rules. --pre-publish prepends
// schema-evolution (which needs previous bytes).
//
// LOCKSTEP: the slice MUST match the plugin's lib.rs dispatcher
// arms. Adding a rule requires updating both this
// slice AND aggrega-cli/wasm-plugins/publishing-linter/src/lib.rs.
func defaultRuleIDs(prePublish bool) []string {
	rules := []string{
		"annotation-prefix",
		"name-shape",
		"narrowing",
		"dynamic-refs",
		"id-overrides",
		"category-matches-directory",
		"x-aggrega-discouragement",
	}
	if prePublish {
		return append([]string{"schema-evolution"}, rules...)
	}
	return rules
}

func lintRun(cfg lintConfig) error {
	if cfg.EntityPath == "" {
		return fmt.Errorf("aggrega lint: entity path required (pass as positional arg or via --entity)")
	}
	wsCfg, err := workspaceconfig.Load(workspaceConfigPath())
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	pluginPath, err := resolvePluginPath(cfg.PluginPath, wsCfg)
	if err != nil {
		return err
	}

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

	var (
		previousJSON     json.RawMessage
		previousVersion  string
		firstPublication bool
	)
	if cfg.PrePublish {
		previousJSON, previousVersion, err = fetchPreviousRegistryVersion(
			context.Background(), cfg.FetchTarget, wsCfg, header,
		)
		if err != nil {
			return fmt.Errorf("aggrega lint: fetch previous: %w", err)
		}
		firstPublication = previousVersion == ""
	}

	parentJSON, _ := resolveParent(header, wsCfg, cfg.EntityPath)

	inputs := buildRuleInputs(header.Kind, proposedJSON, previousJSON, parentJSON, policies, cfg.EntityPath, cfg.PrePublish)
	rawFindings, err := validator.DispatchPublishingLinterBatch(pluginPath, inputs)
	if err != nil {
		return fmt.Errorf("aggrega lint: dispatch: %w", err)
	}

	aggregated := aggregateFindings(rawFindings)
	if !cfg.PrePublish {
		aggregated = downgradeToWarning(aggregated)
	}

	if firstPublication {
		fmt.Fprintf(os.Stderr,
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
	if cfg.PrePublish && !firstPublication {
		verdict := classificationFromFindings(aggregated)
		if verdict != "" {
			proposedBump := bumpDirection(previousVersion, header.Metadata.Version)
			if !proposedBumpSatisfies(proposedBump, verdict) {
				printFindings(aggregated)
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

	halted := printFindings(aggregated)
	if halted {
		return fmt.Errorf("aggrega lint: %d error finding(s)", countErrors(aggregated))
	}
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

// resolvePluginPath implements three-tier resolution.
// --plugin > workspace plugins.linter[0].uri > loud diagnostic.
// No implicit dev fallback.
//
// Relative paths resolve against the workspace root, NOT the
// invocation CWD. workspaceRoot walks up from CWD looking for
// `.aggrega-config.yaml`; CWD is used as fallback only when no
// marker is found.
func resolvePluginPath(cliFlag string, wsCfg workspaceconfig.Config) (string, error) {
	if cliFlag != "" {
		if filepath.IsAbs(cliFlag) {
			return cliFlag, nil
		}
		root, err := workspaceRoot()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, cliFlag), nil
	}
	if len(wsCfg.Plugins.Linter) > 0 {
		uri := wsCfg.Plugins.Linter[0].URI
		if looksLikeOCIURI(uri) {
			return "", fmt.Errorf(
				"aggrega lint: workspace config plugins.linter[0].uri = %q is an OCI reference; "+
					"OCI plugin URI resolution not yet wired. Either:\n"+
					"  - pass --plugin <filesystem-path>\n"+
					"  - or set plugins.linter[0].uri to a workspace-relative path until OCI loading lands\n"+
					"(standard URI for the publishing-linter: hub.aggrega.ai/wasm/linter/aggrega-publishing:1.0.0)",
				uri,
			)
		}
		if filepath.IsAbs(uri) {
			return uri, nil
		}
		root, err := workspaceRoot()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, uri), nil
	}
	return "", fmt.Errorf(
		"aggrega lint: no plugin path configured. Either:\n" +
			"  - pass --plugin <filesystem-path>\n" +
			"  - or set plugins.linter[0].uri in .aggrega-config.yaml to a workspace-relative path")
}

// workspaceRoot walks up from the current working directory looking
// for `.aggrega-config.yaml`. The directory containing that file is
// the workspace root. When no marker is found before the filesystem
// root, falls back to CWD (preserving prior behaviour for the
// no-marker case).
//
// Resolution discipline: relative plugin paths
// resolve against the workspace root rather than the invocation
// CWD. Operators running `aggrega lint ../other-workspace/entity.yaml`
// from a sub-directory get the correct plugin path lookup.
func workspaceRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("workspace root: getwd: %w", err)
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".aggrega-config.yaml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root; fall back to CWD.
			return cwd, nil
		}
		dir = parent
	}
}

// looksLikeOCIURI returns true when the URI looks like an OCI
// reference. Heuristic: oci:// scheme, or hub.aggrega.ai host, or
// <host>/<path>:<tag> shape with a slash before the colon.
func looksLikeOCIURI(uri string) bool {
	if strings.HasPrefix(uri, "oci://") {
		return true
	}
	if strings.Contains(uri, ".aggrega.ai") || strings.Contains(uri, "hub.aggrega.ai") {
		return true
	}
	if !filepath.IsAbs(uri) && strings.Contains(uri, "/") && strings.Contains(uri, ":") {
		return true
	}
	return false
}

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
// (gopkg.in/yaml.v3 default for nested maps) to map[string]interface{}
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
// publication.
func fetchPreviousRegistryVersion(
	ctx context.Context,
	overrideTarget oras.ReadOnlyTarget,
	wsCfg workspaceconfig.Config,
	header lintEntityHeader,
) (json.RawMessage, string, error) {
	target := overrideTarget
	if target == nil {
		reference := buildPushReference(wsCfg.Workspace.DefaultRegistry, header.Kind, header.Spec.Provider, header.Metadata.Name)
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
	latest := oci.LatestNonPreRelease(tags)
	if latest == "" {
		// Distinguish "no previous version exists" (true first publication)
		// from "tags exist but all are pre-release" (different shape;
		// operator should know).
		if oci.HasAnySemverTag(tags) {
			fmt.Fprintf(os.Stderr,
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
// resolvable — the narrowing rule short-circuits on nil parent.
func resolveParent(header lintEntityHeader, wsCfg workspaceconfig.Config, entityPath string) (json.RawMessage, error) {
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
	fmt.Fprintf(os.Stderr, "narrowing rule: parent %q not locally resolvable; rule skipped\n", header.Spec.Extends)
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

// printFindings writes each finding to stderr in a stable format
// and returns true when at least one finding is severity=error.
func printFindings(findings []validator.LinterFinding) bool {
	halted := false
	for _, f := range findings {
		if f.Severity == "error" {
			halted = true
		}
		fmt.Fprintf(os.Stderr, "[%s] %s: %s\n", f.Severity, f.RuleID, f.Message)
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
