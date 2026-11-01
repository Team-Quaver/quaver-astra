// Package purego re-exports the purego symbols Liino's vendored forks use, backed
// by github.com/ebitengine/purego.
//
// Only the surface actually referenced by third_party/Liino is forwarded: add a
// new alias here if a future Liino bump starts using another symbol.
package purego

import (
	ep "github.com/ebitengine/purego"
)

// CDecl marks a callback as using the __cdecl calling convention. Liino's ffgo
// passes it as the first argument of its FFmpeg callbacks.
type CDecl = ep.CDecl

// RegisterLibFunc resolves a symbol and binds it to fptr.
var RegisterLibFunc = ep.RegisterLibFunc

// NewCallback turns a Go function into a C-callable function pointer.
var NewCallback = ep.NewCallback
