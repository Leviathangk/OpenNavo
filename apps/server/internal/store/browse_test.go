package store

import "testing"

func TestBrowseBucket(t *testing.T) {
	for name, want := range map[string]string{"Ångström": "a", "Éditeur": "e", "Über": "u", "1Password": "0-9", "*** Safari": "s", "日本語": "other", "Русский": "other", "": "other", "１２３": "0-9"} {
		t.Run(name, func(t *testing.T) {
			if got := BrowseBucket(name); got != want {
				t.Errorf("BrowseBucket(%q)=%q, want %q", name, got, want)
			}
		})
	}
}
