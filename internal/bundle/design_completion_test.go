package bundle

import "testing"

func TestSharedDesignCompletionRequiresEveryAssignedTask(t *testing.T) {
	batch := Batch{DesignTask: "first", AdditionalDesignTasks: []string{"second"}}
	for _, completed := range [][]string{nil, {"first"}, {"second"}, {"other"}} {
		if batch.DesignAssessed(completed) {
			t.Fatalf("partial or unrelated evidence claimed shared completion: %v", completed)
		}
	}
	if !batch.DesignAssessed([]string{"first", "second"}) {
		t.Fatal("complete shared request lost coverage")
	}
	if (Batch{}).DesignAssessed([]string{"first", "second"}) {
		t.Fatal("ordinary request invented a design assessment")
	}
}
