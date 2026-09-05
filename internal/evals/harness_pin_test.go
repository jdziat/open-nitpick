package evals

import "testing"

func TestParseModelReadsProviderPrefixAndRoutingPin(t *testing.T) {
	m := ParseModel("google/gemma-4-31b-it@deepinfra/turbo")
	if m.ID != "google/gemma-4-31b-it@deepinfra/turbo" || m.modelName() != "google/gemma-4-31b-it" || m.Pin != "deepinfra/turbo" || m.PriceID() != "google/gemma-4-31b-it" {
		t.Errorf("%+v", m)
	}
	m = ParseModel("synthetic:hf:Qwen/Qwen3.8-27B")
	if m.provider() != "synthetic" || m.modelName() != "hf:Qwen/Qwen3.8-27B" || m.Pin != "" {
		t.Errorf("%+v", m)
	}
	m = ParseModel("z-ai/glm-5.3-flash")
	if m.provider() != "openrouter" || m.modelName() != "z-ai/glm-5.3-flash" || m.Name != "" {
		t.Errorf("%+v", m)
	}
	cfg := evalConfig(ParseModel("google/gemma-4-31b-it@deepinfra/turbo"))
	if len(cfg.Models.Default.Providers) != 1 || cfg.Models.Default.Providers[0] != "deepinfra/turbo" || cfg.Models.Default.Model != "google/gemma-4-31b-it" {
		t.Errorf("%+v", cfg.Models.Default)
	}

	prices, err := Prices()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prices.Price("google/gemma-4-31b-it@deepinfra/turbo"); !ok {
		t.Error("the pinned endpoint is the one the table records, so it is priced")
	}
	if _, ok := prices.Price("google/gemma-4-31b-it@together"); ok {
		t.Error("a pin to an endpoint the table does not record is unknown, not the recorded rate")
	}
}
