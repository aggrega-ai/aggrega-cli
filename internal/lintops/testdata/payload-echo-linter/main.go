// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture payload-echo-linter plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is a linter guest that reports back exactly what it was handed. For every
// input it returns one advisory finding: its rule is the rule the input names,
// and its message is the input's bytes, verbatim. A test dispatching it through
// the host's own linter dispatcher therefore reads, in the finding it gets back,
// the payload this guest received — not a structure the host built on the way to
// encoding one.
//
// It hand-writes its output rather than using the generated codec, so it builds
// inside this module with nothing but the Go toolchain and the plugin runtime
// binding.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o payload-echo-linter.wasm ./
package main

import (
	"encoding/json"

	pdk "github.com/extism/go-pdk"
)

// descriptor is the fixed describe() self-report: a linter plugin whose single
// export is the socket's default rule-evaluation entry point.
const descriptor = `{"abi_version":"v1alpha1","plugin_type":"linter","exports":[{"name":"lint_rules"}]}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(descriptor)
	return 0
}

// echoOutput is the one finding this guest returns for input: the rule the input
// names, at the advisory severity, carrying the input itself as its message.
func echoOutput(input []byte) ([]byte, error) {
	var named struct {
		RuleID string `json:"rule_id"`
	}
	if err := json.Unmarshal(input, &named); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"findings": []map[string]string{{
			"rule_id":  named.RuleID,
			"severity": "warn",
			"message":  string(input),
		}},
	})
}

//go:wasmexport lint_rules
func lintRules() int32 {
	out, err := echoOutput(pdk.Input())
	if err != nil {
		// A failure is reported through the output's own error field, which the
		// host surfaces as a failed invocation, so an input this guest could not
		// read never reads as a run that found nothing.
		pdk.OutputString(`{"findings":[],"error":"payload-echo-linter: the input is not a linter payload"}`)
		return 0
	}
	pdk.Output(out)
	return 0
}

func main() {}
