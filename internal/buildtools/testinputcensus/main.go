// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command testinputcensus identifies the test packages in this workspace that
// read input files from outside their own module root, and fails when a package
// joins that set without the set having been recorded.
//
// # Why that population is worth watching
//
// Go's test cache keys a package's result on the files the test binary actually
// read — but it deliberately leaves out any read that lands outside the module
// root, because such a file is not part of the module's own source. A test whose
// inputs live above its module therefore has those inputs in no cache key at
// all: change them and the cached pass is served again, unchanged, for bytes
// nothing looked at. The set of such packages is the shape of that exposure, and
// its size is the shape's extent. The remedy in use is to run the affected
// modules with caching defeated, which is a per-module decision, so what this
// command watches is whether the population needing that decision is growing.
//
// It watches a SET rather than a total, and the difference is the difference
// between two questions. A total answers "has it grown", which is the alarm; a
// set also answers "what grew", which is the diagnosis. A gate that raises the
// alarm without carrying the diagnosis sends its reader back to re-run the whole
// measurement to learn something the failure could have said, and this
// measurement is a whole-suite execution. So the recorded expectation is the set
// of carrying packages, and a failure names the ones that joined it.
//
// # Why it reads the toolchain's own test log
//
// Counting relative-path literals in source does not work, and not for want of a
// better implementation. Whether a bare ".." argument inside a path join is
// itself a path expression is a judgement call, and two defensible answers give
// totals that differ roughly twofold — so the criterion does not determine a
// number. Worse, a literal count is blind to a path assembled at run time: a
// test that declares its root once and builds several escaping paths from it by
// concatenation scores as one site, or as none.
//
// So this command asks the toolchain instead. Each test package is run once
// with caching defeated and with -test.testlogfile, the file the toolchain
// itself consumes to decide whether a cached result is still valid. Every file
// the binary opened or stat'd is in there, whether the path was written as a
// literal, concatenated, computed, or read from somewhere else entirely. The
// classification of each entry — see classify — is transcribed from the
// toolchain's own reading of that file, so the census has no criterion of its
// own to get wrong.
//
// # Where the logs come from, and what that costs
//
// The measurement requires the tests to have RUN. A cached package does not
// execute its binary and so writes no log at all, so a census over a cached
// suite would measure nothing and report a confident zero.
//
// This command does not run them. It READS the per-package logs the
// authoritative run's own cache-defeated test leg already collected, which the
// caller arranges by handing that leg a `go test -exec` shim that appends
// -test.testlogfile to each test binary's arguments. The logs are a by-product
// of an execution that was happening anyway, so the census itself costs a
// directory scan rather than a second execution of every Go test package in the
// tree.
//
// 🔴 It is still bound to the authoritative run and must not be wired into a
// per-change gate, and the reason is no longer cost. It can only speak for
// packages whose tests actually ran with the cache defeated, so a caller that
// collected no logs — or collected a partial set — must be refused rather than
// reported clean. An absent log is an UNMEASURED package, never a package that
// read nothing; censusPackage is where that refusal lives.
//
// # What the caller has to get right
//
// The log for a package is found by name, and the name is derived from the
// package's own directory — see testLogPath, which is the one home of that
// derivation and states the contract the shim has to satisfy. A caller whose
// shim keys logs differently will present the census with a directory full of
// logs it cannot match to any package, and every package will read as
// unmeasured: a loud refusal rather than a quiet zero, which is the direction
// this whole command is built to fail in.
//
// # Scope, and what it deliberately leaves out
//
// It censuses exactly the modules it is given, and prints them, so a report can
// never be mistaken for wider coverage than it has. It measures Go test packages
// only: suites in other languages are not governed by Go's test cache, so they
// are neither exposed to this failure nor covered by this count.
//
// Of the inputs that land outside a module root, only those still inside the
// repository tree are recorded and gated — a committed file in a sibling module
// or at the tree root, read across a module boundary. Inputs outside the tree
// altogether are reported on every run but not gated, and the reasoning for that
// split is on classified, where the criterion lives.
//
// The set is a floor, for one reason worth stating plainly: the log is written
// by the test binary, so a file read by a child process the test spawns appears
// nowhere in it. A test that shells out and lets the child do the reading is
// invisible here, and no amount of care in this command would change that.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// testInputExpectation is the recorded answer to "which test packages read a
// shared input from elsewhere in the repository tree", against which each run's
// measurement is compared.
//
// It is GENERATED, by this command's own -write-expectation mode, and that is
// what makes recording a set admissible at all. A hand-kept list beside a census
// would be a second copy of what the census measures, and the copy is what goes
// stale between readings; a set the census itself emits cannot drift from what it
// measured. The generated form also carries per-carrier detail a hand-kept list
// would not survive: the tree components each carrier's shared reads resolved
// into, so a failure can say which corpus a new carrier reached.
//
// A module absent from the record is expected to carry none, so a newly added
// module needs no bookkeeping until one of its packages actually reads a shared
// input.
//
// The comparison is per module and between SETS of package import paths, which
// makes its two directions deliberately asymmetric:
//
//   - a measured carrier the record does not hold is GROWTH, and the census
//     fails, because growth going unobserved is the thing it exists to prevent;
//   - a recorded carrier no longer measured means the record is STALE: it is
//     reported and moves no exit code, since a record left wider than the truth
//     quietly buys room for the next addition but is not itself a new exposure.
//
// # What is a measurement here, and what it was measured over
//
// Every figure this record or a report carries is a measurement rather than a
// target, so it carries what it was measured over. Criterion: a test package
// carries when its test log records at least one open or stat of a path that
// resolves outside its own module root and inside the repository tree. The set's
// own date is written into the generated file beside it.
//
// The denominator — how many test packages exist to be carriers at all — is
// measured under a different and much cheaper criterion: the module set is the
// same walk this census is driven from, and a package counts toward the
// denominator when `go list` reports it carrying TestGoFiles or XTestGoFiles.
// That gave 97 test packages across exactly four modules — aggrega-cli 94,
// aggrega-abi/go 1, aggrega-pdk/go 1, aggrega-schemas 1 — measured 2026-08-07 on
// go1.26.0, with aggrega-cli re-measured at 96 on 2026-08-08 and the other three
// unchanged. Re-measured 2026-08-18: aggrega-cli carries 101 and the other three
// are unchanged, for 104 in total. The denominator moves whenever a package gains
// its first test, so expect it to drift between readings: it is a snapshot for
// scale, and the gate watches the carrier set alone.
//
// # Where the current carriers reach, and why they reach there
//
// aggrega-abi/go and aggrega-pdk/go each carry one: their shared-payload parity
// suites, whose input corpus is the golden-vector tree at the repository root
// above each module — read by the Go, Rust and TypeScript sides alike, which is
// why it cannot live inside any one of them. Both modules run with caching
// defeated for exactly that reason.
//
// aggrega-cli reads across module boundaries from several directions: the same
// shared golden corpus as those two, the guest sources of the sibling plugin
// toolkits, and the catalog content it compiles against.
//
// Four of its packages are the ROOTS of that exposure — the ones that reach out
// of the module on their own account, rather than through a shared harness. 🔴
// They are NOT the whole set: the rest reach the same corpus through one of these
// four or through workspace-root discovery.
//
//   - internal/wasm            reads the shared schema tree.
//   - pkg/validator            resolves the repository root at run time and
//     joins paths beneath it.
//   - pkg/plugintest           the shared harness other packages reach the
//     out-of-module corpus through.
//   - internal/tools/typegen   byte-compares the codecs it commits into the
//     sibling shared-payload module.
//
// Those four are named because their reach MECHANISM is worth knowing, never as a
// roster. The roster is the generated record, and naming it by hand here would be
// the second copy that goes stale — an objection that binds a hand-written list
// and does not bind a generated one. The record exists precisely because a
// generated set cannot drift from what the census measures.
//
// 🔴 A TEXTUAL census cannot find these, and that is worth stating because it is
// the obvious thing to try. pkg/validator reaches outside the module via a helper
// that walks UP to the repository root and joins from there, so the path is
// assembled at run time and no relative-path literal appears in the source at
// all. A grep for an escaping path finds two of the four and reads as a complete
// answer. The census reads the test log precisely because the log records the
// open that happened rather than the spelling of a path.
//
// # A package can join the set without any new escaping read being written
//
// Every current aggrega-cli carrier's out-of-module reach mechanism was already
// in place before 2026-08-08, the day the earlier whole-module figure was
// recorded; the most recent of them first appeared 2026-07-24. So a package joins
// this set without a new escaping read being authored at all: an existing reach
// gets exercised by a test that did not exercise it before, or a package gains
// its first test and inherits a shared harness's reach.
//
// That is the case a total structurally cannot express and a source scan
// structurally cannot see, and it is the sharpest argument for recording the set.
// Asked what grew, a total can only be measured again; a set can be subtracted.
//
// # Why the cache-defeat note is not a theoretical worry
//
// Measured end to end 2026-08-07 on go1.26.0, against the shared golden corpus
// named above. Criterion: warm the cache on a passing package, change the golden
// by exactly one character keeping its byte length identical, then run the same
// package twice — once as the routine gate runs it, once with caching defeated.
//
//   - The cached run reported `ok (cached)` and exited 0. Go's test-input
//     identity was BIT-IDENTICAL across the mutation, because the golden sits
//     outside the module whose sources the cache keys on, so it is in no hash.
//   - The same tree state with caching defeated FAILED, exit 1, on three
//     separate byte-identity assertions naming the mutated field.
//
// So a green from the routine gate is not evidence that the current bytes of an
// out-of-module input were read — only that they were read at some point.
//
// 🔴 aggrega-cli does NOT run with caching defeated. That is why its absence from
// that enrolment is called out rather than left implicit, and it is what makes
// its carriers a LIVE stale-pass surface rather than a nominal one: the two
// modules above are enrolled, so their out-of-module inputs cannot serve a stale
// result, while every aggrega-cli carrier's can. Whether to enrol it is a
// separate decision, and the census reports the condition rather than taking it.
type testInputExpectation struct {
	// Measured is the plain YYYY-MM-DD UTC date the set was taken on. Empty means
	// no set has been recorded yet, which is a different state from a recorded
	// empty set and is reported as such: the first says nothing is known, the
	// second says the tree was measured and found clean.
	Measured string
	// Modules holds one entry per module carrying at least one carrier, ordered
	// by Rel. A module carrying none is absent rather than present-and-empty.
	Modules []expectedModule
}

