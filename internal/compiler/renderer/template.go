// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// templateMissingKey is text/template's missing-key policy for one surface.
type templateMissingKey string

const (
	// missingKeyZero lets an absent key under an open root render empty, which is
	// what keeps a defensively-written optional key working on a value template.
	missingKeyZero templateMissingKey = "zero"
	// missingKeyError halts on an absent key. It is the policy for surfaces whose
	// output is a filesystem path or a predicate, where a silently empty value is
	// discovered long after the build rather than at it.
	missingKeyError templateMissingKey = "error"
)

// templateEvalStage names which half of one evaluation a failure came from, so a
// surface can say so in its own diagnostic.
type templateEvalStage string

const (
	stageParse   templateEvalStage = "parse"
	stageExecute templateEvalStage = "execute"
)

// templateEval is one template evaluation. Every template-bearing surface in the
// renderer runs through it, which is what makes the reference check and the
// execution unable to disagree: the tree that is walked is the tree that runs,
// and the context the references are checked against is the context the template
// is rendered against — both are scope.data, read once.
//
// The fields after the text are the only things that differ between surfaces: the
// delimiter pair, the missing-key policy, any per-surface function, and how a
// surface names a refused reference or a failed stage with its own published
// diagnostic. A surface that needs anything else needs a change here, where every
// surface sees it, rather than a second evaluator.
type templateEval struct {
	name string
	text string
	// scope carries the render context in .data and this surface's narrowing over
	// what that context type defines.
	scope templateScope
	// leftDelim and rightDelim are passed through to text/template, which reads an
	// empty delimiter as its own default.
	leftDelim  string
	rightDelim string
	// missingKey is the surface's missing-key policy; empty is missingKeyZero.
	missingKey templateMissingKey
	// numbers hands every number the context carries to the template as a number
	// rather than as its text; see numbersAsNumbers. The value surfaces set it
	// through valueTemplate, and the assertion surface in EvaluateAssertion.
	numbers bool
	// category is the authoring domain a diagnostic raised here is charged to,
	// which is what decides whether the author is sent to the Recipe or to the
	// Aggregate. Empty is cerrors.CatalogError, the Recipe-authored default every
	// surface but a developer-authored one carries. A surface stating its own
	// diagnostic through refused or failed carries the category in that
	// diagnostic instead.
	category cerrors.Category
	// extraFuncs are functions this surface adds over the shared function set. The
	// map is template.FuncMap's own underlying type, so a surface contributing one
	// does not have to reach for text/template itself.
	extraFuncs map[string]any
	// refused names a reference the scope refuses with this surface's own
	// diagnostic; nil reports ErrTemplateUnknownRoot.
	refused func(*templateScopeViolation) error
	// failed names a parse or execute failure with this surface's own diagnostic;
	// nil returns the underlying error unwrapped.
	failed func(templateEvalStage, error) error
}

func (e templateEval) run() (string, error) {
	t, refs, err := e.prepare()
	if err != nil {
		return "", err
	}
	p := preparedTemplate{eval: e, tree: t, refs: refs}
	// A context carrying the placeholders of a connection that crosses into
	// another output holds the template to copying what it reads off that
	// connection; see runCopyOnly. Every other evaluation runs exactly as below.
	if links := linkReadsOf(e.scope.data); links != nil {
		if out, checked, err := links.runCopyOnly(p); checked {
			return out, err
		}
	}
	out, err := e.execute(t, refs, false)
	if err != nil {
		return "", p.executionFailure(err)
	}
	return out, nil
}

// preparedTemplate is one evaluation whose template has been parsed and whose
// references have been checked: the evaluation, the tree to execute and the
// references it makes. It is how an evaluation hands its own execution to a step
// that runs the same template more than once over different data, so that step
// executes through this evaluator rather than holding a template of its own.
type preparedTemplate struct {
	eval templateEval
	tree *template.Template
	refs []gotmpl.RootRef
}

// executeOver runs the prepared tree over data already converted from the
// surface's context — a fresh private copy allocated for this execution alone.
func (p preparedTemplate) executeOver(data any) (string, error) {
	return p.eval.executeData(p.tree, p.refs, data, false)
}

// executionFailure reports a failed execution of the prepared tree the way the
// surface names it, withholding any text a value an author declared sensitive
// could reach.
//
// A path the template both prints and iterates while it holds nothing is refused
// before the template runs, and that refusal is reported as it stands: its text is
// the path as the template spells it and never anything the data holds, so there
// is nothing in it to withhold.
func (p preparedTemplate) executionFailure(err error) error {
	if errors.Is(err, gotmpl.ErrAbsentPathReadAndIterated()) {
		return p.eval.wrap(stageExecute, err)
	}
	return p.eval.wrap(stageExecute, p.eval.withholdFailure(p.tree, p.refs, err))
}

// runSubstituted evaluates the template once, over its data with every datum the
// surface's withheld set names replaced by the placeholder. It is the evaluation a
// display shows in place of output it cannot show as rendered, and it is the same
// parse, the same reference check and the same execution run() performs — only
// the data differs — so the two cannot disagree about what the template does.
func (e templateEval) runSubstituted() (string, error) {
	t, refs, err := e.prepare()
	if err != nil {
		return "", err
	}
	out, err := e.execute(t, refs, true)
	if err != nil {
		return "", e.wrap(stageExecute, err)
	}
	return out, nil
}

// prepare parses the template and checks its references against the surface's
// scope, returning the tree to execute and the references it makes. A parse
// failure and a refused reference are returned already named by the surface.
func (e templateEval) prepare() (*template.Template, []gotmpl.RootRef, error) {
	category := e.category
	if category == "" {
		category = cerrors.CatalogError
	}
	funcs := gotmpl.FuncMap()
	// Iterating a map is safe here where it usually would not be: the loop only
	// assigns distinct keys into another map, so the merged function set is the
	// same whatever order the keys arrive in and no iteration order can reach the
	// rendered bytes.
	for name, fn := range e.extraFuncs {
		funcs[name] = fn
	}
	t, err := template.New(e.name).
		Option("missingkey="+string(e.missingKeyPolicy())).
		Delims(e.leftDelim, e.rightDelim).
		Funcs(funcs).
		Parse(e.text)
	if err != nil {
		return nil, nil, e.wrap(stageParse, err)
	}
	// One walk of the tree serves both consumers: the admission check, and the
	// absent-leaf seeding the execution data needs. Walking twice would let the
	// two disagree about which references the template makes.
	refs := gotmpl.CollectRootRefs(t.Tree.Root)
	if v := e.scope.checkRefs(refs); v != nil {
		if e.refused != nil {
			return nil, nil, e.refused(v)
		}
		return nil, nil, templateUnknownRootErr(category, e.name, v)
	}
	return t, refs, nil
}

