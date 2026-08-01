package agent

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"gospy/internal/history"
)

// hopByHopHeaders never survive a rebuild: Go recomputes framing from the body
// bytes and takes the host from the URL.
var hopByHopHeaders = map[string]bool{
	"Connection":        true,
	"Proxy-Connection":  true,
	"Keep-Alive":        true,
	"Te":                true,
	"Trailer":           true,
	"Transfer-Encoding": true,
	"Upgrade":           true,
	"Content-Length":    true,
	"Host":              true,
}

// builtRequest is a resolved template request, ready to forward.
type builtRequest struct {
	method     string
	url        string
	headers    map[string][]string
	body       []byte
	bodySource string
}

// buildTemplateRequest resolves an agent spec against the captured template
// entry. The host (scheme+host+port) is fixed to the template's; sensitive
// headers come exclusively from the vault; every omitted field is inherited.
// The template must be in the agent's visible set (IsVisible), like get_entry.
func (sc *Scope) buildTemplateRequest(spec RequestSpec) (*builtRequest, error) {
	if spec.Template == "" {
		return nil, fmt.Errorf("template is required")
	}
	if !sc.IsVisible(spec.Template) {
		return nil, fmt.Errorf("template %s is not in the agent's visible set", spec.Template)
	}
	entry, err := sc.hist.Get(spec.Template)
	if err != nil {
		return nil, fmt.Errorf("load template: %w", err)
	}

	base, err := url.Parse(entry.Request.URL)
	if err != nil {
		return nil, fmt.Errorf("parse template url: %q", entry.Request.URL)
	}

	method := spec.Method
	if method == "" {
		method = entry.Request.Method
	}

	u := *base
	if spec.Path != "" {
		u.Path = spec.Path
	}
	if spec.Query != nil {
		u.RawQuery = url.Values(spec.Query).Encode()
	}

	headers := cloneHeaders(entry.Request.Headers)
	for k, vs := range spec.Headers {
		if isSensitiveHeader(k) {
			return nil, fmt.Errorf("header %q is sensitive and can only come from the template", k)
		}
		delete(headers, k)
		headers[k] = append([]string(nil), vs...)
	}
	for h := range hopByHopHeaders {
		delete(headers, h)
	}

	body, err := sc.templateBody(entry)
	if err != nil {
		return nil, err
	}
	bodySource := "template"
	if spec.Body != "" {
		body = []byte(spec.Body)
		bodySource = "override"
		delete(headers, "Content-Encoding")
	}

	return &builtRequest{
		method:     method,
		url:        u.String(),
		headers:    headers,
		body:       body,
		bodySource: bodySource,
	}, nil
}

// templateBody returns the template's captured request body for byte-exact
// replay: the raw .bin bytes when stored (compressed bodies keep their
// compressed bytes), with a decoded-text fallback for legacy entries.
func (sc *Scope) templateBody(entry *history.Entry) ([]byte, error) {
	if entry.Request.BodyFile != "" {
		data, err := os.ReadFile(filepath.Join(sc.hist.Dir(), "bin", entry.Request.BodyFile))
		if err != nil {
			return nil, fmt.Errorf("read template body: %w", err)
		}
		return data, nil
	}
	if entry.Request.Body != "" {
		return []byte(entry.Request.Body), nil
	}
	return nil, nil
}

func cloneHeaders(h map[string][]string) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	return out
}
