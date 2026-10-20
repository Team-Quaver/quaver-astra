package vt

import "unsafe"

// What a Mirror reads of a terminal besides its rows, which a render state
// reads.

const (
	rowDataWrap = 1

	dataKittyKeyboardFlags = 8
	dataScrollbackRows     = 15
	dataModifyOtherKeys    = 41
)

// ScrollbackRows returns how many rows the scrollback holds.
func (t *Terminal) ScrollbackRows() int {
	var n uintptr
	t.get(dataScrollbackRows, unsafe.Pointer(&n))
	return int(n)
}

// KittyKeyboardFlags returns the flags of the kitty keyboard protocol
// programs set.
func (t *Terminal) KittyKeyboardFlags() int {
	var f uint8
	t.get(dataKittyKeyboardFlags, unsafe.Pointer(&f))
	return int(f)
}

// ModifyOtherKeys reports whether xterm's modifyOtherKeys mode 2 is on.
func (t *Terminal) ModifyOtherKeys() bool {
	var v bool
	return t.get(dataModifyOtherKeys, unsafe.Pointer(&v)) == nil && v
}