// expectedModule is one module's recorded carriers.
type expectedModule struct {
	// Rel is the module path relative to the census root — the same string a
	// report names the module by.
	Rel string
	// CacheDefeated is whether the module ran with caching defeated at the moment
	// of recording. It is a record of the condition the measurement was taken
	// under and never a second authority on the enrolment, which lives with the
	// caller that supplies it. So a divergence between this and what a later run
	// is told is reported as a stale record and moves no exit code.
	CacheDefeated bool
	// Carriers holds the module's carrying test packages, ordered by ImportPath.
	Carriers []expectedCarrier
}

// expectedCarrier is one test package that reads at least one shared input from
// elsewhere in the repository tree.
type expectedCarrier struct {
	// ImportPath is the package's import path, and it is the identity the set
	// comparison is made on.
	ImportPath string
	// Reach holds the distinct top-level tree components this package's shared
	// reads resolved into, sorted — which corpus it reached, rather than which
	// file.
	//
	// The component and not the lowest path, and the reason is structural rather
	// than aesthetic: the shared reads are sorted absolute paths, so the lowest of
	// them is whatever sorts first beneath the tree root, and a dotfile at the
	// root sorts before every submodule directory. Recording the lowest path would
	// therefore name the same root file for almost any carrier — satisfying the
	// letter of naming what grew while telling a reader nothing about where it
	// reached.
	//
	// Reach is CONTEXT measured on the recording run and is not part of the gated
	// comparison. Which packages carry is a property of the tree; how far a given
	// run's reads happened to spread is not, so comparing it would fail on a
	// difference that means nothing.
	Reach []string
}

