package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/config"
	llms "github.com/nocturnium/llm-go-sdk/v6"
)

type streamFixture struct {
	countingLLM
	chunks    []llms.StreamChunk
	streamErr error
}

func (s *streamFixture) Stream(ctx context.Context, _ []llms.Message, _ ...llms.CallOption) (<-chan llms.StreamChunk, error) {
	if s.streamErr != nil {
		return nil, s.streamErr
	}
	out := make(chan llms.StreamChunk, len(s.chunks))
	for _, chunk := range s.chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

func TestStreamingRecordsUsageAndFailedAttempts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []llms.StreamChunk
		err    error
		failed bool
	}{
		{"complete", []llms.StreamChunk{{Content: "hello"}, {Done: true, Usage: &llms.Usage{PromptTokens: 2, CompletionTokens: 3}}}, nil, false},
		{"open failure", nil, errors.New("unavailable"), true},
		{"midstream failure", []llms.StreamChunk{{Content: "partial"}, {Error: errors.New("disconnected")}}, nil, true},
		{"closed early", []llms.StreamChunk{{Content: "partial"}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &streamFixture{chunks: tc.chunks, streamErr: tc.err}
			client := NewClientForTest(inner, config.ModelSpec{Model: "m"})
			var records []CallRecord
			client.ObserveCalls(func(record CallRecord) { records = append(records, record) })
			chunks, err := client.LLM.Stream(WithCallRole(t.Context(), "review"), nil)
			if err == nil {
				for range chunks {
				}
			}
			if len(records) != 1 || records[0].Failed != tc.failed || records[0].Role != "review" {
				t.Fatalf("stream records %+v", records)
			}
			if !tc.failed && (records[0].Usage == nil || records[0].Usage.CompletionTokens != 3) {
				t.Fatalf("stream usage lost: %+v", records[0])
			}
		})
	}
}
