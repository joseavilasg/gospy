package rules

import "time"

type Action string

const (
	ActionPassthrough  Action = "passthrough"
	ActionModify       Action = "modify"
	ActionMock         Action = "mock"
	ActionDrop         Action = "drop"
	ActionResponseMock Action = "response_mock"
)

type MatchRule struct {
	Method     string            `json:"method,omitempty"`
	URLPattern string            `json:"url_pattern,omitempty"`
	Host       string            `json:"host,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type MockResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body"`
}

type ModifiedRequest struct {
	Host    string              `json:"host,omitempty"`
	URL     string              `json:"url,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}

type Rule struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Match       MatchRule        `json:"match"`
	Action      Action           `json:"action"`
	MockResp    *MockResponse    `json:"mock_response,omitempty"`
	ModifiedReq *ModifiedRequest `json:"modified_request,omitempty"`
	Enabled     bool             `json:"enabled"`
	CreatedAt   time.Time        `json:"created_at"`
}
