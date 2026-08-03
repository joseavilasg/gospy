package session

import (
	"encoding/json"
	"os"
)

type MatchConfig struct {
	IgnoreQueryParams []string `json:"ignore_query_params"`
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
