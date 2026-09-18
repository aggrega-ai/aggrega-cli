// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// errorsPackageDir is the module-relative directory of the package that declares
// Meta and Register.
//
// A registration is recognised only when the file carrying it imports that
// package, or is that package. Matching a bare function name instead would let
// any unrelated function called Register declare an identity, and the resulting
// entry would look exactly like a real one.
const errorsPackageDir = "pkg/errors"

// Identity is one handle a caller can match: the package that declares the name,
// the name itself, and the two codes and the message the condition carries.
//
// A re-export declares no new condition. It forwards to one, and the value it
// returns IS that condition's value, so it reports the same codes and the same
// message. All three are therefore resolved through the forward rather than left
// empty: a handle that reports a code at run time and shows none here would read
// as a condition without one. AliasOf is what distinguishes the two kinds — it
// names the handle forwarded to, and only a re-export carries it.
type Identity struct {
	Pkg        string // module-relative directory, e.g. "pkg/resolver"
	Name       string // exported identifier, e.g. "ErrParameterMissing"
	Code       string // registered descriptive code, resolved through a re-export
	ShortCode  string // registered short code, E and four digits, resolved through a re-export
	Message    string // registered message, resolved through a re-export
	AliasOf    string // "pkg/oci.ErrFetchNotFound" for a re-export, else empty
	Importable bool   // reachable by `import` from outside this module

	// Raised reports whether production code anywhere in this module reaches
	// the condition's value, which is what makes it reachable by a build. Both
	// spellings count: a caller outside the declaring package invokes the
	// accessor, and one inside it names the binding the accessor publishes,
	// because the accessor yields a plain error and the constructor a raise goes
	// through takes the registered identity. Resolved through a re-export in
	// both directions: a re-export and the identity it forwards to are one
	// value, so raising either raises both.
	Raised bool

	// DeclaredAhead is the note the registration carries naming the work that
	// will raise the condition, and is empty for every condition something
	// already raises. Resolved through a re-export, like the message.
	DeclaredAhead string

	// Advisory reports whether a check declares this condition at warning
	// severity — the declaration that makes a build report the finding and
	// carry on rather than stop.
	//
	// 🔴 False is narrower than "halts", and reading it as the opposite of
	// advisory would publish a claim no declaration supports. It says only that
	// no check declares this condition at warning severity, which covers a
	// condition that halts AND a condition raised outside the check framework,
	// where there is no severity declared at all. The registration beside a
	// condition declares wording and codes and never consequence, so the
	// distinction cannot be recovered from it.
	//
	// Resolved through a re-export in both directions, like Raised: a re-export
	// and the identity it forwards to are one value, so a check naming either
	// declares the severity of both.
	Advisory bool
}

// admitted reports whether the condition reaches the published reference.
//
// This is the rule the whole generator turns on, so it is one predicate rather
// than a condition repeated at each renderer: a condition is published when
// something raises it, OR when its registration names the work that will. A
// registration that is neither is absent from the reference by construction —
// no hand writes those pages, so there is no edit that puts it there — and is
// named on every run by [reportUnpublished] instead.
//
// Neither half can be acquired by neglect, which is what makes the rule mean
// something. A raise site is code that had to be written; a declared-ahead note
// is refused by registration unless it carries real text. So the two ways onto
// the reference are both deliberate acts, and doing nothing keeps a condition
// off it.
func (id Identity) admitted() bool { return id.Raised || id.DeclaredAhead != "" }

// Handle is the identity's key. It is (package, name) and never the bare name:
// a name-keyed map merges two same-named declarations in different packages into
// one entry, and it collapses a re-export onto the origin it forwards to, which
// loses the package a caller was told to import.
func (id Identity) Handle() string { return id.Pkg + "." + id.Name }

// skipDirs are directory names whose contents declare nothing the matching
// surface contains: version-control and dependency trees, and the deliberately
// malformed fixtures under testdata.
//
// The plugin tree is deliberately not among them. What shares no identity with
// the host binary is a guest's entry point, which is constrained to the
// WebAssembly guest target and compiled into nothing else; the translation core
// beside it carries no constraint at all and builds for the host and the guest
// alike, so the conditions it declares are the very ones a caller matches. That
// line runs between files rather than around a directory, so it is read per file
// by [guestEntryPoint] and not written down here as a path.
var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
}

// wasmGuestTags are the build tags only a WebAssembly guest target satisfies:
// the operating system a guest is built for, and the architecture it is built
// as.
//
// This set is the whole of what separates a guest's entry point from the core
// beside it, so it is declared once and consulted by both axes a build
// constraint can be written on — the //go:build expression and the file name.
var wasmGuestTags = map[string]bool{
	"wasip1": true,
	"wasm":   true,
}

// maxConstraintTags bounds the enumeration in [requiresGuestTag]. A build
// constraint names a handful of tags at most; the bound exists so that an
// expression naming an implausible number of them is refused by name, rather
// than silently taking a fallback in whichever direction happens to be
// convenient.
const maxConstraintTags = 16

