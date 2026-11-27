package safeenv

import (
	"slices"
	"strings"
	"testing"
)

func TestCleanDropsSecrets(t *testing.T) {
	env := []string{
		"PATH=/usr/bin", "HOME=/home/a", "HTTPS_PROXY=http://proxy:8080", "JAVA_HOME=/jdk",
		"SSH_AUTH_SOCK=/tmp/agent", "NODE_EXTRA_CA_CERTS=/ca.pem", "npm_config_registry=https://r",
		"AWS_SECRET_ACCESS_KEY=x", "AWS_SESSION_TOKEN=x", "GITHUB_TOKEN=x", "OPENAI_API_KEY=x",
		"ANTHROPIC_API_KEY=x", "CODEGEN_BRIDGE_TOKEN=x", "SPECWRIGHT_RUNNER_TOKEN=x", "DB_PASSWORD=x",
		"AZURE_CLIENT_SECRET=x", "NPM_TOKEN=x", "DATABASE_URL=postgres://u:p@h/d", "slack_webhook_url=x",
		"GOOGLE_APPLICATION_CREDENTIALS=/k.json",
	}
	got := strings.Join(Clean(env), "\n")
	for _, keep := range env[:7] {
		if !strings.Contains(got, keep) {
			t.Errorf("dropped %s", keep)
		}
	}
	for _, kv := range env[7:] {
		if strings.Contains(got, kv) {
			t.Errorf("kept %s", kv)
		}
	}
	SetKeep(func() []string { return []string{"npm_token"} })
	defer SetKeep(nil)
	if c := Clean(env); !slices.Contains(c, "NPM_TOKEN=x") || slices.Contains(c, "GITHUB_TOKEN=x") {
		t.Fatalf("policy keep: %v", c)
	}
}
