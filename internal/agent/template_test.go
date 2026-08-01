package agent

import (
	"strings"
	"testing"

	"gospy/internal/history"
)

func newTemplateScope(t *testing.T) (*Scope, *history.Store) {
	t.Helper()
	hist := newTestHistory(t)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}
	return NewScope(hist, fs, nil, nil), hist
}

func saveTemplateEntry(t *testing.T, hist *history.Store) *history.Entry {
	t.Helper()
	e := &history.Entry{
		Request: history.RequestRecord{
			Method: "POST",
			URL:    "http://a.com:8080/api/original?a=1&b=2",
			Host:   "a.com",
			Headers: map[string][]string{
				"Authorization": {"Bearer vault-secret"},
				"Content-Type":  {"application/json"},
				"X-Business":    {"keep-me"},
			},
			Body: `{"orig":true}`,
		},
	}
	if err := hist.Save(e); err != nil {
		t.Fatalf("save template: %v", err)
	}
	return e
}

func TestTemplate_InheritsOmitted(t *testing.T) {
	sc, hist := newTemplateScope(t)
	e := saveTemplateEntry(t, hist)

	built, err := sc.buildTemplateRequest(RequestSpec{Template: e.ID})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if built.method != "POST" {
		t.Errorf("method = %q, want inherited POST", built.method)
	}
	if built.url != "http://a.com:8080/api/original?a=1&b=2" {
		t.Errorf("url = %q, want the template url unchanged", built.url)
	}
	if string(built.body) != `{"orig":true}` || built.bodySource != "template" {
		t.Errorf("body = %q (source %q)", built.body, built.bodySource)
	}
	if built.headers["Authorization"][0] != "Bearer vault-secret" {
		t.Errorf("vault auth lost: %v", built.headers["Authorization"])
	}
	if built.headers["Content-Length"] != nil || built.headers["Host"] != nil {
		t.Errorf("framing headers must be dropped, got %v", built.headers)
	}
}

