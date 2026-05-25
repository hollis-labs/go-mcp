package server

import (
	"context"
	"testing"
)

func TestNotifyHelpers(t *testing.T) {
	var got []Notification
	ctx := WithNotifier(context.Background(), func(n Notification) {
		got = append(got, n)
	})

	if !NotifyMessage(ctx, "info", "hello") {
		t.Fatal("NotifyMessage returned false")
	}
	if !NotifyProgress(ctx, "tok-1", 1, 3, "step") {
		t.Fatal("NotifyProgress returned false")
	}
	if len(got) != 2 {
		t.Fatalf("notification count = %d, want 2", len(got))
	}
	if got[0].Method != "notifications/message" {
		t.Fatalf("first method = %q", got[0].Method)
	}
	if got[1].Method != "notifications/progress" {
		t.Fatalf("second method = %q", got[1].Method)
	}
}