// missingKeyPolicy is the surface's missing-key policy, defaulted.
func (e templateEval) missingKeyPolicy() templateMissingKey {
	if e.missingKey == "" {
		return missingKeyZero
	}
	return e.missingKey
}

// execute runs a prepared tree over a fresh private copy of the surface's data,
// substituted first when substitute is set.
//
// The copy is the one the conversion allocates for this execution alone, so the
// substitution and the seeding below write into nothing the caller holds and
// nothing another surface reads. The substitution runs before the seeding, so a
// datum the author left unset is still unset when it is seeded — a template
// testing whether it was supplied takes the branch it took on the real data.
func (e templateEval) execute(t *template.Template, refs []gotmpl.RootRef, substitute bool) (string, error) {
	return e.executeData(t, refs, e.data(), substitute)
}

// data converts the surface's context into the fresh private tree one execution
// reads: the conversion toLowercaseMap performs, with every number made a number
// where the surface reads numbers as numbers. Every execution of a template reads
// its data through here, so no execution of one template can see a number where
// another execution of it sees the number's text.
func (e templateEval) data() any {
	tree := toLowercaseMap(e.scope.data)
	if e.numbers {
		tree = numbersAsNumbers(tree)
	}
	return tree
}

// executeData runs a prepared tree over data already converted from the surface's
// context — a fresh private copy the caller allocated for this execution alone —
// substituted first when substitute is set. On a surface that supplies unset
// values, a path the template both prints and iterates while it holds nothing
// halts here with gotmpl.ErrAbsentPathReadAndIterated, before the tree runs.
func (e templateEval) executeData(t *template.Template, refs []gotmpl.RootRef, data any, substitute bool) (string, error) {
	if substitute {
		e.scope.withheld.SubstituteTree(data)
	}
	if e.missingKeyPolicy() == missingKeyZero {
		// The roots the scope admits are the roots this surface defines, so a
		// root read whole that the surface defines and the data leaves nil is
		// supplied for exactly as an absent key beneath a root is.
		admitted, _ := e.scope.admittedRoots()
		if err := gotmpl.SeedAbsentLeafKeys(data, refs, admitted); err != nil {
			return "", err
		}
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// readsWithheld reports whether the references of defined, a prepared template's
// set as [definedTemplates] reads it, reach a datum the surface's withheld set
// names. Every template of the set is read — the body and any it defines —
// through the same over-collecting reference census the display's reading uses,
// answered by the set in the coordinates the data is read by. A template handing
// on the whole of its data — `toJson .`, a bare `$` — reads every datum in it, so
// it reaches whatever the set names.
func (e templateEval) readsWithheld(defined []definedTemplate) bool {
	if !e.scope.withheld.AnyWithheld() {
		return false
	}
	var refs []string
	for _, d := range defined {
		refs = append(refs, gotmpl.TreeDataReads(d.tree.Root)...)
	}
	return e.scope.withheld.ReachedBy(refs)
}

// definedTemplates reads the set a prepared template carries — the body and every
// template it defines — into the form the readers deciding what a failure may
// report take, so the template itself stays in the evaluator and those readers
// work on parse trees alone. A template the set names without a parse tree is
// left out: it has nothing to read and no fail call to find.
func definedTemplates(t *template.Template) []definedTemplate {
	var out []definedTemplate
	for _, defined := range t.Templates() {
		if defined.Tree == nil {
			continue
		}
		out = append(out, definedTemplate{name: defined.Name(), tree: defined.Tree})
	}
	return out
}

// withholdFailure decides what a failed execution may report. A template whose
// references reach no withheld datum reports its failure exactly as it failed, so
// its text is byte-identical to what it would be without any declaration.
//
// A template that reads one and failed on a fail call whose sentence is written
// out in the template reports that failure as it failed too: its text is the
// failing position and the author's sentence, both template source, so it carries
// nothing the template read — and the position, which the withheld form below
// prints as well, already spells the sentence out. Which calls those are is
// decided from the tree; see [templateEval.literalFailure].
//
// Any other failure of a template that reads one is run again, the same tree over
// its data with every withheld datum replaced by the placeholder. When that run
// fails at the same action with the same condition, its failure is the report: an
// author's own sentence survives, with the placeholder where the value stood, and
// a value the template transformed — encoded, quoted, upper-cased — is withheld
// too, because the substitution happened before the transformation. When it does
// not — the template took a different branch, or failed elsewhere, because what it
// did depended on what the value holds — the report is the failing position with
// a fixed sentence saying the failure text is withheld.
//
// Whatever the comparison decides, the text reported for any of these is never the
// real run's: it is either a run that never saw the value or a sentence that never
// quotes it. The comparison only chooses which of the two a reader gets, so an
// imprecise one costs a reader the author's sentence and never prints a value.
func (e templateEval) withholdFailure(t *template.Template, refs []gotmpl.RootRef, err error) error {
	defined := definedTemplates(t)
	if !e.readsWithheld(defined) || e.literalFailure(defined, err) {
		return err
	}
	if _, substituted := e.execute(t, refs, true); substituted != nil && sameExecutionFailure(err, substituted) {
		return substituted
	}
	return newWithheldFailure(executionPosition(err), templateFailureWithheld, err)
}

// templateFailureWithheld is the sentence a template failure carries in place of
// its own text when that text depends on a value an author declared sensitive.
const templateFailureWithheld = "failure text withheld: this template reads a value declared sensitive, " +
	"and the failure depends on what that value holds"

// decodeFailureWithheld is the sentence a failure to read a template's output
// carries in place of its own text when the output was produced from a value an
// author declared sensitive and the failure depends on it.
const decodeFailureWithheld = "the reason is withheld: the template producing this output reads a value " +
	"declared sensitive, and the failure depends on what that value holds"

// withheldFailure is a failure reported without its text: where it happened, as
// far as that can be said from the template alone, and a fixed sentence. It
// unwraps to the registered condition the real failure carried and to nothing
// else, so the condition's codes reach every surface while no text the failure
// composed from a value does.
type withheldFailure struct {
	position  string
	sentence  string
	condition error
}

// newWithheldFailure builds a withheld failure for err. position is the failing
// position as the template alone spells it, empty where none can be recovered.
// The registered condition is the first sealed identity in err's chain — a value
// whose text is the condition's registered sentence and nothing an evaluation
// composed.
func newWithheldFailure(position, sentence string, err error) error {
	f := &withheldFailure{position: position, sentence: sentence}
	var condition cerrors.Identity
	if errors.As(err, &condition) {
		f.condition = condition
	}
	return f
}

func (f *withheldFailure) Error() string {
	if f.position == "" {
		return f.sentence
	}
	return f.position + ": " + f.sentence
}

func (f *withheldFailure) Unwrap() error { return f.condition }

// executionPositionEnd closes the position text/template prefixes onto an
// execution failure: `template: <name>:<line>:<col>: executing "<name>" at
// <<action>>: `, where the action is the failing node as the template spells it.
const executionPositionEnd = ">: "

// executionPosition returns the position an execution failure reports — the
// template, the line and column, and the failing action as the template spells
// it — and the empty string where the failure carries none.
//
// Everything in it is authored template text, never data: the data an evaluation
// read only appears after it. The position is cut at the FIRST occurrence of the
// separator that closes it, so an action whose own text carries that separator is
// cut short inside the action rather than past it, which can shorten the position
// and can never reach the data that follows.
func executionPosition(err error) string {
	msg := err.Error()
	if !strings.HasPrefix(msg, "template: ") || !strings.Contains(msg, ": executing ") {
		return ""
	}
	end := strings.Index(msg, executionPositionEnd)
	if end < 0 {
		return ""
	}
	return msg[:end+1]
}

// sameExecutionFailure reports whether two failures of one template happened at
// the same action for the same reason: the same position, the same registered
// condition (or none in both), and the same leading clause after the position —
// the function a call failed in, or the kind of evaluation fault.
func sameExecutionFailure(real, substituted error) bool {
	position := executionPosition(real)
	if position == "" || position != executionPosition(substituted) {
		return false
	}
	realCode, realCoded := cerrors.CodeOf(real)
	subCode, subCoded := cerrors.CodeOf(substituted)
	if realCoded != subCoded || realCode != subCode {
		return false
	}
	return leadingClause(real.Error()[len(position):]) == leadingClause(substituted.Error()[len(position):])
}

// leadingClause returns the first clause of what follows a failure's position:
// the text up to the next separator, after the one that closes the position.
func leadingClause(rest string) string {
	rest = strings.TrimPrefix(rest, ": ")
	if i := strings.Index(rest, ": "); i >= 0 {
		return rest[:i]
	}
	return rest
}

// decodeTemplateOutput reads what a template produced into dst, reporting a
// failure to read it the way a failure to evaluate it is reported: without any
// text a value an author declared sensitive could reach.
//
// A decoder quotes the text it could not read — a scalar whole when it is short,
// its first characters when it is long — so a value interpolated where the
// document wants another type reaches the failure text. When the template's
// references reach a withheld datum, the template is evaluated again over its
// substituted data and that output is read instead; a failure there of the same
// shape is the report, with the placeholder where the value stood. Any other
// outcome reports a fixed sentence. A template reading no withheld datum reports
// the decoder's own failure, byte-identical to what it was.
//
// e is the evaluation that produced output, so the second evaluation is the same
// template over the same data with only the substitution between them.
func decodeTemplateOutput(e templateEval, output string, dst any) error {
	err := parse.TemplateOutput([]byte(output), dst)
	if err == nil {
		return nil
	}
	return withholdDecodeFailure(e, err, reflect.TypeOf(dst).Elem())
}

// withholdDecodeFailure decides what err — a failure to read e's output into a
// value of type target — may report, on the terms decodeTemplateOutput states. It
// is the half of that reading a caller decoding the output itself needs: one that
// tries more than one shape and reports a failure only once every shape failed
// still withholds that failure here, so there is one place deciding what a
// failure to read a template's output may say.
//
// target is the type err's read decoded into, because the substituted output is
// read into a fresh value of it and the two failures compared.
func withholdDecodeFailure(e templateEval, err error, target reflect.Type) error {
	t, _, perr := e.prepare()
	if perr != nil || !e.readsWithheld(definedTemplates(t)) {
		return err
	}
	substituted, serr := e.runSubstituted()
	if serr == nil {
		fresh := reflect.New(target).Interface()
		if derr := parse.TemplateOutput([]byte(substituted), fresh); derr != nil && sameDecodeFailure(err, derr) {
			return derr
		}
	}
	return newWithheldFailure("", decodeFailureWithheld, err)
}

// sameDecodeFailure reports whether two failures to read one template's output
// are the same failure apart from the text each quotes from its document: the same
// lines, the same tags, the same target types.
//
// A decoder quotes what it could not read between backquotes, so the two messages
// are compared piece by piece around them: the same number of backquotes, and the
// same text outside every closed pair. The text inside a closed pair is the quote
// and is not compared. A backquote left without a partner opens no quote, so the
// text after it is compared like the rest.
func sameDecodeFailure(real, substituted error) bool {
	r := strings.Split(real.Error(), "`")
	s := strings.Split(substituted.Error(), "`")
	if len(r) != len(s) {
		return false
	}
	last := len(r) - 1
	for i := range r {
		quoted := i%2 == 1 && i < last
		if !quoted && r[i] != s[i] {
			return false
		}
	}
	return true
}

func (e templateEval) wrap(stage templateEvalStage, err error) error {
	if e.failed == nil {
		return err
	}
	return e.failed(stage, err)
}

// EvalTemplate evaluates a Go template string with the default `{{ }}`
// delimiters, Sprig functions, and missingkey=zero. Thin wrapper over
// EvalTemplateWithDelims hard-coding the default delimiter pair so callers
// outside the artifacts-engine-dispatch path don't carry the delimiter
// noise.
//
// The input struct is walked reflectively and converted to a tree of plain
// Go values (maps / slices / scalars) with lowercase JSON-tag keys, so
// template authors write `.values.foo` matching the
// template-context-v1beta1.schema.json contract.
//
// OrderedMap values are unwrapped during the walk into plain map[string]any
// containers. Text/template then iterates them in alphabetical key order
// (Go stdlib default). Author-order preservation across the JSON-
// serialization boundary (IR, --trace, Wasm payloads) lives in
// OrderedMap.MarshalJSON; the template-iteration layer is intentionally
// alphabetical to match Helm-community authoring expectations.
//
// withheld names the data an author declared sensitive, in the coordinates the
// template reads data by; a failure whose text could carry such a value is
// reported without it. A caller rendering data no declaration governs passes an
// empty set.
func EvalTemplate(name, tmplText string, data any, withheld redact.Keys) (string, error) {
	return EvalTemplateWithDelims(name, tmplText, data, "{{", "}}", withheld)
}

// EvalTemplateDecoded evaluates a Go template exactly as EvalTemplate does and
// reads the YAML it produced into dst, so a caller reading a template's output
// as a document reports a failure to read it on the same terms a failure to
// evaluate it is reported. The two errors are returned apart because a caller
// names them apart: evalErr is a failure to evaluate the template, decodeErr a
// failure to read what it produced.
func EvalTemplateDecoded(name, tmplText string, data any, withheld redact.Keys, dst any) (output string, evalErr, decodeErr error) {
	return valueTemplate(name, tmplText, templateScope{}.with(data, withheld), "{{", "}}").runDecoded(dst)
}

// runDecoded runs the evaluation and reads the YAML it produced into dst, on the
// terms EvalTemplateDecoded states.
func (e templateEval) runDecoded(dst any) (output string, evalErr, decodeErr error) {
	output, evalErr = e.run()
	if evalErr != nil {
		return "", evalErr, nil
	}
	return output, nil, decodeTemplateOutput(e, output, dst)
}

// evalValuesMapping evaluates a values mapping — a Recipe's, or an attachment's
// FeatureRecipe's — exactly as EvalTemplateDecoded does, and reports whether
// what it produced depends on a value an author declared sensitive: the marking
// the provider guest receives with the values, which keeps every one of them out
// of a refusal or a warning and has a guest refuse a literal it would otherwise
// write. The output's keys are the mapping's own invention, so this is the only
// marking its values can carry.
//
// Two readings of withheld take part. The static first filter reads the plugin
// marking of withheld (withheld.PluginMarking, which leaves out what the set names
// inside lists): a mapping whose references reach nothing that marking names, as
// templateReadsWithheld reads them, is unmarked and evaluated once, so a mapping
// reading only list entries is never flagged. A flagged mapping is evaluated twice
// more, once over each text redact.SubstitutionTexts returns, with every datum the
// whole of withheld names replaced — list entries and the locals computed from them
// included — and is marked where either evaluation fails or produces text differing
// from the reference in any byte; see templateEval.outputDependsOn.
//
// So a mapping that reads an object holding a declared leaf and prints only its
// keys is unmarked. One that only tests whether a declared value is set is
// unmarked where the value is one a template reads as true; both texts read as
// true, so the same test over a value a template reads as false (an empty text,
// false, 0, null, an empty list or an empty map) marks it. One that
// prints a declared value is marked. One that prints a property of a declared
// value, or branches on it, is marked where either text gives that property or
// branch differently from the value — a printed length always, since the texts
// differ in length — and unmarked where both give it as the value does.
//
// The substituted evaluations feed only the marking: what the build renders is
// the real evaluation's output, decoded into dst, whatever the marking decides.
// The marking is decided after that output decoded, so a failure is reported
// exactly as EvalTemplateDecoded reports it.
func evalValuesMapping(name, tmplText string, data any, withheld redact.Keys, dst any) (marked bool, evalErr, decodeErr error) {
	e := valueTemplate(name, tmplText, templateScope{}.with(data, withheld), "{{", "}}")
	output, evalErr, decodeErr := e.runDecoded(dst)
	if evalErr != nil || decodeErr != nil {
		return false, evalErr, decodeErr
	}
	if !templateReadsWithheld(tmplText, withheld.PluginMarking()) {
		return false, nil, nil
	}
	return e.outputDependsOn(withheld, output), nil, nil
}

// outputDependsOn reports whether the evaluation's output depends on a datum
// refusal names, refusal being the evaluation's whole refusal set, what it names
// inside lists included (the static first filter that decides whether to ask is
// evalValuesMapping's, over the plugin marking). The template is executed once
// over each replacement text redact.SubstitutionTexts returns, each time over a
// fresh private copy of its data with every datum refusal names replaced by that
// text before the execution supplies absent leaves, and the output depends on
// such a datum where an execution fails or its text differs from reference in any
// byte. The texts differ from each other in length and share no character, so a printed
// length, a character printed from a position both texts have, or a value equal
// to one text changes the output over at least one of them.
//
// reference is the real evaluation's output. Where the data carries the
// placeholders of a connection that crosses into another output, the real
// evaluation rewrote each one it copied into the form a provider locates (see
// crossBoundaryLinks.runCopyOnly), so the substituted text is compared instead
// with one execution over the same data unsubstituted, and a failure of that
// execution marks.
//
// Every execution is the evaluation's own — the same parse, reference check,
// missing-key policy, number handling and functions — through a copy whose
// withheld set is refusal, so what the comparison reads differs from the real
// evaluation in the substituted data alone. No output or failure of these
// executions is reported or kept.
//
// The answer is fail-safe in the direction a marking needs: anything that keeps
// the comparison from showing the output unchanged marks it. What it cannot
// see is a branch or property both texts give the real value's outcome for,
// which the output then carries without the value's text.
func (e templateEval) outputDependsOn(refusal redact.Keys, reference string) bool {
	sub := e
	sub.scope.withheld = refusal
	t, refs, err := sub.prepare()
	if err != nil {
		return true
	}
	if linkReadsOf(sub.scope.data) != nil {
		unsubstituted, err := sub.executeData(t, refs, sub.data(), false)
		if err != nil {
			return true
		}
		reference = unsubstituted
	}
	for _, text := range redact.SubstitutionTexts() {
		data := sub.data()
		sub.scope.withheld.SubstituteTreeWith(data, text)
		out, err := sub.executeData(t, refs, data, false)
		if err != nil || out != reference {
			return true
		}
	}
	return false
}

// evalSubstitutedTemplate evaluates a Go template once, over its data with every
// datum the withheld set names replaced by the placeholder — the evaluation a
// display shows in place of output it cannot show as rendered.
func evalSubstitutedTemplate(name, tmplText string, data any, withheld redact.Keys) (string, error) {
	return valueTemplate(name, tmplText, templateScope{}.with(data, withheld), "{{", "}}").runSubstituted()
}

// EvalTemplateWithDelims evaluates a Go template with caller-controlled
// delimiters. Used by Phase B.3 when the engine satisfies
// ArtifactsTemplateDelims and returns alternates (e.g., helm-chart's
// "[[", "]]") so Aggrega's pre-templating pass leaves a downstream
// templating pass's `{{ }}` syntax untouched.
//
// Caller passes the explicit delimiter pair every time — there is no
// empty-string sentinel. The default-delimiter path is the EvalTemplate
// thin wrapper above.
//
// Every template evaluated here is checked against the root names its context
// type defines before it executes: a reference to a root the context does not
// define halts with ErrTemplateUnknownRoot instead of resolving to nothing. A
// missing KEY under a defined root is unaffected and still follows
// missingkey=zero — the read renders nothing, so an optional-key idiom keeps
// working; what cannot happen is a fallback wrapper turning an unknown ROOT into
// an empty value.
func EvalTemplateWithDelims(name, tmplText string, data any, leftDelim, rightDelim string, withheld redact.Keys) (string, error) {
	// The value-template surface declares no narrowing: every root the context
	// type defines is reachable from a template rendered against it.
	return evalValueTemplate(name, tmplText, templateScope{}.with(data, withheld), leftDelim, rightDelim)
}

// evalValueTemplate is the value-template evaluation, taking the surface's scope
// rather than only the data it renders against. It exists for the value surfaces
// that admit less than their context type defines: a scope-bearing caller states
// its narrowing and gets the same missing-key policy, the same diagnostic wording
// and the same category as every other value template, so a narrowing does not
// cost the surface its own evaluator.
func evalValueTemplate(name, tmplText string, scope templateScope, leftDelim, rightDelim string) (string, error) {
	return valueTemplate(name, tmplText, scope, leftDelim, rightDelim).run()
}

// valueTemplate is the one construction of a value-template evaluation, shared
// by the evaluation itself, by the second evaluation a failure to read its output
// may need, by the evaluations that decide whether a values mapping's output
// depends on a declared value, and by the self-inject surface, so all of them are
// the same template on the same terms.
//
// A value template reads every number as a number. A number an author wrote
// reaches it as a number — see numbersAsNumbers — and its comparisons are the
// ones the assertion surface reads, gotmpl.AssertionComparisons: two numbers
// compare by value whatever kind carries them, so an integer compares with a
// decimal, and a number never compares with text.
func valueTemplate(name, tmplText string, scope templateScope, leftDelim, rightDelim string) templateEval {
	return templateEval{
		name:       name,
		text:       tmplText,
		scope:      scope,
		leftDelim:  leftDelim,
		rightDelim: rightDelim,
		missingKey: missingKeyZero,
		numbers:    true,
		extraFuncs: gotmpl.AssertionComparisons(),
		failed: func(stage templateEvalStage, err error) error {
			return cerrors.Wrap(valueTemplateRunFailure(stage, err), cerrors.CatalogError,
				cerrors.SourceLocation{File: name}, fmt.Sprintf("template %q %s", name, stage))
		},
	}
}

// valueTemplateRunFailure joins a value template's failed run to the condition
// such a run carries, ErrValueTemplateExecutionFailed, keeping the engine's
// failure as the cause beneath it. Every value surface's failure wrap calls it, so
// the condition is decided once rather than by each surface.
//
// A failed parse is returned as it is, because the condition names a template
// that ran. So is a failure already naming a condition: the condition read off a
// chain is the first one found, and joining this one ahead of it would report the
// specific fault as the general one.
func valueTemplateRunFailure(stage templateEvalStage, err error) error {
	if _, named := cerrors.CodeOf(err); stage != stageExecute || named {
		return err
	}
	return fmt.Errorf("%w: %w", ErrValueTemplateExecutionFailed(), err)
}

// pathTemplateDirectoryScope is the reference set a `directory:` path template is
// checked against. The universe is PathTemplateContext — the type both path
// templates are evaluated against — so the variables the check admits are derived
// from that type rather than enumerated here, and a variable added to or retired
// from the context reaches both surfaces with no edit at either.
//
// The surface declares one narrowing, and it is a deliberately kept exclusion:
// .component_id names a single component, while a `directory:` template is
// evaluated once for the whole boundary. Admitting it would make the emitted path
// depend on which component happened to be evaluated first, so the reference is
// refused rather than resolved to an arbitrary one.
var pathTemplateDirectoryScope = templateScope{
	data:    PathTemplateContext{},
	exclude: []string{"component_id"},
}

// pathTemplateSubDirectoryScope is the reference set a `subDirectory:` path
// template is checked against: the same context type with no narrowing. A
// subDirectory segment is evaluated once per component, so .component_id names
// exactly the component being placed.
var pathTemplateSubDirectoryScope = templateScope{
	data: PathTemplateContext{},
}

// pathTemplateSubDirectoryField is the authored key a `subDirectory:` template is
// written under, and the string the evaluation selects that surface's scope by.
//
// One constant because the two are one thing rather than two that agree: the
// field a rule is invoked for and the surface an evaluation resolves against are
// the same authored key, and spelling it twice would let a rule read one surface
// while the render evaluated the other.
const pathTemplateSubDirectoryField = "subDirectory"

// pathTemplateDirectoryField is the authored key a `directory:` template is
// written under, for the reason the constant above states.
const pathTemplateDirectoryField = "directory"

// pathTemplateScopeFor is the reference set one path-template surface admits,
// selected by the authored field the template was written under.
//
// It is the ONE home of that selection, and both surfaces route through it: the
// evaluation the render performs when it reaches a template, and the rules
// declared over the same authored text. Two readings of which surface a field
// belongs to would be free to check a template against a set the render never
// applies to it, and the disagreement would surface as a build refusing something
// it then rendered — or rendering something it claimed to refuse.
//
// Anything other than the sub-directory field selects the directory scope, which
// is the narrower of the two. A caller naming a surface this function does not
// know is therefore held to the stricter set rather than the looser one, so an
// unrecognised name cannot widen what a template may reach.
func pathTemplateScopeFor(field string) templateScope {
	if field == pathTemplateSubDirectoryField {
		return pathTemplateSubDirectoryScope
	}
	return pathTemplateDirectoryScope
}

// BoundaryPathTemplateField is one authored path template on a boundary: the
// field it was written under, and the text as written.
//
// The two travel together because a surface deriving one from the other would be
// free to differ from this one. The field is what a diagnostic quotes, what
// positions a finding inside the Release document, and what selects the
// reference set the text is checked against; the text is what that check parses.
type BoundaryPathTemplateField struct {
	// Field is the authored key the template was written under, which is also the
	// label an author reads it back by. It is a single key at the boundary's own
	// level rather than a composed label, so it spells its own locator segment.
	Field string

	// Text is the template as written, before anything is evaluated.
	Text string
}

// BoundaryPathTemplateFields presents every path template one boundary declares,
// as the one enumeration of that set.
//
// It is one home because three surfaces need the same answer: the axis a declared
// rule over these fields is invoked along, the presentation that hands one field's
// authored text to such a rule, and any caller asking which fields a boundary
// wrote. Two enumerations would be free to differ on which fields they carry, and
// a field either of them omitted would be silently unchecked with nothing
// reporting it.
//
// A field the boundary leaves empty contributes nothing, which is the honest
// reading rather than a filter: the operator authored no template there, and what
// the render evaluates in its place is a default this package supplies. A rule
// invoked over a compiler-supplied default would report a fault against text
// nobody wrote.
//
// The order is fixed — the whole-boundary field before the per-component one —
// so a caller reporting the first offending field reports the same field on every
// run.
func BoundaryPathTemplateFields(b types.Boundary) []BoundaryPathTemplateField {
	var out []BoundaryPathTemplateField
	if b.Directory != "" {
		out = append(out, BoundaryPathTemplateField{Field: pathTemplateDirectoryField, Text: b.Directory})
	}
	if b.SubDirectory != "" {
		out = append(out, BoundaryPathTemplateField{Field: pathTemplateSubDirectoryField, Text: b.SubDirectory})
	}
	return out
}

// pathTemplateFault names what one authored path template is refused for, or
// records that every reference it makes is admitted.
type pathTemplateFault int

const (
	// pathTemplateAdmitted is a template the surface admits every reference of.
	pathTemplateAdmitted pathTemplateFault = iota

	// pathTemplateUnparsed is text that is not a template at all. It is a failure
	// to READ the field rather than a verdict on what it says, which is why it is
	// a class of its own: naming a refused reference for one would describe the
	// author's mistake wrongly.
	pathTemplateUnparsed

	// pathTemplateInvalidField is a template naming a variable this surface does
	// not admit at the root: one the context type does not define at all, or
	// .component_id in a `directory:` template, where a single component's id
	// would make the emitted path depend on evaluation order.
	pathTemplateInvalidField

	// pathTemplateUnknownKey is a template naming a key the referenced variable's
	// own type does not define — a mistyped key beneath a closed variable, which
	// would otherwise render as an empty path segment.
	pathTemplateUnknownKey
)

// pathTemplateRef is one field's classification: the fault it carries, the
// refused reference where there is one, and the parse failure where the text
// could not be read at all.
type pathTemplateRef struct {
	Fault     pathTemplateFault
	Violation *templateScopeViolation
	ParseErr  error
}

// pathTemplateFaultOf is the ONE mapping from a refused path-template reference
// to the fault that describes it.
//
// Both surfaces route through it: the halt the evaluation raises when it reaches
// a template, and the rules declared over the same authored text. Two readings of
// this split would be free to disagree about which identity describes one
// refusal, and the disagreement would surface as one author being told two
// different things about one template depending on which surface caught it.
//
// The split is on the nesting the refusal happened at, because that is what the
// two identities genuinely distinguish: a refused ROOT is a variable this surface
// does not offer, which the path-template halt is published for, while a refused
// KEY beneath an admitted variable is the general unknown-reference halt every
// template surface reports for a mistyped key.
func pathTemplateFaultOf(v *templateScopeViolation) pathTemplateFault {
	if v.Level == scopeLevelKey {
		return pathTemplateUnknownKey
	}
	return pathTemplateInvalidField
}

// classifyPathTemplate decides what one authored path template is refused for,
// against the reference set the field's own surface admits.
//
// It parses the text where the evaluation shares one walk between its admission
// check and the data it seeds — the same rule over the same collection, differing
// only in who performed the walk. Both route the verdict through
// [pathTemplateFaultOf], so what the two can disagree about is only which fields
// they were asked over.
func classifyPathTemplate(field, text string) pathTemplateRef {
	tree, err := gotmpl.ParseTree(text, "{{", "}}")
	if err != nil {
		return pathTemplateRef{Fault: pathTemplateUnparsed, ParseErr: err}
	}
	v := pathTemplateScopeFor(field).check(tree)
	if v == nil {
		return pathTemplateRef{Fault: pathTemplateAdmitted}
	}
	return pathTemplateRef{Fault: pathTemplateFaultOf(v), Violation: v}
}

// EvalPathTemplate evaluates a Release.spec.boundaries[].directory or
// subDirectory template. Differs from EvalTemplate in two load-bearing ways:
//
//   - missingkey=error policy. A defined variable with no value at eval time
//     produces an explicit error rather than rendering empty as EvalTemplate's
//     missingkey=zero would. Path templates use stricter missing-key handling
//     than value templates because a silently empty path segment produces broken
//     filesystem layout that operators only discover post-build.
//   - The reference check refuses a variable this surface does not admit before
//     the template executes, reporting ErrPathTemplateInvalidField with the
//     offending reference and the boundary name.
//
// contextKind selects the surface: "subDirectory" admits .component_id, and any
// other value — "directory" included — selects the narrower `directory:` scope.
//
// boundaryName is named in the diagnostic when a reference is refused, so the
// operator can locate the offending template without searching the Release file.
func EvalPathTemplate(name, contextKind, boundaryName, tmplText string, data PathTemplateContext) (string, error) {
	scope := pathTemplateScopeFor(contextKind)
	return templateEval{
		name: name,
		text: tmplText,
		// A path template reads a boundary's identity, the Release's parameters and
		// the bound Recipe's identity — none of it a pillar an entity's schema
		// declares sensitive — so no declaration governs its data.
		scope:      scope.with(data, redact.Keys{}),
		missingKey: missingKeyError,
		refused: func(v *templateScopeViolation) error {
			return pathTemplateRefusedErr(boundaryName, v)
		},
		failed: func(stage templateEvalStage, err error) error {
			return cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: name},
				fmt.Sprintf("path template %q %s", name, stage))
		},
	}.run()
}

