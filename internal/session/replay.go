package session

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gospy/internal/ca"

	"github.com/elazarl/goproxy"
)

type ReplayServer struct {
	addr    string
	proxy   *goproxy.ProxyHttpServer
	session *Store
	cfg     *MatchConfig
}

func NewReplayServer(addr string, caCert *ca.CA, session *Store, cfg *MatchConfig) *ReplayServer {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false

	caTLSCert := caCert.TLSCert()
	goproxy.MitmConnect = &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		TLSConfig: goproxy.TLSConfigFromCA(&caTLSCert),
	}
	proxy.CertStore = ca.NewCertStorage(caCert)

	rs := &ReplayServer{
		addr:    addr,
		proxy:   proxy,
		session: session,
		cfg:     cfg,
	}

	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().DoFunc(rs.handleRequest)

	return rs
}

func (rs *ReplayServer) ListenAndServe() error {
	return http.ListenAndServe(rs.addr, rs.proxy)
}

func (rs *ReplayServer) handleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	url := req.URL.Scheme + "://" + req.Host + req.URL.Path
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}

	entry, exhausted := rs.session.Match(req.Method, url, rs.cfg)
	if entry == nil {
		msg := "no recording for " + req.Method + " " + url
		if exhausted {
			LogReplayExhausted(req.Method, url)
			msg = "recording exhausted for " + req.Method + " " + url
		} else {
			LogReplayMiss(req.Method, url)
		}
		return nil, &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(msg)),
			Request:    req,
		}
	}

	resp, err := buildResponse(entry, req, rs.session.dir)
	if err != nil {
		LogReplayMiss(req.Method, url)
		return nil, &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("replay error: " + err.Error())),
			Request:    req,
		}
	}

	LogReplayHit(req.Method, url, entry.Response.Status)
	return nil, resp
}

func buildResponse(entry *Entry, req *http.Request, sessionDir string) (*http.Response, error) {
	status := entry.Response.Status
	if status == 0 {
		return nil, fmt.Errorf("entry %s has no response status", entry.ID)
	}

	headers := make(http.Header)
	for k, vals := range entry.Response.Headers {
		for _, v := range vals {
			headers.Add(k, v)
		}
	}

	var body io.ReadCloser
	if entry.Response.BodyFile != "" {
		data, err := os.ReadFile(filepath.Join(sessionDir, "entries", entry.Response.BodyFile))
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		body = io.NopCloser(strings.NewReader(string(data)))
	} else {
		body = http.NoBody
	}

	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     headers,
		Body:       body,
		Request:    req,
	}, nil
}

func LogReplayHit(method, url string, status int) {
	fmt.Printf("[REPLAY] HIT  %s %s → %d\n", method, url, status)
}

func LogReplayMiss(method, url string) {
	fmt.Printf("[REPLAY] MISS %s %s\n", method, url)
}

func LogReplayExhausted(method, url string) {
	fmt.Printf("[REPLAY] EXHAUSTED %s %s\n", method, url)
}