// guestEntryPoint reports whether a file's build constraints can be satisfied
// only by a WebAssembly guest target.
//
// That is the property the matching surface turns on, and it is read from each
// file rather than from where the file sits. A guest's entry point is
// constrained to the guest target, so it enters no host binary and publishes
// nothing a caller can match; a shared core carries no constraint, builds for
// host and guest alike, and declares the conditions a caller reaches through an
// ordinary import. Deciding this by directory would restate the same split as a
// list of paths, and a guest added later would sit outside the list while
// looking exactly like one inside it.
//
// Both axes are read. The expression is decided by satisfiability with the guest
// tags held false; the file name is decided by its final underscore-separated
// segment, which is where a target suffix restricting the file to a guest
// appears.
func guestEntryPoint(path string, file *ast.File) (bool, error) {
	if wasmGuestTags[targetSuffix(path)] {
		return true, nil
	}
	expr, err := buildConstraint(file)
	if err != nil || expr == nil {
		return false, err
	}
	return requiresGuestTag(expr)
}

// targetSuffix returns the build-target suffix a file name carries, which Go
// reads from the final underscore-separated segment of the name.
//
// A name with no underscore carries no suffix at all: the constrained form is
// name_target.go, so a file called wasm.go is restricted by nothing and its
// declarations belong to every build.
func targetSuffix(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".go")
	i := strings.LastIndex(base, "_")
	if i < 0 {
		return ""
	}
	return base[i+1:]
}

// buildConstraint returns the //go:build expression the file carries, or nil
// when it carries none. Only a comment above the package clause is read, which
// is where the toolchain requires the constraint to sit.
func buildConstraint(file *ast.File) (constraint.Expr, error) {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			return constraint.Parse(c.Text)
		}
	}
	return nil, nil
}

// requiresGuestTag reports whether expr is unsatisfiable unless one of
// [wasmGuestTags] holds.
//
// It decides that by enumerating the assignments of the tags expr actually
// names, with the guest tags held false: when no such assignment satisfies expr,
// no build lacking a guest target can include the file. Enumerating is what
// makes the answer exact. A structural recursion over the expression would have
// to treat each operand as independent, and a tag appearing on both sides of an
// operator — unix && !unix — is then read as satisfiable when it is not.
//
// Holding the guest tags false rather than testing one reference target is what
// keeps the result a function of the tree alone. A file excluded from the target
// that happens to be running the scan is a different property, and reading that
// one would make the identity set differ between the machines it is generated
// on.
func requiresGuestTag(expr constraint.Expr) (bool, error) {
	var free []string
	seen := map[string]bool{}
	var collect func(constraint.Expr)
	collect = func(e constraint.Expr) {
		switch v := e.(type) {
		case *constraint.TagExpr:
			if wasmGuestTags[v.Tag] || seen[v.Tag] {
				return
			}
			seen[v.Tag] = true
			free = append(free, v.Tag)
		case *constraint.NotExpr:
			collect(v.X)
		case *constraint.AndExpr:
			collect(v.X)
			collect(v.Y)
		case *constraint.OrExpr:
			collect(v.X)
			collect(v.Y)
		}
	}
	collect(expr)
	if len(free) > maxConstraintTags {
		return false, fmt.Errorf("the build constraint %q names %d tags, more than the %d this scan enumerates",
			expr.String(), len(free), maxConstraintTags)
	}
	for mask := 0; mask < 1<<len(free); mask++ {
		on := make(map[string]bool, len(free))
		for i, tag := range free {
			if mask&(1<<i) != 0 {
				on[tag] = true
			}
		}
		if expr.Eval(func(tag string) bool { return on[tag] }) {
			return false, nil
		}
	}
	return true, nil
}

// registered is what one registration declares: the two codes the identity is
// registered under and the message it renders. It is keyed by the package-local
// name the registration is bound to, which is the name the accessor beside it
// returns and never a name a caller can reach.
type registered struct {
	code          string
	shortCode     string
	message       string
	declaredAhead string
	place         declaredPlace
}

// declaredPlace is what a registration's rule-place key declares: which of the
// errors package's four place constructors it names, the reason the three that
// take one were handed, and — where the value could not be read at all — why.
//
// # Why this scan reads a key it carries nowhere
//
// The place a condition's rule belongs to reaches no page: it is a fact about how
// the product is built, and the reference is composed from the values carried onto
// an [Identity], which this is not. What this scan does with it is refuse a
// declaration nothing can read, and that is worth doing here for the reason the
// missing-short-code refusal is worth doing here. A registration whose place
// cannot be read cannot run either — the registration itself refuses the shapes
// it can see — so the tree is already wrong, and this is the earlier and cheaper
// place to say so, over source rather than over a program that has to start.
//
// It matters more for this key than for the prose beside it, because a second
// reader of the same registrations reads this one and acts on it. A value only
// one of the two can read is a value they disagree about silently: this scan
// would pass over it as absent while the other reports on a declaration that is
// there. Refusing is what keeps the two readings the same reading.
type declaredPlace struct {
	constructor string
	reason      string
	unreadable  string
}

