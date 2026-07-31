package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRefusingOpener_RefusesAndNamesTheURL(t *testing.T) {
	err := NewRefusingOpener().Open(context.Background(), "http://127.0.0.1:8642/")
	if err == nil {
		t.Fatal("RefusingOpener must fail, not silently succeed")
	}
	if !errors.Is(err, ErrNoOpener) {
		t.Errorf("refusal does not unwrap to ErrNoOpener: %v", err)
	}
	if !strings.Contains(err.Error(), "http://127.0.0.1:8642/") {
		t.Errorf("refusal must name the URL it declined to open, got: %v", err)
	}
}

func TestFakeOpener_RecordsURLsInOrder(t *testing.T) {
	f := NewFakeOpener()
	if got := f.URLs(); got == nil || len(got) != 0 {
		t.Fatalf("fresh FakeOpener should report an empty non-nil slice, got %#v", got)
	}

	if err := f.Open(context.Background(), "http://127.0.0.1:8642/"); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := f.Open(context.Background(), "http://127.0.0.1:8643/"); err != nil {
		t.Fatalf("second Open: %v", err)
	}

	got := f.URLs()
	if len(got) != 2 || got[0] != "http://127.0.0.1:8642/" || got[1] != "http://127.0.0.1:8643/" {
		t.Errorf("recorded URLs wrong or out of order: %v", got)
	}
}

func TestFakeOpener_InjectedErrorSurfacesAndTheCallIsStillRecorded(t *testing.T) {
	f := NewFakeOpener()
	injected := errors.New("no display")
	f.InjectError(injected)

	err := f.Open(context.Background(), "http://127.0.0.1:8642/")
	if !errors.Is(err, injected) {
		t.Fatalf("Open did not surface the injected error, got: %v", err)
	}
	if got := f.URLs(); len(got) != 1 {
		t.Errorf("a failed Open must still be recorded, got %v", got)
	}
}

func TestFakeOpener_CancelledContextRefusesWithoutRecording(t *testing.T) {
	f := NewFakeOpener()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := f.Open(ctx, "http://127.0.0.1:8642/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open on a cancelled context must return the context error, got: %v", err)
	}
	if got := f.URLs(); len(got) != 0 {
		t.Errorf("a cancelled Open must not be recorded, got %v", got)
	}
}
