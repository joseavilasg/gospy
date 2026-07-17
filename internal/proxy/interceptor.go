package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"gospy/internal/history"

	"github.com/elazarl/goproxy"
)

type Interceptor struct {
	history *history.Store
}

func NewInterceptor(h *history.Store) *Interceptor {
	return &Interceptor{history: h}
}

func (ic *Interceptor) HandleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	body := ""
	if req.Body != nil {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, req.Body); err == nil {
			body = buf.String()
		}
		req.Body = io.NopCloser(&buf)
	}

	url := req.URL.Scheme + "://" + req.Host + req.URL.Path
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}

	LogRequest(req.Method, url)

	entry := &history.Entry{
		Request: history.RequestRecord{
			Method:  req.Method,
			URL:     url,
			Host:    req.Host,
			Headers: req.Header,
			Body:    body,
		},
		Action: "passthrough",
	}

	_ = ic.history.Save(entry)

	return req, nil
}

func (ic *Interceptor) HandleResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	if resp == nil || ctx.Req == nil {
		return resp
	}

	body := ""
	if resp.Body != nil {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, resp.Body); err == nil {
			body = buf.String()
		}
		resp.Body = io.NopCloser(&buf)
	}

	contentType := resp.Header.Get("Content-Type")
	LogResponse(ctx.Req.Method, ctx.Req.URL.String(), resp.StatusCode, contentType)

	entries := ic.history.List()
	if len(entries) > 0 {
		entry := entries[0]
		entry.Response = &history.ResponseRecord{
			Status:  resp.StatusCode,
			Headers: resp.Header,
			Body:    body,
		}
		entry.Modified = false

		data, _ := json.MarshalIndent(entry, "", "  ")
		_ = data
	}

	return resp
}

func (ic *Interceptor) HandleConnect(host string, ctx *goproxy.ProxyCtx) *goproxy.ConnectAction {
	LogConnect(host)
	LogMITM(host)
	return goproxy.MitmConnect
}

func ReadBodyString(body io.Reader) string {
	if body == nil {
		return ""
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, body); err != nil {
		return ""
	}
	return buf.String()
}

func IsTextResponse(contentType string) bool {
	textTypes := []string{
		"application/json",
		"text/html",
		"text/plain",
		"text/css",
		"text/javascript",
		"application/javascript",
		"application/xml",
		"text/xml",
	}
	ct := strings.ToLower(contentType)
	for _, t := range textTypes {
		if strings.Contains(ct, t) {
			return true
		}
	}
	return false
}
