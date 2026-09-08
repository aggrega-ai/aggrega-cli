// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// twoReleaseWorkspace lays down one Aggregate and TWO Releases that deploy it,
// differing only in how they fulfil the Aggregate's declared `port` parameter:
// one supplies an integer, the other a string the contract does not admit.
//
// One Aggregate rather than two is what makes the workspace a discriminator. The
// validated file, its bytes and the daemon are identical across both requests
// below, so the only thing that can move the verdict is which Release the request
// named — and a daemon reading a Release of its own would answer both requests
// the same way whichever it had chosen.
//
// Returns the workspace root and the two Releases' workspace-relative paths, in
// the spelling the request parameter takes.
func twoReleaseWorkspace(t *testing.T) (ws, fulfilled, violating string) {
	t.Helper()
	ws = t.TempDir()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "parameter-type-mismatch")
	for _, name := range []string{"aggregate.yaml", "manifest.yaml", "release.yaml"} {
		data, rerr := os.ReadFile(filepath.Join(fixtureDir, name))
		if rerr != nil {
			t.Fatalf("read fixture %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(ws, name), data, 0o644); werr != nil {
			t.Fatalf("write fixture %s: %v", name, werr)
		}
	}

	// The sibling Release: same Aggregate, same Manifest, a port the contract
	// admits. Its metadata.name differs so the two are independent documents.
	ok := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: tier4-param-fulfilled-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  parameters:
    port: 8080
  boundaries:
    main:
      directory: ./dist/main
      components:
        port: {}
`)
	if err := os.WriteFile(filepath.Join(ws, "release-ok.yaml"), ok, 0o644); err != nil {
		t.Fatalf("write sibling release: %v", err)
	}

	return ws, "release-ok.yaml", "release.yaml"
}

// validateResultNaming drives one aggrega/validate request over the real
// framing and write path, naming release — the empty string for a request that
// names none — and returns the whole answer, the checked key included.
func validateResultNaming(t *testing.T, srv *Server, file, content, release string) validateResult {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    file,
		Content: content,
		Release: release,
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("validate naming %q returned an error envelope: %+v", release, resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal validate result: %v", err)
	}
	return result
}

// validateNaming is validateResultNaming narrowed to the diagnostics, for the
// assertions that turn on those alone.
func validateNaming(t *testing.T, srv *Server, file, content, release string) []Diagnostic {
	t.Helper()
	return validateResultNaming(t, srv, file, content, release).Diagnostics
}

// TestValidateAnswersAboutTheReleaseEachRequestNames is the invocation proof for
// the parameter: ONE running daemon, no restart between the two requests, the
// same file and the same bytes both times, and two answers that differ in a way
// only the Release named could produce.
//
// The discriminator is the parameter contract. The Aggregate declares `port` as
// an integer; one Release fulfils it with an integer and the other with a string.
// So the violating Release must draw the contract-violation condition and the
// fulfilling one must not, and neither answer is reachable from the file being
// validated.
//
// Both directions are asserted, and the order is deliberate: the violating
// Release is named SECOND, after the fulfilling one, so a daemon that resolved a
// Release once and kept it would answer the second request from the first
// request's Release and fail here. Running them in the other order would let such
// a daemon pass by accident on the negative half.
func TestValidateAnswersAboutTheReleaseEachRequestNames(t *testing.T) {
	ws, fulfilled, violating := twoReleaseWorkspace(t)

	aggPath := filepath.Join(ws, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	content := string(aggBytes)

	srv := newTestServer(t, ws, nil, nil)

	// The condition the contract violation is registered under. Read from the
	// declaration rather than matched as text, so the assertion keeps holding
	// when the sentence is reworded.
	want := conditionCodesOf(resolver.ErrParameterContractViolation()).Code
	if want == "" {
		t.Fatal("the parameter-contract condition carries no diagnostic code; both assertions below would be vacuous")
	}

	carriesViolation := func(diags []Diagnostic) bool {
		for _, d := range diags {
			if d.Code == want {
				return true
			}
		}
		return false
	}

	// A: the Release whose parameters satisfy the contract.
	okDiags := validateNaming(t, srv, aggPath, content, fulfilled)
	if carriesViolation(okDiags) {
		t.Errorf("naming %q reported the parameter-contract violation, but that Release fulfils the contract; got %+v", fulfilled, okDiags)
	}

	// B: the Release whose parameters do not — same daemon, same file, same bytes.
	badDiags := validateNaming(t, srv, aggPath, content, violating)
	if !carriesViolation(badDiags) {
		t.Fatalf("naming %q did not report the parameter-contract violation (code %q); got %+v", violating, want, badDiags)
	}

	// The two answers differ, and the difference is the Release. Asserted
	// explicitly rather than left implied by the two checks above: a change that
	// made both answers carry the violation would satisfy neither reading of
	// "answers about the Release named".
	if len(okDiags) == len(badDiags) && carriesViolation(okDiags) == carriesViolation(badDiags) {
		t.Errorf("both Releases produced the same verdict; the answer does not depend on the Release named")
	}

	// The violation names the parameter, so the reader can act on it.
	var sawPort bool
	for _, d := range badDiags {
		if d.Code == want && strings.Contains(d.Message, "port") {
			sawPort = true
		}
	}
	if !sawPort {
		t.Errorf("the violation does not name the offending parameter; got %+v", badDiags)
	}
}

// TestValidateReversedOrderStillAnswersAboutTheReleaseNamed runs the same two
// requests against a fresh daemon in the opposite order. It is the other half of
// the A/B: a daemon that froze the FIRST Release it saw would pass the forward
// test's negative half and fail its positive half, or the reverse here, and only
// running both orders rules out both freezes.
func TestValidateReversedOrderStillAnswersAboutTheReleaseNamed(t *testing.T) {
	ws, fulfilled, violating := twoReleaseWorkspace(t)

	aggPath := filepath.Join(ws, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	content := string(aggBytes)

	srv := newTestServer(t, ws, nil, nil)
	want := conditionCodesOf(resolver.ErrParameterContractViolation()).Code
	if want == "" {
		t.Fatal("the parameter-contract condition carries no diagnostic code; the assertions below would be vacuous")
	}

	carriesViolation := func(diags []Diagnostic) bool {
		for _, d := range diags {
			if d.Code == want {
				return true
			}
		}
		return false
	}

	if diags := validateNaming(t, srv, aggPath, content, violating); !carriesViolation(diags) {
		t.Fatalf("naming %q first did not report the violation; got %+v", violating, diags)
	}
	if diags := validateNaming(t, srv, aggPath, content, fulfilled); carriesViolation(diags) {
		t.Errorf("naming %q second still reported the violation — the daemon answered from the previous request's Release; got %+v", fulfilled, diags)
	}
}

// TestValidateWithNoReleaseRunsTheDocumentHalfAndNotTheSemanticOne is the
// invocation proof for the other branch of the parameter, and the workspace is
// chosen so that an absent answer cannot be an accident of an empty tree: it
// HOLDS two Releases, and one of them produces a finding on this very file with
// these very bytes. A daemon that defaulted to a Release, remembered one, or
// inferred one would report that finding here.
//
// One daemon answers both requests and the only thing that moves is whether a
// Release is named. The named request is run FIRST and asserted, so it is a
// positive control rather than an afterthought: without it a green below would
// be consistent with a workspace that simply had nothing to say. Running it
// first also puts a Release-scoped finding in the file's diagnostic entry, so
// the unnamed request that follows has something to wrongly repeat — silence
// after it is a statement, not an absence.
func TestValidateWithNoReleaseRunsTheDocumentHalfAndNotTheSemanticOne(t *testing.T) {
	ws, _, violating := twoReleaseWorkspace(t)

	aggPath := filepath.Join(ws, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	content := string(aggBytes)

	srv := newTestServer(t, ws, nil, nil)

	want := conditionCodesOf(resolver.ErrParameterContractViolation()).Code
	if want == "" {
		t.Fatal("the parameter-contract condition carries no diagnostic code; the assertions below would be vacuous")
	}
	carriesViolation := func(diags []Diagnostic) bool {
		for _, d := range diags {
			if d.Code == want {
				return true
			}
		}
		return false
	}

	named := validateResultNaming(t, srv, aggPath, content, violating)
	if !carriesViolation(named.Diagnostics) {
		t.Fatalf("the control failed: naming %q reported no parameter-contract violation (code %q), so the absence asserted below would prove nothing; got %+v", violating, want, named.Diagnostics)
	}
	if named.Checked != "" {
		t.Errorf("Checked = %q on an answer that named a Release; want it omitted", named.Checked)
	}

	unnamed := validateResultNaming(t, srv, aggPath, content, "")
	if carriesViolation(unnamed.Diagnostics) {
		t.Errorf("naming no Release still reported the Release-scoped violation; the daemon acted on a Release the request did not name — got %+v", unnamed.Diagnostics)
	}
	if unnamed.Checked != checkedDocument {
		t.Errorf("Checked = %q; want %q, so this answer cannot be read as the full verdict above", unnamed.Checked, checkedDocument)
	}

	// The document half ran rather than being skipped along with the semantic
	// one. Asserted at the surface that consumes the parse cache, because that is
	// where the loss would be felt: aggrega/diagnostics answers only from a
	// cached parse and otherwise refuses with file-not-validated.
	var din, dout bytes.Buffer
	din.Write(framedRequest(2, "aggrega/diagnostics", diagnosticsParams{File: aggPath}))
	if resp := runOneRequest(t, srv, &din, &dout); resp.Error != nil {
		t.Errorf("diagnostics after a Release-less validation returned %+v; the parse cache was not seeded, so every cache-driven operation is dead on this workspace", resp.Error)
	}

	// And the document half REPORTS rather than merely running: a
	// document-envelope violation in the same workspace, named against no
	// Release, still reaches the caller.
	badRelease := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: r
  environment: dev
  version: "1.0.0"
spec:
  aggregate: a
  manifest: m
  boundaries:
    main:
      directory: ./d
      components:
        api:
          values:
            port: 9999
`
	envelopeAnswer := validateResultNaming(t, srv, filepath.Join(ws, "candidate.yaml"), badRelease, "")
	if len(envelopeAnswer.Diagnostics) == 0 {
		t.Errorf("a document-envelope violation reported nothing when no Release was named; the document half is not running")
	}
	if envelopeAnswer.Checked != checkedDocument {
		t.Errorf("Checked = %q on the envelope answer; want %q", envelopeAnswer.Checked, checkedDocument)
	}
}
