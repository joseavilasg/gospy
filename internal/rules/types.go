package rules

import "time"

type Action string

const (
	ActionPassthrough Action = "passthrough"
	ActionLog         Action = "log"
	ActionPause       Action = "pause"
	ActionMock        Action = "mock"
)

type MatchRule struct {
	Method     string            `json:"method,omitempty"`
	URLPattern string            `json:"url_pattern,omitempty"`
	Host       string            `json:"host,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type MockResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
}

type Rule struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Match     MatchRule     `json:"match"`
	Action    Action        `json:"action"`
	MockResp  *MockResponse `json:"mock_response,omitempty"`
	Enabled   bool          `json:"enabled"`
	CreatedAt time.Time     `json:"created_at"`
}
