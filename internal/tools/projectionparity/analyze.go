// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package projectionparity

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"slices"
	"strings"
)

// Kind is what a finding says. Values are the identifiers a report prints and a
// driver matches on.
type Kind string

const (
	// DroppedField is the defect this package exists to find: one surface
	// carries a field the shared computation produced and another, which calls
	// the same computation, does not, with no reason stated where it drops it.
	DroppedField Kind = "dropped-field"

	// ResultDiscarded is a surface that calls a shared computation and throws
	// its structured result away, keeping only the error. Every fact the
	// computation produced is dropped at once, so it is reported as one finding
	// about the result rather than as one per field.
	ResultDiscarded Kind = "result-discarded"

	// UnreadableSite is a surface whose use of a shared result this read cannot
	// follow, so no parity verdict is drawn for that surface and that result.
	// The value escapes to somewhere the reading does not go.
	UnreadableSite Kind = "unreadable-site"

	// UnreadableCore is a shared computation whose declaration or result struct
	// could not be read, so the field set it hands its callers is unknown and
	// nothing may be concluded from a surface's reads of it.
	UnreadableCore Kind = "unreadable-core"

	// ExemptionUnsupported is a stated exemption the site does not bear out: it
	// names a field the surface does read, or one no shared result at the site
	// carries, or it states no reason, or it sits away from any projection of
	// the result it names. An exemption for a whole result adds two ways to be
	// unsupported, both of which would have it clear more than a field exemption
	// could: the surface never discarded that result, or it does project fields
	// from it and so does not omit the whole of it.
	//
	// The declaration is the one way a dropped field is cleared here, so it is
	// read back against the site rather than taken on trust. A declaration
	// nothing checks is one a later author applies by habit, and at that point a
	// surface dropping a fact for a reason is indistinguishable from one that
	// dropped it by accident.
	ExemptionUnsupported Kind = "exemption-unsupported"
)

// Finding is one thing read about one surface's projection of one shared result.
//
// Kind is what the finding says, and File and Line are where the surface's
// projection sits. Surface names the surface, Core the shared computation as
// package directory and function name, and Result the struct it hands back.
// Field is the field at issue, empty for a finding about a whole result.
// Carriers are the surfaces that do carry the field, sorted. Detail carries
// whatever the kind needs beyond those.
type Finding struct {
	Kind     Kind
	File     string
	Line     int
	Surface  string
	Core     string
	Result   string
	Field    string
	Carriers []string
	Detail   string
}

// String renders a finding as one line, and is what a report prints.
func (f Finding) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d: %s: ", f.File, f.Line, f.Kind)
	switch f.Kind {
	case DroppedField:
		fmt.Fprintf(&b, "%s drops %s.%s, which %s computes and %s carries. "+
			"Carry it, or say where it is dropped why this surface cannot: "+
			"// projection-omits %s.%s: <reason>",
			f.Surface, f.Result, f.Field, f.Core, strings.Join(f.Carriers, " and "),
			f.Result, f.Field)
	case ResultDiscarded:
		fmt.Fprintf(&b, "%s calls %s and discards its %s, so every fact it computed is dropped at once, "+
			"while %s carries %s. Carry it, or say where it is discarded why this surface cannot: "+
			"// projection-omits %s: <reason>",
			f.Surface, f.Core, f.Result, strings.Join(f.Carriers, " and "), f.Detail, f.Result)
	case UnreadableSite:
		fmt.Fprintf(&b, "%s's use of the %s from %s %s, so no parity verdict is drawn for it here",
			f.Surface, f.Result, f.Core, f.Detail)
	case UnreadableCore:
		fmt.Fprintf(&b, "%s hands back %s and %s, so what its callers are given cannot be compared",
			f.Core, f.Result, f.Detail)
	case ExemptionUnsupported:
		omitted := f.Result
		if f.Field != "" {
			omitted += "." + f.Field
		}
		fmt.Fprintf(&b, "%s states that it omits %s, but %s", f.Surface, omitted, f.Detail)
	}
	return b.String()
}

