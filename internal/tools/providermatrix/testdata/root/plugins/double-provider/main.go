//go:build wasip1

package main

import "example.test/pdk"

func one() pdk.ProviderContract {
	return pdk.ProviderContract{InputKinds: pdk.InputKinds{Sourced: true}}
}
func two() pdk.ProviderContract {
	return pdk.ProviderContract{InputKinds: pdk.InputKinds{Synthesized: true}}
}
