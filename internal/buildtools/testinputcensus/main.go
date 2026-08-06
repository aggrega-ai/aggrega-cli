// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command testinputcensus counts the test packages in this workspace that read
// input files from outside their own module root, and fails when that count
// grows beyond the expectation recorded in this file.
//
// # Why the count is worth watching
//
// Go's test cache keys a package's result on the files the test binary actually
// read — but it deliberately leaves out any read that lands outside the module
// root, because such a file is not part of the module's own source. A test whose
// inputs live above its module therefore has those inputs in no cache key at
// all: change them and the cached pass is served again, unchanged, for bytes
// nothing looked at. The count of such packages is the size of that exposure.
// The remedy in use is to run the affected modules with caching defeated, which
// is a per-module decision, so what this command watches is whether the
// population needing that decision is growing.
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
// So this command asks the toolchain instead. Each test package runs once with
// caching defeated and with -test.testlogfile, the file the toolchain itself
// consumes to decide whether a cached result is still valid. Every file the
// binary opened or stat'd is in there, whether the path was written as a
// literal, concatenated, computed, or read from somewhere else entirely. The
// classification of each entry — see classify — is transcribed from the
// toolchain's own reading of that file, so the census has no criterion of its
// own to get wrong.
//
// # What it costs, and what it therefore cannot be
//
// The measurement requires the tests to run, and a cached package does not run
// its binary and so writes no log at all. A census over a cached suite would
// measure nothing and report a confident zero. So every package runs with the
// cache defeated, which makes this a whole-suite execution and not a cheap
// check: it belongs in the authoritative, deliberately-slow run, never in a
// per-change gate.
//
// # Scope, and what it deliberately leaves out
//
// It censuses exactly the modules it is given, and prints them, so a report can
// never be mistaken for wider coverage than it has. It measures Go test packages
// only: suites in other languages are not governed by Go's test cache, so they
// are neither exposed to this failure nor covered by this count.
//
// Of the inputs that land outside a module root, only those still inside the
// repository tree are counted and gated — a committed file in a sibling module or
// at the tree root, read across a module boundary. Inputs outside the tree
// altogether are reported on every run but not gated, and the reasoning for that
// split is on classified, where the criterion lives.
//
// The count is a floor, for one reason worth stating plainly: the log is written
// by the test binary, so a file read by a child process the test spawns appears
// nowhere in it. A test that shells out and lets the child do the reading is
// invisible here, and no amount of care in this command would change that.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// expected records, per module path relative to the census root, how many of
// that module's test packages read at least one shared input from elsewhere in
// the repository tree. A module absent from this table is expected to have none,
// so a newly added module needs no bookkeeping until it actually reads one.
//
// The gate fails when a measured count is HIGHER than the entry here, because
// growth going unobserved is the thing it exists to prevent. A measured count
// LOWER than the entry is reported and does not fail — the entry is then stale
// and lowering it is the follow-up, since an expectation left above the truth
// quietly buys room for the next addition.
//
// Each figure is a measurement rather than a target, so it carries what it was
// measured over. Criterion: a test package counts when its test log records at
// least one open or stat of a path that resolves outside its own module root and
// inside the repository tree. Measured 2026-08-06 on go1.26.0 with caching
// defeated, over the four Go modules in the tree that carry test packages —
// 96 test packages in total, of which 15 count.
var expected = map[string]int{
	// The two shared-payload parity suites, whose input corpus is the
	// golden-vector tree at the repository root above each module — read by the
	// Go, Rust and TypeScript sides alike, which is why it cannot live inside any
	// one of them. Both modules run with caching defeated for exactly this reason.
	"aggrega-abi/go": 1,
	"aggrega-pdk/go": 1,
	// aggrega-cli reads across module boundaries from several directions: the
	// same shared golden corpus as the two above, the guest sources of the
	// sibling plugin toolkits, and the catalog content it compiles against. This
	// module does NOT currently run with caching defeated, which the census says
	// on every run.
	"aggrega-cli": 13,
}

// expectationSite is where a reader is sent when a count has moved. It is
// written out rather than derived so the message survives being read in a log
// far from the source.
const expectationSite = "aggrega-cli/internal/buildtools/testinputcensus/main.go"

