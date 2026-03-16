package container_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"jira-ai-issue-solver/container"
)

// --- NewResolver ---

func TestNewResolver_RejectsNilLogger(t *testing.T) {
	_, err := container.NewResolver(container.ResolverDefaults{}, nil)
	if err == nil {
		t.Fatal("expected error for nil logger")
	}
}

func TestNewResolver_AcceptsEmptyDefaults(t *testing.T) {
	r, err := container.NewResolver(container.ResolverDefaults{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
}

// --- Resolution priority ---

func TestResolve_BotConfigTakesPriority(t *testing.T) {
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{
		"image": "bot-image:latest",
	})
	writeDevcontainer(t, repoDir, map[string]any{
		"image": "devcontainer-image:latest",
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "bot-image:latest" {
		t.Errorf("Image = %q, want bot-image:latest", cfg.Image)
	}
}

func TestResolve_FallsBackToDevcontainer(t *testing.T) {
	repoDir := t.TempDir()
	writeDevcontainer(t, repoDir, map[string]any{
		"image": "devcontainer-image:latest",
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "devcontainer-image:latest" {
		t.Errorf("Image = %q, want devcontainer-image:latest", cfg.Image)
	}
}

func TestResolve_ProfileOverrideProvidesImage(t *testing.T) {
	repoDir := t.TempDir()

	r := newTestResolver(t, "", container.ResourceLimits{})
	override := &container.SettingsOverride{Image: "admin-default:latest"}
	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "admin-default:latest" {
		t.Errorf("Image = %q, want admin-default:latest", cfg.Image)
	}
	if cfg.Source != "profile" {
		t.Errorf("Source = %q, want profile", cfg.Source)
	}
}

func TestResolve_ErrorWhenNoImageConfigured(t *testing.T) {
	repoDir := t.TempDir()

	r := newTestResolver(t, "", container.ResourceLimits{})
	_, err := r.Resolve(repoDir, nil)
	if err == nil {
		t.Fatal("expected error when no image is configured")
	}
	if !strings.Contains(err.Error(), "no container image configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Field merging ---

func TestResolve_ProfileLimitsFillGapsInRepoConfig(t *testing.T) {
	repoDir := t.TempDir()
	// Repo config sets image but not resource limits.
	writeBotConfig(t, repoDir, map[string]any{
		"image": "repo-image:latest",
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	override := &container.SettingsOverride{
		Limits: container.ResourceLimits{
			Memory: "8g",
			CPUs:   "4",
		},
	}
	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "repo-image:latest" {
		t.Errorf("Image = %q, want repo-image:latest", cfg.Image)
	}
	if cfg.ResourceLimits.Memory != "8g" {
		t.Errorf("Memory = %q, want 8g", cfg.ResourceLimits.Memory)
	}
	if cfg.ResourceLimits.CPUs != "4" {
		t.Errorf("CPUs = %q, want 4", cfg.ResourceLimits.CPUs)
	}
}

func TestResolve_ProfileLimitsOverrideRepoLimits(t *testing.T) {
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{
		"image": "repo-image:latest",
		"resourceLimits": map[string]string{
			"memory": "16g",
			"cpus":   "2",
		},
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	override := &container.SettingsOverride{
		Limits: container.ResourceLimits{
			Memory: "8g",
			CPUs:   "4",
		},
	}
	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	// Profile limits win over repo limits.
	if cfg.ResourceLimits.Memory != "8g" {
		t.Errorf("Memory = %q, want 8g (profile override wins)", cfg.ResourceLimits.Memory)
	}
	if cfg.ResourceLimits.CPUs != "4" {
		t.Errorf("CPUs = %q, want 4 (profile override wins)", cfg.ResourceLimits.CPUs)
	}
}

func TestResolve_RepoConfigNoImage_ProfileProvidesImage(t *testing.T) {
	repoDir := t.TempDir()
	// Repo config sets only env, not image.
	writeBotConfig(t, repoDir, map[string]any{
		"env": map[string]string{"KEY": "val"},
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	override := &container.SettingsOverride{
		Image:  "admin-default:latest",
		Limits: container.ResourceLimits{Memory: "4g"},
	}
	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "admin-default:latest" {
		t.Errorf("Image = %q, want admin-default:latest", cfg.Image)
	}
	if cfg.Env["KEY"] != "val" {
		t.Errorf("Env[KEY] = %q, want val", cfg.Env["KEY"])
	}
	if cfg.ResourceLimits.Memory != "4g" {
		t.Errorf("Memory = %q, want 4g", cfg.ResourceLimits.Memory)
	}
	// Profile provided the image, so source is "profile".
	if cfg.Source != "profile" {
		t.Errorf("Source = %q, want profile", cfg.Source)
	}
}

func TestResolve_EnvFromBotConfig(t *testing.T) {
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{
		"image": "img:latest",
		"env": map[string]string{
			"LANG": "en_US.UTF-8",
			"FOO":  "bar",
		},
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env["LANG"] != "en_US.UTF-8" {
		t.Errorf("Env[LANG] = %q, want en_US.UTF-8", cfg.Env["LANG"])
	}
	if cfg.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", cfg.Env["FOO"])
	}
}

func TestResolve_PostCreateCommandFromBotConfig(t *testing.T) {
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{
		"image":             "img:latest",
		"postCreateCommand": "make setup",
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PostCreateCommand != "make setup" {
		t.Errorf("PostCreateCommand = %q, want make setup", cfg.PostCreateCommand)
	}
}

func TestResolve_EnvAlwaysNonNil(t *testing.T) {
	repoDir := t.TempDir()
	r := newTestResolver(t, "", container.ResourceLimits{})
	override := &container.SettingsOverride{Image: "test:latest"}
	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env == nil {
		t.Fatal("Env should never be nil")
	}
}

// --- Devcontainer-specific tests ---

func TestResolve_DevcontainerPostCreateCommandString(t *testing.T) {
	repoDir := t.TempDir()
	writeDevcontainer(t, repoDir, map[string]any{
		"image":             "img:latest",
		"postCreateCommand": "npm install",
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PostCreateCommand != "npm install" {
		t.Errorf("PostCreateCommand = %q, want npm install", cfg.PostCreateCommand)
	}
}

func TestResolve_DevcontainerPostCreateCommandArray(t *testing.T) {
	repoDir := t.TempDir()
	writeDevcontainer(t, repoDir, map[string]any{
		"image":             "img:latest",
		"postCreateCommand": []string{"npm install", "npm run build"},
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "npm install && npm run build"
	if cfg.PostCreateCommand != want {
		t.Errorf("PostCreateCommand = %q, want %q", cfg.PostCreateCommand, want)
	}
}

func TestResolve_DevcontainerPostCreateCommandObjectIgnored(t *testing.T) {
	repoDir := t.TempDir()
	// The devcontainer spec also allows object form for postCreateCommand.
	// We only support string and string array; object form should be
	// ignored with a warning.
	writeDevcontainer(t, repoDir, map[string]any{
		"image": "img:latest",
		"postCreateCommand": map[string]any{
			"install": "npm install",
			"build":   "npm run build",
		},
	})

	core, logs := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	r, err := container.NewResolver(container.ResolverDefaults{}, logger)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PostCreateCommand != "" {
		t.Errorf("PostCreateCommand = %q, want empty for unsupported object form", cfg.PostCreateCommand)
	}

	warnings := logs.FilterMessage("Unsupported postCreateCommand format ignored (expected string or string array)")
	if warnings.Len() != 1 {
		t.Errorf("expected 1 warning for unsupported postCreateCommand format, got %d", warnings.Len())
	}
}

func TestResolve_DevcontainerContainerEnv(t *testing.T) {
	repoDir := t.TempDir()
	writeDevcontainer(t, repoDir, map[string]any{
		"image": "img:latest",
		"containerEnv": map[string]string{
			"GOPATH": "/go",
		},
	})

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env["GOPATH"] != "/go" {
		t.Errorf("Env[GOPATH] = %q, want /go", cfg.Env["GOPATH"])
	}
}

func TestResolve_DevcontainerUnsupportedFieldsLogged(t *testing.T) {
	repoDir := t.TempDir()
	writeDevcontainer(t, repoDir, map[string]any{
		"image":    "img:latest",
		"build":    map[string]string{"dockerfile": "Dockerfile"},
		"features": map[string]any{},
	})

	core, logs := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	r, err := container.NewResolver(container.ResolverDefaults{}, logger)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "img:latest" {
		t.Errorf("Image = %q, want img:latest", cfg.Image)
	}

	warnings := logs.FilterMessage("Unsupported devcontainer.json field ignored")
	if warnings.Len() < 2 {
		t.Errorf("expected at least 2 warnings for unsupported fields, got %d", warnings.Len())
	}
}

func TestResolve_DevcontainerJSONCComments(t *testing.T) {
	repoDir := t.TempDir()

	jsonc := `{
		// This is a single-line comment
		"image": "img-with-comments:latest",
		/* Multi-line
		   comment */
		"containerEnv": {
			"KEY": "value",
		}
	}`

	writeRawDevcontainer(t, repoDir, jsonc)

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "img-with-comments:latest" {
		t.Errorf("Image = %q, want img-with-comments:latest", cfg.Image)
	}
	if cfg.Env["KEY"] != "value" {
		t.Errorf("Env[KEY] = %q, want value", cfg.Env["KEY"])
	}
}

func TestResolve_DevcontainerJSONCUnterminatedComment(t *testing.T) {
	repoDir := t.TempDir()

	// An unterminated multi-line comment consumes remaining input
	// (except the final byte), producing truncated output that
	// json.Unmarshal rejects.
	jsonc := `{"image": "img:latest", /* never closed`

	writeRawDevcontainer(t, repoDir, jsonc)

	r := newTestResolver(t, "", container.ResourceLimits{})
	_, err := r.Resolve(repoDir, nil)
	if err == nil {
		t.Fatal("expected error for JSONC with unterminated multi-line comment")
	}
}

func TestResolve_DevcontainerJSONCPreservesURLsInStrings(t *testing.T) {
	repoDir := t.TempDir()

	// The // in the URL must not be treated as a comment.
	jsonc := `{
		"image": "registry.example.com//nested/image:latest",
		"containerEnv": {
			"SCHEMA": "https://example.com/schema.json"
		}
	}`

	writeRawDevcontainer(t, repoDir, jsonc)

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "registry.example.com//nested/image:latest" {
		t.Errorf("Image = %q, want URL preserved", cfg.Image)
	}
	if cfg.Env["SCHEMA"] != "https://example.com/schema.json" {
		t.Errorf("Env[SCHEMA] = %q, want URL preserved", cfg.Env["SCHEMA"])
	}
}

func TestResolve_DevcontainerTrailingCommaInStringPreserved(t *testing.T) {
	repoDir := t.TempDir()

	// The comma inside the string value must not be stripped, even
	// though it appears before }.
	jsonc := `{
		"image": "img:latest",
		"containerEnv": {
			"MSG": "hello, world"
		}
	}`

	writeRawDevcontainer(t, repoDir, jsonc)

	r := newTestResolver(t, "", container.ResourceLimits{})
	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env["MSG"] != "hello, world" {
		t.Errorf("Env[MSG] = %q, want 'hello, world'", cfg.Env["MSG"])
	}
}

// --- Error handling ---

func TestResolve_InvalidBotConfigJSON(t *testing.T) {
	repoDir := t.TempDir()
	dir := filepath.Join(repoDir, ".ai-bot")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "container.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newTestResolver(t, "", container.ResourceLimits{})
	_, err := r.Resolve(repoDir, nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestResolve_InvalidDevcontainerJSON(t *testing.T) {
	repoDir := t.TempDir()
	dir := filepath.Join(repoDir, ".devcontainer")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newTestResolver(t, "", container.ResourceLimits{})
	_, err := r.Resolve(repoDir, nil)
	if err == nil {
		t.Fatal("expected error for invalid devcontainer JSON")
	}
}

func TestResolve_EmptyBotConfig_NoProfile_Errors(t *testing.T) {
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{})

	r := newTestResolver(t, "", container.ResourceLimits{})
	_, err := r.Resolve(repoDir, nil)
	if err == nil {
		t.Fatal("expected error when repo config has no image and no profile override")
	}
	if !strings.Contains(err.Error(), "no container image configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolve_EmptyBotConfig_ProfileProvidesImage(t *testing.T) {
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{})

	r := newTestResolver(t, "", container.ResourceLimits{})
	override := &container.SettingsOverride{Image: "admin:latest"}
	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "admin:latest" {
		t.Errorf("Image = %q, want admin:latest", cfg.Image)
	}
	if cfg.Source != "profile" {
		t.Errorf("Source = %q, want profile", cfg.Source)
	}
}

// --- Source tracking ---

func TestResolve_SourceTracking(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, repoDir string)
		override *container.SettingsOverride
		wantSrc  string
	}{
		{
			name:     "profile",
			setup:    func(_ *testing.T, _ string) {},
			override: &container.SettingsOverride{Image: "profile:latest"},
			wantSrc:  "profile",
		},
		{
			name: "devcontainer",
			setup: func(t *testing.T, repoDir string) {
				t.Helper()
				writeDevcontainer(t, repoDir, map[string]any{
					"image": "dev:latest",
				})
			},
			wantSrc: ".devcontainer/devcontainer.json",
		},
		{
			name: "bot config",
			setup: func(t *testing.T, repoDir string) {
				t.Helper()
				writeBotConfig(t, repoDir, map[string]any{
					"image": "bot:latest",
				})
			},
			wantSrc: ".ai-bot/container.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := t.TempDir()
			tt.setup(t, repoDir)

			r := newTestResolver(t, "", container.ResourceLimits{})
			cfg, err := r.Resolve(repoDir, tt.override)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Source != tt.wantSrc {
				t.Errorf("Source = %q, want %q", cfg.Source, tt.wantSrc)
			}
		})
	}
}

// --- Profile override ---

func TestResolve_ProfileOverrideSetsImage(t *testing.T) {
	repoDir := t.TempDir()

	r, err := container.NewResolver(container.ResolverDefaults{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	override := &container.SettingsOverride{
		Image: "profile:latest",
	}

	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "profile:latest" {
		t.Errorf("Image = %q, want profile:latest", cfg.Image)
	}
	if cfg.Source != "profile" {
		t.Errorf("Source = %q, want profile", cfg.Source)
	}
}

func TestResolve_ProfileOverrideEnvMergedWithRepoConfig(t *testing.T) {
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{
		"image": "repo:latest",
		"env":   map[string]string{"FROM_REPO": "yes", "SHARED": "repo"},
	})

	r, err := container.NewResolver(container.ResolverDefaults{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	override := &container.SettingsOverride{
		Env: map[string]string{"FROM_PROFILE": "yes", "SHARED": "profile"},
	}

	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}

	// Repo env should be preserved.
	if cfg.Env["FROM_REPO"] != "yes" {
		t.Errorf("Env[FROM_REPO] = %q, want yes", cfg.Env["FROM_REPO"])
	}
	// Profile override env should be added.
	if cfg.Env["FROM_PROFILE"] != "yes" {
		t.Errorf("Env[FROM_PROFILE] = %q, want yes", cfg.Env["FROM_PROFILE"])
	}
	// Profile override wins on shared keys (operator wins).
	if cfg.Env["SHARED"] != "profile" {
		t.Errorf("Env[SHARED] = %q, want profile (profile override wins)", cfg.Env["SHARED"])
	}
}

func TestResolve_ProfileOverrideImageOverridesRepoConfig(t *testing.T) {
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{
		"image": "repo:latest",
	})

	r, err := container.NewResolver(container.ResolverDefaults{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	override := &container.SettingsOverride{
		Image: "profile:latest",
	}

	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "profile:latest" {
		t.Errorf("Image = %q, want profile:latest (profile wins over repo)", cfg.Image)
	}
}

// --- Host policy ---

func TestResolve_HostPolicyDisableSELinux(t *testing.T) {
	repoDir := t.TempDir()

	r, err := container.NewResolver(container.ResolverDefaults{
		DisableSELinux: true,
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	override := &container.SettingsOverride{Image: "test:latest"}
	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DisableSELinux {
		t.Error("expected DisableSELinux = true from host policy")
	}
}

func TestResolve_HostPolicyUserNS(t *testing.T) {
	repoDir := t.TempDir()

	r, err := container.NewResolver(container.ResolverDefaults{
		UserNS: "keep-id",
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	override := &container.SettingsOverride{Image: "test:latest"}
	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UserNS != "keep-id" {
		t.Errorf("UserNS = %q, want keep-id", cfg.UserNS)
	}
}

func TestResolve_HostPolicyOverridesRepoConfig(t *testing.T) {
	// Repo config cannot override host-level policy. Even if a repo
	// somehow provides conflicting values, host policy wins.
	repoDir := t.TempDir()
	writeBotConfig(t, repoDir, map[string]any{
		"image": "repo:latest",
	})

	r, err := container.NewResolver(container.ResolverDefaults{
		DisableSELinux: true,
		UserNS:         "keep-id:uid=1000,gid=1000",
	}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := r.Resolve(repoDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DisableSELinux {
		t.Error("expected DisableSELinux = true (host policy overrides)")
	}
	if cfg.UserNS != "keep-id:uid=1000,gid=1000" {
		t.Errorf("UserNS = %q, want keep-id:uid=1000,gid=1000", cfg.UserNS)
	}
}

// --- SettingsOverride Tmpfs and ExtraMounts ---

func TestResolve_ProfileOverrideTmpfs(t *testing.T) {
	repoDir := t.TempDir()

	r, err := container.NewResolver(container.ResolverDefaults{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	override := &container.SettingsOverride{
		Image: "test:latest",
		Tmpfs: []string{"/tmp:size=4g", "/run"},
	}

	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tmpfs) != 2 || cfg.Tmpfs[0] != "/tmp:size=4g" {
		t.Errorf("Tmpfs = %v, want [/tmp:size=4g /run]", cfg.Tmpfs)
	}
}

func TestResolve_ProfileOverrideExtraMounts(t *testing.T) {
	repoDir := t.TempDir()

	r, err := container.NewResolver(container.ResolverDefaults{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	override := &container.SettingsOverride{
		Image: "test:latest",
		ExtraMounts: []container.Mount{
			{Source: "/host/cache", Target: "/cache", Options: "ro"},
		},
	}

	cfg, err := r.Resolve(repoDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ExtraMounts) != 1 {
		t.Fatalf("ExtraMounts count = %d, want 1", len(cfg.ExtraMounts))
	}
	if cfg.ExtraMounts[0].Source != "/host/cache" || cfg.ExtraMounts[0].Target != "/cache" {
		t.Errorf("ExtraMounts[0] = %+v, want cache mount", cfg.ExtraMounts[0])
	}
}

// --- helpers ---

func newTestResolver(t *testing.T, _ string, _ container.ResourceLimits) *container.Resolver {
	t.Helper()
	r, err := container.NewResolver(container.ResolverDefaults{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func writeBotConfig(t *testing.T, repoDir string, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(repoDir, ".ai-bot")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "container.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDevcontainer(t *testing.T, repoDir string, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(repoDir, ".devcontainer")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devcontainer.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRawDevcontainer(t *testing.T, repoDir string, content string) {
	t.Helper()
	dir := filepath.Join(repoDir, ".devcontainer")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