// accessorTarget is what an accessor's body yields. Exactly one field is set:
// the package-local name of the registration that declares the identity, or the
// qualified handle of an identity in another package that the accessor
// re-exports.
type accessorTarget struct {
	local string
	alias string
}

// scan reads every non-test Go file under root and returns the identities the
// module declares, ordered by package then name so the output is a function of
// the tree alone and not of the order the filesystem happened to yield.
//
// It reads declaration syntax rather than a compiled program, which is what lets
// it run without building the module. An identity is declared in two halves that
// sit beside each other: a registration bound to a package-local name, carrying
// the code and the message, and an exported accessor returning that name. Only
// the accessor's name is reachable by a caller, so it is the accessor that fixes
// the handle and the registration that supplies what the handle describes. The
// two halves are joined after the whole tree is read rather than as each file is
// parsed, because an accessor may be written either side of the registration it
// returns.
//
// It reads two further facts about each identity, from the same walk: whether
// production code anywhere in the module reaches the condition's value, which is
// what makes it reachable by a build, and whether a check declares the condition
// at warning severity, which is what makes it an advisory rather than a halt.
// One walk answers all three questions because they are asked of the same files
// and the same parse — a second walk would be a second recogniser of which files
// count, and two recognisers of one population agree until the day a file takes
// a shape only one of them reads. What counts as reaching a condition, and the
// occurrences that are excluded, is [collectRaiseSites]; what counts as a
// severity declaration is [collectAdvisories].
//
// Reaching a condition has two spellings and only one of them is the accessor.
// Outside the declaring package the accessor is the only way in; inside it the
// raise names the binding the registration produced, because the constructor
// takes the registered identity and the accessor yields a plain error. The
// mention half is therefore collected during the walk and JOINED below, after
// the walk has finished, against the binding each accessor's declaration
// returns — for the same reason the registration join is deferred, that the two
// halves may be written in either order and in either file.
//
// The cost is stated rather than hidden: a handle produced by anything other
// than an accessor over a registration, or an accessor re-exporting one from
// another package, is not tabulated. Two such handles exist. One is a variable
// wrapping another identity so that a caller may match either, which registers no
// message for a table to carry; it is described in the page's curated prose
// instead. The other is an accessor forwarding to a second accessor in its own
// package, which names the same identity twice within one import — a reader who
// can reach either name can already reach the other, so the page loses no
// reachable match by carrying it once.
//
// Untabulated is not unseen, and the difference is what keeps the cost bounded.
// Every package-level name matching the condition convention is read and held
// against the identities resolved here, whatever declaration kind bound it, by
// [conventionalDecls]; each one is either an identity this carries, or a handle
// whose own text reaches one, or it is refused by name. So the two handles above
// are absent from the table because each resolves to a condition that is on it —
// read from the declaration on every run rather than permitted by a recorded
// name — and a handle resolving to nothing stops the scan instead of thinning
// the table in silence. What remains outside is narrower than the shapes this
// paragraph names: a handle built by a mechanism [reachedHandles] cannot follow,
// because it reaches its condition through something other than a call to that
// condition's accessor.
func scan(root, modulePath string) ([]Identity, error) {
	type accessorDecl struct {
		pkg    string
		name   string
		target accessorTarget
	}

	registrations := map[string]registered{} // pkgDir + "." + local name
	accessors := []accessorDecl{}            // in discovery order, sorted later
	seen := map[string]bool{}                // accessor handles already declared
	raised := map[string]bool{}              // handles production code invokes
	named := map[string]bool{}               // pkgDir + "." + every package-local name production code mentions
	advisory := map[string]bool{}            // handles a check declares at warning severity
	conventional := []conventionalDecl{}     // every package-level name matching the condition convention

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, filepath.Dir(path))
		if rerr != nil {
			return rerr
		}
		pkgDir := filepath.ToSlash(rel)
		if pkgDir == "." {
			pkgDir = ""
		}

		fset := token.NewFileSet()
		// Comments are retained because a build constraint is one, and the
		// constraint is what separates a guest's entry point from the core beside
		// it.
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		guest, gerr := guestEntryPoint(path, file)
		if gerr != nil {
			return fmt.Errorf("%s: %w", path, gerr)
		}

		imports := importMap(file, modulePath)

		// Raise sites are read from every production file, a guest's entry point
		// included; declarations are read only from the files a host build can
		// reach. The two questions take different file sets deliberately, and
		// [collectRaiseSites] records why: a condition declared in a shared core
		// and raised only from the guest beside it is raised.
		collectRaiseSites(raised, named, file, pkgDir, imports)

		// Check declarations are read from the same file set as raise sites, and
		// for the same reason: a check is production code that runs, and where it
		// sits decides nothing about the severity it declares.
		if aerr := collectAdvisories(advisory, file, pkgDir, imports); aerr != nil {
			return fmt.Errorf("%s: %w", path, aerr)
		}

		if guest {
			return nil
		}

		errPkg, isErrorsPkg := errorsPackageAlias(imports, pkgDir)

		// Read from the same files and inside the same guard as the declarations
		// below, because the question this answers is about those declarations:
		// which names a reader takes for a condition's handle did the walk not
		// tabulate. Reading it from a wider file set would name a declaration the
		// identity walk was never going to read.
		conventional = append(conventional, conventionalDecls(file, path, pkgDir, imports)...)

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				name, target, ok := accessor(d, imports)
				if !ok {
					continue
				}
				h := pkgDir + "." + name
				// Two declarations of one handle are unreachable in a package
				// Go compiles, but the scan reads files that exclude one another
				// — a unix file and its !unix counterpart — so both reach here
				// together.
				if seen[h] {
					return fmt.Errorf("%s: %s declared twice", path, h)
				}
				seen[h] = true
				accessors = append(accessors, accessorDecl{pkg: pkgDir, name: name, target: target})
			case *ast.GenDecl:
				if d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					name, reg, ok := registration(vs, errPkg, isErrorsPkg)
					if !ok {
						continue
					}
					key := pkgDir + "." + name
					if why := reg.place.unreadable; why != "" {
						// Refused rather than passed over, for the reason
						// recorded on declaredPlace: a value this scan skips as
						// absent is a value the other reader of these same
						// registrations reports on, and the two readings then
						// describe different declarations with nothing failing.
						return fmt.Errorf("%s: %s declares where its rule belongs in a form this scan cannot "+
							"read: %s; the value is a call to one of the four place constructors, and the three "+
							"that take a reason take it as a string literal written at the declaration — a "+
							"constant or a call moves the text out of the declaration, where neither this scan "+
							"nor a reader of the source can see it",
							path, key, why)
					}
					if prev, dup := registrations[key]; dup && prev != reg {
						return fmt.Errorf("%s: %s is registered twice, with different metadata", path, key)
					}
					registrations[key] = reg
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The mention side of the raise question is resolved once the whole tree has
	// been read, and only here can it be: a package-local binding is
	// package-scoped, so the accessor that publishes it may be declared in any
	// file of that package — either side of the raise that names it. Joining per
	// file would make a condition's raise state depend on the order the walk
	// reached the two files in, which is an answer about the filesystem rather
	// than about the tree.
	//
	// The binding is taken from the accessor's own declaration, which is what
	// [accessor] already returns, so the correspondence between a published name
	// and the value behind it is read rather than inferred from a spelling
	// convention the compiler does not enforce.
	for _, acc := range accessors {
		if acc.target.local == "" {
			continue
		}
		if named[acc.pkg+"."+acc.target.local] {
			raised[acc.pkg+"."+acc.name] = true
		}
	}

	// The declaring accessors are resolved first, because a re-export takes the
	// code and the message registered against the identity it forwards to and
	// cannot be resolved until that identity has them. The re-exported name is the
	// one a caller is told to match, and its own declaration carries neither.
	byHandle := map[string]*Identity{}
	out := make([]Identity, 0, len(accessors))
	for _, acc := range accessors {
		if acc.target.alias != "" {
			continue
		}
		reg, ok := registrations[acc.pkg+"."+acc.target.local]
		if !ok {
			// The accessor returns a package-local name that no registration
			// binds, so the handle names no identity: matching it would read
			// whatever that name happens to hold. It is reported rather than
			// skipped, because a skip removes the handle from the page while
			// leaving it exported and apparently matchable.
			return nil, fmt.Errorf("%s.%s returns %s, which no registration in that package binds; "+
				"a caller matching the handle would match whatever that name holds rather than a declared condition",
				acc.pkg, acc.name, acc.target.local)
		}
		if reg.shortCode == "" {
			// Reported rather than served with an empty column, for the reason
			// the missing-registration refusal above is reported: a condition
			// that shows no short code here reads as a condition that has none,
			// and the short code is the identifier a reader is given to quote.
			// A registration reaching here without one cannot run either — the
			// registration itself refuses it — so the tree is already broken and
			// this is the earlier, cheaper place to say so.
			return nil, fmt.Errorf("%s.%s registers no short code; "+
				"every condition declares one, and a listing that showed none for this handle "+
				"would read as a condition without one", acc.pkg, acc.name)
		}
		byHandle[acc.pkg+"."+acc.name] = &Identity{
			Pkg: acc.pkg, Name: acc.name, Code: reg.code, ShortCode: reg.shortCode, Message: reg.message,
			DeclaredAhead: reg.declaredAhead, Raised: raised[acc.pkg+"."+acc.name],
			Advisory: advisory[acc.pkg+"."+acc.name],
		}
	}
	// A re-export forwards to a DECLARING accessor and never to another
	// re-export. The restriction is checked rather than assumed: the loop above
	// resolved every declaring accessor, so a target absent from byHandle either
	// declares nothing or is itself a re-export, and the two are separated here
	// so a chain is refused by name instead of resolving or not according to the
	// order the two happened to be walked in.
	reExported := map[string]bool{}
	for _, acc := range accessors {
		if acc.target.alias != "" {
			reExported[acc.pkg+"."+acc.name] = true
		}
	}
	// Every re-export is resolved to its origin, and raise credit is propagated
	// to that origin, BEFORE any re-exported identity is built. A re-export and
	// the identity it forwards to are one value, so raising either raises both —
	// and an origin read before every one of its re-exports had been credited
	// would carry the wrong answer into whichever of them happened to be built
	// first, which is a result that depends on walk order rather than on the
	// tree.
	origins := make(map[string]*Identity, len(accessors))
	for _, acc := range accessors {
		if acc.target.alias == "" {
			continue
		}
		origin, ok := byHandle[acc.target.alias]
		if !ok {
			if reExported[acc.target.alias] {
				return nil, fmt.Errorf("%s.%s re-exports %s, which is itself a re-export; "+
					"a re-export names the identity it forwards to, so a chain would print a reader "+
					"a second name to follow rather than the one that declares the condition",
					acc.pkg, acc.name, acc.target.alias)
			}
			return nil, fmt.Errorf("%s.%s re-exports %s, which registers no message", acc.pkg, acc.name, acc.target.alias)
		}
		origins[acc.pkg+"."+acc.name] = origin
		if raised[acc.pkg+"."+acc.name] {
			origin.Raised = true
		}
		// A check naming a re-export declares the severity of the one value both
		// names reach, so the credit is propagated to the origin exactly as raise
		// credit is — and before any re-exported identity is built, so the answer
		// does not depend on which of them the walk reached first.
		if advisory[acc.pkg+"."+acc.name] {
			origin.Advisory = true
		}
	}
	for _, acc := range accessors {
		if acc.target.alias == "" {
			continue
		}
		origin := origins[acc.pkg+"."+acc.name]
		byHandle[acc.pkg+"."+acc.name] = &Identity{
			Pkg: acc.pkg, Name: acc.name, Code: origin.Code, ShortCode: origin.ShortCode,
			Message: origin.Message, AliasOf: acc.target.alias,
			DeclaredAhead: origin.DeclaredAhead, Raised: origin.Raised,
			Advisory: origin.Advisory,
		}
	}
	// Held here rather than during the walk, because placing a declaration means
	// asking whether it reaches an identity, and no identity exists until every
	// accessor and every re-export above has been resolved. Asking earlier would
	// answer from whichever half of the tree the walk had reached.
	if err := refuseUnplaceable(conventional, byHandle); err != nil {
		return nil, err
	}

	for _, acc := range accessors {
		id := byHandle[acc.pkg+"."+acc.name]
		id.Importable = importable(id.Pkg)
		out = append(out, *id)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pkg != out[j].Pkg {
			return out[i].Pkg < out[j].Pkg
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Retirement is one retired short code: a number that named a condition, names
// none now, and will never name another.
//
// It is not an Identity and deliberately carries none of an Identity's fields.
// There is no accessor to import, no message a build renders and no package to
// group it under, because the condition it named is gone — what is left is the
// number and the sentence saying what it named. Folding it into Identity would
// mean an Identity whose every other column is empty, and an empty column
// repeated across a table reads as a fact about the row rather than as the
// absence of one.
type Retirement struct {
	ShortCode string // the retired number, E and four digits
	Note      string // one sentence, published verbatim, saying what the code named
}

// retirementRecordName is the package-level name the registry binds its
// retirement record to.
//
// This is the same class of coupling every other extraction here rests on — the
// names Register, Meta, ShortCode and Message are read the same way — so it is a
// name this generator knows rather than a shape it infers. A record that moved
// out from under the name is reported by [scanRetirements] returning nothing,
// which the caller states as a count rather than passing over in silence.
const retirementRecordName = "retiredShortCodes"

// scanRetirements reads the retirement record the errors package declares and
// returns its entries in declaration order.
//
// # Why it reads the same declaration the program does
//
// The record is the one home of which numbers are retired. The registry consults
// it to refuse a registration under a retired number; this reads that same
// declaration so the published reference can resolve such a number for a reader
// who quotes it off an old report. Neither keeps a copy — a second list to hold
// in step is what would drift, and it would drift invisibly, because the two
// would agree right up until one of them was edited.
//
// # Why it is its own walk rather than a third question inside [scan]
//
// [scan] reads two questions from one walk of the whole tree, because both are
// asked of every production file. This one is asked of exactly one directory:
// the record is declared in the package that declares Register, and nowhere
// else. Threading it through the whole-tree walk would mean testing every file
// in the module against a condition only that directory can satisfy.
//
// # An absent record is not an error
//
// A module that has retired nothing declares no record, and that is the ordinary
// state of a tree early in its life. So an absent record yields no entries and
// no error, and the caller reports the size of what it read: a record that
// vanished because the declaration moved looks exactly like a module that has
// retired nothing, and only the reported count tells them apart.
//
// # What it does refuse
//
// An entry it cannot READ — an element that is not two string literals — is
// refused by name rather than skipped, because a skipped entry is a number that
// is silently free again. The entries' SHAPE is not checked here: whether a
// short code is well formed and whether a note carries usable text are refused
// by the errors package at its own initialisation, and re-deciding either here
// would be a second implementation of one rule.
func scanRetirements(root string) ([]Retirement, error) {
	dir := filepath.Join(root, errorsPackageDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		names = append(names, e.Name())
	}
	// Sorted, because a record declared twice is reported against whichever file
	// the walk reached first, and a refusal that moves between runs cannot be
	// compared with a previous one.
	sort.Strings(names)

	var out []Retirement
	found := ""
	for _, name := range names {
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, perr)
		}
		for _, decl := range file.Decls {
			d, ok := decl.(*ast.GenDecl)
			if !ok || d.Tok != token.VAR {
				continue
			}
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || vs.Names[0].Name != retirementRecordName || len(vs.Values) != 1 {
					continue
				}
				if found != "" {
					return nil, fmt.Errorf("%s and %s both declare %s, so which numbers are retired has two answers",
						found, path, retirementRecordName)
				}
				found = path
				lit, ok := vs.Values[0].(*ast.CompositeLit)
				if !ok {
					return nil, fmt.Errorf("%s declares %s as something other than a literal record, "+
						"which this reads from the declaration rather than by evaluating the package",
						path, retirementRecordName)
				}
				for i, el := range lit.Elts {
					r, rerr := retirementEntry(el)
					if rerr != nil {
						return nil, fmt.Errorf("%s: entry %d of %s: %w", path, i, retirementRecordName, rerr)
					}
					out = append(out, r)
				}
			}
		}
	}
	return out, nil
}

