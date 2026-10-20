//go:build !linux

package appui

// Windows and macOS can pop the tray menu up explicitly from OnRightClick,
// which keeps left clicks free for the open gesture.
func trayNeedsAttachedMenu() bool { return false }
