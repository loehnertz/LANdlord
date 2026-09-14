package version

import "testing"

func TestString(t *testing.T) {
	Version, Commit = "1.2.3", ""
	if got := String(); got != "1.2.3" {
		t.Fatalf("String() = %q, want %q", got, "1.2.3")
	}
	Commit = "abc123"
	if got := String(); got != "1.2.3 (abc123)" {
		t.Fatalf("String() = %q, want %q", got, "1.2.3 (abc123)")
	}
}
