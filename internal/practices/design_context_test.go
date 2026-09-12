package practices

import (
	"slices"
	"testing"

	"github.com/jdziat/open-nitpick/internal/standards"
)

func TestDesignContextKeepsHelpersWithoutUnrelatedPackageFiles(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "service/service.go", Src: []byte("package service\nimport data \"example.com/app/store\"\nfunc Read() int { return data.Read() }\nfunc shadow() { data := struct{ Unknown int }{}; _ = data.Unknown }\n")},
		{Path: "service/state.go", Src: []byte("package service\nvar state int\n")},
		{Path: "store/read.go", Src: []byte("package store\nfunc Read() int { return readValue() }\n")},
		{Path: "store/helper.go", Src: []byte("package store\nfunc readValue() int { return 42 }\n")},
		{Path: "store/unrelated.go", Src: []byte("package store\nfunc Unrelated() int { return 1 }\n")},
		{Path: "store/noise_test.go", Src: []byte("package store\nimport \"testing\"\nfunc TestUnrelated(t *testing.T) { t.Log(Read()) }\n")},
		{Path: "client/client.go", Src: []byte("package client\nimport api \"example.com/app/service\"\nfunc Start() int { return api.Read() }\n")},
		{Path: "client/unrelated.go", Src: []byte("package client\nfunc Unrelated() int { return 2 }\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesign(t.Context(), inventory, files, []string{"service/service.go"})
	if len(plan.Errors) != 0 || len(plan.Tasks) != 1 || len(plan.Tasks[0].Sources) != 2 || len(plan.Tasks[0].Omitted) != 0 {
		t.Fatalf("selected context lost primary scope: %+v", plan)
	}
	var names []string
	for _, target := range plan.Tasks[0].Context {
		names = append(names, target.ID)
	}
	if !slices.Equal(names, []string{"client/client.go", "store/helper.go", "store/read.go"}) {
		t.Fatalf("required helpers lost or unrelated source admitted: %v", names)
	}
	before := plan.Tasks[0].SourceDigest
	files[5].Src = []byte("package store\nfunc Unrelated() int { return 99 }\n")
	unrelated := PlanDesign(t.Context(), inventory, files, []string{"service/service.go"})
	if unrelated.Tasks[0].SourceDigest != before {
		t.Fatal("unread unrelated code changed the task binding")
	}
	files[4].Src = []byte("package store\nfunc readValue() int { return 43 }\n")
	changed := PlanDesign(t.Context(), inventory, files, []string{"service/service.go"})
	if changed.Tasks[0].SourceDigest == before {
		t.Fatal("referenced helper changed without changing the task binding")
	}
}

func TestDesignContextRetainsGenericReceiverMethodsAndInitializers(t *testing.T) {
	files := []standards.File{
		{Path: "go.mod", Src: []byte("module example.com/app\n")},
		{Path: "service/service.go", Src: []byte("package service\nimport \"example.com/app/store\"\nfunc Read() int { return store.New().Read() }\n")},
		{Path: "store/new.go", Src: []byte("package store\nfunc New() *Reader[int] { return &Reader[int]{} }\n")},
		{Path: "store/types.go", Src: []byte("package store\ntype Reader[T any] struct{}\n")},
		{Path: "store/method.go", Src: []byte("package store\nfunc (r *Reader[T]) Read() int { return readValue() }\n")},
		{Path: "store/helper.go", Src: []byte("package store\nfunc readValue() int { return 42 }\n")},
		{Path: "store/init.go", Src: []byte("package store\nfunc init() { register() }\n")},
		{Path: "store/register.go", Src: []byte("package store\nfunc register() {}\n")},
	}
	inventory, _ := InspectDesign(files, nil)
	plan := PlanDesign(t.Context(), inventory, files, []string{"service/service.go"})
	if len(plan.Errors) != 0 || len(plan.Tasks) != 1 || len(plan.Tasks[0].Omitted) != 0 || len(plan.Tasks[0].Context) != 6 {
		t.Fatalf("constructor, receiver or init dependency disappeared: %+v", plan)
	}
}
