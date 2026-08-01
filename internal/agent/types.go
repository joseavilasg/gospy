package agent

import "time"

// RequestSpec is the input of the send_request tool. The template is required:
// the request is rebuilt from the captured entry (host fixed, auth headers from
// the vault), and every omitted field is inherited from the template.
type RequestSpec struct {
	Template string              `json:"template"`
	Method   string              `json:"method,omitempty"`
	Path     string              `json:"path,omitempty"`
	Query    map[string][]string `json:"query,omitempty"`
	Headers  map[string][]string `json:"headers,omitempty"`
	Body     string              `json:"body,omitempty"`
}

// AgentEntry is the lean summary served by list_entries. Headers are
// intentionally omitted to keep the payload small; get_entry returns them.
type AgentEntry struct {
	ID                  string    `json:"id"`
	Timestamp           time.Time `json:"timestamp"`
	UpdatedAt           time.Time `json:"updatedAt"`
	Method              string    `json:"method"`
	URL                 string    `json:"url"`
	Host                string    `json:"host"`
	Status              *int      `json:"status,omitempty"`
	Origin              string    `json:"origin,omitempty"`
	AppliedAction       string    `json:"appliedAction,omitempty"`
	RuleName            string    `json:"ruleName,omitempty"`
	RequestContentType  string    `json:"requestContentType,omitempty"`
	ResponseContentType string    `json:"responseContentType,omitempty"`
}

// ListPage is the output of the list_entries tool.
type ListPage struct {
	Entries      []*AgentEntry `json:"entries"`
	Total        int           `json:"total"`
	VisibleCount int           `json:"visibleCount"`
	Offset       int           `json:"offset"`
	HasMore      bool          `json:"hasMore"`
}

// ResolvedRequest echoes what was actually sent. Headers are never echoed: the
// sensitive ones belong to the vault and the agent already knows the rest.
type ResolvedRequest struct {
	Method     string `json:"method"`
	URL        string `json:"url"`
	BodySource string `json:"bodySource"`
}

// ForwardResponse is the output of the send_request tool.
type ForwardResponse struct {
	EntryID string              `json:"entryId,omitempty"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body,omitempty"`
	Request ResolvedRequest     `json:"request"`
}
