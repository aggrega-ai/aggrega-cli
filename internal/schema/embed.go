// Package cuesources holds embedded CUE schema files for blueprint validation.
//
// Go's //go:embed cannot reference parent paths (no ".." segments), so embedding
// lives next to the .cue files; internal/compiler/schema loads from here.
package cuesources

import "embed"

//go:embed *.cue
var Files embed.FS
