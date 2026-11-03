package version

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })

	Version, Commit, Date = "1.2.3", "abc1234", "2026-11-03"

	got := String()
	for _, want := range []string{"1.2.3", "abc1234", "2026-11-03"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}

func TestDefaults(t *testing.T) {
	if Version == "" || Commit == "" || Date == "" {
		t.Error("version variables must have non-empty defaults")
	}
}
