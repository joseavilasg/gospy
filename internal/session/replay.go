package session

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gospy/internal/ca"
	"gospy/internal/history"

	"github.com/elazarl/goproxy"
)

type ReplayServer struct {
	addr           string
	proxy          *goproxy.ProxyHttpServer
	session        *ReplayStore
	cfg            *MatchConfig
	logRoot        string
	logMu          sync.Mutex
	log            *ReplayLog
	notifier       func(ReplayEvent)
	originResolver OriginResolver
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

// SetReplayLogRoot enables persistence of every replay run under root. The
// first request served after a process start opens a new run directory.
func (rs *ReplayServer) SetReplayLogRoot(root string) {
	rs.logRoot = root
}

// SetReplayNotifier registers a callback invoked for every request handled by
// the replay server, after the event has been persisted.
func (rs *ReplayServer) SetReplayNotifier(fn func(ReplayEvent)) {
	rs.notifier = fn
}

// SetMatchConfig replaces the match config used for matching and persisted as
// the run's snapshot. Takes effect immediately.
func (rs *ReplayServer) SetMatchConfig(cfg *MatchConfig) {
	rs.cfg = cfg
}

// SetOriginResolver registers a callback that resolves the client process of
// an incoming request from its remote TCP address, captured into the event so
// the UI can show who made the replay request.
func (rs *ReplayServer) SetOriginResolver(fn OriginResolver) {
	rs.originResolver = fn
}

// Close finalizes the active run log.
func (rs *ReplayServer) Close() error {
	rs.logMu.Lock()
	defer rs.logMu.Unlock()
	if rs.log == nil {
		return nil
	}
	return rs.log.Close()
}

func (rs *ReplayServer) ListenAndServe() error {
	return http.ListenAndServe(rs.addr, rs.proxy)
}

// MissBody returns the body of a synthetic miss response, shared by the replay
// server and the UI that previews it.
func MissBody(method, url string) string {
	return "no recording for " + method + " " + url
}

// ExhaustedBody returns the body of a synthetic exhausted response.
func ExhaustedBody() string {
	return "replay exhausted: all recorded requests have been served"
}

func (rs *ReplayServer) handleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	url := req.URL.Scheme + "://" + req.Host + req.URL.Path
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}

	rec, rawBody := captureRequest(req, url)
	entry, result, unconsumed, totalPending := rs.session.MatchDetailed(req.Method, url, rs.cfg)

	ev := ReplayEvent{
		Timestamp: time.Now(),
		Method:    req.Method,
		URL:       url,
		Request:   rec,
		Result:    result.String(),
	}

	if rs.originResolver != nil {
		if info := rs.originResolver(req.RemoteAddr); info != nil {
			ev.ClientProcess = info.Name
			ev.ClientPath = info.Path
			ev.ClientPID = info.PID
			ev.ClientDisplayName = info.DisplayName
		}
	}

	var resp *http.Response
	var buildErr error
	switch result {
	case ResultHit:
		resp, buildErr = buildResponse(entry, req, rs.session.Dir())
		if buildErr != nil {
			LogReplayMiss(req.Method, url)
			errText := "replay error: " + buildErr.Error()
			resp = &http.Response{
				StatusCode:    http.StatusInternalServerError,
				Header:        make(http.Header),
				Body:          io.NopCloser(strings.NewReader(errText)),
				ContentLength: int64(len(errText)),
				Request:       req,
			}
		}
		resp.Header.Set("X-Gospy-Replay", "hit")
		ev.Status = resp.StatusCode
		ev.EntryID = entry.ID
		ev.MatchedURL = entry.Request.URL
		LogReplayHit(req.Method, url, resp.StatusCode)
	case ResultMiss:
		ev.Status = http.StatusNotFound
		ev.Unconsumed = unconsumed
		ev.TotalPending = totalPending
		missText := MissBody(req.Method, url)
		resp = &http.Response{
			StatusCode:    http.StatusNotFound,
			Header:        http.Header{"X-Gospy-Replay": {"miss"}},
			Body:          io.NopCloser(strings.NewReader(missText)),
			ContentLength: int64(len(missText)),
			Request:       req,
		}
		LogReplayMiss(req.Method, url)
	default:
		ev.Status = http.StatusGone
		exhaustedText := ExhaustedBody()
		resp = &http.Response{
			StatusCode:    http.StatusGone,
			Header:        http.Header{"X-Gospy-Replay": {"exhausted"}},
			Body:          io.NopCloser(strings.NewReader(exhaustedText)),
			ContentLength: int64(len(exhaustedText)),
			Request:       req,
		}
		LogReplayExhausted(req.Method, url)
	}

	rs.emit(&ev, rawBody)
	return nil, resp
}

