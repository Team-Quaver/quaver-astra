package main

import (
	"sync/atomic"
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

func TestStartupShowOnce(t *testing.T) {
	var shown atomic.Bool
	var calls atomic.Int32
	show := startupShowOnce(&shown, func() { calls.Add(1) })
	show()
	show()
	if got := calls.Load(); got != 1 {
		t.Fatalf("show callback ran %d times, want 1", got)
	}
}
