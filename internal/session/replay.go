package session

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gospy/internal/ca"
	"gospy/internal/history"

	"github.com/elazarl/goproxy"
)

type ReplayServer struct {
	addr    string
	proxy   *goproxy.ProxyHttpServer
	session *ReplayStore
	cfg     *MatchConfig
}

func NewReplayServer(addr string, caCert *ca.CA, session *ReplayStore, cfg *MatchConfig) *ReplayServer {
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

	entry, result := rs.session.Match(req.Method, url, rs.cfg)
	switch result {
	case ResultHit:
		resp, err := buildResponse(entry, req, rs.session.Dir())
		if err != nil {
			LogReplayMiss(req.Method, url)
			return nil, &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("replay error: " + err.Error())),
				Request:    req,
			}
		}
		resp.Header.Set("X-Gospy-Replay", "hit")
		LogReplayHit(req.Method, url, entry.Response.Status)
		return nil, resp
	case ResultMiss:
		LogReplayMiss(req.Method, url)
		return nil, &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"X-Gospy-Replay": {"miss"}},
			Body:       io.NopCloser(strings.NewReader("no recording for " + req.Method + " " + url)),
			Request:    req,
		}
	default:
		LogReplayExhausted(req.Method, url)
		return nil, &http.Response{
			StatusCode: http.StatusGone,
			Header:     http.Header{"X-Gospy-Replay": {"exhausted"}},
			Body:       io.NopCloser(strings.NewReader("replay exhausted: all recorded requests have been served")),
			Request:    req,
		}
	}
}

func buildResponse(entry *history.Entry, req *http.Request, sessionDir string) (*http.Response, error) {
	if entry.Response == nil {
		return nil, fmt.Errorf("entry %s has no response", entry.ID)
	}
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
		data, err := os.ReadFile(filepath.Join(sessionDir, "bin", entry.Response.BodyFile))
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
