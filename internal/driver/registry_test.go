package driver

import (
	"context"
	"testing"
)

type stubDriver struct{ name string }

func (s *stubDriver) Info() Info                                { return Info{Name: s.name} }
func (s *stubDriver) Detect(ctx context.Context) error          { return nil }
func (s *stubDriver) Open(ctx context.Context) (Session, error) { return nil, nil }

func TestRegisterAndGet(t *testing.T) {
	Register("test-driver-a", func() Driver { return &stubDriver{name: "test-driver-a"} })

	got, err := Get("test-driver-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Info().Name != "test-driver-a" {
		t.Errorf("got %q", got.Info().Name)
	}
}

func TestGet_UnknownDriver(t *testing.T) {
	if _, err := Get("does-not-exist"); err == nil {
		t.Fatal("expected an error for an unknown driver name")
	}
}

func TestRegister_NilConstructorPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for a nil constructor")
		}
	}()
	Register("test-driver-nil", nil)
}

func TestRegister_DuplicatePanics(t *testing.T) {
	Register("test-driver-b", func() Driver { return &stubDriver{name: "test-driver-b"} })
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for a duplicate registration")
		}
	}()
	Register("test-driver-b", func() Driver { return &stubDriver{name: "test-driver-b"} })
}

func TestNames(t *testing.T) {
	Register("test-driver-c", func() Driver { return &stubDriver{name: "test-driver-c"} })
	found := false
	for _, n := range Names() {
		if n == "test-driver-c" {
			found = true
		}
	}
	if !found {
		t.Error("expected Names() to include test-driver-c")
	}
}
