// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// RunCatalogWide runs the structural publishing-linter rules over every catalog
// entity found under the catalog directories of each pass in passes, which the
// caller takes from the one decision of which passes a whole-catalog check runs
// (see [workspaceconfig.ResolvedWorkspace.WholeCatalogPasses]). Each pass's stack
// is the shared one — the --catalog directories prepended to the configured
// list, with the conventional catalog directory at the workspace root last.
//
// A workspace that gives no Release directories of its own has one pass, over
// its own stack, and is reported exactly as a single catalog is. A workspace that
// does has one pass for each Release — Releases reading identical directories
// sharing one — and each pass is reported under a line naming its Releases and
// the directories it read, with its findings and its own summary after it, so a
// catalog variant one Release reads is never checked against another's. A pass
// whose Releases read no directory says so and fails none of the others.
//
// A run whose passes read nothing at all is refused rather than reported as a
// clean catalog, because a pass over no entity at all passes vacuously and reads
// exactly like a pass over a healthy one. This is the conventions counterpart to
// whole-catalog correctness (`aggrega validate`): correctness halts on a broken
// entity, conventions report publishing-discipline advisories and never halt on a
// convention finding alone.
//
// collect optionally gathers the findings as data alongside the human report; nil
// collects nothing. For a run of several passes it also gathers each pass on its
// own, under the Releases it was run for.
//
// policyResolve decides each publishing-discipline suppression's effective value
// from the workspace's own, keyed on the suppression's flag name, exactly as it
// does for the release-scoped pass and the single-entity check — so a command
// line's --allow-*/--no-allow-* pair reaches every pass of this check as well.
// nil takes every workspace value as it is, which is what a surface with no such
// flags passes.
//
// ctx ends the pass where the caller stops wanting it. This is the pass whose
// cost scales with the catalog — one guest dispatch per entity over every entity
// the configured directories hold — so it is the one where abandoning the work
// and discarding the answer are materially different outcomes. Both the tree
// walk and the per-entity loop observe it, so a stop is honoured whichever of
// the two the pass is in.
func RunCatalogWide(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, passes []workspaceconfig.CatalogPass, policyResolve func(flagBase string, wsValue bool) bool, out, diagOut io.Writer, collect *FindingCollector) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	// The refusal keys on the RESOLVED stacks rather than on the flag and the
	// configured lists, so a workspace whose entities sit in the conventional
	// directory is scanned rather than refused, and a workspace that switched the
	// local catalog off with an empty `catalog_dirs` is refused rather than
	// silently scanning nothing.
	perRelease := len(passes) > 0 && passes[0].NamesReleases()
	if !workspaceconfig.AnyCatalogPassReads(passes) {
		return emptyCatalogRefusal(perRelease)
	}
	if !perRelease {
		return lintCatalogPass(ctx, rw, passes[0].Dirs, passes[0].Named, policyResolve, out, diagOut, collect)
	}
	for i, pass := range passes {
		if i > 0 {
			fmt.Fprintln(out)
		}
		reported := make([]string, 0, len(pass.Dirs))
		for _, d := range pass.Dirs {
			reported = append(reported, pathscope.ReportedName(rw.Root(), d))
		}
		fmt.Fprintln(out, workspaceconfig.CatalogPassHeading(pass.Releases, reported))
		gathered := collect.forPass()
		var passErr error
		if len(pass.Dirs) > 0 {
			passErr = lintCatalogPass(ctx, rw, pass.Dirs, pass.Named, policyResolve, out, diagOut, gathered)
		}
		collect.addPass(pass.Releases, pass.Dirs, gathered)
		if passErr != nil {
			return passErr
		}
	}
	return nil
}

