package ir

import (
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func buildProvenance(res *resolver.ResolveResult, compilerVersion string) types.IRProvenance {
	prov := types.IRProvenance{
		SchemaVersion:   types.IRAPIVersion,
		CompilerVersion: compilerVersion,
	}
	if res.Aggregate != nil {
		prov.AggregateVersion = res.Aggregate.Metadata.Version
	}
	if res.Manifest != nil {
		prov.ManifestVersion = res.Manifest.Metadata.Version
	}
	if res.Release != nil {
		prov.ReleaseVersion = res.Release.Metadata.Version
	}
	return prov
}
