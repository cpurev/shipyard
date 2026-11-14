package credentials

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cpurev/shipyard/internal/providers"
)

func TestFileStoreModeAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := NewFileStore(path)
	ctx := context.Background()
	want := providers.Credentials{Token: "local-token", Project: "demo-project", Extra: map[string]string{"teamId": "team_1"}}
	if err := store.Save(ctx, "vercel", want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("credential dir mode = %o, want owner-only", dirInfo.Mode().Perm())
	}
	got, ok, err := store.Load(ctx, "vercel")
	if err != nil || !ok {
		t.Fatalf("load ok=%v err=%v", ok, err)
	}
	if got.Token != want.Token || got.Project != want.Project || got.Extra["teamId"] != "team_1" {
		t.Fatalf("got %+v", got)
	}
	all, err := store.LoadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := all["vercel"]; !ok {
		t.Fatal("missing vercel")
	}
}

func TestFileStoreMissing(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "missing.json"))
	all, err := store.LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("got %#v", all)
	}
}

func TestOSKeyringStub(t *testing.T) {
	var store Store = OSKeyring{}
	err := store.Save(context.Background(), "fake", providers.Credentials{Token: "x"})
	if err != ErrKeychainNotSupported {
		t.Fatalf("save err = %v", err)
	}
	if _, _, err := store.Load(context.Background(), "fake"); err != ErrKeychainNotSupported {
		t.Fatalf("load err = %v", err)
	}
	if _, err := store.LoadAll(context.Background()); err != ErrKeychainNotSupported {
		t.Fatalf("loadall err = %v", err)
	}
}
