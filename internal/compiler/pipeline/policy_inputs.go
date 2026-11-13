// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// PolicyInputs collects, for each governance role, the payload a plugin attached
// at that role receives, as the JSON document an author evaluates a policy
// against. A caller that wants the payloads hands one to the run on
// [Options.PolicyInputs]; its presence is the request.
//
// Each payload is recorded at the site that dispatches it, from the value that
// site then encodes for the plugin, so a file holds exactly what the plugin was
// handed — the same leaves withheld, the same attachment sources and settings —
// and a payload that changes for a plugin changes in the file. A role with
// nothing attached records the payload that site would hand a plugin attached
// there with no sources and no settings, through the same assembly, so an author
// can write a policy before attaching it.
//
// The run records and writes nothing: a surface reads [PolicyInputs.Files] once
// the run returns, whether it finished or stopped, and writes them where its
// caller named. Recording changes no artifact, no verdict and no outcome.
//
// It is reset at the start of each run it is handed to, so a surface whose
// operation renders more than once reads the last render's payloads. One value
// serves one operation at a time; concurrent runs each take their own.
type PolicyInputs struct {
	roles map[plugintype.Role]*policyInputRole
}

// policyInputRole is what one role's site recorded: how many attachments it
// dispatches, and the payload each one reached was handed.
type policyInputRole struct {
	attachments int
	payloads    []policyInputPayload
	// failed carries the reason a payload could not be produced. It is reported
	// rather than raised, because writing the payloads must not change the run.
	failed error
}

// policyInputPayload is one attachment's payload: its 1-based position among
// the role's attachments (0 for a role with none attached), the label of the
// plugin attached there, and the JSON document.
type policyInputPayload struct {
	position int
	plugin   string
	document []byte
}

// NewPolicyInputs returns an empty collector.
func NewPolicyInputs() *PolicyInputs {
	return &PolicyInputs{roles: map[plugintype.Role]*policyInputRole{}}
}

// reset forgets everything recorded, for a run starting over.
func (p *PolicyInputs) reset() {
	if p == nil {
		return
	}
	p.roles = map[plugintype.Role]*policyInputRole{}
}

// wanted reports whether a caller asked for the payloads, so a role with nothing
// attached knows whether to assemble one.
func (p *PolicyInputs) wanted() bool { return p != nil }

// reach records that the role's site ran and how many attachments it dispatches.
// A nil collector records nothing.
func (p *PolicyInputs) reach(role plugintype.Role, attachments int) {
	if p == nil {
		return
	}
	p.roles[role] = &policyInputRole{attachments: attachments}
}

// record keeps the payload the role's site hands the attachment at refs[index],
// or — for index -1 — the payload it would hand an attachment with no sources
// and no settings, for a role with nothing attached. The value is written as
// JSON at once, so a later change the run makes to what it shares with the
// payload cannot reach the recorded document.
func (p *PolicyInputs) record(role plugintype.Role, index int, ref workspaceconfig.PluginRef, payload any) {
	if p == nil {
		return
	}
	r, ok := p.roles[role]
	if !ok {
		r = &policyInputRole{}
		p.roles[role] = r
	}
	doc, err := policyInputDocument(payload)
	if err != nil {
		p.fail(role, fmt.Errorf("writing it as JSON: %w", err))
		return
	}
	entry := policyInputPayload{document: doc}
	if index >= 0 {
		entry.position = index + 1
		entry.plugin = pluginRefName(ref.URI)
	}
	r.payloads = append(r.payloads, entry)
}

// fail records that the role's payload could not be produced, for a reason that
// is the payload's alone — no plugin was handed it, so the run has nothing to
// stop on. The first reason is kept.
func (p *PolicyInputs) fail(role plugintype.Role, err error) {
	if p == nil {
		return
	}
	r, ok := p.roles[role]
	if !ok {
		r = &policyInputRole{}
		p.roles[role] = r
	}
	if r.failed == nil {
		r.failed = err
	}
}

