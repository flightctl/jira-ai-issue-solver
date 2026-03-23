package container

import (
	"errors"
	"fmt"
	"os/exec"
)

// Runtime identifies a container runtime.
type Runtime string

const (
	// RuntimeAuto detects the available runtime automatically,
	// preferring Podman for its rootless security model.
	RuntimeAuto Runtime = "auto"

	// RuntimePodman uses Podman.
	RuntimePodman Runtime = "podman"

	// RuntimeDocker uses Docker.
	RuntimeDocker Runtime = "docker"
)

// IsValid reports whether r is a recognized runtime value.
func (r Runtime) IsValid() bool {
	switch r {
	case RuntimeAuto, RuntimePodman, RuntimeDocker:
		return true
	default:
		return false
	}
}

// ErrNoRuntime indicates that no container runtime was found on PATH.
var ErrNoRuntime = errors.New("no container runtime found: install podman or docker")

// DetectedRuntime holds the result of runtime auto-detection.
type DetectedRuntime struct {
	// Runtime is the detected runtime type.
	Runtime Runtime

	// Path is the absolute path to the runtime binary.
	Path string
}

// LookPathFunc matches the signature of [exec.LookPath], allowing
// callers to inject a test double for runtime detection.
type LookPathFunc func(file string) (string, error)

// DetectRuntime finds a container runtime binary based on the given
// preference. When preference is [RuntimeAuto] (or empty), it tries
// Podman first (preferred for rootless security), then Docker.
// When a specific runtime is requested, only that runtime is checked.
//
// Pass nil for lookPath to use [exec.LookPath].
//
// Returns [ErrNoRuntime] if no matching runtime is found with
// [RuntimeAuto]. Returns a descriptive error if a specific runtime
// is requested but not found.
func DetectRuntime(preference Runtime, lookPath LookPathFunc) (*DetectedRuntime, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	switch preference {
	case RuntimePodman:
		return lookupRuntime(RuntimePodman, lookPath)
	case RuntimeDocker:
		return lookupRuntime(RuntimeDocker, lookPath)
	case RuntimeAuto, "":
		if result, err := lookupRuntime(RuntimePodman, lookPath); err == nil {
			return result, nil
		}
		if result, err := lookupRuntime(RuntimeDocker, lookPath); err == nil {
			return result, nil
		}
		return nil, ErrNoRuntime
	default:
		return nil, fmt.Errorf("unknown container runtime %q: must be %q, %q, or %q",
			preference, RuntimeAuto, RuntimePodman, RuntimeDocker)
	}
}

func lookupRuntime(rt Runtime, lookPath LookPathFunc) (*DetectedRuntime, error) {
	path, err := lookPath(string(rt))
	if err != nil {
		return nil, err
	}
	return &DetectedRuntime{Runtime: rt, Path: path}, nil
}