// retirementEntry reads one element of the retirement record: a literal carrying
// the retired short code and the note, positionally or by field name.
//
// Both spellings are read because either is a legitimate way to write the
// declaration, and a reader who switched to field names would otherwise silently
// empty the published list. Anything else is refused rather than skipped: an
// entry this cannot read is a number that stops being reported as retired while
// still being retired.
func retirementEntry(el ast.Expr) (Retirement, error) {
	lit, ok := el.(*ast.CompositeLit)
	if !ok {
		return Retirement{}, fmt.Errorf("is not a literal, so the short code it records cannot be read")
	}
	var out Retirement
	positional := make([]string, 0, 2)
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				return Retirement{}, fmt.Errorf("names a field this cannot read")
			}
			value, ok := stringLiteral(kv.Value)
			if !ok {
				return Retirement{}, fmt.Errorf("declares %s as something other than a string literal", key.Name)
			}
			switch key.Name {
			case "shortCode":
				out.ShortCode = value
			case "note":
				out.Note = value
			default:
				return Retirement{}, fmt.Errorf("names the unknown field %s", key.Name)
			}
			continue
		}
		value, ok := stringLiteral(e)
		if !ok {
			return Retirement{}, fmt.Errorf("carries a value that is not a string literal")
		}
		positional = append(positional, value)
	}
	if len(positional) > 0 {
		if out.ShortCode != "" || out.Note != "" {
			return Retirement{}, fmt.Errorf("mixes positional and named values, so which value is the short code is ambiguous")
		}
		if len(positional) != 2 {
			return Retirement{}, fmt.Errorf("carries %d values where a short code and a note are expected", len(positional))
		}
		out.ShortCode, out.Note = positional[0], positional[1]
	}
	if out.ShortCode == "" {
		return Retirement{}, fmt.Errorf("records no short code")
	}
	if out.Note == "" {
		return Retirement{}, fmt.Errorf("records no note, so the published reference would carry the code %s "+
			"with nothing to tell a reader who quotes it", out.ShortCode)
	}
	return out, nil
}

