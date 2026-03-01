package optimized_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	legacy "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude"
	optimized "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude/optimized"
)

type fixtureFile struct {
	Cases []fixtureCase `json:"cases"`
}

type fixtureCase struct {
	Name        string            `json:"name"`
	Request     json.RawMessage   `json:"request"`
	NonStream   json.RawMessage   `json:"non_stream"`
	Stream      []json.RawMessage `json:"stream_chunks"`
	IncludeDone *bool             `json:"include_done,omitempty"`
}

func TestEquivalenceFixtures_NonStream_LegacyVsOptimized(t *testing.T) {
	cases := loadFixtureCases(t)
	for i := range cases {
		tc := cases[i]
		t.Run(tc.Name, func(t *testing.T) {
			var legacyParam any
			var optimizedParam any

			legacyOut := legacy.ConvertAntigravityResponseToClaudeNonStream(
				context.Background(),
				"",
				tc.RequestRaw,
				tc.RequestRaw,
				tc.NonStreamRaw,
				&legacyParam,
			)
			optimizedOut := optimized.ConvertAntigravityResponseToClaudeNonStream(
				context.Background(),
				"",
				tc.RequestRaw,
				tc.RequestRaw,
				tc.NonStreamRaw,
				&optimizedParam,
			)

			legacyCanonical, err := canonicalizeJSON(legacyOut)
			if err != nil {
				t.Fatalf("legacy output invalid json: %v\nraw=%s", err, legacyOut)
			}
			optimizedCanonical, err := canonicalizeJSON(optimizedOut)
			if err != nil {
				t.Fatalf("optimized output invalid json: %v\nraw=%s", err, optimizedOut)
			}

			if legacyCanonical != optimizedCanonical {
				t.Fatalf(
					"non-stream mismatch\nlegacy=%s\noptimized=%s\nlegacy_raw=%s\noptimized_raw=%s",
					legacyCanonical,
					optimizedCanonical,
					legacyOut,
					optimizedOut,
				)
			}
		})
	}
}

func TestEquivalenceFixtures_Stream_LegacyVsOptimized(t *testing.T) {
	cases := loadFixtureCases(t)
	for i := range cases {
		tc := cases[i]
		t.Run(tc.Name, func(t *testing.T) {
			legacyOut := runLegacyStream(tc.RequestRaw, tc.StreamChunks, tc.IncludeDone)
			optimizedOut := runOptimizedStream(tc.RequestRaw, tc.StreamChunks, tc.IncludeDone)

			legacyEvents := flattenSSEEvents(legacyOut)
			optimizedEvents := flattenSSEEvents(optimizedOut)
			if len(legacyEvents) != len(optimizedEvents) {
				t.Fatalf(
					"stream event count mismatch: legacy=%d optimized=%d\nlegacy_out=%q\noptimized_out=%q",
					len(legacyEvents),
					len(optimizedEvents),
					legacyOut,
					optimizedOut,
				)
			}

			for j := range legacyEvents {
				l := legacyEvents[j]
				o := optimizedEvents[j]
				if l.Event != o.Event {
					t.Fatalf(
						"event mismatch at idx=%d: legacy=%q optimized=%q\nlegacy_data=%s\noptimized_data=%s",
						j,
						l.Event,
						o.Event,
						l.Data,
						o.Data,
					)
				}

				legacyCanonical, err := normalizeAndCanonicalizeSSEData(l.Event, l.Data)
				if err != nil {
					t.Fatalf("legacy SSE invalid json at idx=%d event=%s: %v\nraw=%s", j, l.Event, err, l.Data)
				}
				optimizedCanonical, err := normalizeAndCanonicalizeSSEData(o.Event, o.Data)
				if err != nil {
					t.Fatalf("optimized SSE invalid json at idx=%d event=%s: %v\nraw=%s", j, o.Event, err, o.Data)
				}

				if legacyCanonical != optimizedCanonical {
					t.Fatalf(
						"SSE mismatch at idx=%d event=%s\nlegacy=%s\noptimized=%s\nlegacy_raw=%s\noptimized_raw=%s",
						j,
						l.Event,
						legacyCanonical,
						optimizedCanonical,
						l.Data,
						o.Data,
					)
				}
			}
		})
	}
}

func loadFixtureCases(t *testing.T) []equivalenceCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/equivalence_cases.json")
	if err != nil {
		t.Fatalf("read fixture file failed: %v", err)
	}

	var payload fixtureFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("parse fixture file failed: %v", err)
	}
	if len(payload.Cases) == 0 {
		t.Fatal("fixture file has no cases")
	}

	out := make([]equivalenceCase, 0, len(payload.Cases))
	for i := range payload.Cases {
		c := payload.Cases[i]
		if c.Name == "" {
			t.Fatalf("fixture case %d has empty name", i)
		}
		if len(c.Request) == 0 {
			t.Fatalf("fixture case %s missing request", c.Name)
		}
		if len(c.NonStream) == 0 {
			t.Fatalf("fixture case %s missing non_stream", c.Name)
		}
		if len(c.Stream) == 0 {
			t.Fatalf("fixture case %s missing stream_chunks", c.Name)
		}
		for j := range c.Stream {
			if len(c.Stream[j]) == 0 {
				t.Fatalf("fixture case %s has empty stream chunk at index %d", c.Name, j)
			}
		}

		includeDone := true
		if c.IncludeDone != nil {
			includeDone = *c.IncludeDone
		}

		streamChunks := make([][]byte, 0, len(c.Stream))
		for j := range c.Stream {
			streamChunks = append(streamChunks, append([]byte(nil), c.Stream[j]...))
		}

		out = append(out, equivalenceCase{
			Name:         c.Name,
			RequestRaw:   append([]byte(nil), c.Request...),
			NonStreamRaw: append([]byte(nil), c.NonStream...),
			StreamChunks: streamChunks,
			IncludeDone:  includeDone,
		})
	}
	return out
}