func TestTemplate_HostIsFixed(t *testing.T) {
	sc, hist := newTemplateScope(t)
	e := saveTemplateEntry(t, hist)

	built, err := sc.buildTemplateRequest(RequestSpec{Template: e.ID, Path: "/api/new", Method: "PUT"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if built.method != "PUT" {
		t.Errorf("method = %q, want PUT", built.method)
	}
	if built.url != "http://a.com:8080/api/new?a=1&b=2" {
		t.Errorf("url = %q, want the host fixed with the new path", built.url)
	}

	// Even an absolute-looking path stays on the template's host.
	built, err = sc.buildTemplateRequest(RequestSpec{Template: e.ID, Path: "//evil.com/x"})
	if err != nil {
		t.Fatalf("build hostile path: %v", err)
	}
	if !strings.HasPrefix(built.url, "http://a.com:8080//evil.com/x") {
		t.Errorf("host was not fixed: %q", built.url)
	}
}

func TestTemplate_QuerySemantics(t *testing.T) {
	sc, hist := newTemplateScope(t)
	e := saveTemplateEntry(t, hist)

	// nil query inherits the template's.
	built, err := sc.buildTemplateRequest(RequestSpec{Template: e.ID})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if built.url != "http://a.com:8080/api/original?a=1&b=2" {
		t.Errorf("inherited query url = %q", built.url)
	}

	// {} clears the query.
	built, err = sc.buildTemplateRequest(RequestSpec{Template: e.ID, Query: map[string][]string{}})
	if err != nil {
		t.Fatalf("build clear: %v", err)
	}
	if built.url != "http://a.com:8080/api/original" {
		t.Errorf("cleared query url = %q", built.url)
	}

	// Provided query replaces the whole thing.
	built, err = sc.buildTemplateRequest(RequestSpec{Template: e.ID, Query: map[string][]string{"x": {"1"}, "y": {"2", "3"}}})
	if err != nil {
		t.Fatalf("build replace: %v", err)
	}
	if built.url != "http://a.com:8080/api/original?x=1&y=2&y=3" {
		t.Errorf("replaced query url = %q", built.url)
	}
}

func TestTemplate_SensitiveOverrideRejected(t *testing.T) {
	sc, hist := newTemplateScope(t)
	e := saveTemplateEntry(t, hist)

	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "set-cookie"} {
		_, err := sc.buildTemplateRequest(RequestSpec{
			Template: e.ID,
			Headers:  map[string][]string{name: {"stolen"}},
		})
		if err == nil || !strings.Contains(err.Error(), "is sensitive") {
			t.Errorf("header %q must be rejected, got %v", name, err)
		}
	}
}

func TestTemplate_HeaderOverride(t *testing.T) {
	sc, hist := newTemplateScope(t)
	e := saveTemplateEntry(t, hist)

	built, err := sc.buildTemplateRequest(RequestSpec{
		Template: e.ID,
		Headers:  map[string][]string{"X-Business": {"new-value"}, "X-New": {"added"}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if built.headers["X-Business"][0] != "new-value" {
		t.Errorf("overridden header = %v", built.headers["X-Business"])
	}
	if built.headers["X-New"][0] != "added" {
		t.Errorf("added header missing: %v", built.headers["X-New"])
	}
	if built.headers["Authorization"][0] != "Bearer vault-secret" {
		t.Errorf("vault auth must survive: %v", built.headers["Authorization"])
	}
}

func TestTemplate_BodyTextOverride(t *testing.T) {
	sc, hist := newTemplateScope(t)
	e := saveTemplateEntry(t, hist)

	built, err := sc.buildTemplateRequest(RequestSpec{Template: e.ID, Body: `{"new":true}`})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if string(built.body) != `{"new":true}` || built.bodySource != "override" {
		t.Errorf("body = %q (source %q)", built.body, built.bodySource)
	}
}

func TestTemplate_BodyRawReplay(t *testing.T) {
	hist := newTestHistory(t)
	fs := &mockFilterStore{gate: true, filters: history.Filters{Host: []string{"a.com"}, MatchMode: "all"}}
	sc := NewScope(hist, fs, nil, nil)

	raw := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01} // binary bytes: byte-exact replay
	filename, err := hist.SaveBinaryBody("body", "req", raw)
	if err != nil {
		t.Fatalf("SaveBinaryBody: %v", err)
	}
	e := &history.Entry{
		Request: history.RequestRecord{
			Method: "POST",
			URL:    "http://a.com/upload",
			Host:   "a.com",
			Headers: map[string][]string{
				"Content-Type":     {"image/png"},
				"Content-Encoding": {"gzip"},
			},
			BodyFile: filename,
		},
	}
	if err := hist.Save(e); err != nil {
		t.Fatalf("save: %v", err)
	}

	built, err := sc.buildTemplateRequest(RequestSpec{Template: e.ID})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if string(built.body) != string(raw) {
		t.Errorf("raw body not replayed byte-exact: %x", built.body)
	}
	if built.bodySource != "template" {
		t.Errorf("bodySource = %q, want template", built.bodySource)
	}
	if built.headers["Content-Encoding"][0] != "gzip" {
		t.Errorf("compression header lost: %v", built.headers["Content-Encoding"])
	}

	// A text override drops Content-Encoding (the bytes are no longer the raw ones).
	built, err = sc.buildTemplateRequest(RequestSpec{Template: e.ID, Body: "plain text"})
	if err != nil {
		t.Fatalf("build override: %v", err)
	}
	if built.headers["Content-Encoding"] != nil {
		t.Errorf("Content-Encoding must drop on text override: %v", built.headers["Content-Encoding"])
	}
	if built.bodySource != "override" {
		t.Errorf("bodySource = %q, want override", built.bodySource)
	}
}

func TestTemplate_NotVisible(t *testing.T) {
	sc, hist := newTemplateScope(t)
	hidden := saveTestEntry(t, hist, "b.com", "", 200)

	_, err := sc.buildTemplateRequest(RequestSpec{Template: hidden.ID})
	if err == nil || !strings.Contains(err.Error(), "not in the agent's visible set") {
		t.Errorf("hidden template error = %v", err)
	}
}

func TestTemplate_RequiresTemplate(t *testing.T) {
	sc, _ := newTemplateScope(t)

	_, err := sc.buildTemplateRequest(RequestSpec{})
	if err == nil || !strings.Contains(err.Error(), "template is required") {
		t.Errorf("missing template error = %v", err)
	}
}
