package container_test

import (
	"errors"
	"testing"

	"jira-ai-issue-solver/container"
)

func TestDetectRuntime_AutoFindsPodman(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "podman" {
			return "/usr/bin/podman", nil
		}
		return "", errors.New("not found")
	}

	result, err := container.DetectRuntime(container.RuntimeAuto, lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime != container.RuntimePodman {
		t.Errorf("Runtime = %q, want podman", result.Runtime)
	}
	if result.Path != "/usr/bin/podman" {
		t.Errorf("Path = %q, want /usr/bin/podman", result.Path)
	}
}

func TestDetectRuntime_AutoFallsBackToDocker(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", errors.New("not found")
	}

	result, err := container.DetectRuntime(container.RuntimeAuto, lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime != container.RuntimeDocker {
		t.Errorf("Runtime = %q, want docker", result.Runtime)
	}
}

func TestDetectRuntime_AutoPrefersPodmanOverDocker(t *testing.T) {
	lookPath := func(name string) (string, error) {
		switch name {
		case "podman":
			return "/usr/bin/podman", nil
		case "docker":
			return "/usr/bin/docker", nil
		}
		return "", errors.New("not found")
	}

	result, err := container.DetectRuntime(container.RuntimeAuto, lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime != container.RuntimePodman {
		t.Errorf("Runtime = %q, want podman (preferred)", result.Runtime)
	}
}

func TestDetectRuntime_AutoNeitherAvailable(t *testing.T) {
	lookPath := func(_ string) (string, error) {
		return "", errors.New("not found")
	}

	_, err := container.DetectRuntime(container.RuntimeAuto, lookPath)
	if !errors.Is(err, container.ErrNoRuntime) {
		t.Errorf("err = %v, want ErrNoRuntime", err)
	}
}

func TestDetectRuntime_ExplicitPodman(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "podman" {
			return "/usr/bin/podman", nil
		}
		return "", errors.New("not found")
	}

	result, err := container.DetectRuntime(container.RuntimePodman, lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime != container.RuntimePodman {
		t.Errorf("Runtime = %q, want podman", result.Runtime)
	}
}

func TestDetectRuntime_ExplicitPodmanNotFound(t *testing.T) {
	lookPath := func(_ string) (string, error) {
		return "", errors.New("not found")
	}

	_, err := container.DetectRuntime(container.RuntimePodman, lookPath)
	if err == nil {
		t.Fatal("expected error when podman not found")
	}
}

func TestDetectRuntime_ExplicitDocker(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", errors.New("not found")
	}

	result, err := container.DetectRuntime(container.RuntimeDocker, lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime != container.RuntimeDocker {
		t.Errorf("Runtime = %q, want docker", result.Runtime)
	}
}

func TestDetectRuntime_ExplicitDockerNotFound(t *testing.T) {
	lookPath := func(_ string) (string, error) {
		return "", errors.New("not found")
	}

	_, err := container.DetectRuntime(container.RuntimeDocker, lookPath)
	if err == nil {
		t.Fatal("expected error when docker not found")
	}
}

func TestDetectRuntime_UnknownRuntime(t *testing.T) {
	lookPath := func(_ string) (string, error) {
		return "/usr/bin/something", nil
	}

	_, err := container.DetectRuntime("containerd", lookPath)
	if err == nil {
		t.Fatal("expected error for unknown runtime")
	}
}

func TestDetectRuntime_EmptyPreferenceActsAsAuto(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", errors.New("not found")
	}

	result, err := container.DetectRuntime("", lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime != container.RuntimeDocker {
		t.Errorf("Runtime = %q, want docker", result.Runtime)
	}
}

func TestDetectRuntime_NilLookPathUsesExecLookPath(t *testing.T) {
	// With nil lookPath, DetectRuntime uses exec.LookPath.
	// We can't predict what's installed, so just verify it doesn't panic.
	// It may return ErrNoRuntime if neither podman nor docker is installed.
	_, _ = container.DetectRuntime(container.RuntimeAuto, nil)
}

func TestRuntime_IsValid(t *testing.T) {
	tests := []struct {
		runtime container.Runtime
		valid   bool
	}{
		{container.RuntimeAuto, true},
		{container.RuntimePodman, true},
		{container.RuntimeDocker, true},
		{"containerd", false},
		{"", false},
	}

	for _, tt := range tests {
		name := string(tt.runtime)
		if name == "" {
			name = "(empty)"
		}
		t.Run(name, func(t *testing.T) {
			if got := tt.runtime.IsValid(); got != tt.valid {
				t.Errorf("IsValid() = %v, want %v", got, tt.valid)
			}
		})
	}
}
