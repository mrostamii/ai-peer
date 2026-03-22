package node

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

type HardwareInfo struct {
	OS        string
	Arch      string
	GPU       string
	RAMBytes  uint64
	VRAMBytes uint64
}

var (
	lookPathFn = exec.LookPath
	runCmdFn   = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	}
	readFileFn = os.ReadFile
)

func DetectHardware() HardwareInfo {
	return detectHardware(runtime.GOOS, runtime.GOARCH)
}

func detectHardware(goos, goarch string) HardwareInfo {
	out := HardwareInfo{
		OS:   goos,
		Arch: goarch,
		GPU:  "none",
	}

	out.RAMBytes = detectRAMBytes(goos)
	out.GPU, out.VRAMBytes = detectGPUAndVRAM(goos, goarch, out.RAMBytes)
	return out
}

func detectRAMBytes(goos string) uint64 {
	switch goos {
	case "linux":
		raw, err := readFileFn("/proc/meminfo")
		if err != nil {
			return 0
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "MemTotal:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0
			}
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	case "darwin":
		raw, err := runCmdFn("sysctl", "-n", "hw.memsize")
		if err != nil {
			return 0
		}
		v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			return 0
		}
		return v
	}
	return 0
}

func detectGPUAndVRAM(goos, goarch string, ramBytes uint64) (string, uint64) {
	if _, err := lookPathFn("nvidia-smi"); err == nil {
		raw, err := runCmdFn("nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits")
		if err == nil {
			line := strings.TrimSpace(string(bytes.SplitN(raw, []byte("\n"), 2)[0]))
			mib, err := strconv.ParseUint(line, 10, 64)
			if err == nil {
				return "nvidia", mib * 1024 * 1024
			}
		}
		return "nvidia", 0
	}

	if goos == "darwin" && goarch == "arm64" {
		// Apple Silicon uses unified memory; report the same budget as VRAM.
		return "apple-silicon", ramBytes
	}

	return "none", 0
}
