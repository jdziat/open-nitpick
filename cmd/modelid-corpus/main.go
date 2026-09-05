// Command modelid-corpus generates the corpus for the model-identification
// experiment (docs/plan-full-review.md, section 4): the same tasks, in the
// same languages, written by each candidate model, plus a human-written
// control set sampled from the Go and Python standard libraries on this
// machine. Nothing here is a product; it is the experiment's input, and the
// experiment decides whether a product is built.
//
//	modelid-corpus -out internal/evals/testdata/modelid -models a,b,c
//
// Files are written with a trailing .txt so that nothing treats the corpus
// as source: it is data, and a formatter or vet run over the tree must not
// reformat or fail on it.
//
// The OpenRouter key comes from the environment (OPENROUTER_API_KEY or
// LLM_API_KEY), as for the eval harness. Files already present are not
// regenerated, so a run can be resumed.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	llms "github.com/nocturnium/llm-go-sdk/v6"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/modelid"
)

func main() {
	var out, models, human string
	var limit int
	flag.StringVar(&out, "out", "internal/evals/testdata/modelid", "corpus directory")
	flag.StringVar(&models, "models", "", "comma-separated OpenRouter model ids")
	flag.StringVar(&human, "human", "", "also sample the human control set from GOROOT and the Python stdlib (yes/no)")
	flag.IntVar(&limit, "limit", 0, "generate at most this many files this run (0: all)")
	flag.Parse()

	if human == "yes" {
		n, err := sampleHuman(out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "human control:", err)
			os.Exit(1)
		}
		fmt.Printf("human control: %d files\n", n)
	}
	if models == "" {
		return
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for _, id := range strings.Split(models, ",") {
		id = strings.TrimSpace(id)
		client, err := llm.Build(config.ModelSpec{Provider: llm.ProviderOpenRouter, Model: id, Temperature: floatPtr(0.7)})
		if err != nil {
			fmt.Fprintln(os.Stderr, id, err)
			os.Exit(1)
		}
		// One goroutine per model: the models are independent providers'
		// worth of latency, and a reasoning model can take minutes a file.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, lang := range modelid.Languages {
				for i, task := range modelid.Tasks {
					path := filepath.Join(out, modelid.Slug(id), lang.Name, fmt.Sprintf("%02d%s.txt", i, lang.Ext))
					if _, err := os.Stat(path); err == nil {
						continue
					}
					// The slot is reserved before the call, so -limit bounds
					// what is paid for, not what was finished.
					mu.Lock()
					if limit > 0 && done >= limit {
						mu.Unlock()
						return
					}
					done++
					mu.Unlock()
					var code string
					var err error
					for attempt := 0; attempt < 2 && len(code) < 50; attempt++ {
						code, err = generate(ctx, client, lang, task)
					}
					if err != nil || len(code) < 50 {
						fmt.Fprintf(os.Stderr, "%s %s %02d: %v (%d bytes)\n", id, lang.Name, i, err, len(code))
						continue
					}
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						fmt.Fprintln(os.Stderr, err)
						return
					}
					if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
						fmt.Fprintln(os.Stderr, err)
						return
					}
					fmt.Printf("%s %s %02d: %d bytes\n", id, lang.Name, i, len(code))
				}
			}
		}()
	}
	wg.Wait()
}

func floatPtr(f float64) *float64 { return &f }

// generate asks for one file: the task, the language, and nothing but code,
// with the fences stripped when a model adds them anyway.
func generate(ctx context.Context, client *llm.Client, lang modelid.Language, task string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	prompt := fmt.Sprintf("Write a single %s source file that does the following. Reply with the file's contents only, no explanation, no markdown fences.\n\n%s", lang.Name, task)
	// Plain options: the client's own carry the structured-output mode a
	// review wants, which would ask the model for JSON here.
	resp, err := client.LLM.GenerateContent(ctx, []llms.Message{{Role: llms.RoleUser, Content: prompt}}, llms.WithTemperature(0.7), llms.WithMaxTokens(6000))
	if err != nil {
		return "", err
	}
	code := strings.TrimSpace(resp.Content)
	if strings.HasPrefix(code, "```") {
		code = strings.TrimPrefix(code, "```")
		if nl := strings.Index(code, "\n"); nl >= 0 {
			code = code[nl+1:]
		}
		code = strings.TrimSuffix(strings.TrimSpace(code), "```")
	}
	return strings.TrimSpace(code) + "\n", nil
}

// sampleHuman copies a fixed random sample of standard-library files, the
// same count per language as the tasks, as the human-written control. Test
// files and generated files are left out. TypeScript has no offline source
// of human-written code on this machine, which the experiment reports.
func sampleHuman(out string) (int, error) {
	roots := map[string]struct{ dir, ext string }{
		"go":     {dir: goroot(), ext: ".go"},
		"python": {dir: pystdlib(), ext: ".py"},
	}
	if roots["go"].dir != "" {
		roots["go"] = struct{ dir, ext string }{dir: roots["go"].dir + "/src", ext: ".go"}
	}
	n := 0
	// Languages in a fixed order, each with its own seeded stream, so the
	// sample is the same set of files on every run on the same machine.
	langs := make([]string, 0, len(roots))
	for lang := range roots {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for seed, lang := range langs {
		root := roots[lang]
		if root.dir == "" {
			return n, fmt.Errorf("%s control: no standard library found on this machine", lang)
		}
		r := rand.New(rand.NewPCG(2026, uint64(seed)))
		var candidates []string
		walkErr := filepath.WalkDir(root.dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && d.IsDir() && (strings.Contains(p, "testdata") || strings.Contains(p, "test") || strings.Contains(p, "vendor") || strings.Contains(p, "site-packages")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, root.ext) || strings.HasSuffix(p, "_test"+root.ext) {
				return nil
			}
			if info, err := d.Info(); err != nil || info.Size() < 1500 || info.Size() > 12000 {
				return nil
			}
			// Generated files are filtered before sampling, so the sample
			// has no gaps and no generated code.
			if data, err := os.ReadFile(p); err != nil || strings.Contains(string(data), "DO NOT EDIT") {
				return nil
			}
			candidates = append(candidates, p)
			return nil
		})
		sort.Strings(candidates)
		if walkErr != nil {
			return n, fmt.Errorf("%s control under %s: %w", lang, root.dir, walkErr)
		}
		if len(candidates) < len(modelid.Tasks) {
			return n, fmt.Errorf("%s control under %s: %d candidate files, fewer than the %d tasks", lang, root.dir, len(candidates), len(modelid.Tasks))
		}
		r.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
		for i := 0; i < len(modelid.Tasks) && i < len(candidates); i++ {
			data, err := os.ReadFile(candidates[i])
			if err != nil {
				return n, err
			}
			path := filepath.Join(out, "human", lang, fmt.Sprintf("%02d%s.txt", i, root.ext))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return n, err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func goroot() string {
	if v := os.Getenv("GOROOT"); v != "" {
		return v
	}
	out, err := execOut("go", "env", "GOROOT")
	if err != nil {
		return ""
	}
	return out
}

func pystdlib() string {
	out, err := execOut("python3", "-c", "import sysconfig;print(sysconfig.get_paths()['stdlib'])")
	if err != nil {
		return ""
	}
	return out
}

func execOut(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}
