package models_test

import (
	"context"
	"testing"

	"github.com/costa/polypod/internal/contextwindow"
	"github.com/costa/polypod/internal/models"
	"github.com/costa/polypod/internal/tracking"
)

func TestBuiltIn1MContextAndVisionCatalog(t *testing.T) {
	models.ResetDynamicModelsForTest()

	qwenCoder := models.GetCapabilities("qwen3-coder-plus")
	if qwenCoder == nil {
		t.Fatal("expected qwen3-coder-plus in built-in catalog")
	}
	if qwenCoder.MaxContextTokens != 1_000_000 {
		t.Fatalf("expected 1M context window for qwen3-coder-plus, got %d", qwenCoder.MaxContextTokens)
	}
	if qwenCoder.SupportsVision {
		t.Fatal("expected qwen3-coder-plus to be text-only so Auto-Vision routes image turns to qwen3-vl-plus")
	}

	if vis := models.BestVisionModelForProvider("qwen"); vis == "" {
		t.Fatal("expected BestVisionModelForProvider('qwen') to return a vision model")
	}

	limits := contextwindow.WellKnownLimits("qwen3-coder-plus")
	if limits.MaxContextTokens != 1_000_000 {
		t.Fatalf("expected contextwindow.WellKnownLimits to reflect 1M tokens, got %d", limits.MaxContextTokens)
	}
}

func TestLoadModelsDevJSONAndCostPropagation(t *testing.T) {
	models.ResetDynamicModelsForTest()
	t.Cleanup(models.ResetDynamicModelsForTest)

	sample := []byte(`{
		"qwen": {
			"id": "qwen",
			"name": "Qwen",
			"models": {
				"qwen3-omni-2026": {
					"id": "qwen3-omni-2026",
					"name": "Qwen 3 Omni 2026",
					"attachment": true,
					"reasoning": true,
					"tool_call": true,
					"modalities": { "input": ["text", "image", "audio"], "output": ["text"] },
					"cost": { "input": 0.60, "output": 2.40, "cache_read": 0.15 },
					"limit": { "context": 1000000, "output": 65536 }
				}
			}
		}
	}`)

	n, err := models.LoadModelsDevJSON(sample)
	if err != nil {
		t.Fatalf("LoadModelsDevJSON: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 loaded model, got %d", n)
	}

	capEntry := models.GetCapabilities("qwen/qwen3-omni-2026")
	if capEntry == nil || !capEntry.SupportsVision || capEntry.MaxContextTokens != 1_000_000 {
		t.Fatalf("unexpected dynamic capability entry: %+v", capEntry)
	}

	cost, ok := tracking.MeasuredCost("qwen", "qwen3-omni-2026", 100_000, 10_000)
	if !ok || cost <= 0 {
		t.Fatalf("expected CostEngine/MeasuredCost to pick up dynamic models.dev pricing, got ok=%v cost=%f", ok, cost)
	}

	// Verify POLYPOD_MODELS_DEV_REFRESH=off prevents network fetch when cache is absent.
	models.ResetDynamicModelsForTest()
	t.Setenv("POLYPOD_MODELS_DEV_REFRESH", "off")
	loaded, err := models.RefreshFromModelsDev(context.Background(), t.TempDir(), "http://127.0.0.1:1/unreachable")
	if err != nil || loaded != 0 {
		t.Fatalf("expected refresh=off to skip HTTP cleanly, got loaded=%d err=%v", loaded, err)
	}
}
