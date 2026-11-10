package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"codegen/internal/bus"
	"codegen/internal/extension"
)

func greet(secrets ...extension.Secret) extension.Extension {
	return extension.Extension{Name: "greet", Kind: "script", Secrets: secrets}
}

func TestSecretFromEnvironment(t *testing.T) {
	h, _ := newHub(t)
	t.Setenv("SPECWRIGHT_TEST_TOKEN", "env-value-123")
	e := greet(extension.Secret{Name: "API_TOKEN", Env: "SPECWRIGHT_TEST_TOKEN", Source: "env"})
	v, err := h.ResolveSecrets(context.Background(), e)
	if err != nil || v["API_TOKEN"] != "env-value-123" {
		t.Fatalf("%v %v", v, err)
	}
	if st := h.SecretInfo(e.Secrets)[0]; st.Via != "env" || !st.EnvSet {
		t.Fatalf("%+v", st)
	}
	if got := h.Store.Redactor().String("token=env-value-123"); strings.Contains(got, "env-value-123") {
		t.Fatalf("env value must be redacted: %s", got)
	}
	_, err = h.ResolveSecrets(context.Background(), greet(extension.Secret{Name: "X", Env: "SPECWRIGHT_UNSET_VAR", Source: "env"}))
	if err == nil || !strings.Contains(err.Error(), "SPECWRIGHT_UNSET_VAR") {
		t.Fatalf("env-only secret must name the missing variable: %v", err)
	}
}

// answerNext answers the next secret.request on the bus.
func answerNext(t *testing.T, h *Hub, values map[string]string, remember, decline bool) {
	ch, unsub := h.Bus.Subscribe(64, func(e bus.Event) bool { return e.Type == "secret.request" })
	go func() {
		defer unsub()
		select {
		case e := <-ch:
			id := e.Str("id")
			if decline {
				_ = h.CancelSecrets(id)
			} else if err := h.AnswerSecrets(id, values, remember); err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("no secret.request")
		}
	}()
}

func TestSecretAskedFromUser(t *testing.T) {
	h, _ := newHub(t)
	e := greet(extension.Secret{Name: "DB_PASSWORD", Env: "SPECWRIGHT_UNSET_VAR"}) // auto: not in env → ask
	if st := h.SecretInfo(e.Secrets)[0]; st.Via != "ask" {
		t.Fatalf("%+v", st)
	}
	answerNext(t, h, map[string]string{"DB_PASSWORD": "typed-secret-9"}, false, false)
	v, err := h.ResolveSecrets(context.Background(), e)
	if err != nil || v["DB_PASSWORD"] != "typed-secret-9" {
		t.Fatalf("%v %v", v, err)
	}
	// not remembered: the next run asks again; this time the user declines
	answerNext(t, h, nil, false, true)
	if _, err := h.ResolveSecrets(context.Background(), e); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("decline must fail the call: %v", err)
	}
	// remembered: the next run does not ask
	ask := greet(extension.Secret{Name: "DB_PASSWORD", Source: "ask"})
	answerNext(t, h, map[string]string{"DB_PASSWORD": "kept-secret-7"}, true, false)
	if _, err := h.ResolveSecrets(context.Background(), ask); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if v, err := h.ResolveSecrets(ctx, ask); err != nil || v["DB_PASSWORD"] != "kept-secret-7" {
		t.Fatalf("remembered value should be reused without asking: %v %v", v, err)
	}
}

func TestToolTestAsksForSecret(t *testing.T) {
	h, _ := newHub(t)
	d := extension.Extension{Name: "show-user", Desc: "Print the DB user.", Kind: "script", Lang: "Bash",
		Secrets: []extension.Secret{{Name: "DB_USER", Source: "ask"}}, Call: map[string]any{},
		Command: "bash .codegen/tools/show-user.sh", Script: "#!/usr/bin/env bash\necho \"user is ${#DB_USER} chars\"\n"}
	answerNext(t, h, map[string]string{"DB_USER": "alice-the-admin"}, false, false)
	tr, err := h.TestExtension(d, "Who is the DB user?")
	if err != nil || !tr.Pass || tr.Output != "user is 15 chars" {
		t.Fatalf("%+v %v", tr, err)
	}
}
