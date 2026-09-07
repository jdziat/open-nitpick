//go:build eval

package evals

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdziat/open-nitpick/internal/config"
	"github.com/jdziat/open-nitpick/internal/llm"
	"github.com/jdziat/open-nitpick/internal/review"
	"github.com/jdziat/open-nitpick/internal/vcs"
)

// TestBigPRCost prices one real multi-batch change, which the fixture corpora
// cannot: every fixture there is about one batch, so their $/review is cost
// per diff and says nothing about what a 24-file pull request costs.
//
// Not an assertion. It reviews a revision range in this repository with each
// contender and logs the spend, the batch count and the findings.
//
// NITPICK_BIGPR_BASE and NITPICK_BIGPR_HEAD select the range; NITPICK_EVAL_MODELS
// selects the contenders.
func TestBigPRCost(t *testing.T) {
	base, head := os.Getenv("NITPICK_BIGPR_BASE"), os.Getenv("NITPICK_BIGPR_HEAD")
	if base == "" || head == "" {
		t.Skip("set NITPICK_BIGPR_BASE and NITPICK_BIGPR_HEAD")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir = dir + "/../.."

	prices, err := Prices()
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		name              string
		usd               float64
		known             bool
		batches, findings int
		dur               time.Duration
	}
	var rows []row

	ids := strings.Split(os.Getenv(EnvModels), ",")
	for _, id := range ids {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		model := ParseModel(id)
		cfg := evalConfig(model)
		cfg.Persona = config.DefaultPersona().Resolve()
		cfg.Review.RelatedContext, cfg.Review.RelatedContextCallers = true, true

		var (
			meters = map[*llm.Client]*Meter{}
			mmu    sync.Mutex
		)
		meterInto := func(c *llm.Client) {
			mmu.Lock()
			defer mmu.Unlock()
			if _, ok := meters[c]; !ok {
				meters[c] = MeterClient(c)
			}
		}

		roles, err := llm.BuildRoles(cfg)
		if err != nil {
			t.Fatalf("%s: build roles: %v", model.ID, err)
		}
		roles.Each(meterInto)
		inner := roles.Build
		roles.Build = func(spec config.ModelSpec) (*llm.Client, error) {
			c, err := inner(spec)
			if err == nil {
				meterInto(c)
			}
			return c, err
		}

		engine := &review.Engine{
			Config:   cfg,
			Roles:    roles,
			Provider: vcs.NewLocal(dir, io.Discard),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		started := time.Now()
		report, err := engine.Review(ctx, vcs.Ref{Base: base, Head: head})
		dur := time.Since(started)
		cancel()
		if err != nil {
			t.Fatalf("%s: review: %v", model.ID, err)
		}

		mmu.Lock()
		_, byModel := sumMeters(meters)
		mmu.Unlock()

		cost := compositeCost(prices, byModel)
		names := make([]string, 0, len(byModel))
		for n := range byModel {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			u := byModel[n]
			t.Logf("  %-28s %-32s calls=%d", model.ID, n, u.Calls())
		}

		rows = append(rows, row{model.ID, cost.USD, cost.Known, len(report.Plan.Batches), len(report.Findings), dur})
	}

	out := fmt.Sprintf("\n%-28s %10s %8s %9s %8s\n", "contender", "COST", "BATCHES", "FINDINGS", "TIME")
	for _, r := range rows {
		usd := "n/a"
		if r.known {
			usd = fmt.Sprintf("$%.4f", r.usd)
		}
		out += fmt.Sprintf("%-28s %10s %8d %9d %8s\n", r.name, usd, r.batches, r.findings, r.dur.Round(time.Second))
	}
	t.Log(out + "\nOne real change, one run each, related context on. Cost is the sum over every model the review reached for.\n")
}