// expectationSite is where a reader is sent when the carrier set has moved. It is
// written out rather than derived so the message survives being read in a log far
// from the source, and it is the one home of that path: the write target is this
// joined onto the resolved tree root.
const expectationSite = "aggrega-cli/internal/buildtools/testinputcensus/expectation_gen.go"

func main() {
	root := flag.String("root", "", "workspace root the module paths are reported relative to (required)")
	// Required rather than defaulted to a temporary directory, because a default
	// would point at an empty directory and every package would then read as
	// unmeasured — a refusal whose cause is invisible from its message. There is
	// no parallelism knob any more: this command runs nothing, so the two axes
	// that used to multiply into memory belong to the test leg that collects the
	// logs and are set there.
	logDir := flag.String("log-dir", "", "directory holding the per-package test logs the caller's test leg collected (required)")
	reportOnly := flag.Bool("report-only", false, "print the census and exit 0 even when the carrier set has grown")
	cacheDefeated := flag.String("cache-defeated", "", "comma-separated module paths the caller already runs with caching defeated; when empty, the census says the enrolment was not supplied rather than implying none")
	// Writing is deliberately NOT gating: it accepts the measurement rather than
	// judging it, so it reports what moved and exits 0. It refuses when part of the
	// scope could not be measured, because a set recorded from a partial census
	// would read every unmeasured module's carriers as dropped.
	writeExpectation := flag.Bool("write-expectation", false, "rewrite the recorded carrier set from this run's measurement, report what moved, and exit 0")
	var modules stringList
	flag.Var(&modules, "module", "a Go module directory to census; repeatable, and the census covers exactly what it is given")
	flag.Parse()

	opts := runOpts{
		root:             *root,
		modules:          modules,
		logDir:           *logDir,
		reportOnly:       *reportOnly,
		cacheDefeated:    *cacheDefeated,
		writeExpectation: *writeExpectation,
	}
	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "testinputcensus: %v\n", err)
		os.Exit(1)
	}
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// pkgResult is one test package's measurement.
type pkgResult struct {
	ImportPath string
	Dir        string
	// SharedTree lists the inputs this package read from outside its module root
	// but inside the repository tree. This is the gated population.
	SharedTree []string
	// OutsideTree counts the inputs it read from outside the tree altogether,
	// reported but not gated; classified explains why.
	OutsideTree int
	Inputs      int
	Err         error
}

// modResult is one module's measurement.
type modResult struct {
	Rel       string
	Dir       string
	Root      string
	Workspace bool
	Packages  []pkgResult
	Err       error
}

// sharedTreeCount is how many of the module's test packages read at least one
// input from outside the module root but inside the repository tree. This is the
// number the gate watches.
func (m modResult) sharedTreeCount() int {
	n := 0
	for _, p := range m.Packages {
		if len(p.SharedTree) > 0 {
			n++
		}
	}
	return n
}

// outsideTreeCount is how many read at least one input from outside the tree
// altogether. Reported for completeness, never gated.
func (m modResult) outsideTreeCount() int {
	n := 0
	for _, p := range m.Packages {
		if p.OutsideTree > 0 {
			n++
		}
	}
	return n
}

// runOpts is what one census invocation was asked to do.
type runOpts struct {
	root string
	// treeRoot is the absolute form of root, resolved once so every
	// classification uses the same string.
	treeRoot string
	modules  []string
	// logDir holds the per-package test logs the caller's test leg collected.
	logDir           string
	reportOnly       bool
	cacheDefeated    string
	writeExpectation bool
}

func run(o runOpts) error {
	if o.root == "" {
		return errors.New("-root is required: the census reports module paths relative to it")
	}
	absRoot, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}
	o.treeRoot = absRoot
	if len(o.modules) == 0 {
		// A census over nothing is the one result that must never read as a
		// pass: it would report zero growth over an empty corpus.
		return errors.New("no -module given: a census with no modules certifies nothing")
	}
	if o.logDir == "" {
		return errors.New("-log-dir is required: the census reads the per-package test logs the caller's test leg collected, and it runs no tests of its own")
	}
	// A missing directory is refused HERE rather than being left to surface as a
	// missing log per package. The two conditions have different causes — a
	// caller that collected nothing versus a caller whose shim keyed one log
	// differently — and reporting the first as a hundred instances of the second
	// buries it.
	if fi, err := os.Stat(o.logDir); err != nil {
		return fmt.Errorf("the collected test logs are not readable at %s, so nothing can be measured: %w", o.logDir, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("-log-dir must be a directory holding one test log per test package, and %s is not one", o.logDir)
	}
	logRoot := o.logDir

	workspaceUse, err := workspaceUseSet(absRoot)
	if err != nil {
		return err
	}

	sorted := make([]string, len(o.modules))
	copy(sorted, o.modules)
	sort.Strings(sorted)

	results := make([]modResult, 0, len(sorted))
	for _, m := range sorted {
		results = append(results, censusModule(absRoot, m, workspaceUse, logRoot, o))
	}

	if o.writeExpectation {
		// Recording owns its own reporting, because whether the report is gated is
		// part of the recording decision rather than something to settle out here.
		return writeMeasured(os.Stdout, absRoot, results, expectation, o)
	}

	return report(os.Stdout, absRoot, results, expectation, o.reportOnly, o.cacheDefeated)
}