// pathTemplateRefusedErr names a refused path-template reference with the
// diagnostic that describes it. A variable the surface does not admit — one the
// context type does not define at all, or .component_id in a `directory:`
// template — reports the path-template halt the language reference documents for
// this surface. A key refused UNDER a defined variable is the general
// unknown-reference halt instead, the same one every other template surface
// reports for a mistyped key, so neither diagnostic tells the author their
// reference is something it is not.
//
// Which of the two a refusal is routes through [pathTemplateFaultOf], the same
// mapping the rules declared over these templates read, so the identity an author
// is told cannot depend on which surface caught the reference.
func pathTemplateRefusedErr(boundaryName string, v *templateScopeViolation) error {
	if pathTemplateFaultOf(v) == pathTemplateUnknownKey {
		return templateUnknownRootErr(cerrors.CatalogError, boundaryName, v)
	}
	return pathTemplateInvalidFieldErr(boundaryName, v)
}

// pathTemplateInvalidFieldErr states the halt for a path template naming a
// variable this surface does not admit, in the terms that describe it.
//
// It is one home because two surfaces state it: the halt the evaluation raises,
// and the detail the rule declared over the same admission carries. The rule
// raises an objection rather than this error, so what the two share is the
// wording and the identity rather than the value — which is why the reference and
// the boundary are the only things it is given.
func pathTemplateInvalidFieldErr(boundaryName string, v *templateScopeViolation) error {
	inner := fmt.Errorf("%w: boundary %q references unknown field %s; allowlist: see aggrega-docs/language-reference/path-template-variables.md",
		ErrPathTemplateInvalidField(), boundaryName, v.Ref.Path)
	return cerrors.Wrap(inner, cerrors.OpsError, cerrors.SourceLocation{}, inner.Error())
}

