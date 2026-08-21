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
	"gospy/internal/rules"

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
	rulesEngine    *rules.Engine
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

// SetRulesEngine enables the rules engine on the replay server. A matched rule
// overrides the response the session would otherwise serve: mock and
// response_mock serve the rule's fake response (in replay there is no upstream
// server, so response_mock collapses into mock), drop returns the silent 504.
// The queue is still consumed on a hit so the run's progress stays aligned.
func (rs *ReplayServer) SetRulesEngine(engine *rules.Engine) {
	rs.rulesEngine = engine
}

// RunLister lists replay runs with their active state. Implemented by
// ReplayServer.
type RunLister interface {
	ListReplayRuns() ([]RunSummary, error)
}

// ListReplayRuns returns summaries of every replay run stored under the log
// root, newest first. The currently active run (if any) is marked Active.
func (rs *ReplayServer) ListReplayRuns() ([]RunSummary, error) {
	if rs.logRoot == "" {
		return []RunSummary{}, nil
	}
	runs, err := ListReplayRuns(rs.logRoot)
	if err != nil {
		return nil, err
	}
	rs.logMu.Lock()
	active := ""
	if rs.log != nil {
		active = rs.log.RunID()
	}
	rs.logMu.Unlock()
	for i := range runs {
		runs[i].Active = runs[i].RunID == active
	}
	return runs, nil
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

	var rule *rules.Rule
	if rs.rulesEngine != nil {
		rule = rs.rulesEngine.Match(req.Method, req.Host, url, req.Header)
	}
	ruleResp := buildRuleResponse(req, rule)

	rec, rawBody := captureRequest(req, url)
	entry, result, unconsumed, totalPending := rs.session.MatchDetailed(req.Method, url, rs.cfg)

	ev := ReplayEvent{
		Timestamp: time.Now(),
		Method:    req.Method,
		URL:       url,
		Request:   rec,
		Result:    result.String(),
	}
	if rule != nil && rule.Action != rules.ActionPassthrough {
		ev.AppliedAction = string(rule.Action)
		ev.RuleName = rule.Name
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
		if ruleResp != nil {
			resp = ruleResp
		} else {
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
		}
		resp.Header.Set("X-Gospy-Replay", "hit")
		ev.EntryID = entry.ID
		ev.MatchedURL = entry.Request.URL
		LogReplayHit(req.Method, url, resp.StatusCode)
	case ResultMiss:
		ev.Unconsumed = unconsumed
		ev.TotalPending = totalPending
		if ruleResp != nil {
			resp = ruleResp
			resp.Header.Set("X-Gospy-Replay", "miss")
		} else {
			missText := MissBody(req.Method, url)
			resp = &http.Response{
				StatusCode:    http.StatusNotFound,
				Header:        http.Header{"X-Gospy-Replay": {"miss"}},
				Body:          io.NopCloser(strings.NewReader(missText)),
				ContentLength: int64(len(missText)),
				Request:       req,
			}
		}
		LogReplayMiss(req.Method, url)
	default:
		if ruleResp != nil {
			resp = ruleResp
			resp.Header.Set("X-Gospy-Replay", "exhausted")
		} else {
			exhaustedText := ExhaustedBody()
			resp = &http.Response{
				StatusCode:    http.StatusGone,
				Header:        http.Header{"X-Gospy-Replay": {"exhausted"}},
				Body:          io.NopCloser(strings.NewReader(exhaustedText)),
				ContentLength: int64(len(exhaustedText)),
				Request:       req,
			}
		}
		LogReplayExhausted(req.Method, url)
	}
	ev.Status = resp.StatusCode

	var srvRaw []byte
	if ruleResp != nil {
		ev.ServedResponse, srvRaw = captureServedResponse(resp)
	}

	rs.emit(&ev, rawBody, srvRaw)
	return nil, resp
}

// buildRuleResponse returns the response a matched non-passthrough rule
// produces for the request, or nil when the rule is nil, passthrough, or uses
// an action replay does not honor. In replay there is no upstream server, so
// response_mock collapses into mock. Synthetic responses must frame from the
// ContentLength field (the client never sees the body EOF otherwise).
func buildRuleResponse(req *http.Request, rule *rules.Rule) *http.Response {
	if rule == nil || rule.Action == rules.ActionPassthrough {
		return nil
	}
	var resp *http.Response
	switch rule.Action {
	case rules.ActionMock, rules.ActionResponseMock:
		resp = rules.BuildMockResponse(req, rule.MockResp)
	case rules.ActionDrop:
		resp = rules.BuildDropResponse(req)
	default:
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	resp.ContentLength = int64(len(body))
	return resp
}

// emit records the event with its run progress and notifies listeners.
func (rs *ReplayServer) emit(ev *ReplayEvent, rawBody []byte, srvRaw []byte) {
	consumed, total, exhausted := rs.session.Progress(rs.cfg)
	ev.Consumed = consumed
	ev.Total = total
	ev.Exhausted = exhausted

	if rs.logRoot != "" {
		if log, err := rs.ensureLog(); err == nil {
			ev.RunID = log.RunID()
			if err := log.Append(ev, rawBody, srvRaw); err != nil {
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

// captureServedResponse mirrors captureRequest for a rule-served response: the
// raw body is always retained (written to the run bin by the log), and text
// bodies are decoded inline for display. The body is restored so the response
// still reaches the client unchanged.
func captureServedResponse(resp *http.Response) (*history.ResponseRecord, []byte) {
	if resp == nil {
		return nil, nil
	}
	rec := &history.ResponseRecord{
		Status:  resp.StatusCode,
		Headers: resp.Header.Clone(),
	}
	var raw []byte
	if resp.Body != nil {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, resp.Body); err == nil {
			data := buf.Bytes()
			ce := resp.Header.Get("Content-Encoding")
			ct := resp.Header.Get("Content-Type")
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
		resp.Body = io.NopCloser(&buf)
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
