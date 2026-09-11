package bundle

import (
	"strings"
	"testing"
)

func TestLanguagesAreSortedAndDeduplicated(t *testing.T) {
	got := Languages([]string{"a/b.go", "c.ts", "d.tsx", "Dockerfile", "README", "e.PY"})
	if want := "docker,go,python,typescript"; strings.Join(got, ",") != want {
		t.Errorf("Languages = %v, want %s", got, want)
	}
}
