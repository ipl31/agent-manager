package config

import (
	"sort"
	"strings"
)

// toolDisplayOrder is the shared order of agent CLIs in new-session pickers.
var toolDisplayOrder = []string{"claude", "opencode", "codex", "grok", "gemini", "pi"}

func AgentToolNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Tools))
	for _, name := range cfg.ToolNames() {
		if !cfg.Tools[name].Shell {
			names = append(names, name)
		}
	}
	rank := make(map[string]int, len(toolDisplayOrder))
	for i, name := range toolDisplayOrder {
		rank[name] = i
	}
	sort.Slice(names, func(i, j int) bool {
		ri, iRanked := rank[names[i]]
		rj, jRanked := rank[names[j]]
		if iRanked && jRanked {
			return ri < rj
		}
		if iRanked != jRanked {
			return iRanked
		}
		return names[i] < names[j]
	})
	return names
}

func HiddenTools(raw string) map[string]bool {
	if raw == "" {
		return nil
	}
	hidden := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(part); name != "" {
			hidden[name] = true
		}
	}
	if len(hidden) == 0 {
		return nil
	}
	return hidden
}

func EnabledAgentTools(cfg Config, hidden map[string]bool) []string {
	all := AgentToolNames(cfg)
	if len(hidden) == 0 {
		return all
	}
	names := make([]string, 0, len(all))
	for _, name := range all {
		if !hidden[name] {
			names = append(names, name)
		}
	}
	return names
}

func DefaultAgentTool(names []string, chosen string) string {
	for _, name := range names {
		if name == chosen {
			return name
		}
	}
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