// importMap maps each in-module import's local name to its module-relative
// directory. Out-of-module imports are dropped: a re-export can only forward to
// an identity this scan can resolve.
func importMap(file *ast.File, modulePath string) map[string]string {
	out := map[string]string{}
	for _, spec := range file.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if p != modulePath && !strings.HasPrefix(p, modulePath+"/") {
			continue
		}
		dir := strings.TrimPrefix(strings.TrimPrefix(p, modulePath), "/")
		local := lastSegment(dir)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		out[local] = dir
	}
	return out
}

// lastSegment returns the default local name of an import: its final path
// segment, which an explicit alias in the import spec overrides.
func lastSegment(dir string) string {
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		return dir[i+1:]
	}
	return dir
}

// errorsPackageAlias reports the local name the file uses for the package that
// declares Code, and whether the file IS that package — in which case the
// declarations are unqualified.
func errorsPackageAlias(imports map[string]string, pkgDir string) (string, bool) {
	if pkgDir == errorsPackageDir {
		return "", true
	}
	for local, dir := range imports {
		if dir == errorsPackageDir {
			return local, false
		}
	}
	return "", false
}

// accessor reports the exported name an accessor declares and what its body
// yields, for the two shapes that publish an identity:
//
//	func ErrX() error { return errX }              // declares, over a registration
//	func ErrX() error { return otherpkg.ErrY() }   // re-exports another package's
//
// The signature is checked rather than the name alone. A name beginning "Err"
// is not on its own evidence of an accessor — a package-level formatting or
// predicate helper can share the prefix — and it is the empty parameter list
// with a single unnamed `error` result that tells them apart.
//
// A body forwarding to another accessor in the SAME package is deliberately not
// recognised. Such a handle names an identity that is already reachable under
// its own name from the same import, so admitting it would put one condition on
// the page twice, under a second name a reader gains nothing by matching. The
// omission is stated on [scan] as a cost rather than left to be inferred here.
func accessor(fd *ast.FuncDecl, imports map[string]string) (string, accessorTarget, bool) {
	none := accessorTarget{}
	if fd.Recv != nil || fd.Body == nil || fd.Name == nil {
		return "", none, false
	}
	name := fd.Name.Name
	if !strings.HasPrefix(name, "Err") || !yieldsErrorAlone(fd.Type) {
		return "", none, false
	}
	if len(fd.Body.List) != 1 {
		return "", none, false
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return "", none, false
	}
	switch v := ret.Results[0].(type) {
	case *ast.Ident:
		return name, accessorTarget{local: v.Name}, true
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok || len(v.Args) != 0 || !strings.HasPrefix(sel.Sel.Name, "Err") {
			return "", none, false
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return "", none, false
		}
		dir, ok := imports[pkgIdent.Name]
		if !ok {
			return "", none, false
		}
		return name, accessorTarget{alias: dir + "." + sel.Sel.Name}, true
	}
	return "", none, false
}

