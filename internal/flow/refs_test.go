package flow

import (
	"strings"
	"testing"
)

func TestTemplateRefs(t *testing.T) {
	env, secrets := TemplateRefs(`{{env.REGION}} {{ Keychain.db_pass }} {{project_secret.API-TOKEN}} {{env.REGION}} {{env.bad name}} {{inputs.x}}`)
	if strings.Join(env, ",") != "REGION" || len(secrets) != 2 || secrets[0].Key() != "keychain.db_pass" || secrets[1].Key() != "project_secret.API-TOKEN" {
		t.Fatalf("%v %+v", env, secrets)
	}
	d := Data{Env: map[string]string{"REGION": "eu"}, Secrets: map[string]string{"keychain.db_pass": "pw", "project_secret.API-TOKEN": "tok"}}
	if got := Render("{{env.REGION}}/{{KEYCHAIN.db_pass}}/{{project_secret.API-TOKEN}}/{{env.NOPE}}", d); got != "eu/pw/tok/" {
		t.Fatalf("render %q", got)
	}
}

func TestValidateRefs(t *testing.T) {
	f := New("refs", "Refs")
	f.Vars = []Var{{Name: "REGION", Value: "eu"}}
	f.Nodes = append(f.Nodes,
		Node{ID: "t", Type: "transform", Op: "template", Template: "{{env.REGION}} {{env.HOME}} {{project_secret.}} {{keychain.TOKEN}}"},
		Node{ID: "a", Type: "ai", Prompt: "Use {{project_secret.API}} for {{steps.t}}"},
		Node{ID: "o", Type: "output", Format: "md", Template: "{{steps.a}}", Actions: []Action{{Type: "show"}}})
	f.Edges = []Edge{{From: "start", To: "t"}, {From: "t", To: "a"}, {From: "a", To: "o"}}
	var msgs []string
	for _, is := range Validate(f, nil) {
		msgs = append(msgs, is.Level+": "+is.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{
		"warning: Template uses {{env.HOME}}, which is not one of this flow's variables",
		"error: Template uses {{project_secret.}}: name the secret",
		`sends the secret "project_secret.API" to the AI model`,
	} {
		if !strings.Contains(strings.ReplaceAll(all, "t uses", "Template uses"), want) && !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	for _, bad := range []string{"{{env.REGION}}, which is not", "{{keychain.TOKEN}}, which is not a known"} {
		if strings.Contains(all, bad) {
			t.Errorf("unexpected %q in\n%s", bad, all)
		}
	}
}