// emptyCatalogRefusal is the refusal a whole-catalog conventions check gives
// when none of its passes has a directory to read. Its remedies are the ones a
// reader can act on in the workspace in front of them: a workspace that gives
// its Releases directories of their own is also told where a Release's own list
// is set, since setting the workspace's list does not reach a Release whose
// entry replaces it.
func emptyCatalogRefusal(perRelease bool) error {
	return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
		fmt.Sprintf("whole-catalog conventions check needs catalog directories to scan; put the entities under `%s/` at the workspace root, pass --catalog, or set %s (or check a specific Release with --release)",
			workspaceconfig.ConventionalCatalogDir, workspaceconfig.CatalogDirsSettingWords(perRelease)),
		nil, nil)
}

// lintCatalogPass is one pass of the whole-catalog conventions check: every
// catalog entity under dirs, linted against lookups over the same dirs and under
// the suppressions policyResolve decides, then the summary and the kinds the walk
// passed over. named is the part of dirs somebody named, handed on to every walk
// of them.
func lintCatalogPass(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, dirs, named []string, policyResolve func(flagBase string, wsValue bool) bool, out, diagOut io.Writer, collect *FindingCollector) error {
	files, passedOver, err := walkCatalogEntityFiles(ctx, rw.Root(), dirs, named, rw.Config().ResolvedArtifactCacheDir())
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	// No pass of the whole-catalog check holds its linter attachments to the
	// record of the plugin identities trusted for the workspace's verdict
	// attachments, including a pass run for a named Release: the pass's subject is
	// the directories it reads, not that Release's dependency closure. So the
	// attachments resolve pin-free, exactly as they did before that record existed.
	// The release-scoped pass below is the lint path that does hold them to it, and
	// it is the same path that reads the lockfile beside the Release, so both files
	// are read from one place.
	if err := lintConventionsFiles(ctx, rw, dirs, named, paths, conventionsRun{
		trusted:       resolver.VerdictPluginTrust{},
		policyResolve: policyResolve,
	}, out, diagOut, collect); err != nil {
		return err
	}
	// This is the pass whose summary claims a corpus — "every catalog entity under
	// the catalog directories" — so it is the pass that owes the reader the part of
	// the tree the claim does not cover. Without this line the count above is the
	// only thing said about coverage, and a kind the walk declines is indistinguishable
	// from a kind that was checked and found clean. The release-scoped pass makes no
	// such claim: it reports on one Release's dependency closure, and membership of
	// that closure, not the kind gate, is what decides which files it reaches.
	writePassedOverKinds(out, passedOver)
	return nil
}

// writePassedOverKinds names the entity kinds the whole-catalog walk recognised
// and did not check, so the summary's count is read as the corpus it actually
// covers. It writes nothing when the walk passed nothing over, which is the
// ordinary case for a catalog tree holding catalog entities alone.
//
// Kinds are named in the entity-kind vocabulary's own declared order, so the line
// is byte-identical across runs over the same tree without this file holding an
// ordering of its own.
func writePassedOverKinds(out io.Writer, passedOver map[string]int) {
	if len(passedOver) == 0 {
		return
	}
	parts := make([]string, 0, len(passedOver))
	for _, k := range entitykind.Kinds() {
		n := passedOver[string(k)]
		if n == 0 {
			continue
		}
		noun := string(k)
		if n != 1 {
			noun += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, noun))
	}
	fmt.Fprintf(out, "not checked: %s — this pass covers catalog entity kinds; check one of these directly with `aggrega lint <path>`\n",
		strings.Join(parts, ", "))
}

