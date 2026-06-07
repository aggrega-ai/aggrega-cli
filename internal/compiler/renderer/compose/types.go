// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

// ComposeFile is a Compose Specification document (no `version:` key — the
// modern compose-spec deprecates it; Compose v2.2+ ignores it). Fragment
// outputs from SynthesizeWorkload populate Services/Volumes/Networks with the
// node's contributions; the Release-level merger (Track 2) collects fragments
// and emits a single ComposeFile at the Release output root.
type ComposeFile struct {
	Services map[string]Service `yaml:"services,omitempty"`
	Volumes  map[string]Volume  `yaml:"volumes,omitempty"`
	Networks map[string]Network `yaml:"networks,omitempty"`
	Configs  map[string]Config  `yaml:"configs,omitempty"`
}

// Service is one compose service definition. Fields populated only as needed
// — omitempty on every field so per-node fragments stay minimal.
//
// The set of fields here is the currently synthesisable surface. Expansion
// (e.g., secrets, deploy) lands as needed in future iterations.
type Service struct {
	Image       string            `yaml:"image,omitempty"`
	Command     []string          `yaml:"command,omitempty"`
	Entrypoint  []string          `yaml:"entrypoint,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	EnvFile     []string          `yaml:"env_file,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Volumes     []ServiceVolume   `yaml:"volumes,omitempty"`
	Tmpfs       []string          `yaml:"tmpfs,omitempty"`
	Networks    []string          `yaml:"networks,omitempty"`
	NetworkMode string            `yaml:"network_mode,omitempty"`
	DependsOn   []string          `yaml:"depends_on,omitempty"`
	Healthcheck *Healthcheck      `yaml:"healthcheck,omitempty"`
	Restart     string            `yaml:"restart,omitempty"`
	User        string            `yaml:"user,omitempty"`
	WorkingDir  string            `yaml:"working_dir,omitempty"`
	Privileged  bool              `yaml:"privileged,omitempty"`
	ReadOnly    bool              `yaml:"read_only,omitempty"`
	CapAdd      []string          `yaml:"cap_add,omitempty"`
	CapDrop     []string          `yaml:"cap_drop,omitempty"`
	Configs     []ServiceConfig   `yaml:"configs,omitempty"`
}

// Config is a top-level compose config declaration with inline content. The
// content is mounted into a service as a file via a ServiceConfig reference.
// Inline content requires Docker Compose >= 2.23.1.
type Config struct {
	Content string `yaml:"content"`
}

// ServiceConfig is one entry in a service's configs: list — the long form that
// mounts a top-level Config at an explicit path inside the container.
type ServiceConfig struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// ServiceVolume is one entry in a service's volumes: list. Compose's "long
// form" — the synthesizer always emits long-form for clarity and for fields
// (read_only, type) that the short form `host:container:mode` cannot express.
type ServiceVolume struct {
	Type     string `yaml:"type"` // "bind" or "volume" or "tmpfs"
	Source   string `yaml:"source,omitempty"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only,omitempty"`
}

// Volume is a top-level named volume declaration. Compose top-level volumes
// have driver/driver_opts/external/labels/name; the synthesizer emits the
// minimum viable form (empty body — `<name>: {}`) today. Future expansion
// covers driver options when Workload's persistent.class signals a
// non-default storage class.
type Volume struct {
	Driver     string            `yaml:"driver,omitempty"`
	DriverOpts map[string]string `yaml:"driver_opts,omitempty"`
	Labels     map[string]string `yaml:"labels,omitempty"`
}

// Network is a top-level docker network declaration. The Track 2 merger
// emits one network per boundary, named after the boundary's
// `aggrega.ai/namespace` annotation when present; until Track 2 lands,
// fragments do not declare networks.
type Network struct {
	Driver     string            `yaml:"driver,omitempty"`
	DriverOpts map[string]string `yaml:"driver_opts,omitempty"`
	Labels     map[string]string `yaml:"labels,omitempty"`
}

// Healthcheck is the compose service healthcheck primitive. Aggrega's three
// probe shapes (http / tcp / exec) translate as:
//
//   - http: → ["CMD-SHELL", "curl -f http://localhost:<port><path> || exit 1"]
//   - tcp:  → ["CMD-SHELL", "nc -z localhost <port>"]
//   - exec: → ["CMD", <token>...] (CMD form when token list available)
//
// Compose only supports CMD/CMD-SHELL/NONE; no native HTTP or TCP probes.
type Healthcheck struct {
	Test        []string `yaml:"test,omitempty"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
}
