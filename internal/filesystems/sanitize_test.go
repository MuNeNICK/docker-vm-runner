package filesystems

import "testing"

func TestSanitizeMountTarget(t *testing.T) {
	tests := map[string]string{
		"myshare":       "myshare",
		"my/share":      "my-share",
		"my share":      "my-share",
		"":              "share",
		"///":           "share",
		"my.share-name": "my.share-name",
	}
	for input, want := range tests {
		if got := SanitizeMountTarget(input); got != want {
			t.Fatalf("SanitizeMountTarget(%q) = %q want %q", input, got, want)
		}
	}
}