// RunRelease computes a Release's resolved closure through the SAME build
// machinery the compiler uses (the shared closure resolver over the lazy lookup)
// and runs the structural publishing-linter rules over the catalog entity files
// that closure reaches. Entities are resolved to their author-owned catalog
// files; registry-pinned entities living in a cache slot carry
// no local file to lint and are skipped (the publishing-linter checks author-time
// conventions, which only apply to locally-authored files). Findings are advisory
// — conventions never halt on a convention finding alone.
//
// collect optionally gathers the findings as data alongside the human report; nil
// collects nothing.
//
// ctx ends the pass where the caller stops wanting it, at the same two places
// the whole-catalog pass observes it — the tree walk and the per-entity loop —
// plus once ahead of the closure resolution, which is the step this pass adds
// and the one that reads the most before any entity is checked.
//
// policyResolve decides each publishing-discipline suppression's effective value
// from the workspace's own, keyed on the suppression's flag name — the command
// line's --allow-*/--no-allow-* pairs, for the terminal. nil takes every
// workspace value as it is, which is what a surface with no such flags passes.
//
// This is also the one pass with a whole Release in hand, so it is where the
// rules that read one are dispatched: once, after the per-entity rules, over the
// Aggregate the Release deploys, the Release, and the Components the Aggregate's
// instances use — see releaseRuleDocumentsFor. Their findings are advisory like
// every other, and are reported against the Release.
func RunRelease(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, policyResolve func(flagBase string, wsValue bool) bool, out, diagOut io.Writer, collect *FindingCollector) error {
	return runRelease(ctx, rw, releasePath, catalogFlagDirs, out, diagOut, collect, releaseRun{
		policyResolve: policyResolve,
		rules:         releaseRuleIDs(),
		dispatch:      dispatchLinterAttachments,
	})
}

// releaseRun is what a release-scoped run reads beyond its workspace and its
// Release: the suppression resolver, the rules that read a whole Release, and the
// dispatch that runs the guests. RunRelease supplies the production values; a
// test supplies its own rule and a dispatch that records the payload it is
// handed, which is how a release-scoped rule's input is observed at the point it
// leaves for a guest.
type releaseRun struct {
	policyResolve func(flagBase string, wsValue bool) bool
	rules         []string
	dispatch      linterDispatch
}

