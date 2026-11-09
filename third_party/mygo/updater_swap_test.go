//go:build !js

package mygo

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/internal/update"
)

// swapTree creates files (path → content, a trailing slash a directory) in
// a new directory, and the update.FileList naming list unless it is nil.
func swapTree(t *testing.T, files map[string]string, list []string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		var err error
		if strings.HasSuffix(name, "/") {
			err = os.MkdirAll(p, 0o755)
		} else {
			err = os.WriteFile(p, []byte(content), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if list != nil {
		if err := update.WriteFileList(dir, list); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// swapState returns what target holds: its top-level entries with the
// content of the files, "/" for directories, and what its update.FileList
// names, nil without one.
func swapState(t *testing.T, target string) (map[string]string, []string) {
	t.Helper()
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]string{}
	for _, e := range entries {
		switch {
		case e.Name() == update.FileList:
		case e.IsDir():
			state[e.Name()] = "/"
		default:
			b, _ := os.ReadFile(filepath.Join(target, e.Name()))
			state[e.Name()] = string(b)
		}
	}
	b, err := os.ReadFile(filepath.Join(target, update.FileList))
	if err != nil {
		return state, nil
	}
	return state, update.ParseFileList(b)
}

func TestSwapFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed map[string]string
		list      []string // nil: an app installed before versions had one
		update    map[string]string
		want      map[string]string
		wantList  []string
	}{{
		name:      "removes what the update lacks",
		installed: map[string]string{"app.exe": "v1", "old.txt": "old", "resources/": "", "resources/icon.png": "icon", "keep.txt": "same"},
		list:      []string{"app.exe", "keep.txt", "old.txt", "resources", "gone.txt"},
		update:    map[string]string{"app.exe": "v2", "keep.txt": "same", "new.txt": "new"},
		want:      map[string]string{"app.exe": "v2", "keep.txt": "same", "new.txt": "new"},
		wantList:  []string{"app.exe", "keep.txt", "new.txt"},
	}, {
		// The user's files, and the installer's uninstaller, are in no list.
		name:      "leaves what no list names",
		installed: map[string]string{"app.exe": "v1", "old.txt": "old", "data/": "", "data/notes": "mine", "Uninstall.exe": "u"},
		list:      []string{"app.exe", "old.txt"},
		update:    map[string]string{"app.exe": "v2"},
		want:      map[string]string{"app.exe": "v2", "data": "/", "Uninstall.exe": "u"},
		wantList:  []string{"app.exe"},
	}, {
		name:      "removes nothing without a list",
		installed: map[string]string{"app.exe": "v1", "old.txt": "old", "data/": ""},
		update:    map[string]string{"app.exe": "v2", "new.txt": "new"},
		want:      map[string]string{"app.exe": "v2", "old.txt": "old", "data": "/", "new.txt": "new"},
		wantList:  []string{"app.exe", "new.txt"},
	}, {
		// The same entry on Windows and macOS, two on Linux.
		name:      "an entry renamed in case",
		installed: map[string]string{"app.exe": "v1", "Readme.txt": "old"},
		list:      []string{"app.exe", "Readme.txt"},
		update:    map[string]string{"app.exe": "v2", "readme.txt": "new"},
		want:      map[string]string{"app.exe": "v2", "readme.txt": "new"},
		wantList:  []string{"app.exe", "readme.txt"},
	}, {
		// The list of the update is replaced by the entries it has.
		name:      "an update with a list",
		installed: map[string]string{"app.exe": "v1", "old.txt": "old"},
		list:      []string{"app.exe", "old.txt"},
		update:    map[string]string{"app.exe": "v2", update.FileList: "app.exe\nother\n"},
		want:      map[string]string{"app.exe": "v2"},
		wantList:  []string{"app.exe"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			target := swapTree(t, tc.installed, tc.list)
			dir := swapTree(t, tc.update, nil)
			if err := swapFiles(dir, target); err != nil {
				t.Fatal(err)
			}
			got, list := swapState(t, target)
			if !maps.Equal(got, tc.want) {
				t.Errorf("after the update the app holds %v, want %v", got, tc.want)
			}
			if !slices.Equal(list, tc.wantList) {
				t.Errorf("after the update the list names %q, want %q", list, tc.wantList)
			}
			if leftovers, _ := filepath.Glob(filepath.Join(target, ".*.old-*")); len(leftovers) > 0 {
				t.Errorf("left behind: %q", leftovers)
			}
		})
	}
}

// A swap that fails puts back what it replaced and removed, and takes out
// what it added.
func TestSwapFilesUndo(t *testing.T) {
	installed := map[string]string{"app.exe": "v1", "old.txt": "old", "resources/": "", "resources/icon.png": "icon", "z.dll": "v1", "data/": ""}
	list := []string{"app.exe", "old.txt", "resources", "z.dll"}
	target := swapTree(t, installed, list)
	// z.dll, the last entry, cannot be moved aside.
	blocker := filepath.Join(target, ".z.dll.old-"+strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := swapTree(t, map[string]string{"app.exe": "v2", "new.txt": "new", "z.dll": "v2"}, nil)
	if err := swapFiles(dir, target); err == nil {
		t.Fatal("the swap succeeded")
	}
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatal(err)
	}
	got, gotList := swapState(t, target)
	want := map[string]string{"app.exe": "v1", "old.txt": "old", "resources": "/", "z.dll": "v1", "data": "/"}
	if !maps.Equal(got, want) {
		t.Errorf("after the failed update the app holds %v, want %v", got, want)
	}
	if !slices.Equal(gotList, list) {
		t.Errorf("after the failed update the list names %q, want %q", gotList, list)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "resources", "icon.png")); string(b) != "icon" {
		t.Errorf("after the failed update resources/icon.png = %q", b)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(target, ".*.old-*")); len(leftovers) > 0 {
		t.Errorf("left behind: %q", leftovers)
	}
}

// The executable the app runs from stays, as shortcuts and Relaunch start
// it, though the new version names its own otherwise.
func TestSwapFilesKeepsTheRunningExecutable(t *testing.T) {
	target := swapTree(t, map[string]string{"Old Name.exe": "v1", "old.txt": "old"}, []string{"Old Name.exe", "old.txt"})
	old := startExe
	startExe = filepath.Join(target, "Old Name.exe")
	t.Cleanup(func() { startExe = old })
	dir := swapTree(t, map[string]string{"New Name.exe": "v2"}, nil)
	if err := swapFiles(dir, target); err != nil {
		t.Fatal(err)
	}
	got, list := swapState(t, target)
	if want := map[string]string{"Old Name.exe": "v1", "New Name.exe": "v2"}; !maps.Equal(got, want) {
		t.Errorf("after the update the app holds %v, want %v", got, want)
	}
	if want := []string{"New Name.exe"}; !slices.Equal(list, want) {
		t.Errorf("after the update the list names %q, want %q", list, want)
	}
}