// yieldsErrorAlone reports whether the signature takes nothing and returns a
// single unnamed error, which is the shape that distinguishes an accessor from
// any other function sharing the name prefix.
func yieldsErrorAlone(ft *ast.FuncType) bool {
	if ft.TypeParams != nil || (ft.Params != nil && len(ft.Params.List) != 0) {
		return false
	}
	if ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	result := ft.Results.List[0]
	if len(result.Names) != 0 {
		return false
	}
	id, ok := result.Type.(*ast.Ident)
	return ok && id.Name == "error"
}

// registration reports the package-local name and the declared content of
// `var errX = Register("…", Meta{ShortCode: "…", Message: "…"})`.
//
// The descriptive code is read from the call's first argument rather than from a
// type ascribed to the declaration, because the code is what the call declares:
// a registration binds an identity that carries the code, and the declaration
// itself names no type at all. The short code is read from the metadata literal
// beside the message, which is where the condition declares it.
//
// The declared-ahead note is read from the same literal, and is absent from
// nearly every registration: it is set only where a condition is declared before
// anything raises it, and it names the work that will. It is what lets such a
// condition reach the published reference at all — see [admitted].
//
// The rule place is read from the same literal and is the one key here that is
// not prose: its value is a call naming one of four constructors. Why this scan
// reads a key it carries onto no page, and what it does with an unreadable one,
// is recorded on [declaredPlace].
//
// A registered message is what makes this a registration at all — a Meta literal
// declaring no string message is a call this scan does not recognise. The short
// code is extracted but NOT part of that test, deliberately: treating a missing
// short code as "not a registration" would drop the whole identity out of the
// index silently, which is the one outcome worse than reporting it. [scan]
// refuses it by name instead, where the handle is known. The declared-ahead note
// and the rule place are outside the test for the opposite reason: their ordinary
// value is absent, so requiring either would recognise almost nothing.
//
// # Each key is read by its own arm, and none by a catch-all
//
// The arms below name every key this scan reads and skip every other, and there
// is no default arm assigning whatever is left to the message. A catch-all is the
// shape that turns a widened filter into a corrupted message: the next key added
// to the recognised set, if its arm were forgotten, would be written into the
// condition's own sentence and reported as the message with nothing failing. The
// arms cost one line each and remove that failure mode outright.
func registration(vs *ast.ValueSpec, errPkg string, isErrorsPkg bool) (string, registered, bool) {
	none := registered{}
	if len(vs.Names) != 1 || len(vs.Values) != 1 {
		return "", none, false
	}
	call, ok := vs.Values[0].(*ast.CallExpr)
	if !ok || !namesErrorsSymbol(call.Fun, "Register", errPkg, isErrorsPkg) || len(call.Args) != 2 {
		return "", none, false
	}
	code, ok := stringLiteral(call.Args[0])
	if !ok {
		return "", none, false
	}
	meta, ok := call.Args[1].(*ast.CompositeLit)
	if !ok {
		return "", none, false
	}
	out := registered{code: code}
	found := false
	for _, el := range meta.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Message":
			if value, ok := stringLiteral(kv.Value); ok {
				out.message = value
				found = true
			}
		case "ShortCode":
			if value, ok := stringLiteral(kv.Value); ok {
				out.shortCode = value
			}
		case "DeclaredAhead":
			if value, ok := stringLiteral(kv.Value); ok {
				out.declaredAhead = value
			}
		case "RulePlace":
			out.place = rulePlace(kv.Value)
		}
	}
	if !found {
		return "", none, false
	}
	return vs.Names[0].Name, out, true
}

