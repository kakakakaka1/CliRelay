// Package codex implements thinking configuration for Codex (OpenAI Responses API) models.
//
// Codex models use the reasoning.effort format with discrete levels
// (low/medium/high). This is similar to OpenAI but uses nested field
// "reasoning.effort" instead of "reasoning_effort".
// See: _bmad-output/planning-artifacts/architecture.md#Epic-8
package codex

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Applier implements thinking.ProviderApplier for Codex models.
//
// Codex-specific behavior:
//   - Output format: reasoning.effort (string: low/medium/high/xhigh)
//   - Level-only mode: no numeric budget support
//   - Some models support ZeroAllowed (gpt-5.1, gpt-5.2)
type Applier struct{}

var _ thinking.ProviderApplier = (*Applier)(nil)

// NewApplier creates a new Codex thinking applier.
func NewApplier() *Applier {
	return &Applier{}
}

func init() {
	thinking.RegisterProvider("codex", NewApplier())
}

// Apply applies thinking configuration to Codex request body.
//
// Expected output format:
//
//	{
//	  "reasoning": {
//	    "effort": "high"
//	  }
//	}
func (a *Applier) Apply(body []byte, config thinking.ThinkingConfig, modelInfo *registry.ModelInfo) ([]byte, error) {
	if thinking.IsUserDefinedModel(modelInfo) {
		return applyCompatibleCodex(body, config)
	}
	if modelInfo.Thinking == nil {
		return body, nil
	}

	// Only handle ModeLevel and ModeNone; other modes pass through unchanged.
	if config.Mode != thinking.ModeLevel && config.Mode != thinking.ModeNone {
		return body, nil
	}

	if len(body) == 0 || !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}

	if config.Mode == thinking.ModeLevel {
		result, _ := sjson.SetBytes(body, "reasoning.effort", string(config.Level))
		return result, nil
	}

	// ModeNone: only wire "none" when the model actually accepts it.
	// ZeroAllowed alone must not force "none" when Levels omit "none"
	// (e.g. grok-4.5 rejects reasoning.effort="none"); fall back to lowest level.
	support := modelInfo.Thinking
	effort := ""
	if supportsNoneEffort(support) {
		effort = string(thinking.LevelNone)
	} else if config.Level != "" {
		effort = string(config.Level)
	} else if len(support.Levels) > 0 {
		effort = support.Levels[0]
	}
	if effort == "" {
		result, err := sjson.DeleteBytes(body, "reasoning.effort")
		if err != nil {
			return body, nil
		}
		return result, nil
	}
	result, _ := sjson.SetBytes(body, "reasoning.effort", effort)
	return result, nil
}

// supportsNoneEffort reports whether the model accepts reasoning.effort="none".
// Explicit Levels win: if the list omits "none", do not emit it even when ZeroAllowed.
func supportsNoneEffort(support *registry.ThinkingSupport) bool {
	if support == nil {
		return false
	}
	if hasLevel(support.Levels, string(thinking.LevelNone)) {
		return true
	}
	return support.ZeroAllowed && len(support.Levels) == 0
}

func applyCompatibleCodex(body []byte, config thinking.ThinkingConfig) ([]byte, error) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		body = []byte(`{}`)
	}

	var effort string
	switch config.Mode {
	case thinking.ModeLevel:
		if config.Level == "" {
			return body, nil
		}
		effort = string(config.Level)
	case thinking.ModeNone:
		effort = string(thinking.LevelNone)
		if config.Level != "" {
			effort = string(config.Level)
		}
	case thinking.ModeAuto:
		// Auto mode for user-defined models: pass through as "auto"
		effort = string(thinking.LevelAuto)
	case thinking.ModeBudget:
		// Budget mode: convert budget to level using threshold mapping
		level, ok := thinking.ConvertBudgetToLevel(config.Budget)
		if !ok {
			return body, nil
		}
		effort = level
	default:
		return body, nil
	}

	result, _ := sjson.SetBytes(body, "reasoning.effort", effort)
	return result, nil
}

func hasLevel(levels []string, target string) bool {
	for _, level := range levels {
		if strings.EqualFold(strings.TrimSpace(level), target) {
			return true
		}
	}
	return false
}
