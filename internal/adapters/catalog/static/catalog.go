package static

import (
	"context"
	"regexp"
	"sort"
	"strings"

	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/model"
)

type Config struct {
	ThinkingSuffix string
}

type Catalog struct {
	thinkingSuffix string
	aliases        map[string]string
	baseModels     []string
	hiddenModels   map[string]string
	hiddenFromList map[string]bool
}

var (
	reStandard    = regexp.MustCompile(`^(claude-(?:haiku|sonnet|opus)-\d+)-(\d{1,2})(?:-(?:\d{8}|latest|\d+))?$`)
	reNoMinor     = regexp.MustCompile(`^(claude-(?:haiku|sonnet|opus)-\d+)(?:-\d{8})?$`)
	reLegacy      = regexp.MustCompile(`^(claude)-(\d+)-(\d+)-(haiku|sonnet|opus)(?:-(?:\d{8}|latest|\d+))?$`)
	reDotWithDate = regexp.MustCompile(`^(claude-(?:\d+\.\d+-)?(?:haiku|sonnet|opus)(?:-\d+\.\d+)?)-\d{8}$`)
	reInverted    = regexp.MustCompile(`^claude-(\d+)\.(\d+)-(haiku|sonnet|opus)-(.+)$`)
)

func New(cfg Config) *Catalog {
	suffix := cfg.ThinkingSuffix
	if suffix == "" {
		suffix = "-thinking"
	}

	return &Catalog{
		thinkingSuffix: suffix,
		aliases: map[string]string{
			"auto":              "auto",
			"auto-kiro":         "auto",
			"gpt-4o":            "claude-sonnet-4.5",
			"gpt-4":             "claude-sonnet-4.5",
			"claude-3.7-sonnet": "claude-3.7-sonnet",
		},
		hiddenModels: map[string]string{
			"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0",
		},
		hiddenFromList: map[string]bool{
			"auto": true,
		},
		baseModels: []string{
			"auto",
			"claude-sonnet-4",
			"claude-haiku-4.5",
			"claude-sonnet-4.5",
			"claude-opus-4.5",
			"claude-sonnet-4.6",
			"claude-opus-4.6",
		},
	}
}

func (c *Catalog) Resolve(ctx context.Context, externalModel string) (model.ResolvedModel, error) {
	_ = ctx

	raw := strings.TrimSpace(externalModel)
	if raw == "" {
		return model.ResolvedModel{}, domainerrors.New(domainerrors.CategoryValidation, "model is required")
	}

	thinkingEnabled := false
	trimmed := raw
	if strings.HasSuffix(strings.ToLower(trimmed), strings.ToLower(c.thinkingSuffix)) {
		thinkingEnabled = true
		trimmed = trimmed[:len(trimmed)-len(c.thinkingSuffix)]
	}

	lower := strings.ToLower(trimmed)
	if alias, ok := c.aliases[lower]; ok {
		resolved := model.ResolvedModel{
			ExternalName:    raw,
			InternalName:    alias,
			ThinkingEnabled: thinkingEnabled,
			Verified:        true,
			Source:          "alias",
		}
		if hiddenInternal, ok := c.hiddenModels[resolved.InternalName]; ok {
			resolved.InternalName = hiddenInternal
			resolved.Source = "hidden"
		}
		return resolved, nil
	}

	normalized := NormalizeModelName(lower)
	if hiddenInternal, ok := c.hiddenModels[normalized]; ok {
		return model.ResolvedModel{
			ExternalName:    raw,
			InternalName:    hiddenInternal,
			ThinkingEnabled: thinkingEnabled,
			Verified:        true,
			Source:          "hidden",
		}, nil
	}
	verified := c.hasBaseModel(normalized)

	return model.ResolvedModel{
		ExternalName:    raw,
		InternalName:    normalized,
		ThinkingEnabled: thinkingEnabled,
		Verified:        verified,
		Source:          sourceFor(verified),
	}, nil
}

func (c *Catalog) List(ctx context.Context) ([]model.ResolvedModel, error) {
	_ = ctx

	unique := make(map[string]bool, len(c.baseModels)*2+len(c.aliases)+len(c.hiddenModels))
	for _, base := range c.baseModels {
		if c.hiddenFromList[base] {
			continue
		}
		unique[base] = true
		unique[base+c.thinkingSuffix] = true
	}
	for alias := range c.aliases {
		if c.hiddenFromList[alias] {
			continue
		}
		unique[alias] = true
		if alias != "auto" {
			unique[alias+c.thinkingSuffix] = true
		}
	}
	for displayName := range c.hiddenModels {
		if c.hiddenFromList[displayName] {
			continue
		}
		unique[displayName] = true
		unique[displayName+c.thinkingSuffix] = true
	}

	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]model.ResolvedModel, 0, len(keys))
	for _, key := range keys {
		resolved, err := c.Resolve(ctx, key)
		if err != nil {
			continue
		}
		result = append(result, resolved)
	}

	return result, nil
}

func (c *Catalog) hasBaseModel(name string) bool {
	for _, item := range c.baseModels {
		if item == name {
			return true
		}
	}
	return false
}

func sourceFor(verified bool) string {
	if verified {
		return "catalog"
	}
	return "passthrough"
}

func (c *Catalog) HiddenFromList(name string) bool {
	base := strings.TrimSpace(strings.ToLower(name))
	if base == "" {
		return false
	}
	if strings.HasSuffix(base, strings.ToLower(c.thinkingSuffix)) {
		base = base[:len(base)-len(c.thinkingSuffix)]
	}
	if alias, ok := c.aliases[base]; ok {
		base = alias
	}
	return c.hiddenFromList[base]
}

func NormalizeModelName(name string) string {
	if name == "" {
		return name
	}

	lower := strings.ToLower(name)
	if m := reStandard.FindStringSubmatch(lower); m != nil {
		return m[1] + "." + m[2]
	}
	if m := reNoMinor.FindStringSubmatch(lower); m != nil {
		return m[1]
	}
	if m := reLegacy.FindStringSubmatch(lower); m != nil {
		return m[1] + "-" + m[2] + "." + m[3] + "-" + m[4]
	}
	if m := reDotWithDate.FindStringSubmatch(lower); m != nil {
		return m[1]
	}
	if m := reInverted.FindStringSubmatch(lower); m != nil {
		return "claude-" + m[3] + "-" + m[1] + "." + m[2]
	}
	return lower
}