// The four place constructors, named here because this scan reads them by name
// and resolves nothing: the value a rule-place key is given is a call, and what
// this reads is which function the call names and what literal it was handed. A
// same-named constructor from outside the errors package would be read as this
// one, which is the admission [namesErrorsSymbol] already makes for the
// registration call itself.
const (
	placeOverAuthoredInput  = "RuleOverAuthoredInput"
	placeEvaluatedElsewhere = "RuleEvaluatedElsewhere"
	placeNoStageReaches     = "RuleNoStageReaches"
	placeNotARule           = "NotARule"
)

// rulePlace reads a rule-place value, and says why it could not be read rather
// than returning a zero value that is indistinguishable from an absent key.
//
// Every unreadable arm names what is wrong in terms a reader can act on at the
// declaration, because the refusal that carries this text is the only thing
// standing between a declaration nothing can read and a tree where one reader
// sees a place and the other sees none.
func rulePlace(expr ast.Expr) declaredPlace {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return declaredPlace{unreadable: "the value is not a call to one of the four place constructors"}
	}
	var name string
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		name = fun.Sel.Name
	default:
		return declaredPlace{unreadable: "the value calls something this scan cannot name"}
	}
	switch name {
	case placeOverAuthoredInput:
		if len(call.Args) != 0 {
			return declaredPlace{unreadable: name + " takes no reason and was given one"}
		}
		return declaredPlace{constructor: name}
	case placeEvaluatedElsewhere, placeNoStageReaches, placeNotARule:
		if len(call.Args) != 1 {
			return declaredPlace{unreadable: name + " takes one reason and was given a different number"}
		}
		reason, readable := stringLiteral(call.Args[0])
		if !readable {
			return declaredPlace{unreadable: "the reason given to " + name +
				" is not a string literal at the declaration"}
		}
		if strings.TrimSpace(reason) == "" {
			return declaredPlace{unreadable: "the reason given to " + name + " says nothing"}
		}
		return declaredPlace{constructor: name, reason: reason}
	}
	return declaredPlace{unreadable: "the value calls " + name + ", which is not one of the four place constructors"}
}

