package experiment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mbl/ocbench/internal/canon"
)

func TestResolveOverlayNone(t *testing.T) {
	got, err := ResolveOverlay("")
	if err != nil {
		t.Fatalf("ResolveOverlay(\"\") error: %v", err)
	}
	if got.Kind != OverlayNone {
		t.Errorf("kind = %q, want %q", got.Kind, OverlayNone)
	}
	if got.Path != "" || got.SHA256 != "" || len(got.Env) != 0 {
		t.Errorf("none overlay carried data: %+v", got)
	}
}

func TestResolveOverlayFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	content := []byte(`{"model":"p/m"}`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveOverlay(path)
	if err != nil {
		t.Fatalf("ResolveOverlay: %v", err)
	}
	if got.Kind != OverlayFile {
		t.Errorf("kind = %q, want %q", got.Kind, OverlayFile)
	}
	if got.Path != path {
		t.Errorf("path = %q, want %q", got.Path, path)
	}
	if want := canon.SHA256Hex(content); got.SHA256 != want {
		t.Errorf("sha256 = %q, want %q", got.SHA256, want)
	}
	wantEnv := []string{"OPENCODE_CONFIG=" + path}
	if len(got.Env) != 1 || got.Env[0] != wantEnv[0] {
		t.Errorf("env = %v, want %v", got.Env, wantEnv)
	}
}

func TestResolveOverlayDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "build.md"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveOverlay(dir)
	if err != nil {
		t.Fatalf("ResolveOverlay: %v", err)
	}
	if got.Kind != OverlayDir {
		t.Errorf("kind = %q, want %q", got.Kind, OverlayDir)
	}
	if got.Path != dir {
		t.Errorf("path = %q, want %q", got.Path, dir)
	}
	if got.SHA256 == "" {
		t.Error("directory overlay sha256 is empty")
	}
	wantEnv := "OPENCODE_CONFIG_DIR=" + dir
	if len(got.Env) != 1 || got.Env[0] != wantEnv {
		t.Errorf("env = %v, want [%s]", got.Env, wantEnv)
	}
}

func TestResolveOverlayPathsAreAbsolute(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	got, err := ResolveOverlay("config.json")
	if err != nil {
		t.Fatalf("ResolveOverlay: %v", err)
	}
	if !filepath.IsAbs(got.Path) {
		t.Errorf("path = %q, want absolute", got.Path)
	}
	if filepath.Base(got.Path) != "config.json" {
		t.Errorf("path = %q, want base config.json", got.Path)
	}
	// The env value must be the absolute path, not the relative input.
	if len(got.Env) != 1 || got.Env[0] != "OPENCODE_CONFIG="+got.Path {
		t.Errorf("env = %v, want OPENCODE_CONFIG=%s", got.Env, got.Path)
	}
}

func TestResolveOverlayMissingPathErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	_, err := ResolveOverlay(path)
	if err == nil {
		t.Fatal("ResolveOverlay missing path: want error, got nil")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name path %q", err, path)
	}
}

func TestResolveOverlayNonRegularNonDirErrors(t *testing.T) {
	// /dev/null exists but is a character device: neither a regular file nor a
	// directory. The error must name the path.
	_, err := ResolveOverlay("/dev/null")
	if err == nil {
		t.Fatal("ResolveOverlay(/dev/null): want error, got nil")
	}
	if !strings.Contains(err.Error(), "/dev/null") {
		t.Errorf("error %q does not name path /dev/null", err)
	}
}

func TestResolveOverlayDirHashStableAndContentSensitive(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	if err := os.WriteFile(file, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := ResolveOverlay(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveOverlay(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 {
		t.Errorf("dir hash not stable: %q vs %q", first.SHA256, second.SHA256)
	}

	if err := os.WriteFile(file, []byte(`{"a":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := ResolveOverlay(dir)
	if err != nil {
		t.Fatal(err)
	}
	if third.SHA256 == first.SHA256 {
		t.Errorf("dir hash unchanged after file edit: %q", third.SHA256)
	}
}