// workspaceUseSet reports which module directories the workspace file at root
// puts in scope, as absolute paths.
//
// The census resolves a module the same way a build of it does, and that differs
// by module: one inside the workspace's use-set must be built WITH the workspace,
// because that is where its dependency on a sibling resolves to the local
// working copy, and one outside it must be built with the workspace switched
// off, because otherwise the toolchain refuses it as being outside the workspace
// module's root. Reading the use-set is how that rule is derived rather than
// restated as a list of module names that would have to be kept in step.
func workspaceUseSet(root string) (map[string]bool, error) {
	use := map[string]bool{}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return use, nil
		}
		return nil, err
	}
	cmd := exec.Command("go", "work", "edit", "-json")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading the workspace use-set: %w", err)
	}
	var parsed struct {
		Use []struct {
			DiskPath string
		}
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("reading the workspace use-set: %w", err)
	}
	for _, u := range parsed.Use {
		p := u.DiskPath
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		use[filepath.Clean(p)] = true
	}
	return use, nil
}

// listedPackage is one entry of the module's test-package listing.
type listedPackage struct {
	ImportPath string
	Dir        string
	Root       string
}

func censusModule(absRoot, module string, workspaceUse map[string]bool, logRoot string, o runOpts) modResult {
	dir := module
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(absRoot, dir)
	}
	dir = filepath.Clean(dir)

	res := modResult{Dir: dir, Workspace: workspaceUse[dir]}
	if rel, err := filepath.Rel(absRoot, dir); err == nil {
		res.Rel = rel
	} else {
		res.Rel = dir
	}

	pkgs, err := listTestPackages(dir, res.Workspace)
	if err != nil {
		res.Err = err
		return res
	}
	if len(pkgs) == 0 {
		return res
	}
	res.Root = pkgs[0].Root
	if res.Root == "" {
		// The toolchain's own rule is that a package with no root has no cache
		// at all, so every read is outside a root that does not exist. That is a
		// different condition from the one being counted here, and reporting it
		// as a count would be wrong in both directions.
		res.Err = fmt.Errorf("the toolchain reports no module root for %s, so in-module cannot be decided", pkgs[0].ImportPath)
		return res
	}

	// Sequential, and not because ordering matters: this is a file read and a
	// path walk per package now, where it used to be a test binary per package.
	// The fan-out that used to be here was bounding memory across concurrent test
	// processes, and there are none left to bound.
	res.Packages = make([]pkgResult, len(pkgs))
	for i, p := range pkgs {
		res.Packages[i] = censusPackage(p, logRoot, o)
	}
	return res
}

// testLogPath is where the log for one test package is expected to be found. It
// is this side of a correspondence the caller's `go test -exec` shim holds the
// other half of, so it is the one home of the derivation here and the contract is
// written out rather than left to be inferred from the shim.
//
// THE CONTRACT. The toolchain runs a test binary with its working directory set
// to the package's own source directory, so the shim mirrors that directory's
// PHYSICAL path — every symbolic link resolved — beneath the log root, and writes
// "testlog.txt" in it. Three properties of that choice are load-bearing:
//
//   - it keys on the directory and never on the binary's name, because the
//     toolchain names every test binary "<pkg>.test" and those collide across a
//     real tree, which would leave one package's log overwritten by another's;
//   - it keys on the PHYSICAL path, because a process whose working directory its
//     parent set inherits the parent's own idea of the logical path, so the
//     logical form would name the wrong directory entirely;
//   - 🔴 it MIRRORS the path rather than flattening it into one filename.
//     Flattening by separator substitution is not injective — "/a/b_c" and
//     "/a_b/c" collapse to the same name — so two packages could share one log,
//     each truncating the other, and this census would then read one package's
//     inputs as the other's. That is a silently WRONG measurement rather than a
//     missing one, and it is the only failure here that no refusal would catch.
//     A mirrored path cannot collide because it is still a path.
//
// The derivation is therefore plain concatenation on both sides, with no encoding
// for either to get wrong. The package directory the toolchain reports is
// resolved the same way here; a path that cannot be resolved is used as given, so
// a mismatch surfaces as a missing log, which is a refusal, rather than as a
// silent zero.
func testLogPath(logRoot, pkgDir string) string {
	physical := pkgDir
	if resolved, err := filepath.EvalSymlinks(pkgDir); err == nil {
		physical = resolved
	}
	return filepath.Join(logRoot, physical, "testlog.txt")
}

func listTestPackages(dir string, workspace bool) ([]listedPackage, error) {
	cmd := exec.Command("go", "list", "-f",
		"{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}\t{{.Dir}}\t{{.Root}}{{end}}", "./...")
	cmd.Dir = dir
	cmd.Env = goEnv(workspace)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing test packages in %s: %w: %s", dir, err, stderrOf(err))
	}
	var pkgs []listedPackage
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			return nil, fmt.Errorf("unexpected package listing line %q in %s", line, dir)
		}
		pkgs = append(pkgs, listedPackage{ImportPath: f[0], Dir: f[1], Root: f[2]})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ImportPath < pkgs[j].ImportPath })
	return pkgs, nil
}

// censusPackage classifies what one test package read, from the log the caller's
// test leg collected for it. It runs nothing.
//
// 🔴 AN ABSENT LOG IS A REFUSAL AND NEVER AN EMPTY RESULT. This is the one
// failure the whole command exists not to make, and the asymmetry that makes it
// dangerous is worth spelling out: a package that ran and read nothing writes a
// log holding the toolchain's marker line and no entries, while a package whose
// log is missing was not measured at all. Reading the second as the first would
// report a confident zero for a package nobody looked at — and because the gate
// compares SETS, a carrier silently reporting zero reads as a carrier that
// stopped carrying, which the report calls a stale record and which moves no
// exit code. A false green, in other words, in the exact direction the recorded
// set is asymmetric.
//
// So a missing log becomes this package's Err, which is the refusal path already
// in place: unmeasured collects it, report fails on a non-empty result, and the
// recording path refuses to record from one.
//
// The two states a log can be missing in are named separately, because their
// remedies differ. Absent means the collecting test leg never ran this package —
// a narrowed selection, an aborted leg, or a shim keying its logs by some other
// name. Present but not a test log means something overwrote it, which classify
// names through its marker-line check.
func censusPackage(p listedPackage, logRoot string, o runOpts) pkgResult {
	res := pkgResult{ImportPath: p.ImportPath, Dir: p.Dir}

	logPath := testLogPath(logRoot, p.Dir)
	if _, err := os.Stat(logPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			res.Err = fmt.Errorf("no test log was collected for %s at %s, so it was NOT measured: either its tests did not run under the collecting test leg, or that leg keyed its log by another name", p.ImportPath, logPath)
			return res
		}
		res.Err = fmt.Errorf("the test log collected for %s is not readable at %s: %w", p.ImportPath, logPath, err)
		return res
	}

	c, err := classify(logPath, p.Dir, p.Root, o.treeRoot)
	if err != nil {
		res.Err = fmt.Errorf("classifying %s: %w", p.ImportPath, err)
		return res
	}
	res.SharedTree = c.SharedTree
	res.OutsideTree = c.OutsideTree
	res.Inputs = c.Inputs
	return res
}

