package execution

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestTrustedCorpusRunsSelectedChecksInsideDocker(t *testing.T) {
	image := os.Getenv("NITPICK_EXECUTION_IMAGE")
	if image == "" {
		t.Skip("set NITPICK_EXECUTION_IMAGE to the checker image content ID")
	}
	policy := Policy{Image: image, Runtime: "runc", TrustedCorpus: true, RiskAccepted: true, TimeoutSeconds: 120, MemoryMiB: 2048, CPUs: 2, Pids: 128, DiskMiB: 1024, MaxOutputBytes: 262144}
	runner, err := NewRunner(policy)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"go.mod": []byte("module example\ngo 1.25\n"), "main.go": []byte("package example\nfunc Value() int { return 1 }\n"), "main_test.go": []byte("package example\nimport \"testing\"\nfunc TestValue(t *testing.T) {if Value()!=1 {t.Fatal(\"wrong value\")}}\n")}
	base, err := NewSnapshot(strings.Repeat("a", 40), files)
	if err != nil {
		t.Fatal(err)
	}
	check := Check{Kind: "go_test", Package: "."}
	result := runner.Run(t.Context(), base, check)
	if result.Status != Passed {
		t.Fatalf("positive control: %+v", result)
	}
	patch := map[string][]byte{"main.go": []byte("package example\nfunc Value() int { return 2 }\n")}
	comparison, err := runner.RunPatch(t.Context(), base, patch, check)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Baseline.Status != Passed || comparison.Candidate.Status != Failed {
		t.Fatalf("broken candidate: %+v", comparison)
	}
	if err := runner.VerifyPatch(comparison, base, patch, check, base.Head); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]string{"no_tests": "package example\n", "early_exit": "package example\nimport (\"testing\";\"os\")\nfunc TestMain(m *testing.M) {os.Exit(0)}\n"} {
		t.Run(name, func(t *testing.T) {
			files["main_test.go"] = []byte(test)
			snapshot, err := NewSnapshot(base.Head, files)
			if err != nil {
				t.Fatal(err)
			}
			result := runner.Run(t.Context(), snapshot, check)
			if result.Status != Unavailable {
				t.Fatalf("silence was called a pass: %+v", result)
			}
		})
	}
	files["main_test.go"] = []byte("package example\nimport (\"testing\";\"os\";\"net\";\"time\")\nfunc TestBoundary(t *testing.T) {\nif os.Getenv(\"GITHUB_TOKEN\")!=\"\" || os.Getenv(\"OPENAI_API_KEY\")!=\"\" {t.Fatal(\"host credentials leaked\")}\nfor _,p:=range []string{\"/var/run/docker.sock\",\"/root/.ssh/id_rsa\"} {if _,e:=os.Stat(p);e==nil {t.Fatalf(\"host path visible: %s\",p)}}\nif e:=os.WriteFile(\"/opt/gomod/poison\",[]byte(\"poison\"),0600);e==nil {t.Fatal(\"shared cache writable\")}\nc,e:=net.DialTimeout(\"tcp\",\"198.51.100.1:443\",time.Second);if e==nil {c.Close();t.Fatal(\"network reachable\")}\n}\n")
	boundary, err := NewSnapshot(base.Head, files)
	if err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(t.Context(), boundary, check); result.Status != Passed {
		t.Fatalf("containment control: %+v", result)
	}
	files["main.go"] = []byte("package example\nimport _ \"private.example/unstaged\"\n")
	missing, err := NewSnapshot(base.Head, files)
	if err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(t.Context(), missing, check); result.Status != Unavailable {
		t.Fatalf("unstaged dependency was not unavailable: %+v", result)
	}
	policy.TimeoutSeconds = 1
	limited, err := NewRunner(policy)
	if err != nil {
		t.Fatal(err)
	}
	result = limited.Run(t.Context(), base, check)
	if result.Status != Unavailable || result.Reason != "execution cancelled or exceeded wall-time limit" {
		t.Fatalf("wall time did not stop and clean up: %+v", result)
	}
	if exec.Command("docker", "inspect", result.JobID).Run() == nil {
		t.Fatal("cancelled check left a container behind")
	}
}
