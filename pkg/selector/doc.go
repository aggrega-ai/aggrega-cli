// Package selector implements Kubernetes-style label matching for Aggrega's
// topology type-safety (@selector strings) and Feature targeting (appliesTo).
//
// Two matching modes:
//   - String selectors: "key=value, key2!=value2, key3 in (a,b)"
//   - Structured selectors: matchLabels, matchExpressions (In/NotIn/Exists/DoesNotExist), appliesToAnyOf
package selector
