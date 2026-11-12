// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Negative-authoring corpus — a parameter contract that constrains nothing. Drives
// each shape through the REAL build pipeline (pipelinetest.BuildE) on the minimal
// workspace the build corpus uses, with no Release value for the parameter at
// fault, and asserts the gate's own identifier: the envelope condition for a
// negative length bound, which the schema refuses when the Aggregate loads, and the
// parameter-contract condition for a pattern that does not compile, which the
// contract load refuses. TestNegativeCorpusParameterContract_WellFormedBuilds is
// the positive control over the same keys, well formed at every nesting, with
// Release values those patterns then judge.
package compiler_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// contractCorpusAggregate is the base Aggregate with parameters (the indented
// body of spec.parameters:) declared ahead of its one component.
func contractCorpusAggregate(parameters string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  parameters:
` + parameters + `  components:
    api:
      type: core/server
      values:
        port: 8080
`
}

// contractCorpusRelease is the base Release supplying parameters (the indented
// body of spec.parameters:).
func contractCorpusRelease(parameters string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  parameters:
` + parameters + `  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`
}

// wellFormedContract declares a length bound at its floor and a compiling pattern
// on a parameter's own declaration and under every nesting a sub-contract takes.
const wellFormedContract = `    name:
      type: string
      minLength: 0
      maxLength: 16
      pattern: "^[a-z]+$"
    names:
      type: array
      items:
        type: string
        pattern: "^[a-z]+$"
    db:
      type: object
      properties:
        host:
          type: string
          pattern: "^[a-z.]+$"
    tags:
      type: object
      valuesType:
        type: string
        pattern: "^[a-z]+$"
`

// wellFormedValues satisfies every pattern wellFormedContract declares.
const wellFormedValues = `    name: api
    names: [first, second]
    db:
      host: db.internal
    tags:
      team: platform
`

// TestNegativeCorpusParameterContract_WellFormedBuilds is the positive control:
// the well-formed contract builds with Release values its patterns accept, and a
// value one of them refuses still halts on that value — compiling the patterns
// when the contract loads leaves them judging what a Release supplies.
func TestNegativeCorpusParameterContract_WellFormedBuilds(t *testing.T) {
	t.Parallel()
	wf := baseWorkspaceFiles()
	wf.aggregate = contractCorpusAggregate(wellFormedContract)
	wf.release = contractCorpusRelease(wellFormedValues)
	if _, err := pipelinetest.BuildE(t, writeMinimalWorkspace(t, wf)); err != nil {
		t.Fatalf("a well-formed parameter contract must build; got: %v", err)
	}

	wf.release = contractCorpusRelease(`    name: api
    names: [first, "Not-Lower"]
`)
	_, err := pipelinetest.BuildE(t, writeMinimalWorkspace(t, wf))
	if !errors.Is(err, resolver.ErrParameterContractViolation()) {
		t.Fatalf("an items value the load-compiled pattern refuses must halt on the contract; got: %v", err)
	}
}

func parameterContractCorpusRows() []buildRow {
	aggregate := func(parameters string) func(*workspaceFiles) {
		return func(wf *workspaceFiles) { wf.aggregate = contractCorpusAggregate(parameters) }
	}
	return []buildRow{
		{name: "negative-minLength", want: envelope.ErrEntityEnvelope(),
			mutate: aggregate("    name:\n      type: string\n      minLength: -5\n")},
		{name: "negative-maxLength", want: envelope.ErrEntityEnvelope(),
			mutate: aggregate("    name:\n      type: string\n      maxLength: -1\n")},
		{name: "pattern-does-not-compile", want: resolver.ErrParameterContractViolation(),
			mutate: aggregate("    name:\n      type: string\n      pattern: \"([a-z\"\n")},
		{name: "pattern-does-not-compile-under-properties", want: resolver.ErrParameterContractViolation(),
			mutate: aggregate("    db:\n      type: object\n      properties:\n        host:\n          type: string\n          pattern: \"([a-z\"\n")},
		{name: "pattern-does-not-compile-under-items", want: resolver.ErrParameterContractViolation(),
			mutate: aggregate("    names:\n      type: array\n      items:\n        type: string\n        pattern: \"([a-z\"\n")},
		{name: "pattern-does-not-compile-under-valuesType", want: resolver.ErrParameterContractViolation(),
			mutate: aggregate("    tags:\n      type: object\n      valuesType:\n        type: string\n        pattern: \"([a-z\"\n")},
	}
}

// TestNegativeCorpusParameterContract_Violations drives each single-fault
// contract through the real build pipeline, with the base Release supplying no
// parameter at all, and asserts the named gate via errors.Is. Each row is driven
// through the validation entry as well — the one a Release validation, the editor
// and the assistant tools reach — because a contract the build refuses must not
// pass an author's pre-flight check.
func TestNegativeCorpusParameterContract_Violations(t *testing.T) {
	t.Parallel()
	for _, r := range parameterContractCorpusRows() {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			wf := baseWorkspaceFiles()
			r.mutate(&wf)
			in := writeMinimalWorkspace(t, wf)
			_, err := pipelinetest.BuildE(t, in)
			if err == nil {
				t.Fatalf("%s: expected the build to halt; got nil", r.name)
			}
			if !errors.Is(err, r.want) {
				t.Fatalf("%s: want errors.Is(err, %v); got %v", r.name, r.want, err)
			}
			if _, validateErr := pipelinetest.Tier4E(t, in); !errors.Is(validateErr, r.want) {
				t.Errorf("%s: the validation entry answered %v; want the condition the build refused with",
					r.name, validateErr)
			}
		})
	}
}
