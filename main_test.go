package main

import "testing"

func TestApplicationIdentifier(t *testing.T) {
	if appIdentifier != "red.0w0.quaver-astra" {
		t.Fatalf("appIdentifier = %q", appIdentifier)
	}
	if mygoPackageIdentifier != appIdentifier {
		t.Fatalf("MyGo package identifier = %q, want %q", mygoPackageIdentifier, appIdentifier)
	}
}
