//go:build darwin || freebsd || linux || netbsd || android

package purego

import (
	ep "github.com/ebitengine/purego"
)

// RTLD flags keep the same semantics as dlfcn.h. The values come from the
// platform-specific files in ebitengine/purego, so they must be re-exported
// rather than hard-coded here.
const (
	RTLD_NOW    = ep.RTLD_NOW
	RTLD_GLOBAL = ep.RTLD_GLOBAL
)

// Dlopen / Dlclose back ffgo's and purego-pipewire's dynamic loaders on
// Unix-like systems. Windows has no dlfcn equivalent; ffgo uses
// LoadLibraryExW there and never references these.
var (
	Dlopen  = ep.Dlopen
	Dlclose = ep.Dlclose
)
