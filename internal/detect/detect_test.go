package detect

import "testing"

func TestDetectFixtures(t *testing.T) {
	tests := []struct {
		dir  string
		want Framework
	}{
		{dir: "testdata/nextjs", want: FrameworkNextJS},
		{dir: "testdata/next-dev", want: FrameworkNextJS},
		{dir: "testdata/vite", want: FrameworkVite},
		{dir: "testdata/python", want: FrameworkPython},
		{dir: "testdata/python-and-node", want: FrameworkPython},
		{dir: "testdata/node", want: FrameworkNode},
		{dir: "testdata/unknown", want: FrameworkUnknown},
		{dir: "testdata/empty", want: FrameworkUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			got, err := Detect(tt.dir)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if got.Framework != tt.want {
				t.Fatalf("framework = %s, want %s (signals %v)", got.Framework, tt.want, got.Signals)
			}
			if tt.want != FrameworkUnknown && len(got.Signals) == 0 {
				t.Fatal("expected at least one signal")
			}
		})
	}
}

func TestDetectMissingDir(t *testing.T) {
	if _, err := Detect(t.TempDir() + "/missing"); err == nil {
		t.Fatal("expected an error for a missing directory")
	}
}
