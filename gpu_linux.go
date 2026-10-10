package main

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	pciDevicesPath           = "/sys/bus/pci/devices"
	nvidiaVendorID           = "0x10de"
	nvidiaThreadOptimEnv     = "__GL_THREADED_OPTIMIZATIONS"
	nvidiaThreadOptimDisable = "0"
)

// configureGPUEnvironment 在 GTK/OpenGL 首次加载前设置 NVIDIA 驱动参数。
// NVIDIA 的 GL 线程化优化会与 MyGo 的 GTK render callback / libepoxy 调度
// 争用 context；检测到 NVIDIA 显卡时统一显式设为 0。
func configureGPUEnvironment() {
	configureGPUEnvironmentAt(pciDevicesPath)
}

func configureGPUEnvironmentAt(root string) {
	if hasNVIDIAGPU(root) {
		_ = os.Setenv(nvidiaThreadOptimEnv, nvidiaThreadOptimDisable)
	}
}

// hasNVIDIAGPU 通过 PCI vendor id 检测 NVIDIA 显示/3D 控制器，不启动
// nvidia-smi，也不加载 GL 库。
func hasNVIDIAGPU(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(root, entry.Name(), "vendor"))
		if err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(string(raw)), nvidiaVendorID) {
			return true
		}
	}
	return false
}
