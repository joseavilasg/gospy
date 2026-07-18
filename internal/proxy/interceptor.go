package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"

	"gospy/internal/history"

	"github.com/andybalholm/brotli"
	"github.com/elazarl/goproxy"
)

type Interceptor struct {
	history     *history.Store
	ignoreStore *IgnoreStore
}

func NewInterceptor(h *history.Store, ignore *IgnoreStore) *Interceptor {
	return &Interceptor{history: h, ignoreStore: ignore}
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

	entry := &history.Entry{
		Request: history.RequestRecord{
			Method:      req.Method,
			URL:         url,
			Host:        req.Host,
			Headers:     req.Header,
			Body:        body,
			RawBody:     rawBody,
			Compression: compression,
		},
		Action: "passthrough",
	}

	_ = ic.history.Save(entry)
	LogRequest(entry.ID, req.Method, url)

	return req, nil
}

func (ic *Interceptor) HandleResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	if resp == nil || ctx.Req == nil {
		return resp
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

	reqURL := ctx.Req.URL.Scheme + "://" + ctx.Req.Host + ctx.Req.URL.Path
	if ctx.Req.URL.RawQuery != "" {
		reqURL += "?" + ctx.Req.URL.RawQuery
	}

	entries := ic.history.List()
	for _, entry := range entries {
		if entry.Request.Method == ctx.Req.Method &&
			entry.Request.URL == reqURL &&
			entry.Response == nil {
			entry.Response = &history.ResponseRecord{
				Status:      resp.StatusCode,
				Headers:     resp.Header,
				Body:        body,
				RawBody:     rawBody,
				Compression: compression,
			}
			entry.Modified = false
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

	// gzip: magic 0x1f 0x8b
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			defer reader.Close()
			if decompressed, err := io.ReadAll(reader); err == nil {
				return decompressResult{Decoded: string(decompressed), Raw: raw, Compression: "gzip"}
			}
		}
	}

	// zlib: magic 0x78 (CMF byte)
	if data[0] == 0x78 {
		reader, err := zlib.NewReader(bytes.NewReader(data))
		if err == nil {
			defer reader.Close()
			if decompressed, err := io.ReadAll(reader); err == nil {
				return decompressResult{Decoded: string(decompressed), Raw: raw, Compression: "zlib"}
			}
		}
	}

	// brotli: content-encoding: br
	if len(contentEncoding) > 0 && strings.Contains(strings.ToLower(contentEncoding), "br") {
		reader := brotli.NewReader(bytes.NewReader(data))
		if decompressed, err := io.ReadAll(reader); err == nil {
			return decompressResult{Decoded: string(decompressed), Raw: raw, Compression: "brotli"}
		}
	}

	// raw deflate: solo si header dice "deflate"
	if len(contentEncoding) > 0 && strings.Contains(strings.ToLower(contentEncoding), "deflate") {
		reader := flate.NewReader(bytes.NewReader(data))
		if decompressed, err := io.ReadAll(reader); err == nil {
			return decompressResult{Decoded: string(decompressed), Raw: raw, Compression: "deflate"}
		}
	}

	return decompressResult{Decoded: raw}
}
