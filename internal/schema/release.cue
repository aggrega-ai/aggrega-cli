package aggrega

#Release: {
	apiVersion: "aggrega.ai/v1alpha1"
	kind:       "Release"
	metadata: {
		name: string
	}
	spec: {
		aggregate: string
		manifest:  string
		policies?: {
			strict_networking?: bool
			...
		}
		scopes: [...{
			name:     string
			provider: string
			...
		}]
		...
	}
}