// orderedMapForTemplate is satisfied by types.OrderedMap[V] for every V via
// its value-receiver TemplateAlphabeticalMap method. The walker uses this
// interface to detect OrderedMap values without depending on the generic
// type parameter.
type orderedMapForTemplate interface {
	TemplateAlphabeticalMap() map[string]any
}

// templateUntaggedKeys is satisfied by a context type carrying data that no struct
// tag exposes but whose template view must include it anyway. The struct walk
// merges the returned keys over the tag-derived ones, so such a type keeps every
// tag-driven field exactly as it was and gains only what the tags hide — which is
// what lets one type declare a template projection without a second surface beside
// its declared fields.
type templateUntaggedKeys interface {
	TemplateUntaggedKeys() map[string]any
}

// toLowercaseMap walks an arbitrary Go value and produces a representation
// shaped for Go text/template consumption. It is what makes the render context
// readable the way a Recipe author writes it: templates address fields by their
// lowercase JSON-tag names — .context.values, .context.infra and so on, exactly
// as template-context-v1beta1.schema.json names them — while the Go types
// behind them use exported field names. This walker is the one place that
// translation happens.
//
// It is also the boundary at which author order is handed off. Author order is
// load-bearing for every consumer that serializes the context (the IR, trace
// output, plugin payloads), so types.OrderedMap keeps its shape all the way
// here; at this boundary it becomes a plain map, which text/template then ranges
// over in alphabetical key order. That handoff is deliberate, not incidental:
// ordered-semantic data is modelled where the shape is declared, never by
// relying on the order a template happens to iterate in.
//
// Conversion rules:
//   - Struct fields: reflected by JSON tag (first token before `,`).
//     `json:"-"` skips the field. No tag falls back to the lowercased field
//     name. `omitempty` and `omitzero` are treated alike — either one omits the
//     field when it is zero, and for a struct that declares IsZero() that
//     method decides.
//   - A struct declaring TemplateUntaggedKeys additionally contributes those
//     keys alongside its tag-derived ones, at the same level and addressed the
//     same way. It is how a type whose own (un)marshal methods route keys into
//     an untagged field — the custom mechanisms of the delivery pipe and of the
//     authored inject block are the cases — reaches a template, since this walk
//     reads tags and never a marshaller. A tag-derived key wins on collision.
//   - types.OrderedMap[V] (detected via orderedMapForTemplate interface):
//     unwrapped to map[string]any via TemplateAlphabeticalMap, then values
//     recursively walked — so a nested OrderedMap is unwrapped at every level
//     and every level iterates alphabetically at the template boundary.
//   - map[K]V where K is string-convertible: walked recursively, emitted
//     as map[string]any.
//   - Slices / arrays: walked element-wise, emitted as []any.
//   - Pointers / interfaces: dereferenced, then walked.
//   - Scalars (bool, numeric, string): passed through unchanged.
func toLowercaseMap(data any) any {
	if data == nil {
		return nil
	}
	return walkForTemplate(reflect.ValueOf(data))
}