func goEnv(workspace bool) []string {
	env := os.Environ()
	if workspace {
		// The caller's GOWORK must not leak in: a module inside the use-set is
		// resolved with the workspace whatever the ambient setting says. An EMPTY
		// value is the way to say that — it means "discover the workspace file
		// normally", not "off", which is a distinct value spelled `off`.
		return append(env, "GOWORK=")
	}
	return append(env, "GOWORK=off")
}

func stderrOf(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return ""
}

// enrolment reads the caller's cache-defeat list into a set, and says separately
// whether the caller supplied one at all.
//
// The second answer is not a convenience. An empty list means nobody told the
// census, which is a different statement from "no module is enrolled", and
// reporting the first as though it were the second would vouch for a condition
// nobody checked.
func enrolment(cacheDefeated string) (map[string]bool, bool) {
	defeated := map[string]bool{}
	for _, m := range strings.Split(cacheDefeated, ",") {
		if m = strings.TrimSpace(m); m != "" {
			defeated[m] = true
		}
	}
	return defeated, strings.TrimSpace(cacheDefeated) != ""
}

// unmeasured lists the modules and packages the census could not measure, one
// description each.
//
// It has two callers — the report, which fails on a non-empty result, and the
// write path, which refuses to record from one — and the derivation lives here so
// the two cannot come apart. A set recorded from a partial census would hold no
// carrier for the modules the run failed to read, so every one of them would read
// as newly grown on the next run, and the refusal this run owed would have been
// converted into a recorded fact.
func unmeasured(results []modResult) []string {
	var problems []string
	for _, m := range results {
		if m.Err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", m.Rel, m.Err))
			continue
		}
		for _, p := range m.Packages {
			if p.Err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", m.Rel, p.Err))
			}
		}
	}
	return problems
}

// reachComponents maps each shared input to the first path component of its path
// relative to treeRoot, de-duplicated and sorted.
//
// The component answers WHICH CORPUS a package reached — a sibling module, the
// catalog, the shared schemas — where a file path answers only which file, and
// the corpus is what a reader needs to judge whether a new carrier is expected.
//
// classify guarantees every entry lies inside the tree, so a path that does not
// is a contradiction of that contract and is kept WHOLE rather than reduced,
// because a visible oddity in the record beats a silent hole in it. Note that
// relativising an outside path SUCCEEDS and yields a leading "..", so the escape
// has to be tested for: reducing that to its first component would record a bare
// ".." and lose the only information the entry had.
func reachComponents(shared []string, treeRoot string) []string {
	seen := map[string]bool{}
	for _, p := range shared {
		c := p
		if rel, err := filepath.Rel(treeRoot, p); err == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			// A read of the tree root directory itself relativises to ".", which is
			// a legitimate answer and is kept as one.
			c = rel
			if i := strings.IndexByte(c, filepath.Separator); i >= 0 {
				c = c[:i]
			}
		}
		seen[c] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// measuredCarriers is the module's carrying test packages as this run measured
// them, ordered by import path.
func measuredCarriers(m modResult, treeRoot string) []expectedCarrier {
	var out []expectedCarrier
	for _, p := range m.Packages {
		if len(p.SharedTree) == 0 {
			continue
		}
		out = append(out, expectedCarrier{
			ImportPath: p.ImportPath,
			Reach:      reachComponents(p.SharedTree, treeRoot),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ImportPath < out[j].ImportPath })
	return out
}

// newExpectation is the record this run's measurement would write.
//
// It is the ONE place the record's ordering is decided — modules by path,
// carriers by import path, reach components lexically — and everything
// downstream reads that order rather than imposing its own, so there is no second
// sort to fall out of step with this one.
//
// measuredOn is passed in rather than read from the clock here, so a caller that
// needs reproducible bytes can have them.
func newExpectation(treeRoot string, results []modResult, defeated map[string]bool, measuredOn string) testInputExpectation {
	out := testInputExpectation{Measured: measuredOn}
	for _, m := range results {
		if m.Err != nil {
			continue
		}
		carriers := measuredCarriers(m, treeRoot)
		if len(carriers) == 0 {
			// Only a module that HAS a carrier is recorded. That is what makes a
			// module's absence mean "expected to carry none", so a newly added
			// module needs no bookkeeping until one of its packages reads a shared
			// input.
			continue
		}
		out.Modules = append(out.Modules, expectedModule{
			Rel:           m.Rel,
			CacheDefeated: defeated[m.Rel],
			Carriers:      carriers,
		})
	}
	sort.Slice(out.Modules, func(i, j int) bool { return out.Modules[i].Rel < out.Modules[j].Rel })
	return out
}

// carrierIndex is the record as a per-module set of carrying import paths, for
// asking whether a given package is held.
func carrierIndex(e testInputExpectation) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(e.Modules))
	for _, m := range e.Modules {
		held := make(map[string]bool, len(m.Carriers))
		for _, c := range m.Carriers {
			held[c.ImportPath] = true
		}
		out[m.Rel] = held
	}
	return out
}

