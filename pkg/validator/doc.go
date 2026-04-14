// Package validator implements CUE-based schema validation for Aggrega entities.
//
// Three engines:
//   - Schema Engine: evaluates CUE schemas against developer values
//   - Inheritance Engine: Two-Tiered Merge (YAML deep-merge + CUE unification via extends)
//   - Split Schema Engine: generates baseUsesCueSchema from usesSchema, unifies with user cueSchema
//
// All CUE errors pass through an Aggrega error translation layer. Raw CUE error
// strings never reach the user.
package validator