func walkForTemplate(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}

	// Peel off pointers and interfaces uniformly.
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil
	}

	// OrderedMap detection — via value-receiver marker interface. Check both
	// the value type and its pointer type to catch both addressable values
	// and pointer-held copies.
	if v.CanInterface() {
		if om, ok := v.Interface().(orderedMapForTemplate); ok {
			raw := om.TemplateAlphabeticalMap()
			out := make(map[string]any, len(raw))
			for k, inner := range raw {
				out[k] = walkForTemplate(reflect.ValueOf(inner))
			}
			return out
		}
	}
	if v.CanAddr() && v.Addr().CanInterface() {
		if om, ok := v.Addr().Interface().(orderedMapForTemplate); ok {
			raw := om.TemplateAlphabeticalMap()
			out := make(map[string]any, len(raw))
			for k, inner := range raw {
				out[k] = walkForTemplate(reflect.ValueOf(inner))
			}
			return out
		}
	}

	switch v.Kind() {
	case reflect.Struct:
		return walkStruct(v)
	case reflect.Map:
		return walkMap(v)
	case reflect.Slice, reflect.Array:
		return walkSlice(v)
	default:
		// Scalars (bool, numeric, string, etc.) and anything else passes
		// through as-is. CanInterface guards against unexported-field
		// reflection panics in edge cases.
		if v.CanInterface() {
			return v.Interface()
		}
		return nil
	}
}

