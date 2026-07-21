package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"gospy/internal/history"
	"gospy/internal/rules"

	"github.com/andybalholm/brotli"
	"github.com/elazarl/goproxy"
)

type requestUserData struct {
	mockResponse *rules.MockResponse
	entryID      string
}

type Interceptor struct {
	history     *history.Store
	ignoreStore *IgnoreStore
	engine      *rules.Engine
}

func NewInterceptor(h *history.Store, ignore *IgnoreStore, engine *rules.Engine) *Interceptor {
	return &Interceptor{history: h, ignoreStore: ignore, engine: engine}
}

func (ic *Interceptor) HandleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	if ic.ignoreStore.IsIgnored(req.Host) {
		url := req.URL.Scheme + "://" + req.Host + req.URL.Path
		LogIgnored(req.Method, url)
		return req, nil
	}

	body := ""
	rawBody := ""
	compression := ""
	if req.Body != nil {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, req.Body); err == nil {
			data := buf.Bytes()
			ce := req.Header.Get("Content-Encoding")
			result := decompressBody(data, ce)
			body = result.Decoded
			rawBody = result.Raw
			compression = result.Compression
		}
		req.Body = io.NopCloser(&buf)
	}

	url := req.URL.Scheme + "://" + req.Host + req.URL.Path
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}

	originalRequest := history.RequestRecord{
		Method:      req.Method,
		URL:         url,
		Host:        req.Host,
		Headers:     req.Header.Clone(),
		Body:        body,
		RawBody:     rawBody,
		Compression: compression,
	}

	rule := ic.engine.Match(req.Method, req.Host, url, req.Header)

	if rule == nil || rule.Action == rules.ActionPassthrough {
		entry := &history.Entry{
			Request: originalRequest,
		}
		_ = ic.history.Save(entry)
		LogRequest(entry.ID, req.Method, url)
		return req, nil
	}

	switch rule.Action {
	case rules.ActionDrop:
		entry := &history.Entry{
			Request:       originalRequest,
			AppliedAction: string(rules.ActionDrop),
			RuleName:      rule.Name,
		}
		_ = ic.history.Save(entry)
		LogRequest(entry.ID, req.Method, url)
		LogInfo(fmt.Sprintf("DROPPED by rule %q: %s %s", rule.Name, req.Method, url))
		dropResp := &http.Response{
			StatusCode: 504,
			Status:     "504 Gateway Timeout",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}
		entry.Response = &history.ResponseRecord{
			Status:  504,
			Headers: make(map[string][]string),
			Body:    "",
		}
		_ = ic.history.Update(entry)
		return req, dropResp

	case rules.ActionMock:
		entry := &history.Entry{
			Request:       originalRequest,
			AppliedAction: string(rules.ActionMock),
			RuleName:      rule.Name,
		}
		_ = ic.history.Save(entry)
		LogRequest(entry.ID, req.Method, url)
		LogInfo(fmt.Sprintf("MOCKED by rule %q: %s %s", rule.Name, req.Method, url))

		resp := buildMockResponse(req, rule.MockResp)
		entry.Response = &history.ResponseRecord{
			Status:  resp.StatusCode,
			Headers: resp.Header,
			Body:    ReadBodyString(resp.Body),
		}
		resp.Body = io.NopCloser(strings.NewReader(entry.Response.Body))
		_ = ic.history.Update(entry)
		LogResponse(entry.ID, req.Method, url, resp.StatusCode, resp.Header.Get("Content-Type"))
		return req, resp

	case rules.ActionModify:
		applyModifications(req, rule.ModifiedReq)
		modifiedURL := req.URL.Scheme + "://" + req.Host + req.URL.Path
		if req.URL.RawQuery != "" {
			modifiedURL += "?" + req.URL.RawQuery
		}
		modifiedBody := ""
		if req.Body != nil {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, req.Body); err == nil {
				modifiedBody = buf.String()
			}
			req.Body = io.NopCloser(&buf)
		}

		entry := &history.Entry{
			Request: originalRequest,
			ServerRequest: &history.RequestRecord{
				Method:  req.Method,
				URL:     modifiedURL,
				Host:    req.Host,
				Headers: req.Header.Clone(),
				Body:    modifiedBody,
			},
			AppliedAction: string(rules.ActionModify),
			RuleName:      rule.Name,
		}
		_ = ic.history.Save(entry)
		LogRequest(entry.ID, req.Method, url)
		LogInfo(fmt.Sprintf("MODIFIED by rule %q: %s %s", rule.Name, req.Method, url))
		return req, nil

	case rules.ActionResponseMock:
		entry := &history.Entry{
			Request:       originalRequest,
			AppliedAction: string(rules.ActionResponseMock),
			RuleName:      rule.Name,
		}
		_ = ic.history.Save(entry)
		LogRequest(entry.ID, req.Method, url)
		LogInfo(fmt.Sprintf("RESPONSE MOCK by rule %q: %s %s", rule.Name, req.Method, url))
		ctx.UserData = &requestUserData{
			mockResponse: rule.MockResp,
			entryID:      entry.ID,
		}
		return req, nil
	}

	entry := &history.Entry{
		Request:       originalRequest,
		AppliedAction: string(rule.Action),
		RuleName:      rule.Name,
	}
	_ = ic.history.Save(entry)
	LogRequest(entry.ID, req.Method, url)
	return req, nil
}

