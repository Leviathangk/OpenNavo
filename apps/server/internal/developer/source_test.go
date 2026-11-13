package developer

import "testing"

func TestInferSourceKind(t *testing.T) {
	cases := map[string]string{
		"https://github.com/owner/app":                    "github",
		"https://apps.apple.com/us/app/things/id123":      "appstore",
		"https://example.com/app":                         "website",
		"https://github.com/owner/app?tab=readme":         "github",
		"https://apps.apple.com/us/app/things/id123?uo=4": "appstore",
	}
	for raw, want := range cases {
		if got := InferSourceKind("", raw); got != want {
			t.Fatalf("InferSourceKind(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := InferSourceKind("appstore", "https://example.com"); got != "appstore" {
		t.Fatalf("explicit source should win, got %q", got)
	}
}
