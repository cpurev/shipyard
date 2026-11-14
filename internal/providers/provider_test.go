package providers

import (
	"errors"
	"testing"
	"time"
)

func TestValidateAppName(t *testing.T) {
	ok := []string{"a", "hello", "hello-app", "a1"}
	bad := []string{"", "Hello", "hello_app", "-hello", "hello-", "héllo"}
	for _, name := range ok {
		if err := ValidateAppName(name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, name := range bad {
		if err := ValidateAppName(name); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestLimitLines(t *testing.T) {
	lines := []LogLine{
		{Time: time.Unix(1, 0).UTC(), Message: "a"},
		{Time: time.Unix(2, 0).UTC(), Message: "b"},
		{Time: time.Unix(3, 0).UTC(), Message: "c"},
	}
	got := LimitLines(lines, LogOptions{Limit: 2})
	if len(got) != 2 || got[0].Message != "b" || got[1].Message != "c" {
		t.Fatalf("got %#v", got)
	}
	since := LimitLines(lines, LogOptions{Since: time.Unix(3, 0).UTC(), Limit: 10})
	if len(since) != 1 || since[0].Message != "c" {
		t.Fatalf("since %#v", since)
	}
}

func TestCredentialsReady(t *testing.T) {
	if !CredentialsReady("fake", Credentials{Token: "local"}) {
		t.Fatal("fake")
	}
	if CredentialsReady("vercel", Credentials{}) {
		t.Fatal("empty vercel")
	}
	if !CredentialsReady("gcp", Credentials{Project: "demo", ServiceAccountJSON: []byte("{}")}) {
		t.Fatal("gcp json")
	}
	if CredentialsReady("gcp", Credentials{Token: "t"}) {
		t.Fatal("gcp without project")
	}
}

func TestRejectProvisionAccount(t *testing.T) {
	account, err := RejectProvisionAccount()
	if account != nil || !errors.Is(err, ErrNotSupported) {
		t.Fatalf("account=%v err=%v", account, err)
	}
}