func walkStruct(v reflect.Value) map[string]any {
	t := v.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, omitEmpty, skip := fieldJSONName(field)
		if skip {
			continue
		}
		fv := v.Field(i)
		if omitEmpty && isEmptyForTemplate(fv) {
			continue
		}
		out[name] = walkForTemplate(fv)
	}
	mergeUntaggedKeys(v, out)
	return out
}

// mergeUntaggedKeys adds a type's untagged template keys to its walked field set.
//
// Iterating the returned map is safe here where it usually would not be: the loop
// only assigns distinct keys into another map, so the merged set is identical
// whatever order the keys arrive in and no iteration order can reach the rendered
// bytes. Author order is carried by the provider's own ordered iteration, which is
// where it is load-bearing.
//
// A tag-derived key wins over an untagged one. The two sets cannot collide today —
// a type routing keys into an untagged field dispatches the tagged names into their
// own fields first, so a name with a field can never also arrive here — and the
// guard states that precedence rather than resolving a live conflict.
func mergeUntaggedKeys(v reflect.Value, out map[string]any) {
	src := untaggedKeysOf(v)
	if src == nil {
		return
	}
	for k, val := range src.TemplateUntaggedKeys() {
		if _, taken := out[k]; taken {
			continue
		}
		out[k] = walkForTemplate(reflect.ValueOf(val))
	}
}