func main() {
	root := flag.String("root", "", "workspace root the module paths are reported relative to (required)")
	// Both parallelism defaults are the safe floor rather than a fast one. Each
	// test package is its own process holding its own copy of whatever the tests
	// load, so the two axes MULTIPLY into memory, and running a whole suite at
	// the machine's core count has taken this container down. A caller that
	// budgets memory passes its own numbers; a caller that forgets gets a slow
	// census rather than an unsafe one.
	jobs := flag.Int("jobs", 2, "how many test packages to run at once")
	parallel := flag.Int("parallel", 2, "how many tests each package may run at once (go test -parallel)")
	timeout := flag.String("timeout", "30m", "per-package bound that tells a stuck package from a slow one; deliberately above the suite's own bound, because a census runs a package at lower in-package parallelism and the same package therefore takes longer here")
	reportOnly := flag.Bool("report-only", false, "print the census and exit 0 even when a count has grown")
	keepLogs := flag.String("keep-logs", "", "directory to write the collected test logs into and leave behind, for inspection")
	cacheDefeated := flag.String("cache-defeated", "", "comma-separated module paths the caller already runs with caching defeated; when empty, the census says the enrolment was not supplied rather than implying none")
	var modules stringList
	flag.Var(&modules, "module", "a Go module directory to census; repeatable, and the census covers exactly what it is given")
	flag.Parse()

	opts := runOpts{
		root:          *root,
		modules:       modules,
		jobs:          *jobs,
		parallel:      *parallel,
		timeout:       *timeout,
		reportOnly:    *reportOnly,
		keepLogs:      *keepLogs,
		cacheDefeated: *cacheDefeated,
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
	treeRoot      string
	modules       []string
	jobs          int
	parallel      int
	timeout       string
	reportOnly    bool
	keepLogs      string
	cacheDefeated string
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
	if o.jobs < 1 {
		return fmt.Errorf("-jobs must be at least 1, got %d", o.jobs)
	}
	if o.parallel < 1 {
		return fmt.Errorf("-parallel must be at least 1, got %d", o.parallel)
	}

	logRoot := o.keepLogs
	if logRoot == "" {
		logRoot, err = os.MkdirTemp("", "testinputcensus-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(logRoot)
	} else if err := os.MkdirAll(logRoot, 0o755); err != nil {
		return err
	}

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

	return report(os.Stdout, absRoot, results, expected, o.reportOnly, o.cacheDefeated)
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

	modLogDir := filepath.Join(logRoot, strings.ReplaceAll(res.Rel, string(filepath.Separator), "_"))
	if err := os.MkdirAll(modLogDir, 0o755); err != nil {
		res.Err = err
		return res
	}

	res.Packages = make([]pkgResult, len(pkgs))
	sem := make(chan struct{}, o.jobs)
	var wg sync.WaitGroup
	for i, p := range pkgs {
		wg.Add(1)
		go func(i int, p listedPackage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res.Packages[i] = censusPackage(dir, res.Workspace, p, filepath.Join(modLogDir, strconv.Itoa(i)+".log"), o)
		}(i, p)
	}
	wg.Wait()
	return res
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

// censusPackage runs one test package and classifies what it read.
//
// -count=1 is not optional. A cached package does not execute its binary and so
// writes no log, and an absent log would be indistinguishable from a package
// that read nothing. It has a second effect worth knowing: the toolchain treats
// an uncacheable test flag as a reason to disable caching for the run, so it
// passes no test-log path of its own and stores no result — which is why each
// package is invoked separately here. Every binary in one invocation would be
// handed the SAME log path, and each opens that path with truncation, so all but
// one package's inputs would be destroyed before they could be read.
func censusPackage(moduleDir string, workspace bool, p listedPackage, logPath string, o runOpts) pkgResult {
	res := pkgResult{ImportPath: p.ImportPath, Dir: p.Dir}

	// The test-binary flag goes AFTER the package argument. Put it before and the
	// go command stops parsing its own flags at the first thing it does not
	// recognise, swallows the package argument as an argument to the test binary,
	// and tests the current directory instead — which fails in a way that looks
	// like the flag is unsupported.
	args := []string{"test", p.ImportPath, "-count=1", "-parallel", strconv.Itoa(o.parallel)}
	if o.timeout != "" {
		args = append(args, "-timeout", o.timeout)
	}
	args = append(args, "-test.testlogfile="+logPath)
	cmd := exec.Command("go", args...)
	cmd.Dir = moduleDir
	cmd.Env = goEnv(workspace)
	out, err := cmd.CombinedOutput()
	if err != nil {
		res.Err = fmt.Errorf("running %s: %w\n%s", p.ImportPath, err, strings.TrimSpace(string(out)))
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

func report(w io.Writer, absRoot string, results []modResult, expect map[string]int, reportOnly bool, cacheDefeated string) error {
	defeated := map[string]bool{}
	enrolmentSupplied := strings.TrimSpace(cacheDefeated) != ""
	for _, m := range strings.Split(cacheDefeated, ",") {
		if m = strings.TrimSpace(m); m != "" {
			defeated[m] = true
		}
	}

	var (
		problems     []string
		grown        []string
		totalPkgs    int
		totalShared  int
		totalOutside int
	)

	// Modules carrying no test package contribute nothing and are named on one
	// line rather than one line each: they belong in the report because the scope
	// has to be legible, but at one line apiece they bury the modules that count.
	var empty []string

	fmt.Fprintf(w, "test packages reading inputs from outside their module root — census over %d module(s) under %s\n",
		len(results), absRoot)
	for _, m := range results {
		if m.Err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", m.Rel, m.Err))
			fmt.Fprintf(w, "  %-24s NOT MEASURED\n", m.Rel)
			continue
		}
		if len(m.Packages) == 0 {
			empty = append(empty, m.Rel)
			continue
		}
		for _, p := range m.Packages {
			if p.Err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", m.Rel, p.Err))
			}
		}
		count := m.sharedTreeCount()
		outside := m.outsideTreeCount()
		totalPkgs += len(m.Packages)
		totalShared += count
		totalOutside += outside
		want := expect[m.Rel]
		fmt.Fprintf(w, "  %-24s %d of %d test packages read shared inputs from elsewhere in the tree (expected %d)\n",
			m.Rel, count, len(m.Packages), want)
		for _, p := range m.Packages {
			if len(p.SharedTree) == 0 {
				continue
			}
			// The second figure is the reads this run observed, and it moves with
			// how warm the caches were — unlike the first, which is a property of
			// the tree. It is printed because "all 61 of its reads are shared"
			// and "one stray read among thousands" are very different findings.
			fmt.Fprintf(w, "      %s — %d shared, of %d reads observed; lowest: %s\n",
				p.ImportPath, len(p.SharedTree), p.Inputs, p.SharedTree[0])
		}
		if count > want {
			grown = append(grown, fmt.Sprintf("%s: %d, expected %d", m.Rel, count, want))
		} else if count < want {
			fmt.Fprintf(w, "      the expectation of %d is now stale — lower it in %s\n", want, expectationSite)
		}
		if count > 0 {
			switch {
			case !enrolmentSupplied:
				fmt.Fprintf(w, "      whether this module already runs with caching defeated was not supplied to the census\n")
			case defeated[m.Rel]:
				fmt.Fprintf(w, "      this module runs with caching defeated, so those inputs cannot serve a stale result\n")
			default:
				fmt.Fprintf(w, "      this module does NOT run with caching defeated, so those inputs can serve a stale pass\n")
			}
		}
		fmt.Fprintf(w, "      %d of %d also read something outside the tree entirely — not counted above\n",
			outside, len(m.Packages))
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
	if len(grown) > 0 {
		fmt.Fprintf(w, "\nthe count grew:\n")
		for _, g := range grown {
			fmt.Fprintf(w, "  %s\n", g)
		}
		fmt.Fprintf(w, "Either move the input inside the module root, or — if the read is deliberate — record the\n")
		fmt.Fprintf(w, "new count in %s and make sure the module runs with caching defeated.\n", expectationSite)
	}

	if reportOnly {
		if len(problems) > 0 || len(grown) > 0 {
			fmt.Fprintf(w, "\nreport-only: the findings above did not move the exit code\n")
		}
		return nil
	}
	switch {
	case len(problems) > 0 && len(grown) > 0:
		return errors.New("the census could not measure part of its scope, and the count grew where it could")
	case len(problems) > 0:
		return errors.New("the census could not measure part of its scope")
	case len(grown) > 0:
		return errors.New("more test packages read shared inputs from elsewhere in the tree than expected")
	}
	return nil
}
