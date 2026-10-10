package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasNVIDIAGPU(t *testing.T) {
	root := t.TempDir()
	if hasNVIDIAGPU(root) {
		t.Fatal("empty PCI tree detected NVIDIA")
	}
	for name, vendor := range map[string]string{
		"0000:01:00.0": "0x10de\n",
		"0000:00:02.0": "0x8086\n",
	} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "vendor"), []byte(vendor), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !hasNVIDIAGPU(root) {
		t.Fatal("NVIDIA PCI device was not detected")
	}

	t.Setenv(nvidiaThreadOptimEnv, "")
	os.Unsetenv(nvidiaThreadOptimEnv)
	configureGPUEnvironmentAt(root)
	if got := os.Getenv(nvidiaThreadOptimEnv); got != "0" {
		t.Fatalf("%s = %q, want 0", nvidiaThreadOptimEnv, got)
	}

	t.Setenv(nvidiaThreadOptimEnv, "1")
	configureGPUEnvironmentAt(root)
	if got := os.Getenv(nvidiaThreadOptimEnv); got != "0" {
		t.Fatalf("explicit %s = %q, want forced 0", nvidiaThreadOptimEnv, got)
	}
}
