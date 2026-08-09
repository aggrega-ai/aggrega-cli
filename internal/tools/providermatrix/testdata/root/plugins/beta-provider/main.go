//go:build wasip1

package main

import sdk "example.test/pdk"

func descriptor() sdk.Descriptor {
	return sdk.Descriptor{
		Contract: &sdk.ProviderContract{
			InputKinds:         sdk.InputKinds{Sourced: true, Synthesized: false, ArtifactsStandalone: false},
			BoundaryAssembly:   "nest",
			ArtifactDelimiters: &sdk.ArtifactDelimiters{Left: "[[", Right: "]]"},
		},
	}
}
