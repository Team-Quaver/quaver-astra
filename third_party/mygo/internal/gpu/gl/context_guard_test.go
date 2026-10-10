package gl

import "testing"

func TestRejectsMissingGDKContext(t *testing.T) {
	old := currentGDKContext
	currentGDKContext = func() uintptr { return 0 }
	defer func() { currentGDKContext = old }()

	if _, err := New(); err == nil {
		t.Error("New() with no current GDK context returned nil error")
	}
	var p Presenter
	if err := p.Present(make([]byte, 4), 4, 1, 1); err == nil {
		t.Error("Presenter.Present with no current GDK context returned nil error")
	}
}