// runRelease is RunRelease over an explicit releaseRun.
func runRelease(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, out, diagOut io.Writer, collect *FindingCollector, run releaseRun) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs, err := rw.ResolveCatalogDirs(releasePath, catalogFlagDirs)
	if err != nil {
		return err
	}
	named, err := rw.NamedCatalogDirs(releasePath, catalogFlagDirs)
	if err != nil {
		return err
	}

	lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil, named...)
	lazyView.WriteNoticesTo(diagOut)
	lf, err := lockfile.LoadIfReadable(wsRoot, lockfile.PathFor(releasePath))
	if err != nil {
		return err
	}
	var lockForClosure *lockfile.Lockfile
	if lf != nil {
		pins, err := catalog.PinsFromLockfile(lf)
		if err != nil {
			return err
		}
		lazyView.SetPins(pins)
		lockForClosure = lf
	}

	// The deprecation report over both stores — the lockfile's recorded markers and
	// the plugin trust record's — the same report the build reads, so this pass
	// names the same references and plugins rather than a subset. A pass that said
	// nothing about a withdrawn dependency or linter would read as though there were
	// none, and this is the pass that runs those linters.
	//
	// It is reported BEFORE the gates and the pass below, because it depends on
	// neither: a recorded marker is a fact about the store that recorded it, so a
	// workspace whose closure does not resolve — or whose gate refuses, the trust
	// record's own dropped-gate refusal included — still learns that something has
	// been withdrawn, which is often the explanation. It does not move the verdict,
	// for the reason the build's own default records: a marker is registry state
	// applied after the version was pinned, and the dial that turns it into a halt
	// governs the build alone. A workspace with no lockfile or no record has no
	// recorded markers in it either, so reporting nothing there skips nothing. A
	// record that cannot be read stops the pass after the lockfile's findings are
	// printed, because a damaged record must not pass as an empty one.
	findings, trustErr := compilerops.ReleaseDeprecationReport(lockForClosure, releasePath)
	for _, f := range findings {
		fmt.Fprintf(diagOut, "deprecation: %s\n", f.Notice)
	}
	if trustErr != nil {
		return trustErr
	}

	// Hold this Release's configuration to the record of the plugin identities
	// trusted for its verdict attachments, and carry the recorded digests and
	// signatures into the linter socket's resolution, so a linter the pass loads is
	// held to its recorded signature as a build holds it. It runs before any entity
	// is linted, so a gate that was reviewed and has since been dropped from the
	// configuration refuses the pass rather than being discovered after the findings
	// are printed.
	//
	// This is the lint path that names a Release, and therefore the one that can
	// locate the record — the same reason it is the lint path that reads the
	// lockfile above. The single-entity pass and the whole-catalog pass read no
	// such record, and say so where they resolve their attachments.
	trusted, terr := compilerops.VerdictPluginTrust(lazyView, wsCfg, releasePath)
	if terr != nil {
		return terr
	}
	// Checked ahead of the closure resolution, which is the heaviest read this
	// pass performs before any entity is checked. The resolution carries the same
	// context on into its own registry steps, so this check is the cheap early
	// exit rather than the only one: a caller that gives up mid-resolution stops
	// the fetches underneath it too.
	if err := stopped(ctx); err != nil {
		return err
	}
	rc, cerr := compilerops.ResolveClosure(ctx, compilerops.ClosureInputs{
		View:            lazyView,
		ReleasePath:     releasePath,
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
		Lockfile:        lockForClosure,
		Augment:         true,
	})
	if cerr != nil {
		return cerr
	}

	// The kinds the walk passes over are not reported here. This pass reports on
	// one Release's dependency closure, and the closure decides which files it
	// reaches; naming the kinds the walk declined would suggest everything else in
	// the walked tree was checked, when most of it was simply outside the closure.
	files, _, err := walkCatalogEntityFiles(ctx, wsRoot, dirs, named, cacheDir)
	if err != nil {
		return err
	}
	paths := closureEntityFiles(lazyView, rc.Closure, entityFileIndex(files))
	// The documents a rule reading a whole Release is handed are assembled only
	// when some such rule is in the set, so a run whose set holds none does the
	// per-entity work alone. They resolve each instance's type through this pass's
	// own view, which carries the lockfile's pins, so an instance reaches the
	// Component the build would use.
	var check *releaseRuleCheck
	if len(run.rules) > 0 {
		docs, derr := releaseRuleDocumentsFor(rc, lazyView.Component)
		if derr != nil {
			return derr
		}
		check = &releaseRuleCheck{rules: run.rules, docs: docs, path: releasePath}
	}
	lintErr := lintConventionsFiles(ctx, rw, dirs, named, paths, conventionsRun{
		trusted:       trusted,
		policyResolve: run.policyResolve,
		releaseRules:  check,
		dispatch:      run.dispatch,
	}, out, diagOut, collect)

	// A Manifest mapping the catalog contradicts is reported here rather than
	// halting ahead of the pass, so the conventions report the author came for
	// arrives whole. It is not a convention finding, though, and it does not
	// inherit the advisory exit code conventions carry: the mapping names a recipe
	// that does not implement the mapped type, which is wrong however the author
	// feels about conventions, so it moves the verdict.
	//
	// A real lint failure — a plugin that would not load, a file that would not
	// read — wins the returned error, because it means the pass itself did not
	// happen and reporting a mapping verdict over an incomplete pass would overstate
	// what ran.
	for _, f := range rc.ManifestConsistencyFindings {
		fmt.Fprintf(diagOut, "mapping: %s\n", f)
	}
	if lintErr != nil {
		return lintErr
	}
	if n := len(rc.ManifestConsistencyFindings); n > 0 {
		return fmt.Errorf("Release Manifest mapping: %d diagnostic(s)", n)
	}
	return nil
}

// linterDispatch runs inputs at every resolved linter attachment and returns the
// findings, one slice per input per attachment. dispatchLinterAttachments is the
// one production value; it is a type so a test can stand in for it and record
// the inputs at the point they leave for a guest.
type linterDispatch func(ctx context.Context, attachments []LinterAttachment, inputs []validator.LinterInput) ([][]abi.Diagnostic, error)

// conventionsRun is what a conventions pass reads beyond the files it checks.
type conventionsRun struct {
	// trusted carries the record's contribution, as lintConventionsFiles describes.
	trusted resolver.VerdictPluginTrust
	// policyResolve decides each suppression's effective value from its workspace
	// value, keyed on the suppression's flag name. nil takes the workspace value
	// as it is.
	policyResolve func(flagBase string, wsValue bool) bool
	// releaseRules, when set, is checked once after the entity files, by the rules
	// that read a whole Release.
	releaseRules *releaseRuleCheck
	// dispatch runs the guests. nil is dispatchLinterAttachments.
	dispatch linterDispatch
}

