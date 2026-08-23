package session

import (
	"encoding/json"
	"os"
	"strings"
)

type HostRule struct {
	Host              string   `json:"host"`
	PathPrefix        string   `json:"path_prefix,omitempty"`
	IgnoreQueryParams []string `json:"ignore_query_params"`
}

type MatchConfig struct {
	IgnoreQueryParams []string   `json:"ignore_query_params"`
	IgnoreHosts       []string   `json:"ignore_hosts"`
	HostRules         []HostRule `json:"host_rules"`
}

// EffectiveIgnoreParams returns the complete set of query-param keys that should
// be stripped before computing a match key for the given host and path. It is the
// union of the global IgnoreQueryParams and every HostRule whose host matches
// (lowercased) and whose path_prefix is a prefix of the request path.
func (cfg *MatchConfig) EffectiveIgnoreParams(host, path string) []string {
	if cfg == nil {
		return nil
	}
	host = strings.ToLower(host)
	var extra []string
	for _, r := range cfg.HostRules {
		if strings.ToLower(r.Host) == host && strings.HasPrefix(path, r.PathPrefix) {
			extra = append(extra, r.IgnoreQueryParams...)
		}
	}
	if len(extra) == 0 {
		return cfg.IgnoreQueryParams
	}
	if len(cfg.IgnoreQueryParams) == 0 {
		return extra
	}
	seen := make(map[string]bool, len(cfg.IgnoreQueryParams)+len(extra))
	var out []string
	for _, k := range cfg.IgnoreQueryParams {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range extra {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func LoadMatchConfig(path string) (*MatchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg MatchConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
