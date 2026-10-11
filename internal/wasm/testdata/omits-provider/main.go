// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture omits-provider plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is a provider guest whose self-report claims to honour omit entries, so the
// load gate's comparison of that claim against a Plugin entity, and the entity
// generated from the guest's describe(), can be driven with a real guest rather
// than a hand-built descriptor.
//
// The claim is honest. render_node renders a fixed set of two units, one file
// each, and leaves out every unit an omit entry it was handed matches by kind and
// name. The units sit in no namespace, so an entry naming one matches nothing. It
// reports one count per entry, in the order the entries arrived, and reports none
// when it was handed none.
//
// A render handed entries also writes one more file, received-omits.txt, saying for
// each entry, in the order it arrived, whether the input marked it sensitive. That
// is what lets a build read the mark as this guest received it, rather than as the
// host meant to send it. The file names no field of any entry: a marked entry's
// fields are kept out of every text the guest returns, this one included.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o omits-provider.wasm ./
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	pdk "github.com/extism/go-pdk"
)

// providerDescriptor is the fixed describe() self-report: a stateless provider
// that renders a sourced node and honours omit entries. The build-provenance
// version is stamped so a Plugin entity can be generated from it.
const providerDescriptor = `{` +
	`"abi_version":"v1alpha1",` +
	`"plugin_type":"provider",` +
	`"capabilities":["omits-example"],` +
	`"description":"Renders two fixed units and leaves out the ones its omit entries name.",` +
	`"exports":[{"name":"render_node","input_pattern":"b","description":"renders two fixed units, less the ones an omit entry names"}],` +
	`"contract":{"input_kinds":{"sourced":true,"synthesized":false,"artifacts_standalone":false},"boundary_assembly":"none",` +
	`"reusable_instance":true,"omits":true},` +
	`"metadata":{"version":"1.0.0"}` +
	`}`

// unit is one thing this guest renders: the kind and name an omit entry selects
// it by, and the file it is written to.
type unit struct {
	kind, name, path, body string
}

// units is the fixed set every render produces before any entry is applied.
var units = []unit{
	{kind: "ConfigMap", name: "settings", path: "settings.yaml", body: "kind: ConfigMap\nname: settings\n"},
	{kind: "Secret", name: "token", path: "token.yaml", body: "kind: Secret\nname: token\n"},
}

// omitEntry mirrors the wire shape of one entry the host hands over.
type omitEntry struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// receivedOmitsPath is the file a render handed entries writes its account of
// their marks to.
const receivedOmitsPath = "received-omits.txt"

// renderNodeInput is the sliver of the render input this guest reads.
type renderNodeInput struct {
	Omits []omitEntry `json:"omits,omitempty"`
}

// renderedFile mirrors the wire shape of one returned output file. Content is
// []byte so the standard encoder base64-encodes it, which is the encoding the
// wire declares for those bytes.
type renderedFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// renderNodeOutput is the sliver of the render reply this guest returns.
type renderNodeOutput struct {
	Files   []renderedFile `json:"files"`
	Omitted []int          `json:"omitted,omitempty"`
}

// matches reports whether an entry selects a unit. The units sit in no
// namespace, so an entry naming one selects none of them.
func matches(e omitEntry, u unit) bool {
	return e.Namespace == "" && e.Kind == u.kind && e.Name == u.name
}

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(providerDescriptor)
	return 0
}

//go:wasmexport render_node
func render_node() int32 {
	var in renderNodeInput
	if err := json.Unmarshal(pdk.Input(), &in); err != nil {
		pdk.SetError(err)
		return 1
	}

	out := renderNodeOutput{Files: []renderedFile{}}
	if len(in.Omits) > 0 {
		out.Omitted = make([]int, len(in.Omits))
	}
	for _, u := range units {
		left := false
		for i, e := range in.Omits {
			if matches(e, u) {
				// Every matching entry counts the unit, so a unit two entries name
				// is counted under both.
				out.Omitted[i]++
				left = true
			}
		}
		if !left {
			out.Files = append(out.Files, renderedFile{Path: u.path, Content: []byte(u.body)})
		}
	}
	if len(in.Omits) > 0 {
		var received strings.Builder
		for i, e := range in.Omits {
			fmt.Fprintf(&received, "omits[%d] sensitive=%t\n", i, e.Sensitive)
		}
		out.Files = append(out.Files, renderedFile{Path: receivedOmitsPath, Content: []byte(received.String())})
	}

	raw, err := json.Marshal(out)
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