// stringLiteral returns the text of a string-valued expression a registration
// declares, for the two shapes one can be written in: a single literal, and
// several joined with +.
//
// The joined form is read rather than refused because it is how a declaration
// that has grown too long for one line is written, and a declared sentence
// legitimately grows. Refusing it would be tolerable for a field whose absence
// is loud — a missing message makes [scan] refuse the handle by name — but not
// for one whose absence means something. A declared-ahead note that went
// unread would read as a condition carrying no note, which takes it off the
// published reference silently, and silence is precisely what the note exists
// to prevent.
//
// Only a concatenation of literals folds. A value naming a constant, or built
// by a call, is not read: reading it would mean evaluating the package, which
// is the thing this scan exists to avoid — and a declaration whose text is not
// in the declaration is one a reader of the source cannot see either.
func stringLiteral(expr ast.Expr) (string, bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		left, ok := stringLiteral(v.X)
		if !ok {
			return "", false
		}
		right, ok := stringLiteral(v.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// namesErrorsSymbol reports whether expr names sym in the package that declares
// Register — `cerrors.Register` from outside it, a bare `Register` from within.
func namesErrorsSymbol(expr ast.Expr, sym, errPkg string, isErrorsPkg bool) bool {
	if isErrorsPkg {
		ident, ok := expr.(*ast.Ident)
		return ok && ident.Name == sym
	}
	if errPkg == "" {
		return false
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != sym {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == errPkg
}

// importable reports whether a reader outside this module can import the
// package. Go makes an internal package structurally unreachable from outside
// the subtree that contains it, so an entry naming one advertises a match no
// reader can perform.
func importable(pkgDir string) bool {
	if pkgDir == "" {
		return false
	}
	for _, seg := range strings.Split(pkgDir, "/") {
		if seg == "internal" {
			return false
		}
	}
	return strings.Split(pkgDir, "/")[0] != "cmd"
}

// modulePathOf reads the module path from go.mod. It is read rather than
// written down here so the import prefix the page teaches has one home.
func modulePathOf(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("no module directive in %s/go.mod", root)
}
