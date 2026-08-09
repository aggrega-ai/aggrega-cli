//go:build wasip1

package main

import "example.test/pdk"

func descriptor() pdk.Descriptor {
	return pdk.Descriptor{
		Contract: &pdk.ProviderContract{
			InputKinds:       pdk.InputKinds{Sourced: true, Synthesized: true, ArtifactsStandalone: true},
			BoundaryAssembly: "none",
			ReusableInstance: true,
		},
	}
}