// carrierTotal is how many carrying packages the record holds across every
// module.
func carrierTotal(e testInputExpectation) int {
	n := 0
	for _, m := range e.Modules {
		n += len(m.Carriers)
	}
	return n
}

// reportStale names the recorded carriers a module no longer measures.
//
// Stale is reported and never fatal: a package that stopped reading out of its
// module is the exposure SHRINKING, and failing on it would leave no way to keep
// the record honest. It is still worth saying, because a record left wider than
// the truth quietly buys room for the next addition.
//
// 🔴 A recorded carrier the run could not MEASURE is not stale, and separating
// the two is the whole reason unmeasured is a parameter here. Nothing looked at
// such a package, so calling it stale would report a shrink that was never
// observed — and it would invite dropping the record entry, which is the one
// edit that converts this run's owed refusal into a recorded fact. The same
// reasoning is applied to a whole module the census could not read; this is that
// rule at package granularity, where the missing-log failure lands.
func reportStale(w io.Writer, rec expectedModule, measured, unmeasured map[string]bool) {
	var held []string
	for _, c := range rec.Carriers {
		if unmeasured[c.ImportPath] {
			held = append(held, c.ImportPath)
		}
	}
	if len(held) > 0 {
		fmt.Fprintf(w, "      %d recorded carrier(s) were NOT measured by this run, so nothing here says they stopped carrying: %s\n",
			len(held), strings.Join(held, ", "))
	}
	var gone []string
	for _, c := range rec.Carriers {
		if unmeasured[c.ImportPath] {
			continue
		}
		if !measured[c.ImportPath] {
			gone = append(gone, c.ImportPath)
		}
	}
	if len(gone) == 0 {
		return
	}
	fmt.Fprintf(w, "      the recorded set is now stale — %d carrier(s) no longer read a shared input: %s\n",
		len(gone), strings.Join(gone, ", "))
	fmt.Fprintf(w, "      re-record it in %s; a record left wider than the truth buys room for the next addition\n",
		expectationSite)
}

