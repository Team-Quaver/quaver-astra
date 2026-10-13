package main

import (
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/appid"
)

func TestApplicationIdentifier(t *testing.T) {
	if appid.Identifier != "red.0w0.quaver-astra" {
		t.Fatalf("appid.Identifier = %q", appid.Identifier)
	}
	if mygoPackageIdentifier != appid.Identifier {
		t.Fatalf("MyGo package identifier = %q, want %q", mygoPackageIdentifier, appid.Identifier)
	}
}