// Report renders findings as text, one per line, in the order given.
func Report(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Result is what one read produced: its findings, and the census that says what
// it reached.
//
// The census is part of the answer rather than diagnostics beside it. A read
// over no shared computation returns no finding, which is also what a clean read
// returns, so the counts are what make an empty finding list mean something.
type Result struct {
	// Findings are ordered by file, then position, then kind, then result and
	// field — an order that depends on the sources and not on a map's iteration,
	// so the same input reports the same bytes.
	Findings []Finding

	// SharedComputations are the producers called from two or more surfaces,
	// each as package directory and function name, sorted.
	SharedComputations []string

	// ResultTypes are the structs those producers hand back, each as package
	// directory and type name, sorted. Only structs carrying at least one
	// exported field appear: a result with no field has nothing to project.
	ResultTypes []string

	// Projections is how many producer results the read followed all the way to
	// a field set: one per call site and result position that was neither
	// unreadable nor discarded. A surface calling one computation twice counts
	// twice, because each call is a projection that had to be read.
	Projections int

	// UnreadResultTypes are the shared results NO surface projects readably, as
	// package directory and type name, sorted.
	//
	// They are the read's own reach reported as data rather than as findings. No
	// parity claim is made about them in either direction, and no finding is
	// emitted for them: with no readable side there is no comparison for an
	// unreadable site to have suppressed. Naming them keeps a green from being
	// read as covering them.
	UnreadResultTypes []string
}

// exemptionRE matches an at-site exemption. The result is always named, because
// one function can project two results and an exemption naming only a field
// would be ambiguous exactly where it mattered.
//
// The field is optional, and its absence is what distinguishes the two things a
// surface can legitimately omit: one field of a result it otherwise carries, or
// the result as a whole. Both are reported as findings — a dropped field and a
// discarded result — so both need a way to be answered at the site, and the two
// forms are read back against the site by different checks because they claim
// different things.
var exemptionRE = regexp.MustCompile(`projection-omits\s+([A-Za-z_][A-Za-z0-9_]*)(?:\.([A-Za-z_][A-Za-z0-9_]*))?\s*:\s*(.*)$`)

// maxFollowDepth bounds how far a value handed on to a same-package helper is
// followed. Beyond it the site is reported as unreadable rather than followed
// further: an unbounded follow would have to resolve names across packages, and
// a resolution that quietly gave up would return a clean result over a body it
// never saw.
const maxFollowDepth = 3

// Analyze reads every surface's projection of every shared computation's result
// and reports what it finds.
//
// root is the module root and modulePath its own import path, which is what
// turns a call's package qualifier into a directory in this module. surfaces are
// the caller-facing surfaces and the directories their projections live in.
// coreScope bounds which package directories count as shared computation cores,
// empty meaning all of them.
//
// It refuses rather than reports over an empty corpus: fewer than two surfaces
// leaves nothing to compare, so a clean result would describe the arguments
// rather than the tree.
func Analyze(root, modulePath, coreScope string, surfaces []Surface) (Result, error) {
	if len(surfaces) < 2 {
		return Result{}, fmt.Errorf("parity needs two surfaces to compare and %d was given, so a clean result would certify nothing",
			len(surfaces))
	}

	s := newSource(root, modulePath)
	surfaceDirs := map[string]bool{}
	for _, surface := range surfaces {
		if surface.Name == "" {
			return Result{}, fmt.Errorf("a surface was given with no name, so no finding could say which surface it is about")
		}
		if len(surface.Dirs) == 0 {
			return Result{}, fmt.Errorf("the surface %q was given no directory, so its projections would read as absent rather than unread",
				surface.Name)
		}
		for _, dir := range surface.Dirs {
			surfaceDirs[dir] = true
		}
	}

	a := &analysis{
		source:        s,
		surfaces:      surfaces,
		reads:         map[string]map[string]bool{},
		consumes:      map[string]bool{},
		incomplete:    map[string]bool{},
		discarded:     map[string]Finding{},
		participating: map[string][]posRange{},
		firstSite:     map[string]Finding{},
		resultTypes:   map[string]resultType{},
		reportedCore:  map[string]bool{},
	}

	sites, err := collectCallSites(s, surfaces, surfaceDirs, coreScope)
	if err != nil {
		return Result{}, err
	}
	shared := sharedProducers(sites)

	producers := map[string]producer{}
	for key := range shared {
		coreDir, funcName := splitProducerKey(key)
		p, perr := readProducer(s, coreDir, funcName, 0)
		if perr != nil {
			return Result{}, perr
		}
		if p.skip {
			delete(shared, key)
			continue
		}
		producers[key] = p
	}

	// Sorted, so a tree with more than one defect reports them in the same order
	// on every run rather than in whichever order a map handed them over.
	slices.SortStableFunc(sites, func(x, y callSite) int {
		xf, xl := s.position(x.call.Pos())
		yf, yl := s.position(y.call.Pos())
		if xf != yf {
			return strings.Compare(xf, yf)
		}
		return xl - yl
	})

	for _, site := range sites {
		key := site.coreDir + "." + site.funcName
		if _, ok := shared[key]; !ok {
			continue
		}
		a.readSite(site, producers[key])
	}

	a.verifyExemptions()
	a.reportDivergence()

	return a.finish(shared), nil
}

// posRange is one function's extent, for deciding whether an exemption sits at
// a projection of the result it names.
type posRange struct {
	from, to token.Pos
}

type analysis struct {
	source   *source
	surfaces []Surface

	// reads maps a surface-and-result pair to the fields that surface reads.
	reads map[string]map[string]bool

	// consumes records that a surface calls a producer handing back the result.
	consumes map[string]bool

	// incomplete records a surface-and-result pair whose read did not complete,
	// so no parity verdict may be drawn over it.
	incomplete map[string]bool

	// discarded holds, per surface-and-result pair the surface threw away
	// whole, the finding that reports it — held rather than emitted, because a
	// result every surface discards is not a divergence and the aggregate is
	// what says which case this is.
	discarded map[string]Finding

	// participating records the functions a surface's read of a result passed
	// through, so an exemption can be placed against them.
	participating map[string][]posRange

	// firstSite is a template finding per surface-and-result pair, carrying the
	// position a divergence is reported at.
	firstSite map[string]Finding

	resultTypes  map[string]resultType
	reportedCore map[string]bool

	findings []Finding

	// pending holds an unreadable-site finding until the aggregate says whether
	// it suppressed anything. A result type NO surface projects readably has no
	// comparison to suppress, so a finding there would report the reach of this
	// read rather than a fact about the tree; the census carries those instead.
	pending []pendingFinding

	exemptions  []exemption
	verified    map[string]bool
	projections int
}

// pendingFinding is a finding held until the result type it concerns is known
// to have a readable projection somewhere.
type pendingFinding struct {
	finding   Finding
	resultKey string
}

// exemption is one stated at-site exemption.
type exemption struct {
	surface string
	file    string
	line    int
	pos     token.Pos
	result  string
	field   string
	reason  string
}

func pairKey(surface, resultKey string) string { return surface + "\x00" + resultKey }

func fieldKey(surface, resultKey, field string) string {
	return surface + "\x00" + resultKey + "\x00" + field
}

func splitProducerKey(key string) (coreDir, funcName string) {
	i := strings.LastIndex(key, ".")
	return key[:i], key[i+1:]
}

// readSite reads one call to a shared computation from one surface.
func (a *analysis) readSite(site callSite, p producer) {
	file, line := a.source.position(site.call.Pos())
	core := site.coreDir + "." + site.funcName

	if p.coreDir != "" {
		// A re-exported producer was followed to the package that declares it,
		// so the result struct is read from there rather than from the package
		// the surface named.
		core = p.coreDir + "." + p.funcName
	}

	if p.unreadable != "" {
		if !a.reportedCore[core] {
			a.reportedCore[core] = true
			a.findings = append(a.findings, Finding{
				Kind: UnreadableCore, File: file, Line: line, Surface: site.surface,
				Core: core, Result: "no readable result", Detail: p.unreadable,
			})
		}
		return
	}
	if len(p.structResults) == 0 {
		return
	}

	binding, bound := bindingFor(site.fn, site.call)

	indices := make([]int, 0, len(p.structResults))
	for index := range p.structResults {
		indices = append(indices, index)
	}
	slices.Sort(indices)

	for _, index := range indices {
		name := p.structResults[index]
		resultKey := p.coreDir + "." + name

		rt, ok := a.resultTypes[resultKey]
		if !ok {
			read, err := readResultType(a.source, p.coreDir, name)
			if err != nil {
				read = resultType{coreDir: p.coreDir, name: name, unreadable: err.Error()}
			}
			a.resultTypes[resultKey] = read
			rt = read
		}
		if rt.unreadable != "" {
			if !a.reportedCore[resultKey] {
				a.reportedCore[resultKey] = true
				a.findings = append(a.findings, Finding{
					Kind: UnreadableCore, File: file, Line: line, Surface: site.surface,
					Core: core, Result: name, Detail: rt.unreadable,
				})
			}
			continue
		}
		if len(rt.fields) == 0 {
			continue
		}

		pair := pairKey(site.surface, resultKey)
		a.consumes[pair] = true
		if _, held := a.firstSite[pair]; !held {
			a.firstSite[pair] = Finding{
				File: file, Line: line, Surface: site.surface, Core: core, Result: name,
			}
		}
		a.participating[pair] = append(a.participating[pair], funcRange(site.fn))

		unreadable := func(detail string) {
			a.incomplete[pair] = true
			a.pending = append(a.pending, pendingFinding{
				finding: Finding{
					Kind: UnreadableSite, File: file, Line: line, Surface: site.surface,
					Core: core, Result: name, Detail: detail,
				},
				resultKey: resultKey,
			})
		}

		if !bound {
			unreadable("is not bound to a name at the call, so what it reads from it is not in this body")
			continue
		}
		ident, held := binding[index]
		if !held || ident == "" {
			unreadable("is assigned to something other than a plain name, which this read does not follow")
			continue
		}
		if ident == "_" {
			a.incomplete[pair] = true
			if _, held := a.discarded[pair]; !held {
				a.discarded[pair] = Finding{
					Kind: ResultDiscarded, File: file, Line: line, Surface: site.surface,
					Core: core, Result: name,
				}
			}
			continue
		}

		fields, escape, visited := a.readFields(site.dir, ident, site.fn, 0, map[string]bool{})
		a.participating[pair] = append(a.participating[pair], visited...)
		if escape != "" {
			unreadable(escape)
			continue
		}
		if a.reads[pair] == nil {
			a.reads[pair] = map[string]bool{}
		}
		for _, field := range fields {
			a.reads[pair][field] = true
		}
		a.projections++
	}
}

func funcRange(fn *ast.FuncDecl) posRange {
	from := fn.Pos()
	if fn.Doc != nil {
		from = fn.Doc.Pos()
	}
	return posRange{from: from, to: fn.End()}
}

// bindingFor is the identifier each of a call's results is bound to, where the
// call is the whole right-hand side of an assignment or a declaration.
func bindingFor(fn *ast.FuncDecl, call *ast.CallExpr) (map[int]string, bool) {
	out := map[int]string{}
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if found {
			return false
		}
		switch st := node.(type) {
		case *ast.AssignStmt:
			if len(st.Rhs) != 1 || st.Rhs[0] != call {
				return true
			}
			for i, target := range st.Lhs {
				out[i] = identName(target)
			}
			found = true
		case *ast.ValueSpec:
			if len(st.Values) != 1 || st.Values[0] != call {
				return true
			}
			for i, target := range st.Names {
				out[i] = target.Name
			}
			found = true
		}
		return !found
	})
	return out, found
}