// policyInputDocument writes a payload as the JSON document a policy reads, on
// one line, with markup characters left as written, and newline-terminated. The
// payload types fix the key order, so identical input yields identical bytes.
//
// It is deliberately not indented. A payload carries its attachment's settings
// as a JSON document of their own, and indenting the whole would re-indent that
// document too, so the file read back into the payload — as `aggrega plugin
// run-local` reads it — would encode to bytes that differ from the dispatched
// ones in the settings' whitespace. Written on one line it reads back to exactly
// the bytes the plugin was handed.
func policyInputDocument(payload any) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// PolicyInputFiles is what a run recorded, ready to write: the files by name,
// and an account of every role whose payload is missing or incomplete.
type PolicyInputFiles struct {
	// Files maps each file name to the JSON document it holds.
	Files map[string][]byte
	// Missing names each role that wrote nothing or less than its attachments,
	// in pipeline order, each with the reason in a reader's words.
	Missing []PolicyInputGap
}

// PolicyInputGap names a role whose payloads are not all written, and why.
type PolicyInputGap struct {
	Role   plugintype.Role
	Reason string
}

// Files composes the recorded payloads into the files a surface writes.
//
// A role's payload is written to <role>.json where nothing is attached there, and
// where every attachment there was handed byte-identical payloads. Where the
// attachments' payloads differ — an attachment carrying its own sources or
// settings, or a transform whose change reaches the next attachment's payload —
// each is written to <role>.<position>.<plugin>.json, position being the
// attachment's 1-based place among that role's attachments in the order they are
// written and plugin the last segment of its reference, so the role and the
// attachment are readable from the name. A role the run stopped part way through
// is written per attachment too, so a single file never stands for attachments
// the run did not reach.
func (p *PolicyInputs) Files() PolicyInputFiles {
	out := PolicyInputFiles{Files: map[string][]byte{}}
	if p == nil {
		return out
	}
	for _, role := range plugintype.GovernanceRoles() {
		r, ok := p.roles[role]
		if !ok {
			out.Missing = append(out.Missing, PolicyInputGap{Role: role, Reason: "the run stopped before this role's turn"})
			continue
		}
		if r.failed != nil {
			out.Missing = append(out.Missing, PolicyInputGap{Role: role, Reason: fmt.Sprintf("its payload could not be produced: %v", r.failed)})
			continue
		}
		complete := len(r.payloads) == max(r.attachments, 1)
		if !complete {
			attachments := "attachments"
			if r.attachments == 1 {
				attachments = "attachment"
			}
			out.Missing = append(out.Missing, PolicyInputGap{Role: role, Reason: fmt.Sprintf("the run stopped after %d of its %d %s", len(r.payloads), r.attachments, attachments)})
		}
		if len(r.payloads) == 0 {
			continue
		}
		if complete && samePolicyInputDocuments(r.payloads) {
			out.Files[string(role)+".json"] = r.payloads[0].document
			continue
		}
		for _, entry := range r.payloads {
			out.Files[PolicyInputFileName(role, entry.position, entry.plugin)] = entry.document
		}
	}
	return out
}

// PolicyInputFileName is the name of the file holding the payload the attachment
// at position (1-based) among role's attachments receives, plugin being the last
// segment of its reference. The plugin part is spelled as every derived file name
// is ([pathsegment.Encode]), so a reference carrying a character a file name
// cannot hold still yields a name every platform accepts, and two references
// never share one.
func PolicyInputFileName(role plugintype.Role, position int, plugin string) string {
	return string(role) + "." + strconv.Itoa(position) + "." + pathsegment.Encode(plugin) + ".json"
}

// IsPolicyInputFileName reports whether name is one this export writes — one of
// the forms [PolicyInputs.Files] composes — so a surface replacing an earlier
// export removes those files and nothing else a directory holds.
func IsPolicyInputFileName(name string) bool {
	for _, role := range plugintype.GovernanceRoles() {
		if name == string(role)+".json" {
			return true
		}
		rest, ok := strings.CutPrefix(name, string(role)+".")
		if !ok || !strings.HasSuffix(rest, ".json") {
			continue
		}
		position, _, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(position); err == nil && n > 0 {
			return true
		}
	}
	return false
}

// samePolicyInputDocuments reports whether every payload holds the same bytes.
func samePolicyInputDocuments(payloads []policyInputPayload) bool {
	for _, entry := range payloads[1:] {
		if !bytes.Equal(entry.document, payloads[0].document) {
			return false
		}
	}
	return true
}
