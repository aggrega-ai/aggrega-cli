// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// unresolvableTransformPlugin is a governance Plugin coordinate no catalog in the
// fixture serves. A dispatch that reaches it fails loudly and names it, which is
// what makes "the attachment was dispatched" observable from outside without a
// real guest.
const unresolvableTransformPlugin = "governance/recorded-identity-probe:1.0.0"

// transformSourceContent is the pinned source body. It is asserted by value
// rather than by length, so a composition that carried an empty pin through would
// fail rather than pass on a non-nil slice.
const transformSourceContent = "rules:\n  - allow: everything\n"

// recordWithTransformAttachment is the record under test: one transform
// attachment, at the role whose runner changes the graph, carrying a pinned
// source. Its Plugins section is deliberately empty — the case this exercises is
// a record that names an attachment, not one that pins its bytes.
func recordWithTransformAttachment() *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Mutators: []lockfile.MutatorAttachment{{
			Plugin: unresolvableTransformPlugin,
			Role:   string(plugintype.RoleLabelTransform),
			Sources: []lockfile.MutatorSourcePin{{
				Path:    "rules.yaml",
				Content: transformSourceContent,
			}},
		}},
	}
}

// TestValidateDispatchesTransformAttachmentsFromTheRecord proves the record
// supplied to a release-scoped validation actually reaches the phases that
// dispatch guests.
//
// The two branches run over identical inputs and differ only in whether the
// record is supplied, so the difference is attributable. Without it the fixture's
// workspace configuration attaches nothing and no transform is dispatched at all;
// with it, the attachment recorded in the record is reconstructed and dispatched,
// which surfaces as a resolution failure naming the role and the plugin.
//
// The failure is the observation rather than the subject: what it establishes is
// that the pipeline got as far as trying to run that attachment, which is
// precisely what a validation missing the record never did.
func TestValidateDispatchesTransformAttachmentsFromTheRecord(t *testing.T) {
	view, resolved, wsCfg, releasePath := resolveCancelInputs(t)

	inputs := func(lf *lockfile.Lockfile) ValidateInputs {
		return ValidateInputs{
			View:            view,
			Closure:         resolved.Closure,
			Release:         resolved.Release,
			ReleasePath:     releasePath,
			Manifest:        resolved.Manifest,
			AggBytes:        resolved.AggBytes,
			Lockfile:        lf,
			AggregatePath:   resolved.AggregatePath,
			WorkspaceConfig: wsCfg,
			CompilerVersion: "test",
		}
	}

	withoutRecord := Validate(context.Background(), inputs(nil))
	if withoutRecord.RunErr != nil && strings.Contains(withoutRecord.RunErr.Error(), unresolvableTransformPlugin) {
		t.Fatalf("the no-record branch already dispatched the attachment, so the comparison below measures nothing: %v",
			withoutRecord.RunErr)
	}

	withRecord := Validate(context.Background(), inputs(recordWithTransformAttachment()))
	if withRecord.RunErr == nil {
		t.Fatalf("supplying the record dispatched no transform attachment: the pipeline completed with no error")
	}
	msg := withRecord.RunErr.Error()
	if !strings.Contains(msg, unresolvableTransformPlugin) {
		t.Fatalf("the recorded attachment was not dispatched; error does not name it: %v", msg)
	}
	if !strings.Contains(msg, string(plugintype.RoleLabelTransform)) {
		t.Errorf("the failure does not name the role it was dispatched at: %v", msg)
	}
}

// TestGovernanceCompositionCarriesTheRecordedSources proves the second half of
// what the record supplies: the pinned input a transform attachment reads.
//
// It is asserted separately from the dispatch above because the two fail
// differently and one masks the other — a dispatch that cannot resolve its plugin
// never assembles an input, so a single end-to-end assertion would report a
// missing source and a missing plugin identically. Here the composition is asked
// directly for what it hands the runner.
//
// The empty-record branch is the calibration: without a record the attachment
// list is whatever the configuration authored, which for this fixture is nothing,
// so the populated branch cannot be passing on inherited state.
func TestGovernanceCompositionCarriesTheRecordedSources(t *testing.T) {
	view, _, wsCfg, releasePath := resolveCancelInputs(t)

	compose := func(lf *lockfile.Lockfile) []string {
		t.Helper()
		// A fresh copy per call: the composition re-points the field at the
		// configuration it rewrites, so sharing one across both calls would let the
		// first call's reconstructed attachments reach the second.
		cfg := wsCfg
		in := pipeline.Inputs{WorkspaceConfig: &cfg, Lockfile: lf}
		if err := ApplyGovernanceComposition(&in, view, releasePath); err != nil {
			t.Fatalf("compose: %v", err)
		}
		var contents []string
		for _, ref := range in.WorkspaceConfig.Plugins.Governance {
			if ref.With == nil {
				continue
			}
			for _, s := range ref.With.PinnedSources {
				contents = append(contents, s.Content)
			}
		}
		return contents
	}

	if got := compose(nil); len(got) != 0 {
		t.Fatalf("the fixture's configuration already carries pinned sources, so the branch below measures nothing: %v", got)
	}

	got := compose(recordWithTransformAttachment())
	if len(got) != 1 || got[0] != transformSourceContent {
		t.Fatalf("the composed attachment does not carry the recorded source content: %#v", got)
	}
}