// releaseRuleCheck is a release-scoped pass's check over the whole Release: the
// rules that read one, the documents they read, and the file their findings are
// reported against.
type releaseRuleCheck struct {
	rules []string
	docs  releaseRuleDocuments
	path  string
}

// lintConventionsFiles runs the structural publishing-linter rules (the default rule
// set minus schema-evolution — that rule needs a previous registry version and
// stays single-entity, --pre-publish only) over each entity file in paths, at
// warn severity, at every linter attachment the workspace configures. It is the
// one body the whole-catalog and release-scoped conventions passes run: both
// resolve a set of entity files, then hand them here, reusing the same
// per-entity dispatch the single-entity lint runs (buildRuleInputs +
// dispatchLinterAttachments). A convention finding is advisory and never halts;
// a plugin-load, read, or parse failure is a real error and does halt. Findings
// are written to diagOut (os.Stderr in production); the final summary is written
// to out (os.Stdout). collect optionally gathers the same findings as data; nil
// collects nothing.
//
// run.trusted carries the digests recorded as trusted for the plugins that return
// an accept-or-reject verdict, and what the record says of their signatures,
// threaded from the caller rather than read here,
// because only one of the two callers reads the record: it sits beside a Release,
// and a whole-catalog pass is about the directories it reads rather than any one
// Release's dependency closure, even where it is run for one. Both arms of the
// linter socket's resolution receive it, so a recorded digest governs the bytes
// whichever tier serves them. An empty set is the ordinary case and resolves
// pin-free.
//
// run.policyResolve decides the suppressions both passes run under, so a command
// line's --allow-*/--no-allow-* pair reaches each of them on the same terms.
//
// dirs are the catalog directories the caller walked to find paths, and the
// plugin and Component lookups below read exactly those: a whole-catalog pass run
// for several Releases that share a stack, and a Release's closure pass, are each
// handed the one stack they read rather than re-deriving it here — and named, the
// part of it somebody named, for the same reason.
//
// ctx is observed once per entity, at the top of the loop, which is the seam
// that makes a stop reach the work: this loop is where both passes spend their
// time, one guest dispatch per entity, and an entity not yet reached is an
// entity never read, never parsed and never dispatched.
//
// A stop returns the caller's own reason and no summary. That is deliberate: the
// summary line claims a corpus — "conventions checked: N entities" — and
// printing it after a stop would report a partial walk in the words of a complete
// one, which is the one thing a reader cannot recover from the output.
func lintConventionsFiles(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, dirs, named []string, paths []string, run conventionsRun, out, diagOut io.Writer, collect *FindingCollector) error {
	wsCfg := rw.Config()
	lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil, named...)
	lazyView.WriteNoticesTo(diagOut)
	attachments, err := ResolveLinterPlugins("", wsCfg, run.trusted, lazyView.PluginByURI,
		pluginpath.ReportUnrecordedTo(diagOut),
		linterCacheResolve(rw.Root(), wsCfg, run.trusted, lazyView.PluginFromCacheByURI))
	if err != nil {
		return err
	}
	resolve := run.policyResolve
	if resolve == nil {
		resolve = func(_ string, ws bool) bool { return ws }
	}
	policies := effectivePolicies(wsCfg.Lint.PublishingDiscipline, resolve)
	dispatch := run.dispatch
	if dispatch == nil {
		dispatch = dispatchLinterAttachments
	}

	findingCount := 0
	for _, path := range paths {
		if err := stopped(ctx); err != nil {
			return err
		}
		proposedBytes, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("aggrega lint: read %s: %w", path, err)
		}
		proposedJSON, header, err := yamlToLinterJSON(proposedBytes)
		if err != nil {
			return fmt.Errorf("aggrega lint: parse %s: %w", path, err)
		}
		inputs := buildRuleInputs(ruleInputSources{
			Kind:                 header.Kind,
			Proposed:             proposedJSON,
			ImplementedComponent: implementedComponentJSON(header, lazyView.Component),
			Policies:             policies,
			EntityPath:           path,
		})
		rawFindings, err := dispatch(ctx, attachments, inputs)
		if err != nil {
			return fmt.Errorf("aggrega lint: dispatch %s: %w", path, err)
		}
		n, err := reportAdvisories(aggregateFindings(rawFindings), header.Kind, path, diagOut, collect)
		if err != nil {
			return err
		}
		findingCount += n
	}
	if d := run.releaseRules; d != nil && len(d.rules) > 0 {
		if err := stopped(ctx); err != nil {
			return err
		}
		rawFindings, err := dispatch(ctx, attachments, buildReleaseRuleInputs(d.rules, d.docs, policies))
		if err != nil {
			return fmt.Errorf("aggrega lint: dispatch %s: %w", d.path, err)
		}
		n, err := reportAdvisories(aggregateFindings(rawFindings), string(entitykind.Release), d.path, diagOut, collect)
		if err != nil {
			return err
		}
		findingCount += n
	}
	collect.Entities(len(paths))
	fmt.Fprintf(out, "conventions checked: %d entit%s, %d advisory finding(s)\n",
		len(paths), plural(len(paths), "y", "ies"), findingCount)
	return nil
}