// untaggedKeysOf returns the value as a templateUntaggedKeys, or nil when its type
// declares no such method — by the value's own method set, or by its pointer's
// where the value is addressable. It is the one answer to whether a value carries
// keys no struct tag exposes, read both by the walk that delivers those keys to a
// template and by the reference check that admits them, so a key the walk delivers
// is a key the check admits.
func untaggedKeysOf(v reflect.Value) templateUntaggedKeys {
	if v.CanInterface() {
		if u, ok := v.Interface().(templateUntaggedKeys); ok {
			return u
		}
	}
	if v.CanAddr() && v.Addr().CanInterface() {
		if u, ok := v.Addr().Interface().(templateUntaggedKeys); ok {
			return u
		}
	}
	return nil
}

func walkMap(v reflect.Value) map[string]any {
	out := make(map[string]any, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		k := iter.Key()
		var keyStr string
		switch k.Kind() {
		case reflect.String:
			keyStr = k.String()
		default:
			// Non-string keys: format via fmt to preserve behavior. Rare in
			// practice — compiler TemplateContext maps are all string-keyed.
			keyStr = fmt.Sprint(k.Interface())
		}
		out[keyStr] = walkForTemplate(iter.Value())
	}
	return out
}

func walkSlice(v reflect.Value) []any {
	if v.Kind() == reflect.Slice && v.IsNil() {
		return nil
	}
	out := make([]any, v.Len())
	for i := 0; i < v.Len(); i++ {
		out[i] = walkForTemplate(v.Index(i))
	}
	return out
}

// fieldJSONName returns the emission-side name of a struct field per its
// json tag, plus whether `omitempty`/`omitzero` is declared, plus whether
// the field should be skipped entirely (`json:"-"`). Falls back to the
// lowercased field name when the struct has no json tag.
func fieldJSONName(f reflect.StructField) (name string, omitEmpty bool, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	if tag == "" {
		return strings.ToLower(f.Name), false, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = strings.ToLower(f.Name)
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" || opt == "omitzero" {
			omitEmpty = true
		}
	}
	return name, omitEmpty, false
}

// isEmptyForTemplate matches encoding/json's omitempty semantics plus the
// omitzero semantics, so a field elides from the template view under the same
// conditions it would elide from the context's JSON form.
func isEmptyForTemplate(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	case reflect.Struct:
		// Honor IsZero() method if present (matches omitzero semantics for
		// types.OrderedMap and any other type implementing it).
		if v.CanInterface() {
			if z, ok := v.Interface().(interface{ IsZero() bool }); ok {
				return z.IsZero()
			}
		}
		if v.CanAddr() && v.Addr().CanInterface() {
			if z, ok := v.Addr().Interface().(interface{ IsZero() bool }); ok {
				return z.IsZero()
			}
		}
		return false
	}
	return false
}
