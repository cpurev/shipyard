package upcloud

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStepsDoNotCallUpCloud(t *testing.T) {
	steps := Steps(Options{})
	text := strings.Join(steps, "\n")
	if !strings.Contains(text, "fi-hel2") || !strings.Contains(text, "upctl kubernetes config") {
		t.Fatalf("steps:\n%s", text)
	}
	if !strings.Contains(text, "does not call UpCloud") {
		t.Fatal("steps do not say that Shipyard skips the API")
	}
	if strings.Contains(text, "api.upcloud.com") {
		t.Fatal("steps include an API URL a caller might treat as a request")
	}
	cmd := KubeconfigCommand("cluster-1", "kube.yaml")
	if cmd != "upctl kubernetes config cluster-1 --output yaml --write kube.yaml" {
		t.Fatalf("cmd = %s", cmd)
	}
	if KubeconfigCommand("", "") != "upctl kubernetes config <cluster-uuid-or-name> --output yaml --write kubeconfig.yaml" {
		t.Fatal(KubeconfigCommand("", ""))
	}
}

func TestPackageHasNoNetworkImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	forbidden := []string{"net/http", "net", "crypto/tls"}
	saw := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		saw = true
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Fatalf("%s imports %s", filepath.Base(name), path)
				}
			}
		}
	}
	if !saw {
		t.Fatal("no non-test Go files")
	}
}
