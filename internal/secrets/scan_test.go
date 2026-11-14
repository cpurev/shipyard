package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScan(t *testing.T) {
	root := t.TempDir()
	aws := "AKIA" + "IOSFODNN7EXAMPLE"
	gh := "ghp_" + strings.Repeat("a", 36)
	slack := "xoxb-" + strings.Repeat("a", 12)
	key := "-----BEGIN " + "PRIVATE KEY-----"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("AWS_ACCESS_KEY_ID="+aws+"\nTOKEN="+gh+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "chat.txt"), []byte("bot="+slack+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "id.pem"), []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("API_KEY=replace-me-please\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("token="+gh+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "secret.txt"), []byte(aws), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range found {
		got[f.Rule] = true
		if strings.Contains(f.Redacted, aws) || strings.Contains(f.Redacted, gh) || strings.Contains(f.Redacted, slack) {
			t.Fatalf("finding leaked the secret: %+v", f)
		}
		if strings.Contains(f.Path, "node_modules") || f.Path == "README.md" || f.Path == ".env.example" {
			t.Fatalf("scanned skipped path %s", f.Path)
		}
	}
	for _, rule := range []string{"aws-access-key", "github-token", "slack-token", "private-key"} {
		if !got[rule] {
			t.Fatalf("missing rule %s in %#v", rule, found)
		}
	}
}

func TestScanClean(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.js"), []byte("console.log('hello')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("findings = %#v", found)
	}
}
