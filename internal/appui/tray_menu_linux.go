//go:build linux

package appui

// Linux's AppIndicator owns the icon menu: the panel opens it on secondary
// click, and the attached menu is also what makes the item visible.
func trayNeedsAttachedMenu() bool { return true }