// report prints the census and returns the gate's verdict: an error when a test
// package reads a shared input the recorded set does not hold, or when part of
// the scope could not be measured. reportOnly prints every finding and returns
// nil.
func report(w io.Writer, absRoot string, results []modResult, expect testInputExpectation, reportOnly bool, cacheDefeated string) error {
	defeated, enrolmentSupplied := enrolment(cacheDefeated)
	recorded := make(map[string]expectedModule, len(expect.Modules))
	for _, m := range expect.Modules {
		recorded[m.Rel] = m
	}

	// The record as a per-module lookup, from the one helper that derives it — the
	// same one the recording path asks. Rebuilding it inline here would be a second
	// answer to "does the record hold this package", which is the comparison the
	// whole gate turns on.
	held := carrierIndex(expect)

	problems := unmeasured(results)

	var (
		added        []string
		totalPkgs    int
		totalShared  int
		totalOutside int
	)

	// Modules carrying no test package contribute nothing and are named on one
	// line rather than one line each: they belong in the report because the scope
	// has to be legible, but at one line apiece they bury the modules that count.
	var empty []string

	// Which modules the census reached at all, and which of those it actually
	// read. The two are different: a module it tried and could not measure must
	// NOT have its recorded carriers called stale, because this run looked at
	// nothing and calling them stale would invite dropping them.
	inScope := make(map[string]bool, len(results))
	for _, m := range results {
		inScope[m.Rel] = true
	}

	if expect.Measured != "" {
		fmt.Fprintf(w, "test packages reading inputs from outside their module root — census over %d module(s) under %s, against the carrier set measured %s\n",
			len(results), absRoot, expect.Measured)
	} else {
		fmt.Fprintf(w, "test packages reading inputs from outside their module root — census over %d module(s) under %s; NO carrier set is recorded, so every carrier below reads as new\n",
			len(results), absRoot)
	}

	for _, m := range results {
		if m.Err != nil {
			fmt.Fprintf(w, "  %-24s NOT MEASURED\n", m.Rel)
			continue
		}
		rec := recorded[m.Rel]
		carriers := measuredCarriers(m, absRoot)
		carries := make(map[string]bool, len(carriers))
		reach := make(map[string][]string, len(carriers))
		for _, c := range carriers {
			carries[c.ImportPath] = true
			reach[c.ImportPath] = c.Reach
		}
		// The packages this run could not read, kept apart from the ones it read
		// and found clean. Both contribute zero carriers, and conflating them is
		// what would let an unmeasured carrier read as one that stopped carrying.
		unread := make(map[string]bool)
		for _, p := range m.Packages {
			if p.Err != nil {
				unread[p.ImportPath] = true
			}
		}

		if len(m.Packages) == 0 {
			empty = append(empty, m.Rel)
			// Measured, and found to carry nothing, so anything recorded for it is
			// stale. Said rather than skipped: a module that lost its last test
			// package is exactly the case the one-line shortcut above would hide.
			reportStale(w, rec, carries, unread)
			continue
		}

		outside := m.outsideTreeCount()
		totalPkgs += len(m.Packages)
		totalShared += len(carriers)
		totalOutside += outside
		fmt.Fprintf(w, "  %-24s %d of %d test packages read shared inputs from elsewhere in the tree (%d recorded)\n",
			m.Rel, len(carriers), len(m.Packages), len(rec.Carriers))
		// Immediately under the count, because without it that count reads as a
		// measurement of every package in the module. A run that could not read
		// part of its scope refuses below; this is what keeps the line above from
		// being quoted as a finding in the meantime.
		if len(unread) > 0 {
			fmt.Fprintf(w, "      %d of those %d packages could NOT be read, so the count above is over the rest and certifies nothing for them\n",
				len(unread), len(m.Packages))
		}
		for _, p := range m.Packages {
			if len(p.SharedTree) == 0 {
				continue
			}
			// The second figure is the reads this run observed, and it moves with
			// how warm the caches were — unlike the first, which is a property of
			// the tree. It is printed because "all 61 of its reads are shared"
			// and "one stray read among thousands" are very different findings.
			fmt.Fprintf(w, "      %s — %d shared, of %d reads observed; reaches %s\n",
				p.ImportPath, len(p.SharedTree), p.Inputs, strings.Join(reach[p.ImportPath], ", "))
		}

		for _, c := range carriers {
			if held[m.Rel][c.ImportPath] {
				continue
			}
			added = append(added, fmt.Sprintf("%s: %s — reaches %s",
				m.Rel, c.ImportPath, strings.Join(c.Reach, ", ")))
		}
		reportStale(w, rec, carries, unread)

		if len(carriers) > 0 {
			switch {
			case !enrolmentSupplied:
				fmt.Fprintf(w, "      whether this module already runs with caching defeated was not supplied to the census\n")
			case defeated[m.Rel]:
				fmt.Fprintf(w, "      this module runs with caching defeated, so those inputs cannot serve a stale result\n")
			default:
				fmt.Fprintf(w, "      this module does NOT run with caching defeated, so those inputs can serve a stale pass\n")
			}
		}
		// The record's copy of the enrolment describes the condition its
		// measurement was taken under; it is never a second authority on the
		// enrolment, which belongs to the caller that supplies it. So a divergence
		// says the record is stale on that point and moves no exit code — and it is
		// only comparable at all when a caller supplied an enrolment.
		if enrolmentSupplied && len(rec.Carriers) > 0 && rec.CacheDefeated != defeated[m.Rel] {
			was := "does NOT"
			if rec.CacheDefeated {
				was = "does"
			}
			fmt.Fprintf(w, "      the recorded set says this module %s run with caching defeated, which is not what the caller reports now — the record is stale on that point, and nothing about it moves the exit code\n", was)
		}
		fmt.Fprintf(w, "      %d of %d also read something outside the tree entirely — not counted above\n",
			outside, len(m.Packages))
	}

	// A recorded module this census was not given. Nothing compared it, so its
	// carriers read as stale — and that must not fail: a module leaving the scope
	// is not new exposure. A module the census was given and could not measure is
	// deliberately absent from this pass; it is a problem below instead.
	for _, rm := range expect.Modules {
		if inScope[rm.Rel] {
			continue
		}
		var names []string
		for _, c := range rm.Carriers {
			names = append(names, c.ImportPath)
		}
		fmt.Fprintf(w, "  %-24s recorded but outside this census's scope, so its %d recorded carrier(s) read as stale: %s\n",
			rm.Rel, len(rm.Carriers), strings.Join(names, ", "))
	}

	if len(empty) > 0 {
		fmt.Fprintf(w, "  %d module(s) carry no test package and contribute nothing: %s\n",
			len(empty), strings.Join(empty, ", "))
	}
	fmt.Fprintf(w, "  total: %d of %d measured test packages read a shared input from elsewhere in the tree; %d read something outside the tree\n",
		totalShared, totalPkgs, totalOutside)
	fmt.Fprintf(w, "  the gate watches the first number only, because a path outside the tree is a directory the run\n")
	fmt.Fprintf(w, "  itself created, the toolchain, the system, an executable lookup, or a path a negative test points\n")
	fmt.Fprintf(w, "  at nothing — none of which is a committed input that could have changed since the last run\n")

	if len(problems) > 0 {
		fmt.Fprintf(w, "\n%d package(s) or module(s) could not be measured, so this census certifies nothing:\n", len(problems))
		for _, p := range problems {
			fmt.Fprintf(w, "  %s\n", p)
		}
	}
	if len(added) > 0 {
		fmt.Fprintf(w, "\nthe carrier set grew — %d test package(s) read a shared input the recorded set does not hold:\n", len(added))
		for _, a := range added {
			fmt.Fprintf(w, "  %s\n", a)
		}
		fmt.Fprintf(w, "Either move the input inside the module root, or — if the read is deliberate — record the\n")
		fmt.Fprintf(w, "measured set into %s by re-running the\n", expectationSite)
		fmt.Fprintf(w, "authoritative whole-workspace battery with --write-test-input-expectation, and make sure the\n")
		fmt.Fprintf(w, "module runs with caching defeated.\n")
	}

	if reportOnly {
		if len(problems) > 0 || len(added) > 0 {
			fmt.Fprintf(w, "\nthe findings above did not move the exit code\n")
		}
		return nil
	}
	switch {
	case len(problems) > 0 && len(added) > 0:
		return errors.New("the census could not measure part of its scope, and the carrier set grew where it could")
	case len(problems) > 0:
		return errors.New("the census could not measure part of its scope")
	case len(added) > 0:
		return errors.New("test packages read shared inputs from elsewhere in the tree that the recorded set does not hold")
	}
	return nil
}

