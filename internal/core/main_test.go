package core

import (
	"context"
	"os"
	"testing"
	"time"

	"codegen/internal/extension"
)

// A permission prompt nobody answers in a test fails it in seconds rather
// than waiting the app's ten minutes: a test that should answer one sets an
// asker (SetPermissionAsker).
func TestMain(m *testing.M) {
	AskPermissionTimeout = 3 * time.Second
	os.Exit(m.Run())
}

// allowAgentActions answers yes to every permission prompt, for tests that
// play an agent whose actions a person approves.
func allowAgentActions(h *Hub) {
	h.SetPermissionAsker(func(context.Context, extension.PermissionRequest) extension.PermissionAnswer {
		return extension.PermissionAnswer{Allow: true}
	})
}

// setTriggerClock gives the trigger scheduler a fake clock for this test.
func setTriggerClock(t *testing.T, f func() time.Time) {
	t.Helper()
	triggerClock.Store(&f)
	t.Cleanup(func() { triggerClock.Store(nil) })
}