// reportAdvisories re-severitizes one dispatch's findings to the advisory
// severity, gathers them under path, writes them to diagOut beneath a line naming
// path, and returns how many there were. kind is the kind of the document at
// path, which decides the concern a halting finding would be filed under.
//
// Every finding was re-severitized before the report, so the report raises
// nothing and the error is nil by construction. It is returned rather than
// discarded so that a finding reaching the report still halting stops the pass
// instead of being printed as advice — which is the one outcome a reader could
// not tell from a clean run.
func reportAdvisories(findings []abi.Diagnostic, kind, path string, diagOut io.Writer, collect *FindingCollector) (int, error) {
	aggregated := downgradeToWarning(findings)
	collect.Add(aggregated, path)
	if len(aggregated) == 0 {
		return 0, nil
	}
	fmt.Fprintf(diagOut, "%s:\n", path)
	if err := reportFindings(aggregated, kind, path, diagOut); err != nil {
		return 0, err
	}
	return len(aggregated), nil
}

// plural picks the singular or plural suffix for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// catalogEntityFile records a catalog entity file discovered by walking the
// catalog directories, with the identity fields peeled from its YAML header. It
// drives the conventions passes: the whole-catalog pass lints every Path; the
// release pass indexes them (entityFileIndex) to map a closure entity back to its
// file.
type catalogEntityFile struct {
	Path    string
	Kind    string
	Name    string
	Version string
	Target  string
}

// isLintableCatalogKind reports whether a peeked kind names a published catalog
// entity the publishing-linter rules apply to. Deployment- and authoring-side
// kinds are not published catalog entities and are excluded from the conventions
// passes.
//
// It reads the declared entity-kind vocabulary rather than naming the kinds here.
// This is the gate the whole walk turns on — a kind it declines is dropped before
// its file enters the name index, so that file reaches no convention rule. A kind
// missing from a list written here would therefore be unlinted, which is exactly
// what happened to the two library kinds added most recently; reading the declared
// vocabulary is what keeps a kind's coverage a property of the vocabulary rather
// than of anyone remembering to extend a list.
func isLintableCatalogKind(kind string) bool {
	return entitykind.IsConventionsLinted(entitykind.Kind(kind))
}