func (ic *Interceptor) HandleResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	if resp == nil || ctx.Req == nil {
		return resp
	}

	reqURL := ctx.Req.URL.Scheme + "://" + ctx.Req.Host + ctx.Req.URL.Path
	if ctx.Req.URL.RawQuery != "" {
		reqURL += "?" + ctx.Req.URL.RawQuery
	}

	if ud, ok := ctx.UserData.(*requestUserData); ok {
		entry, err := ic.history.Get(ud.entryID)
		if err == nil {
			body := ""
			rawBody := ""
			compression := ""
			if resp.Body != nil {
				var buf bytes.Buffer
				if _, err := io.Copy(&buf, resp.Body); err == nil {
					result := decompressBody(buf.Bytes(), resp.Header.Get("Content-Encoding"))
					body = result.Decoded
					rawBody = result.Raw
					compression = result.Compression
				}
				resp.Body = io.NopCloser(&buf)
			}
			entry.ServerResponse = &history.ResponseRecord{
				Status:      resp.StatusCode,
				Headers:     resp.Header,
				Body:        body,
				RawBody:     rawBody,
				Compression: compression,
			}
			fakeResp := buildMockResponse(ctx.Req, ud.mockResponse)
			entry.Response = &history.ResponseRecord{
				Status:  fakeResp.StatusCode,
				Headers: fakeResp.Header,
				Body:    ReadBodyString(fakeResp.Body),
			}
			_ = ic.history.Update(entry)
			LogResponse(entry.ID, ctx.Req.Method, reqURL, fakeResp.StatusCode, fakeResp.Header.Get("Content-Type"))
		}
		return buildHttpResponse(ctx.Req, ud.mockResponse)
	}

	body := ""
	rawBody := ""
	compression := ""
	if resp.Body != nil {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, resp.Body); err == nil {
			result := decompressBody(buf.Bytes(), resp.Header.Get("Content-Encoding"))
			body = result.Decoded
			rawBody = result.Raw
			compression = result.Compression
		}
		resp.Body = io.NopCloser(&buf)
	}

	contentType := resp.Header.Get("Content-Type")

	entries := ic.history.List()
	for _, entry := range entries {
		if entry.Request.Method == ctx.Req.Method &&
			(entry.Request.URL == reqURL || (entry.ServerRequest != nil && entry.ServerRequest.URL == reqURL)) &&
			entry.Response == nil {
			entry.Response = &history.ResponseRecord{
				Status:      resp.StatusCode,
				Headers:     resp.Header,
				Body:        body,
				RawBody:     rawBody,
				Compression: compression,
			}
			_ = ic.history.Update(entry)
			LogResponse(entry.ID, ctx.Req.Method, reqURL, resp.StatusCode, contentType)
			break
		}
	}

	return resp
}

func (ic *Interceptor) HandleConnect(host string, ctx *goproxy.ProxyCtx) *goproxy.ConnectAction {
	LogConnect(host)
	LogMITM(host)
	return goproxy.MitmConnect
}

func applyModifications(req *http.Request, mod *rules.ModifiedRequest) {
	if mod == nil {
		return
	}
	if mod.Host != "" {
		req.Host = mod.Host
		req.URL.Host = mod.Host
	}
	if mod.URL != "" {
		if parsed, err := url.Parse(mod.URL); err == nil {
			req.URL.Path = parsed.Path
			req.URL.RawQuery = parsed.RawQuery
		} else {
			req.URL.Path = mod.URL
			req.URL.RawQuery = ""
		}
	}
	if mod.Headers != nil {
		for k, vals := range mod.Headers {
			req.Header.Del(k)
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}
	}
	if mod.Body != "" {
		req.Body = io.NopCloser(strings.NewReader(mod.Body))
		req.ContentLength = int64(len(mod.Body))
	}
}

func buildMockResponse(req *http.Request, mock *rules.MockResponse) *http.Response {
	status := 200
	headers := http.Header{}
	body := ""

	if mock != nil {
		status = mock.Status
		if status == 0 {
			status = 200
		}
		for k, vals := range mock.Headers {
			for _, v := range vals {
				headers.Set(k, v)
			}
		}
		body = mock.Body
	}

	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func buildHttpResponse(req *http.Request, mock *rules.MockResponse) *http.Response {
	resp := buildMockResponse(req, mock)
	return &http.Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       resp.Body,
		Request:    req,
	}
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

type decompressResult struct {
	Decoded     string
	Raw         string
	Compression string
}

func decompressBody(data []byte, contentEncoding string) decompressResult {
	if len(data) == 0 {
		return decompressResult{}
	}

	raw := string(data)

	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			defer reader.Close()
			if decompressed, err := io.ReadAll(reader); err == nil {
				return decompressResult{Decoded: string(decompressed), Raw: raw, Compression: "gzip"}
			}
		}
	}

	if data[0] == 0x78 {
		reader, err := zlib.NewReader(bytes.NewReader(data))
		if err == nil {
			defer reader.Close()
			if decompressed, err := io.ReadAll(reader); err == nil {
				return decompressResult{Decoded: string(decompressed), Raw: raw, Compression: "zlib"}
			}
		}
	}

	if len(contentEncoding) > 0 && strings.Contains(strings.ToLower(contentEncoding), "br") {
		reader := brotli.NewReader(bytes.NewReader(data))
		if decompressed, err := io.ReadAll(reader); err == nil {
			return decompressResult{Decoded: string(decompressed), Raw: raw, Compression: "brotli"}
		}
	}

	if len(contentEncoding) > 0 && strings.Contains(strings.ToLower(contentEncoding), "deflate") {
		reader := flate.NewReader(bytes.NewReader(data))
		if decompressed, err := io.ReadAll(reader); err == nil {
			return decompressResult{Decoded: string(decompressed), Raw: raw, Compression: "deflate"}
		}
	}

	return decompressResult{Decoded: raw}
}
