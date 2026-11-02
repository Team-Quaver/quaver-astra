// This module exists only to unify purego copies inside the Quaver Astra binary.
//
// Liino's vendored ffgo and purego-pipewire import github.com/bnema/purego (a
// fork of ebitengine/purego), while MyGo and Astra's own FFI code import
// github.com/ebitengine/purego. Both packages ship dlfcn_stubs.s, whose
// `TEXT dlopen(SB)` symbols are process-global, so linking both copies fails
// with "duplicated definition of symbol dlopen".
//
// The shim keeps the import path (so Liino's sources stay untouched) but backs
// every symbol with ebitengine/purego, leaving exactly one copy of the
// assembly stubs in the final binary.
module github.com/bnema/purego

go 1.26.2

require github.com/ebitengine/purego v0.11.1