// walkCatalogEntityFiles walks dirs in order, reads every .yaml / .yml file, and
// returns one record per file whose top-level kind names a published catalog
// entity. The result is sorted by path for deterministic iteration and reporting.
//
// The second return counts, per kind, the files the walk recognised as entities
// and did not return: a top-level kind that IS in the declared entity-kind
// vocabulary but that the publishing conventions do not cover. A caller reporting
// on the walked tree owes those to the reader, because a declined file reaches no
// convention rule and would otherwise be indistinguishable from a file that was
// checked and found clean.
//
// Anything else the walk passes over is absent from that count by design and is
// skipped silently: a file that is not an Aggrega document, and a file whose
// top-level kind is not one of the entity kinds at all. A catalog tree
// legitimately carries YAML that is not an Aggrega document — chart values and
// templates, workflow definitions, editor configuration — and every such file
// names some kind or none. Counting those would report the tree's ordinary
// contents as a coverage gap.
//
// An Aggrega document the walk cannot load is the one file it does not pass
// over: [catalog.UnloadableDocument] decides which files those are, and the walk
// reads every file before it halts with [catalog.UnloadableDocumentsHalt],
// naming each of them and returning no file at all. A check over a tree that
// returned the rest would report a clean catalog over a document it never read —
// the one outcome its count would then misstate. The release-scoped pass walks
// the same way and halts alike.
//
// cacheDir is the workspace's artifact cache location, interpreted relative to
// wsRoot; the walk steps over it, so an entity resolved from a cache slot is
// never returned as an author-owned file. That is what lets the conventions
// passes keep their promise to report only against files the author can edit —
// a catalog directory commonly IS the workspace root, which puts the cache
// inside the walked tree, but it may equally sit above or beside it, which is
// why the cache is anchored on wsRoot rather than on each walked directory.
// Passing "" for cacheDir disables the skip.
//
// ctx ends the walk where the caller stops wanting it. The walk reads and parses
// the YAML header of every file it meets, so over a real catalog it is work in
// its own right and not merely a directory listing; the stop is returned from
// the callback, which is how the walk is asked to abandon a traversal, and it is
// the caller's own reason rather than fs.SkipAll — skipping the rest would
// return a SHORT file list as though the walk had completed, which is a partial
// answer wearing a complete one's shape.
//
// The walk is the catalog's own (catalog.WalkDocuments), so this check reads
// exactly the documents a lookup over the same directories reads: a catalog
// directory that does not exist, or cannot be read, adds no file. A link leading
// out of the root a directory is held to is not read, and halts the check naming
// the link, as a compile over the same directories halts on it: the check's
// verdict answers for every document the directories hold, so it does not pass in
// silence over one it may not read. named is the part of dirs somebody named,
// handed to the walk unjudged.
func walkCatalogEntityFiles(ctx context.Context, wsRoot string, dirs, named []string, cacheDir string) ([]catalogEntityFile, map[string]int, error) {
	var files []catalogEntityFile
	var unloadable []error
	passedOver := map[string]int{}
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		err := catalog.WalkDocuments(wsRoot, dir, cacheDir, named, func(path string) error {
			if serr := stopped(ctx); serr != nil {
				return serr
			}
			// Identity is established before anything is read so that a file reached
			// twice — catalog directories are allowed to overlap — is read, counted
			// and reported once whichever way it falls.
			abs, aerr := filepath.Abs(path)
			if aerr != nil {
				abs = path
			}
			if _, dup := seen[abs]; dup {
				return nil
			}
			seen[abs] = struct{}{}
			data, rerr := os.ReadFile(path)
			var header lintEntityHeader
			if f := catalog.UnloadableDocument(path, data, rerr, &header); f != nil {
				unloadable = append(unloadable, f)
				return nil
			}
			if uerr := yaml.Unmarshal(data, &header); uerr != nil {
				return nil
			}
			if !isLintableCatalogKind(header.Kind) {
				if entitykind.IsKind(entitykind.Kind(header.Kind)) {
					passedOver[header.Kind]++
				}
				return nil
			}
			files = append(files, catalogEntityFile{
				Path:    path,
				Kind:    header.Kind,
				Name:    header.Metadata.Name,
				Version: header.Metadata.Version,
				Target:  header.Spec.Capability,
			})
			return nil
		})
		if err != nil {
			// A stop is returned as the caller's own reason, not as a walk failure:
			// naming the directory would read as a problem with that directory, when
			// what happened is that the caller withdrew. Every other error genuinely
			// is about the directory and keeps the location.
			if ctx.Err() != nil {
				return nil, nil, err
			}
			return nil, nil, fmt.Errorf("walk catalog directory %s: %w", dir, err)
		}
	}
	if halt := catalog.UnloadableDocumentsHalt(unloadable); halt != nil {
		return nil, nil, halt
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, passedOver, nil
}

