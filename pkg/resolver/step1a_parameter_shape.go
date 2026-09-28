// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
)

// refuseMalformedParameterContract is the one pass over a parameter contract's own
// shape that runs in code when the contract loads. It walks every declaration in
// author order — each parameter, then every sub-contract the parameter nests under
// properties:, items: and valuesType:, at any depth — and refuses what the schema
// cannot express.
//
// What the schema can express stays the schema's. Which keys a declaration may
// carry, the type each key takes and the floor under a length bound are all
// checked by the envelope validation that runs just before this pass, so a rule
// belongs here only when no schema keyword states it. A pattern: is that case: a
// schema can say the value is a string but not that the string compiles. So every
// pattern is compiled here, when the contract loads, and a broken one is refused
// whether or not any Release supplies the parameter; the per-value check later
// matches against the expression compiled here rather than compiling its own.
//
// A format: name is held here too, for a narrower reason. The schema closes the
// name on a parameter's own declaration, but it does not describe a sub-contract
// nested in one, so a name under properties:, items: or valuesType: reaches this
// pass unjudged. Every name is therefore held to the supported set at every depth;
// at a parameter's own declaration that repeats what the envelope already refused,
// and is reached only by a contract that did not come through the envelope.
//
// A declaration that does not decode is passed over rather than refused here. The
// value check owns that refusal and reports it naming the level it was decoding —
// a parameter's own declaration on every build, a nested one when a value for it
// arrives — so refusing it here as well would report one fault twice, and would
// halt an Aggregate whose malformed sub-contract no Release ever reaches.
//
// Every refusal found is returned together, in author order, so one load reports
// every broken pattern and unsupported format in the contract rather than the
// first.
func refuseMalformedParameterContract(contract types.OrderedMap[any]) error {
	var verrs []*ResolverError
	for _, key := range contract.Keys() {
		entry, _ := contract.Get(key)
		verrs = appendParameterShapeRefusals(verrs, key, entry)
	}
	if len(verrs) == 0 {
		return nil
	}
	return aggregateParameterViolations(verrs)
}

// appendParameterShapeRefusals checks one declaration, then descends into the
// sub-contracts it nests. path is the declaration's place in the contract as the
// author wrote it — the parameter's name, then each nesting key and child name —
// so a refusal points at the key to open.
func appendParameterShapeRefusals(verrs []*ResolverError, path string, entry any) []*ResolverError {
	pc, err := types.DecodeParameterContract(entry)
	if err != nil {
		return verrs
	}
	if pc.Pattern != "" {
		if _, perr := compileParameterPattern(pc.Pattern); perr != nil {
			verrs = append(verrs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s.pattern", path),
				Message: patternDoesNotCompile(path, pc.Pattern, perr),
				Err:     ErrParameterContractViolation(),
			})
		}
	}
	if pc.Format != "" {
		if ferr := stringformat.CheckName(pc.Format); ferr != nil {
			verrs = append(verrs, &ResolverError{
				Entity:  "Aggregate",
				Field:   fmt.Sprintf(".spec.parameters.%s.format", path),
				Message: formatNotSupported(path, ferr),
				Err:     ErrParameterContractViolation(),
			})
		}
	}
	for _, child := range pc.Properties.Keys() {
		sub, _ := pc.Properties.Get(child)
		verrs = appendParameterShapeRefusals(verrs, path+".properties."+child, sub)
	}
	if pc.Items != nil {
		verrs = appendParameterShapeRefusals(verrs, path+".items", pc.Items)
	}
	if pc.ValuesType != nil {
		verrs = appendParameterShapeRefusals(verrs, path+".valuesType", pc.ValuesType)
	}
	return verrs
}

// patternDoesNotCompile is the one wording of a parameter pattern that is not a
// valid regular expression, naming where it was declared, the pattern as written
// and the reason it does not compile.
func patternDoesNotCompile(path, pattern string, err error) string {
	return fmt.Sprintf("parameter %q pattern %q does not compile: %v", path, pattern, err)
}

// formatNotSupported is the one wording of a parameter declaring a format name
// outside the supported set, naming where it was declared; err is the checkers'
// own refusal, which names the format and lists every supported one.
func formatNotSupported(path string, err error) string {
	return fmt.Sprintf("parameter %q: %v", path, err)
}

// parameterPatterns holds every parameter pattern compiled in this process,
// keyed by the pattern's text, and is what makes the compile at contract load the
// only compile a pattern gets: the value check asks for the same text and is
// handed the expression already compiled.
//
// It holds only patterns that compiled. It is never evicted; it grows with the
// distinct patterns the process has read, which authored contracts keep small,
// and a compiled expression is safe for concurrent use.
var parameterPatterns sync.Map

// compileParameterPattern returns the compiled form of a parameter pattern,
// compiling it on first use. It is the only place a parameter pattern is
// compiled, so the contract load that refuses a broken pattern and the value check
// that matches against a good one cannot come to read the same text differently.
func compileParameterPattern(pattern string) (*regexp.Regexp, error) {
	if re, ok := parameterPatterns.Load(pattern); ok {
		return re.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	stored, _ := parameterPatterns.LoadOrStore(pattern, re)
	return stored.(*regexp.Regexp), nil
}
