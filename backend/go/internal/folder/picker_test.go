package folder

import (
	"context"
	"testing"
	"time"
)

func TestPickerResultsDoNotChangeConfiguration(t *testing.T) {
	for _, tc := range []struct{ raw, status, path string }{
		{`{"status":"SELECTED","path":"D:\\Downloads"}`, "SELECTED", `D:\Downloads`},
		{`{"status":"CANCELLED","path":"must not leak"}`, "CANCELLED", ""},
		{`{"status":"UNAVAILABLE","reasonCode":"NO_INTERACTIVE_DESKTOP"}`, "UNAVAILABLE", ""},
		{`{"status":"SELECTED"}`, "UNAVAILABLE", ""},
		{`invalid`, "UNAVAILABLE", ""},
	} {
		p := Picker{available: func() bool { return true }, run: func(context.Context) ([]byte, error) { return []byte(tc.raw), nil }}
		got := p.Pick(context.Background(), time.Second)
		if got.Status != tc.status || got.Path != tc.path {
			t.Fatalf("%+v", got)
		}
	}
}

func TestPickerUnavailableBusyAndBoundedCancellation(t *testing.T) {
	p := Picker{available: func() bool { return false }, run: func(context.Context) ([]byte, error) { t.Fatal("opened a dialog without desktop"); return nil, nil }}
	if got := p.Pick(context.Background(), time.Second); got.Reason != "NO_INTERACTIVE_DESKTOP" {
		t.Fatal(got)
	}
	p.available = func() bool { return true }
	started := make(chan struct{})
	p.run = func(ctx context.Context) ([]byte, error) { close(started); <-ctx.Done(); return nil, ctx.Err() }
	done := make(chan Result, 1)
	go func() { done <- p.Pick(context.Background(), 50*time.Millisecond) }()
	<-started
	if got := p.Pick(context.Background(), time.Second); got.Reason != "PICKER_BUSY" {
		t.Fatal(got)
	}
	if got := <-done; got.Status != "CANCELLED" || got.Reason != "PICKER_TIMED_OUT" {
		t.Fatal(got)
	}
}