func identName(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// argRef is one call and the position a value was passed at.
type argRef struct {
	call  *ast.CallExpr
	index int
}

// readFields collects the fields a function reads from ident, following the
// value one hop at a time into a same-package helper it is handed to whole.
//
// escape is non-empty for the first use the reading cannot follow, in which case
// the field set is incomplete and no verdict may be drawn from it. visited are
// the function extents the reading passed through, so an exemption stated in a
// helper counts as stated at the site.
func (a *analysis) readFields(dir, ident string, fn *ast.FuncDecl, depth int, seen map[string]bool) (fields []string, escape string, visited []posRange) {
	if fn.Body == nil {
		return nil, "is read in a function with no body", nil
	}
	marker := dir + "." + fn.Name.Name + "(" + ident + ")"
	if seen[marker] {
		return nil, "", nil
	}
	seen[marker] = true
	visited = append(visited, funcRange(fn))

	selectorBase := map[*ast.Ident]string{}
	argOf := map[*ast.Ident]argRef{}
	binding := map[*ast.Ident]bool{}
	nilTest := map[*ast.Ident]bool{}
	var uses []*ast.Ident

	target := func(expr ast.Expr) {
		if id, ok := expr.(*ast.Ident); ok && id.Name == ident {
			binding[id] = true
		}
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			if base, ok := n.X.(*ast.Ident); ok && base.Name == ident {
				selectorBase[base] = n.Sel.Name
			}
		case *ast.BinaryExpr:
			// A test against nil reads no field and lets nothing out of the
			// body, so it is neither a read nor an escape. Every surface tests
			// whether it was handed anything at all; treating that as an escape
			// would report the shape common to all of them.
			if n.Op != token.EQL && n.Op != token.NEQ {
				break
			}
			if identName(n.Y) == "nil" {
				target := n.X
				if id, ok := target.(*ast.Ident); ok && id.Name == ident {
					nilTest[id] = true
				}
			}
			if identName(n.X) == "nil" {
				if id, ok := n.Y.(*ast.Ident); ok && id.Name == ident {
					nilTest[id] = true
				}
			}
		case *ast.CallExpr:
			for i, arg := range n.Args {
				if id, ok := arg.(*ast.Ident); ok && id.Name == ident {
					argOf[id] = argRef{call: n, index: i}
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				target(lhs)
			}
		case *ast.ValueSpec:
			for _, name := range n.Names {
				target(name)
			}
		case *ast.RangeStmt:
			target(n.Key)
			target(n.Value)
		case *ast.Ident:
			if n.Name == ident {
				uses = append(uses, n)
			}
		}
		return true
	})

	// A name bound more than once in one body no longer identifies one value, so
	// a field read from it after the second binding may belong to something else
	// entirely. The reading declines rather than attributing the reads to the
	// shared result.
	if len(binding) > 1 {
		return nil, fmt.Sprintf("binds %s more than once, so a field read from it need not be the shared computation's result", ident), visited
	}

	parsed, err := a.source.read(dir)
	if err != nil {
		return nil, fmt.Sprintf("sits in %s, which could not be parsed: %v", dir, err), visited
	}
	decls := funcDeclsByName(parsed.Files)

	for _, use := range uses {
		if binding[use] || nilTest[use] {
			// The occurrence that binds the name is not a use of the value, and
			// a test against nil reads nothing from it.
			continue
		}
		if field, isSelectorBase := selectorBase[use]; isSelectorBase {
			fields = append(fields, field)
			continue
		}
		ref, isArg := argOf[use]
		if !isArg {
			if escape == "" {
				escape = fmt.Sprintf("uses %s as a value rather than reading a field from it, so the fields it reads are not all in this body", ident)
			}
			continue
		}
		callee := identName(ref.call.Fun)
		decl, known := decls[callee]
		if callee == "" || !known {
			if escape == "" {
				escape = fmt.Sprintf("hands %s to a call this read cannot resolve to a function in %s", ident, dir)
			}
			continue
		}
		if depth+1 >= maxFollowDepth {
			if escape == "" {
				escape = fmt.Sprintf("hands %s on to %s beyond the depth this read follows", ident, callee)
			}
			continue
		}
		params, variadic := flatParamNames(decl)
		if ref.index >= len(params) || (variadic && ref.index >= len(params)-1) {
			if escape == "" {
				escape = fmt.Sprintf("hands %s to %s at a position this read cannot bind to a parameter", ident, callee)
			}
			continue
		}
		param := params[ref.index]
		if param == "" {
			// The callee cannot name the value, so it reads no field from it.
			// That is a complete answer rather than an escape.
			continue
		}
		deeper, deeperEscape, deeperVisited := a.readFields(dir, param, decl, depth+1, seen)
		visited = append(visited, deeperVisited...)
		if deeperEscape != "" {
			if escape == "" {
				escape = fmt.Sprintf("hands %s to %s, which %s", ident, callee, deeperEscape)
			}
			continue
		}
		fields = append(fields, deeper...)
	}

	slices.Sort(fields)
	return slices.Compact(fields), escape, visited
}

