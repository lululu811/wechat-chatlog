package mdexport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_RequiresScript(t *testing.T) {
	_, err := New(Config{OutputDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "script") {
		t.Errorf("expected script required error, got: %v", err)
	}
}

func TestNew_RequiresOutputDir(t *testing.T) {
	_, err := New(Config{Script: "/nonexistent.sh"})
	if err == nil || !strings.Contains(err.Error(), "output dir") {
		t.Errorf("expected output dir required error, got: %v", err)
	}
}

func TestNew_CreatesOutputDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	exp, err := New(Config{Script: "/tmp/nonexistent.sh", OutputDir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("output dir not created: %v", err)
	}
}

func TestExport_RejectsInvalidURL(t *testing.T) {
	exp, err := New(Config{Script: "/tmp/x.sh", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = exp.Export(context.Background(), "https://example.com/not-wechat")
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected invalid url error, got: %v", err)
	}
}

func TestExport_NormalizesHTTPToHTTPS(t *testing.T) {
	exp, err := New(Config{Script: "/nonexistent-script.sh", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should fail on missing script, NOT on invalid URL — proves http→https normalization worked
	_, err = exp.Export(context.Background(), "http://mp.weixin.qq.com/s?test=1")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "invalid") {
		t.Errorf("http URL should have been normalized to https, got: %v", err)
	}
}

func TestCheck_MissingScript(t *testing.T) {
	exp, err := New(Config{Script: "/nonexistent/path.sh", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := exp.Check(); err == nil {
		t.Error("expected error for missing script")
	}
}

func TestCheck_Valid(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "test.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\necho ok\n"), 0755); err != nil {
		t.Fatal(err)
	}
	exp, err := New(Config{Script: script, OutputDir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := exp.Check(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestScanOutputDir(t *testing.T) {
	dir := t.TempDir()
	// Create nested structure
	os.MkdirAll(filepath.Join(dir, "acc1", "art1"), 0755)
	os.MkdirAll(filepath.Join(dir, "acc2", "art2"), 0755)
	os.WriteFile(filepath.Join(dir, "acc1", "art1", "art1.md"), []byte("# test"), 0644)
	os.WriteFile(filepath.Join(dir, "acc2", "art2", "art2.md"), []byte("# test2"), 0644)
	os.WriteFile(filepath.Join(dir, "acc1", "art1", "img.png"), []byte("fake"), 0644)

	result, err := scanOutputDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 .md files, got %d: %v", len(result), result)
	}
}
