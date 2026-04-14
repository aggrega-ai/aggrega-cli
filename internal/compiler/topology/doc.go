// Package topology implements Step 2 of the Aggrega compiler pipeline:
// the Topology Builder & Mutation Gate.
//
// It consumes a resolver.ResolveResult and produces a TopologyResult containing
// flattened nodes (dot-notation IDs), directed edges (from uses declarations),
// feature/secret attachments, and exported component mappings.
//
// Key behaviors:
//   - Polymorphic uses normalization (string arrays and verbose maps)
//   - Sub-aggregate recursive flattening with dot-notation IDs
//   - Cyclic edges allowed (no DAG constraint)
//   - Deterministic output (all arrays from sorted map iteration)
package topology