// collectExemptions reads every stated exemption from every surface's own
// source.
func (a *analysis) collectExemptions() {
	for _, surface := range a.surfaces {
		for _, dir := range surface.Dirs {
			parsed, err := a.source.read(dir)
			if err != nil {
				continue
			}
			for _, file := range parsed.Files {
				for _, group := range file.Comments {
					for _, c := range group.List {
						match := exemptionRE.FindStringSubmatch(c.Text)
						if match == nil {
							continue
						}
						path, line := a.source.position(c.Pos())
						a.exemptions = append(a.exemptions, exemption{
							surface: surface.Name, file: path, line: line, pos: c.Pos(),
							result: match[1], field: match[2], reason: strings.TrimSpace(match[3]),
						})
					}
				}
			}
		}
	}
	slices.SortStableFunc(a.exemptions, func(x, y exemption) int {
		if x.file != y.file {
			return strings.Compare(x.file, y.file)
		}
		return x.line - y.line
	})
}

// verifyExemptions reads every stated exemption back against what the surface
// was measured to do, and records the ones the site bears out.
func (a *analysis) verifyExemptions() {
	a.collectExemptions()
	a.verified = map[string]bool{}

	for _, e := range a.exemptions {
		reject := func(detail string) {
			a.findings = append(a.findings, Finding{
				Kind: ExemptionUnsupported, File: e.file, Line: e.line, Surface: e.surface,
				Result: e.result, Field: e.field, Detail: detail,
			})
		}

		var matches []string
		for key, rt := range a.resultTypes {
			if rt.name == e.result {
				matches = append(matches, key)
			}
		}
		slices.Sort(matches)
		switch len(matches) {
		case 0:
			reject("no shared computation this surface calls hands back a result of that name")
			continue
		case 1:
		default:
			reject(fmt.Sprintf("two shared results are named that — %s — so the exemption does not say which it means",
				strings.Join(matches, " and ")))
			continue
		}
		resultKey := matches[0]
		rt := a.resultTypes[resultKey]

		if e.field != "" && !slices.Contains(rt.fields, e.field) {
			reject(fmt.Sprintf("%s carries no field of that name (it carries %s)", e.result, strings.Join(rt.fields, ", ")))
			continue
		}
		// The subject is what the exemption claims to omit, and restating it is
		// the one reason a declaration can give that says nothing at all.
		subject := e.field
		if subject == "" {
			subject = e.result
		}
		if e.reason == "" || strings.EqualFold(strings.TrimSuffix(e.reason, "."), subject) {
			reject("it states no reason, and a declaration satisfiable by saying nothing says nothing")
			continue
		}

		pair := pairKey(e.surface, resultKey)
		if !a.consumes[pair] {
			reject(fmt.Sprintf("this surface calls nothing that hands back a %s, so it drops no field of one", e.result))
			continue
		}
		// Each form is held against what it actually claims. A field exemption is
		// stale where the surface reads that field; a whole-result exemption is
		// unsupported where the surface never discarded the result, and stale
		// where it does project fields from it — which is the reading that keeps
		// the broader form from clearing more than the narrow one could.
		if e.field != "" {
			if a.reads[pair][e.field] {
				reject(fmt.Sprintf("this surface does read %s.%s, so the exemption is stale — delete it rather than leaving a reason nothing is dropped for",
					e.result, e.field))
				continue
			}
		} else {
			if _, discards := a.discarded[pair]; !discards {
				reject(fmt.Sprintf("this surface does not discard the %s it is handed, so there is no whole result here for it to excuse — name the field it omits instead",
					e.result))
				continue
			}
			if len(a.reads[pair]) > 0 {
				reject(fmt.Sprintf("this surface does project %s (it reads %s), so it does not omit the whole of it — name the field it omits instead",
					e.result, strings.Join(sortedKeys(a.reads[pair]), ", ")))
				continue
			}
		}
		if !withinAny(e.pos, a.participating[pair]) {
			reject(fmt.Sprintf("it sits away from every projection of %s on this surface, which makes it a list entry rather than a statement at the site",
				e.result))
			continue
		}
		// A whole-result exemption is recorded under the empty field, which is
		// the same key the discarded finding is cleared against below.
		a.verified[fieldKey(e.surface, resultKey, e.field)] = true
	}
}

