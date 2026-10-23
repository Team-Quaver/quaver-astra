package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// debData returns the files of the data archive of a Debian package: their
// contents, and for links the target.
func debData(t *testing.T, deb string) map[string]string { return debMember(t, deb, "data.tar.gz") }

// debMember returns the files of an archive of a Debian package, such as
// control.tar.gz or data.tar.gz.
func debMember(t *testing.T, deb, archive string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(deb)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := bytes.CutPrefix(b, []byte("!<arch>\n"))
	if !ok {
		t.Fatal("not an ar archive")
	}
	for len(b) >= 60 {
		name := strings.TrimSpace(string(b[:16]))
		size, err := strconv.Atoi(strings.TrimSpace(string(b[48:58])))
		if err != nil {
			t.Fatal(err)
		}
		member := b[60 : 60+size]
		b = b[60+size+size%2:]
		if name != archive {
			continue
		}
		gz, err := gzip.NewReader(bytes.NewReader(member))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return files
			}
			if err != nil {
				t.Fatal(err)
			}
			content, _ := io.ReadAll(tr)
			if hdr.Typeflag == tar.TypeSymlink {
				content = []byte(hdr.Linkname)
			}
			files[strings.TrimPrefix(hdr.Name, "./")] = string(content)
		}
	}
	t.Fatal("no " + archive)
	return nil
}

func TestDebCommand(t *testing.T) {
	for _, command := range []string{"", "my-app-gui"} {
		c := &Config{root: t.TempDir(), Name: "My App", Version: "1.0.0"}
		c.Linux.Command = command
		stage := t.TempDir()
		if err := os.WriteFile(filepath.Join(stage, "my-app"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		deb, err := writeDeb(c, stage, "my-app", "amd64", []string{"my-app"})
		if err != nil {
			t.Fatal(err)
		}
		files := debData(t, deb)
		var commands []string
		for name := range files {
			if rest, ok := strings.CutPrefix(name, "usr/bin/"); ok && rest != "" {
				commands = append(commands, rest)
			}
		}
		switch {
		case command == "" && len(commands) != 0:
			t.Errorf("without a command, the package has %v", commands)
		case command != "" && (len(commands) != 1 || files["usr/bin/"+command] != "../../opt/my-app/my-app"):
			t.Errorf("with the command %s, the package has %v, linked to %q", command, commands, files["usr/bin/"+command])
		}
		if entry := files["usr/share/applications/my-app.desktop"]; !strings.Contains(entry, "\nExec=/opt/my-app/my-app\n") {
			t.Errorf("desktop entry:\n%s", entry)
		}
	}
}

// An app that shows no web page depends on GTK alone, and install.sh does
// not ask for WebKitGTK (#551).
func TestLinuxWebView(t *testing.T) {
	for _, webview := range []*bool{nil, new(true), new(false)} {
		c := &Config{root: t.TempDir(), Name: "My App", Version: "1.0.0"}
		c.Linux.WebView = webview
		c.Linux.Depends = []string{"libayatana-appindicator3-1"}
		stage := t.TempDir()
		if err := os.WriteFile(filepath.Join(stage, "my-app"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		deb, err := writeDeb(c, stage, "my-app", "amd64", []string{"my-app"})
		if err != nil {
			t.Fatal(err)
		}
		want := "\nDepends: libgtk-3-0 | libgtk-3-0t64, libwebkit2gtk-4.1-0, libayatana-appindicator3-1\n"
		asks := true
		if webview != nil && !*webview {
			want = "\nDepends: libgtk-3-0 | libgtk-3-0t64, libayatana-appindicator3-1\n"
			asks = false
		}
		if control := debMember(t, deb, "control.tar.gz")["control"]; !strings.Contains(control, want) {
			t.Errorf("webview %v: control:\n%s", c.Linux.webView(), control)
		}
		if got := strings.Contains(installScript(c), "if ! has_webkit; then"); got != asks {
			t.Errorf("webview %v: install.sh checks for WebKitGTK: %v", c.Linux.webView(), got)
		}
	}
}

func TestLinuxCommandName(t *testing.T) {
	for command, ok := range map[string]bool{"": true, "my-app": true, "my_app2.1+": true, "-v": false, "my app": false, "../app": false, "bin/app": false} {
		c := &Config{}
		c.Linux.Command = command
		if err := c.validate(); (err == nil) != ok {
			t.Errorf("linux.command %q: %v", command, err)
		}
	}
}
