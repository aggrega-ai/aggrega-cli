package resolver

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// BoundaryInfra holds the collected infra and annotations for one component from the Release boundaries.
type BoundaryInfra struct {
	Infra        map[string]interface{} // physical sizing from boundary_component.infra
	Annotations  map[string]string      // cascaded from boundary.annotations
	BoundaryPath string                 // e.g. "aws-prod/eks-main/payments"
	Directory    string                 // resolved output directory
	SubDirectory string                 // resolved sub-directory
}

// WireBoundaryInfra walks the recursive Release boundaries and collects per-component
// infra maps, boundary annotations, and boundary paths.
// Returns a map keyed by component instance name.
func WireBoundaryInfra(release *types.Release) map[string]*BoundaryInfra {
	if release == nil {
		return nil
	}
	result := map[string]*BoundaryInfra{}
	for bName, boundary := range release.Spec.Boundaries {
		walkBoundary(bName, "", boundary, nil, result)
	}
	return result
}

func walkBoundary(name, parentPath string, b types.Boundary, parentAnnotations map[string]string, result map[string]*BoundaryInfra) {
	currentPath := name
	if parentPath != "" {
		currentPath = parentPath + "/" + name
	}

	// Merge annotations: parent cascades down, boundary's own annotations override
	mergedAnnotations := mergeStringMaps(parentAnnotations, b.Annotations)

	// Collect components in this boundary
	for compName, comp := range b.Components {
		bi := &BoundaryInfra{
			Infra:        comp.Infra,
			Annotations:  mergedAnnotations,
			BoundaryPath: currentPath,
			Directory:    b.Directory,
			SubDirectory: resolveSubDirectory(b.SubDirectory, comp.SubDirectory),
		}
		result[compName] = bi
	}

	// Recurse into nested boundaries
	for childName, childBoundary := range b.Boundaries {
		walkBoundary(childName, currentPath, childBoundary, mergedAnnotations, result)
	}
}

func resolveSubDirectory(boundaryLevel, componentLevel string) string {
	if componentLevel != "" {
		return componentLevel
	}
	return boundaryLevel
}

func mergeStringMaps(base, overlay map[string]string) map[string]string {
	if base == nil && overlay == nil {
		return nil
	}
	result := make(map[string]string)
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		result[k] = v
	}
	return result
}