// sortedKeys are a set's members in order, so a refusal naming them reads the
// same on every run rather than in whichever order a map handed them over.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func withinAny(pos token.Pos, ranges []posRange) bool {
	for _, r := range ranges {
		if pos >= r.from && pos <= r.to {
			return true
		}
	}
	return false
}

// reportDivergence names every field one surface carries and another drops.
func (a *analysis) reportDivergence() {
	resultKeys := make([]string, 0, len(a.resultTypes))
	for key := range a.resultTypes {
		resultKeys = append(resultKeys, key)
	}
	slices.Sort(resultKeys)

	for _, resultKey := range resultKeys {
		rt := a.resultTypes[resultKey]
		if rt.unreadable != "" || len(rt.fields) == 0 {
			continue
		}

		var consumers []string
		for _, surface := range a.surfaces {
			if a.consumes[pairKey(surface.Name, resultKey)] {
				consumers = append(consumers, surface.Name)
			}
		}
		if len(consumers) < 2 {
			continue
		}

		// A result one surface threw away whole is reported once, naming what the
		// OTHER surfaces carry. One exclusion, and it is the difference between a
		// divergence and a shape every surface shares: a result no surface reads
		// any field of is a computation nobody projects, so there is no carrier
		// for the discarding surface to disagree with.
		//
		// Deliberately NOT excluded: a surface that discards the result at one
		// call and reads it at another. The call that discarded it still carries
		// nothing of what it was handed, and which call a reader reaches is not
		// something this read can know — so the discard is reported and the
		// surface answers it at the site if the choice was deliberate. Its
		// per-field findings are withheld separately, because a read that met a
		// discarded binding did not follow that projection to a full field set.
		var carriedAnywhere []string
		for _, surface := range consumers {
			if len(a.reads[pairKey(surface, resultKey)]) > 0 {
				carriedAnywhere = append(carriedAnywhere, surface)
			}
		}
		for _, surface := range consumers {
			finding, held := a.discarded[pairKey(surface, resultKey)]
			if !held {
				continue
			}
			if a.verified[fieldKey(surface, resultKey, "")] {
				// The surface stated at the site why it consumes the presence of
				// this result rather than its content, and the statement was read
				// back against the site.
				continue
			}
			var others, carried []string
			for _, carrier := range carriedAnywhere {
				if carrier == surface {
					continue
				}
				others = append(others, carrier)
				for field := range a.reads[pairKey(carrier, resultKey)] {
					carried = append(carried, field)
				}
			}
			if len(others) == 0 {
				continue
			}
			slices.Sort(carried)
			finding.Carriers = others
			finding.Detail = strings.Join(slices.Compact(carried), ", ")
			a.findings = append(a.findings, finding)
		}

		for _, field := range rt.fields {
			var carriers, droppers []string
			for _, surface := range consumers {
				if a.reads[pairKey(surface, resultKey)][field] {
					carriers = append(carriers, surface)
					continue
				}
				droppers = append(droppers, surface)
			}
			if len(carriers) == 0 {
				// No surface projects it. The shared computation may be
				// producing something nobody carries, which is a different
				// defect with a different repair.
				continue
			}
			for _, surface := range droppers {
				pair := pairKey(surface, resultKey)
				if a.incomplete[pair] {
					// The read of this surface's projection did not complete
					// and has already been reported. A verdict over a value
					// only partly followed would be a clean result certifying
					// nothing.
					continue
				}
				if a.verified[fieldKey(surface, resultKey, field)] {
					continue
				}
				finding := a.firstSite[pair]
				finding.Kind = DroppedField
				finding.Field = field
				finding.Carriers = carriers
				a.findings = append(a.findings, finding)
			}
		}
	}
}

