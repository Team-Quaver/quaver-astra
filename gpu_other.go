//go:build !linux

package main

// configureGPUEnvironment 在非 Linux 平台不设置 Linux NVIDIA 驱动参数。
func configureGPUEnvironment() {}
