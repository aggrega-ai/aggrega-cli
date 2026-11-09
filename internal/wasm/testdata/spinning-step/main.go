// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture spinning-step plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is a pipeline-step guest whose step call never returns on its own. The
// step body is an unconditional loop that calls nothing — no host function, no
// allocation — so the guest never yields and every moment of the call is its
// own execution. The host's per-call execution ceiling is the only thing that
// ends it: once the ceiling is spent the host closes the module, and the call
// comes back cancelled.
//
// That is what makes it the guest to reach for when a ceiling has to bite on
// every run. A guest that merely runs long can finish inside a small ceiling on
// a fast or quiet machine; this one cannot finish inside any ceiling at all, so
// whether a call is cancelled depends only on whether a ceiling applies to it.
//
// describe() returns at once with a conforming self-report, so the guest loads,
// passes the load gate and publishes like any step plugin. Its step export
// claims the patch operation on own-scope documents and compatibility with the
// helm and helm-chart providers, so a Plugin entity claiming that export
// publishes it and a helm-rendered Recipe may run it; the claim is never
// exercised, because the step never returns an output.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o spinning-step.wasm ./
package main

import pdk "github.com/extism/go-pdk"

// stepDescriptor is the fixed describe() self-report: a pipeline-step plugin
// with a single step export declared compatible with the helm and helm-chart
// providers. It reports version 1.0.0 as its own, because publishing refuses a
// Plugin entity whose metadata.version the guest does not report, and the
// entity that publishes this guest records 1.0.0.
const stepDescriptor = `{` +
	`"abi_version":"v1beta1",` +
	`"plugin_type":"pipeline-step",` +
	`"capabilities":["helm","helm-chart"],` +
	`"metadata":{"version":"1.0.0"},` +
	`"description":"A pipeline step whose step call never returns on its own.",` +
	`"exports":[{"name":"step","operates":"patch","scope":"own","description":"runs until the host's execution ceiling cancels the call"}]` +
	`}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(stepDescriptor)
	return 0
}

// step never returns. It reads no input and writes no output: the loop is the
// whole body, and the host closing the module is what ends the call.
//
//go:wasmexport step
func step() int32 {
	for {
	}
}

func main() {}