// releasePending emits each held unreadable-site finding whose result type some
// surface does project readably, and names the rest in the census.
//
// The distinction is what the finding would mean. Where one surface's reads are
// known, a site this read could not follow is a comparison it declined to draw,
// and passing over it would leave a clean result certifying nothing. Where NO
// surface's reads are known, there was never a comparison to draw: reporting it
// would describe how far this read reaches rather than anything about the tree,
// and the corpus is where that belongs.
func (a *analysis) releasePending() []string {
	readable := map[string]bool{}
	for pair := range a.reads {
		if len(a.reads[pair]) > 0 {
			readable[pair[strings.Index(pair, "\x00")+1:]] = true
		}
	}
	unread := map[string]bool{}
	for _, held := range a.pending {
		if readable[held.resultKey] {
			a.findings = append(a.findings, held.finding)
			continue
		}
		unread[held.resultKey] = true
	}

	out := make([]string, 0, len(unread))
	for key := range unread {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// finish orders the findings and assembles the census.
func (a *analysis) finish(shared map[string]map[string]bool) Result {
	unread := a.releasePending()
	out := Result{Findings: a.findings, Projections: a.projections, UnreadResultTypes: unread}

	for key := range shared {
		out.SharedComputations = append(out.SharedComputations, key)
	}
	slices.Sort(out.SharedComputations)

	for key, rt := range a.resultTypes {
		if rt.unreadable == "" && len(rt.fields) > 0 {
			out.ResultTypes = append(out.ResultTypes, key)
		}
	}
	slices.Sort(out.ResultTypes)

	slices.SortStableFunc(out.Findings, func(x, y Finding) int {
		if x.File != y.File {
			return strings.Compare(x.File, y.File)
		}
		if x.Line != y.Line {
			return x.Line - y.Line
		}
		if x.Kind != y.Kind {
			return strings.Compare(string(x.Kind), string(y.Kind))
		}
		if x.Result != y.Result {
			return strings.Compare(x.Result, y.Result)
		}
		return strings.Compare(x.Field, y.Field)
	})
	return out
}