// writeMeasured rewrites the recorded carrier set from this run's measurement and
// says what moved, so an operator reads what they have just accepted.
//
// It prints the census UNGATED, because recording accepts the measurement rather
// than judging it, and a growth is exactly what an operator recording has decided
// to accept.
//
// The ONE exception is a census that could not read part of its scope. Recording
// from that would turn this run's owed refusal into a recorded fact, for the reason
// unmeasured states — so the run reverts to GATING: the same report, and its
// verdict as the verdict. That keeps the condition in one place, and it means the
// operator is told WHICH part could not be measured rather than only that
// something was not.
func writeMeasured(w io.Writer, absRoot string, results []modResult, previous testInputExpectation, o runOpts) error {
	if len(unmeasured(results)) > 0 {
		err := report(w, absRoot, results, previous, false, o.cacheDefeated)
		if err == nil {
			// Defensive, and it cannot fire: report and this branch read the same
			// derivation, so a non-empty one fails there too. It is here because a
			// silent success would record nothing and exit 0, and that is the one
			// outcome a refusal must never be mistaken for.
			err = errors.New("the census could not measure part of its scope")
		}
		return fmt.Errorf("%w; nothing was recorded, because a set taken from a partial census would read the modules it missed as dropped", err)
	}

	if err := report(w, absRoot, results, previous, true, o.cacheDefeated); err != nil {
		return err
	}

	defeated, _ := enrolment(o.cacheDefeated)
	// The date the measurement was taken, which is the record's own scope in time.
	measured := newExpectation(absRoot, results, defeated, time.Now().UTC().Format("2006-01-02"))

	// One home for the path: the constant, joined onto the resolved tree root.
	path := filepath.Join(absRoot, filepath.FromSlash(expectationSite))
	if err := writeExpectationFile(path, measured); err != nil {
		return err
	}

	before, now := carrierIndex(previous), carrierIndex(measured)
	var gained, dropped []string
	for _, m := range measured.Modules {
		for _, c := range m.Carriers {
			if !before[m.Rel][c.ImportPath] {
				gained = append(gained, fmt.Sprintf("%s: %s — reaches %s",
					m.Rel, c.ImportPath, strings.Join(c.Reach, ", ")))
			}
		}
	}
	for _, m := range previous.Modules {
		for _, c := range m.Carriers {
			if !now[m.Rel][c.ImportPath] {
				dropped = append(dropped, fmt.Sprintf("%s: %s", m.Rel, c.ImportPath))
			}
		}
	}

	fmt.Fprintf(w, "\nrecorded the measured carrier set in %s, measured %s\n", expectationSite, measured.Measured)
	fmt.Fprintf(w, "  %d module(s) carrying %d test package(s) in total\n",
		len(measured.Modules), carrierTotal(measured))
	if len(gained) == 0 && len(dropped) == 0 {
		fmt.Fprintf(w, "  nothing moved: the recorded set already matched what this run measured\n")
	}
	for _, l := range gained {
		fmt.Fprintf(w, "  added:   %s\n", l)
	}
	for _, l := range dropped {
		fmt.Fprintf(w, "  dropped: %s\n", l)
	}
	fmt.Fprintf(w, "  this run recorded the measurement rather than judging it, so nothing here moved the exit code\n")
	return nil
}

// writeExpectationFile writes the record as Go source at path.
//
// The output goes through go/format, which does two jobs at once: the committed
// bytes are gofmt-clean without this emitter reproducing gofmt's rules, and an
// emission that would not parse fails HERE rather than at the next build. So a
// formatting failure is an error and never a warning.
//
// Ordering is not imposed here. newExpectation is the record's one ordering home
// and this walks its slices in the order it produced them, so there is no second
// sort to fall out of step with the first.
func writeExpectationFile(path string, e testInputExpectation) error {
	var b strings.Builder
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n")
	b.WriteString("// Copyright 2026 Cognitio S.r.l.\n")
	b.WriteString("\n")
	b.WriteString("// Code generated by internal/buildtools/testinputcensus from a measurement of\n")
	b.WriteString("// this tree. DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// The census writes this file from its own -write-expectation mode. Regenerate it\n")
	b.WriteString("// by re-running the authoritative whole-workspace battery in recording mode, from\n")
	b.WriteString("// the umbrella checkout:\n")
	b.WriteString("//\n")
	b.WriteString("//\t./scripts/build.sh --test-full --write-test-input-expectation\n")
	b.WriteString("//\n")
	b.WriteString("// There is deliberately no `go:generate` directive naming this file, and the\n")
	b.WriteString("// omission is load-bearing rather than an oversight. Regenerating means re-running\n")
	b.WriteString("// every Go test package in the tree with caching defeated, so a blanket recursive\n")
	b.WriteString("// generate would silently pay a whole-suite execution for a file nobody asked to\n")
	b.WriteString("// refresh. The command above is the only way in, and it rides the run that already\n")
	b.WriteString("// measures under exactly these conditions.\n")
	b.WriteString("//\n")
	b.WriteString("// What is gated here and what is merely recorded. The carrier PACKAGE SET is the\n")
	b.WriteString("// gated fact: which packages read a shared input is a property of the tree, so a\n")
	b.WriteString("// package joining the set fails the census. Each carrier's reach components are\n")
	b.WriteString("// CONTEXT measured on the recording run — how far that run's reads happened to\n")
	b.WriteString("// spread — and are never compared, because they move with what a run touched\n")
	b.WriteString("// rather than with the tree.\n")
	b.WriteString("\n")
	b.WriteString("package main\n")
	b.WriteString("\n")
	b.WriteString("// expectation is the recorded carrier set. What the two directions of the\n")
	b.WriteString("// comparison mean, and what every figure here was measured over, is on\n")
	b.WriteString("// testInputExpectation.\n")
	b.WriteString("var expectation = testInputExpectation{\n")
	fmt.Fprintf(&b, "\tMeasured: %q,\n", e.Measured)
	if len(e.Modules) > 0 {
		b.WriteString("\tModules: []expectedModule{\n")
		for _, m := range e.Modules {
			b.WriteString("\t\t{\n")
			fmt.Fprintf(&b, "\t\t\tRel:           %q,\n", m.Rel)
			fmt.Fprintf(&b, "\t\t\tCacheDefeated: %t,\n", m.CacheDefeated)
			b.WriteString("\t\t\tCarriers: []expectedCarrier{\n")
			for _, c := range m.Carriers {
				fmt.Fprintf(&b, "\t\t\t\t{ImportPath: %q, Reach: []string{", c.ImportPath)
				for i, r := range c.Reach {
					if i > 0 {
						b.WriteString(", ")
					}
					fmt.Fprintf(&b, "%q", r)
				}
				b.WriteString("}},\n")
			}
			b.WriteString("\t\t\t},\n")
			b.WriteString("\t\t},\n")
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("formatting the generated record: %w", err)
	}
	return os.WriteFile(path, formatted, 0o644)
}
