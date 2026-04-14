package selector

// MatchString parses a Kubernetes-style label selector string and evaluates it
// against the provided labels. Returns true if all requirements are satisfied.
// An empty selector matches everything.
func MatchString(selectorStr string, labels map[string]string) (bool, error) {
	reqs, err := Parse(selectorStr)
	if err != nil {
		return false, err
	}
	for _, req := range reqs {
		if !matchRequirement(req, labels) {
			return false, nil
		}
	}
	return true, nil
}

func matchRequirement(req Requirement, labels map[string]string) bool {
	val, exists := labels[req.Key]
	switch req.Operator {
	case OpEquals:
		return exists && val == req.Values[0]
	case OpNotEquals:
		return !exists || val != req.Values[0]
	case OpIn:
		if !exists {
			return false
		}
		for _, v := range req.Values {
			if val == v {
				return true
			}
		}
		return false
	case OpNotIn:
		if !exists {
			return true
		}
		for _, v := range req.Values {
			if val == v {
				return false
			}
		}
		return true
	case OpExists:
		return exists
	case OpDoesNotExist:
		return !exists
	default:
		return false
	}
}