// emit records the event with its run progress and notifies listeners.
func (rs *ReplayServer) emit(ev *ReplayEvent, rawBody []byte) {
	consumed, total, exhausted := rs.session.Progress(rs.cfg)
	ev.Consumed = consumed
	ev.Total = total
	ev.Exhausted = exhausted

	if rs.logRoot != "" {
		if log, err := rs.ensureLog(); err == nil {
			ev.RunID = log.RunID()
			if err := log.Append(ev, rawBody); err != nil {
				fmt.Printf("[REPLAY] warn: write run log: %v\n", err)
			}
		}
	}
	if rs.notifier != nil {
		rs.notifier(*ev)
	}
}

func (rs *ReplayServer) ensureLog() (*ReplayLog, error) {
	rs.logMu.Lock()
	defer rs.logMu.Unlock()
	if rs.log != nil {
		return rs.log, nil
	}
	runDir := nextRunDir(rs.logRoot)
	log, err := OpenReplayLog(runDir, filepath.Base(runDir))
	if err != nil {
		return nil, err
	}
	if err := WriteMatchConfig(runDir, rs.cfg); err != nil {
		fmt.Printf("[REPLAY] warn: write match config: %v\n", err)
	}
	rs.log = log
	return log, nil
}

// captureRequest mirrors the interceptor's body handling: the raw body is
// always retained (written to the run bin by the log), and text bodies are
// decoded inline for display.
func captureRequest(req *http.Request, url string) (history.RequestRecord, []byte) {
	rec := history.RequestRecord{
		Method:  req.Method,
		URL:     url,
		Host:    req.Host,
		Headers: req.Header.Clone(),
	}
	var raw []byte
	if req.Body != nil {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, req.Body); err == nil {
			data := buf.Bytes()
			ce := req.Header.Get("Content-Encoding")
			ct := req.Header.Get("Content-Type")
			if len(data) > 0 {
				raw = data
				rec.IsBinaryBody = history.IsBinaryBody(data, ce, ct)
				if !rec.IsBinaryBody {
					result := history.DecompressBody(data, ce)
					rec.Body = result.Decoded
					rec.RawBody = result.Raw
					rec.Compression = result.Compression
				}
			}
		}
		req.Body = io.NopCloser(&buf)
	}
	return rec, raw
}

// nextRunDir picks a run directory under root using a timestamp, appending a
// numeric suffix when the directory already exists.
func nextRunDir(root string) string {
	base := time.Now().Format("20060102-150405")
	dir := filepath.Join(root, base)
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return dir
		}
		dir = filepath.Join(root, fmt.Sprintf("%s-%d", base, i))
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
	var contentLength int64
	if entry.Response.BodyFile != "" {
		data, err := os.ReadFile(filepath.Join(sessionDir, "bin", entry.Response.BodyFile))
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		body = io.NopCloser(strings.NewReader(string(data)))
		contentLength = int64(len(data))
	} else {
		body = http.NoBody
	}

	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        headers,
		Body:          body,
		ContentLength: contentLength,
		Request:       req,
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
