package core

// Hooks the built-in tools need from the app: a secret by name (for
// http_request and notify), and a desktop notification (notify).

import (
	"context"
	"encoding/base64"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf16"

	"codegen/internal/bus"
	"codegen/internal/extension"
)

// secretByName finds a secret's value as a project tool would (session,
// environment, keychain, or asking the user) and keeps it out of every
// result and record (the redactor).
func (h *Hub) secretByName(ctx context.Context, name string) (string, error) {
	vals, err := h.ResolveSecrets(ctx, extension.Extension{Name: "a built-in tool", Desc: "A built-in tool needs " + name,
		Secrets: []extension.Secret{{Name: name}}})
	if err != nil {
		return "", err
	}
	v := vals[name]
	h.Store.Redactor().Add(v)
	return v, nil
}

// desktopNotify shows a notification on this computer, and the app's own
// message (the toast and the status bar's list).
func (h *Hub) desktopNotify(title, message, link string) error {
	h.Bus.Publish(bus.Event{Type: "notify", Summary: title, Data: map[string]any{"title": title, "message": message, "link": link}})
	if notifyCommand == nil {
		return nil
	}
	go func() { _ = notifyCommand(title, message) }()
	return nil
}

// notifyCommand shows an OS notification (replaceable in tests). The text
// goes to the OS as arguments, never inside a script.
var notifyCommand = func(title, message string) error {
	msg := strings.TrimSpace(message)
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, msg).Run()
	case "linux":
		if _, err := exec.LookPath("notify-send"); err != nil {
			return err
		}
		return exec.Command("notify-send", "--app-name=SpecWright", "--", title, msg).Run()
	case "windows":
		// PowerShell would join extra arguments into the command, so the
		// text goes in as single-quoted literals (a quote doubled) and the
		// script is sent encoded
		lit := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		ps := `$t = ` + lit(title) + `; $m = ` + lit(msg) + `; ` +
			`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null; ` +
			`$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); ` +
			`$n = $x.GetElementsByTagName('text'); $n.Item(0).AppendChild($x.CreateTextNode($t)) > $null; $n.Item(1).AppendChild($x.CreateTextNode($m)) > $null; ` +
			`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('SpecWright').Show([Windows.UI.Notifications.ToastNotification]::new($x))`
		u := utf16.Encode([]rune(ps))
		raw := make([]byte, 0, 2*len(u))
		for _, c := range u {
			raw = append(raw, byte(c), byte(c>>8))
		}
		return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw)).Run()
	}
	return nil
}