// libraryIdentityKey is the index key of one library at one version, so a catalog
// holding several versions of a library name keeps each version's file apart.
func libraryIdentityKey(kind, name, version string) string {
	return kind + "|" + name + ":" + version
}

// entityFileIndex is the index closureEntityFiles joins a closure on, built from
// the catalog walk: every file under its Kind+Name, which a Component, Feature or
// Plugin is looked up by, and under its library identity (libraryIdentityKey),
// which a library is looked up by at the version the closure resolved.
func entityFileIndex(files []catalogEntityFile) map[string]string {
	index := map[string]string{}
	for _, f := range files {
		index[f.Kind+"|"+f.Name] = f.Path
		index[libraryIdentityKey(f.Kind, f.Name, f.Version)] = f.Path
	}
	return index
}

// closureEntityFiles maps the entities a Release's closure reaches back to their
// author-owned catalog files. Recipes and FeatureRecipes resolve through the lazy
// view's local-path bridge; Components, Features and Plugins resolve through the
// Kind+Name index built from the catalog walk, and libraries through the
// Kind+Name:Version identity index, at the version the closure resolved
// (entityFileIndex). A closure entity with no local file (a registry-pinned cache
// slot) or one absent from the index is skipped — the conventions rules apply to
// locally-authored files. Sub-aggregate
// references (KindHint "aggregate") are authoring-side and are not conventions-
// linted. The returned paths are deduplicated and sorted for determinism.
func closureEntityFiles(lazyView *catalog.LazyLookup, cl closure.Closure, index map[string]string) []string {
	set := map[string]struct{}{}
	add := func(p string) {
		if p != "" {
			set[p] = struct{}{}
		}
	}
	byName := func(kind, name string) string { return index[kind+"|"+name] }
	for _, e := range cl.Entities {
		switch e.KindHint {
		case "recipe":
			if r, ok := lazyView.RecipeByURI(e.URI); ok {
				if p, ok := lazyView.LocalRecipePath(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version); ok {
					add(p)
				}
			}
		case "feature_recipe":
			if fr, ok := lazyView.FeatureRecipeByURI(e.URI); ok {
				if p, ok := lazyView.LocalFeatureRecipePath(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version); ok {
					add(p)
				}
			}
		case "aggregate":
			// Authoring-side; not conventions-linted.
		default:
			// Only an entity collected as a Component or a Feature, or with no kind,
			// is read as one: another kind's entity — the Release's own Aggregate or
			// Manifest, a plugin — is not a Component or a Feature, whatever its name.
			if e.ReadableAs(closure.KindComponent) {
				if c, ok := lazyView.Component(e.URI); ok {
					add(byName("Component", c.Metadata.Name))
					continue
				}
			}
			if e.ReadableAs(closure.KindFeature) {
				if f, ok := lazyView.Feature(e.URI); ok {
					add(byName("Feature", f.Metadata.Name))
				}
			}
		}
	}
	for _, p := range cl.Plugins {
		if pl, ok := lazyView.PluginByURI(p.URI); ok {
			add(byName("Plugin", pl.Metadata.Name))
		}
	}
	// Every declared library axis, not a chosen subset: a locally-authored library
	// on an axis this loop skipped would reach no convention rule at all, and
	// nothing else would report that it had been skipped. Each library is read at
	// the version the closure resolved, so a catalog holding other versions of the
	// name lints the file the Release builds from.
	for _, axis := range closure.LibraryAxes() {
		for _, lib := range axis.Libraries(&cl) {
			add(index[libraryIdentityKey(axis.Kind, lib.URI, lib.ResolvedVersion)])
		}
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
