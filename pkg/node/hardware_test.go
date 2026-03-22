package node

import (
	"errors"
	"testing"
)

func TestDetectHardwareLinuxNVIDIA(t *testing.T) {
	t.Parallel()
	oldLookPath := lookPathFn
	oldRunCmd := runCmdFn
	oldReadFile := readFileFn
	t.Cleanup(func() {
		lookPathFn = oldLookPath
		runCmdFn = oldRunCmd
		readFileFn = oldReadFile
	})

	lookPathFn = func(file string) (string, error) {
		if file == "nvidia-smi" {
			return "/usr/bin/nvidia-smi", nil
		}
		return "", errors.New("not found")
	}
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return []byte("12288\n"), nil
		}
		return nil, errors.New("unexpected command")
	}
	readFileFn = func(name string) ([]byte, error) {
		if name == "/proc/meminfo" {
			return []byte("MemTotal:       32768000 kB\n"), nil
		}
		return nil, errors.New("unexpected file")
	}

	hw := detectHardware("linux", "amd64")
	if hw.OS != "linux" || hw.Arch != "amd64" {
		t.Fatalf("unexpected platform: %+v", hw)
	}
	if hw.GPU != "nvidia" {
		t.Fatalf("expected nvidia gpu, got %q", hw.GPU)
	}
	if hw.RAMBytes != 32768000*1024 {
		t.Fatalf("unexpected ram bytes: %d", hw.RAMBytes)
	}
	if hw.VRAMBytes != 12288*1024*1024 {
		t.Fatalf("unexpected vram bytes: %d", hw.VRAMBytes)
	}
}

func TestDetectHardwareAppleSiliconFallback(t *testing.T) {
	t.Parallel()
	oldLookPath := lookPathFn
	oldRunCmd := runCmdFn
	t.Cleanup(func() {
		lookPathFn = oldLookPath
		runCmdFn = oldRunCmd
	})

	lookPathFn = func(file string) (string, error) {
		return "", errors.New("not found")
	}
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		if name == "sysctl" {
			return []byte("17179869184\n"), nil
		}
		return nil, errors.New("unexpected command")
	}

	hw := detectHardware("darwin", "arm64")
	if hw.GPU != "apple-silicon" {
		t.Fatalf("expected apple-silicon gpu, got %q", hw.GPU)
	}
	if hw.RAMBytes != 17179869184 {
		t.Fatalf("unexpected ram bytes: %d", hw.RAMBytes)
	}
	if hw.VRAMBytes != 17179869184 {
		t.Fatalf("expected unified memory as vram bytes, got %d", hw.VRAMBytes)
	}
}
