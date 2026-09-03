package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gospy/internal/history"
	"gospy/internal/rules"

	"github.com/elazarl/goproxy"
	"github.com/google/uuid"
)

type requestUserData struct {
	mockResponse *rules.MockResponse
	entryID      string
}

type entryUserData struct {
	entryID string
}

type Interceptor struct {
	history        atomic.Pointer[history.Store]
	captureStopped atomic.Bool
	ignoreStore    *IgnoreStore
	engine         *rules.Engine
	skipPorts      map[string]bool
	resolver       *ClientResolver
	sigCache       *SignatureCache

	// StreamNotifier is invoked (throttled) as a live SSE response body grows,
	// and once more with done=true when it ends. Wired by main to the webui
	// stream hub so an open detail panel can render the body incrementally.
	StreamNotifier func(entryID string, size int64, done bool)

	streamCheckpointInterval time.Duration
	streamCheckpointBytes    int64
	streamNotifyInterval     time.Duration
}

func NewInterceptor(h *history.Store, ignore *IgnoreStore, engine *rules.Engine, skipPorts []string, resolver *ClientResolver, sigCache *SignatureCache) *Interceptor {
	skip := make(map[string]bool, len(skipPorts))
	for _, p := range skipPorts {
		skip[p] = true
	}
	ic := &Interceptor{
		ignoreStore:              ignore,
		engine:                   engine,
		skipPorts:                skip,
		resolver:                 resolver,
		sigCache:                 sigCache,
		streamCheckpointInterval: 2 * time.Second,
		streamCheckpointBytes:    64 * 1024,
		streamNotifyInterval:     250 * time.Millisecond,
	}
	ic.history.Store(h)
	return ic
}

// hist returns the current history store. The pointer is swapped atomically
// when a recording session rotates, so concurrent handlers always target the
// active store.
func (ic *Interceptor) hist() *history.Store {
	return ic.history.Load()
}

// SetHistoryStore rotates the capture target (used by session start). Each
// session starts with a fresh recording window, so any max-duration stop
// from a previous session is cleared.
func (ic *Interceptor) SetHistoryStore(h *history.Store) {
	ic.captureStopped.Store(false)
	ic.history.Store(h)
}

// SetCaptureStopped stops recording for the active session: every request
// after the stop is rejected with 410 + X-Gospy-Recording: stopped (the
// replay-style hard cut), so the client knows recording ended.
func (ic *Interceptor) SetCaptureStopped(on bool) {
	ic.captureStopped.Store(on)
}

// CaptureStore returns the store currently receiving captured traffic.
func (ic *Interceptor) CaptureStore() *history.Store {
	return ic.history.Load()
}

// capture writes an entry to the active session store. Entries whose requests
// passed the HandleRequest stop gate are always captured in full, even if the
// stop fires mid-flight. Returns whether the entry was saved.
func (ic *Interceptor) capture(entry *history.Entry) bool {
	h := ic.hist()
	if h == nil {
		return false
	}
	return h.Save(entry) == nil
}

func (ic *Interceptor) isSelfRequest(host string) bool {
	_, port, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	return ic.skipPorts[port]
}

func (ic *Interceptor) HandleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	// No store means no active session (record auto mode before the first
	// POST /api/session/start). The proxy rejects all traffic: nothing is
	// proxied and nothing is captured.
	if ic.hist() == nil {
		return req, noSessionResponse(req)
	}

	if ic.isSelfRequest(req.Host) {
		return req, nil
	}

	// Recording stopped (max-duration reached): hard cut, replay-style. The
	// session is sealed and every request is rejected so the client can tell
	// recording ended.
	if ic.captureStopped.Load() {
		return req, recordingStoppedResponse(req)
	}

	if ic.ignoreStore.IsIgnored(req.Host) {
		url := req.URL.Scheme + "://" + req.Host + req.URL.Path
		LogIgnored(req.Method, url)
		return req, nil
	}

	origin := ""
	agentCallID := ""
	if v := req.Header.Get("X-Gospy-Agent"); v != "" {
		req.Header.Del("X-Gospy-Agent")
		origin = "agent"
		agentCallID = v
	}

	var entryID string
	var sb *streamingBody
	if req.Body != nil {
		entryID = uuid.New().String()
		ct := req.Header.Get("Content-Type")
		ce := req.Header.Get("Content-Encoding")
		sb = newStreamingBody(ic, entryID, "req", req.Body, ct, ce)
		req.Body = sb
	}

	url := req.URL.Scheme + "://" + req.Host + req.URL.Path
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}

	originalRequest := history.RequestRecord{
		Method:  req.Method,
		URL:     url,
		Host:    req.Host,
		Headers: req.Header.Clone(),
	}

	var clientProcess, clientDisplayName, clientPath string
	var clientPID uint32
	if ic.resolver != nil {
		if info := ic.resolver.Resolve(req.RemoteAddr); info != nil {
			clientProcess = info.Name
			clientDisplayName = info.DisplayName
			clientPID = info.PID
			clientPath = info.Path
			if ic.sigCache != nil && info.Path != "" {
				ic.sigCache.VerifyAsync(info.Path)
			}
		}
	}

	rule := ic.engine.Match(req.Method, req.Host, url, req.Header)

	if rule == nil || rule.Action == rules.ActionPassthrough {
		entry := &history.Entry{
			ID:                entryID,
			Request:           originalRequest,
			ClientProcess:     clientProcess,
			ClientPID:         clientPID,
			ClientPath:        clientPath,
			ClientDisplayName: clientDisplayName,
			Origin:            origin,
			AgentCallID:       agentCallID,
		}
		ic.capture(entry)
		ctx.UserData = &entryUserData{entryID: entry.ID}
		LogRequest(entry.ID, req.Method, url)
		return req, nil
	}

	switch rule.Action {
	case rules.ActionDrop:
		entry := &history.Entry{
			ID:                entryID,
			Request:           originalRequest,
			AppliedAction:     string(rules.ActionDrop),
			RuleName:          rule.Name,
			ClientProcess:     clientProcess,
			ClientPID:         clientPID,
			ClientPath:        clientPath,
			ClientDisplayName: clientDisplayName,
			Origin:            origin,
			AgentCallID:       agentCallID,
		}
		ic.capture(entry)
		LogRequest(entry.ID, req.Method, url)
		LogInfo(fmt.Sprintf("DROPPED by rule %q: %s %s", rule.Name, req.Method, url))
		dropResp := rules.BuildDropResponse(req)
		entry.Response = &history.ResponseRecord{
			Status:  504,
			Headers: make(map[string][]string),
			Body:    "",
		}
		_ = ic.hist().Update(entry)
		return req, dropResp

	case rules.ActionMock:
		entry := &history.Entry{
			ID:                entryID,
			Request:           originalRequest,
			AppliedAction:     string(rules.ActionMock),
			RuleName:          rule.Name,
			ClientProcess:     clientProcess,
			ClientPID:         clientPID,
			ClientPath:        clientPath,
			ClientDisplayName: clientDisplayName,
			Origin:            origin,
			AgentCallID:       agentCallID,
		}
		ic.capture(entry)
		LogRequest(entry.ID, req.Method, url)
		LogInfo(fmt.Sprintf("MOCKED by rule %q: %s %s", rule.Name, req.Method, url))

		resp := rules.BuildMockResponse(req, rule.MockResp)
		entry.Response = &history.ResponseRecord{
			Status:  resp.StatusCode,
			Headers: resp.Header,
			Body:    ReadBodyString(resp.Body),
		}
		resp.Body = io.NopCloser(strings.NewReader(entry.Response.Body))
		_ = ic.hist().Update(entry)
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
			ID:      entryID,
			Request: originalRequest,
			ServerRequest: &history.RequestRecord{
				Method:  req.Method,
				URL:     modifiedURL,
				Host:    req.Host,
				Headers: req.Header.Clone(),
				Body:    modifiedBody,
			},
			AppliedAction:     string(rules.ActionModify),
			RuleName:          rule.Name,
			ClientProcess:     clientProcess,
			ClientPID:         clientPID,
			ClientPath:        clientPath,
			ClientDisplayName: clientDisplayName,
			Origin:            origin,
			AgentCallID:       agentCallID,
		}
		ic.capture(entry)
		ctx.UserData = &entryUserData{entryID: entry.ID}
		LogRequest(entry.ID, req.Method, url)
		LogInfo(fmt.Sprintf("MODIFIED by rule %q: %s %s", rule.Name, req.Method, url))
		return req, nil

	case rules.ActionResponseMock:
		entry := &history.Entry{
			ID:                entryID,
			Request:           originalRequest,
			AppliedAction:     string(rules.ActionResponseMock),
			RuleName:          rule.Name,
			ClientProcess:     clientProcess,
			ClientPID:         clientPID,
			ClientPath:        clientPath,
			ClientDisplayName: clientDisplayName,
			Origin:            origin,
			AgentCallID:       agentCallID,
		}
		ic.capture(entry)
		LogRequest(entry.ID, req.Method, url)
		LogInfo(fmt.Sprintf("RESPONSE MOCK by rule %q: %s %s", rule.Name, req.Method, url))
		ctx.UserData = &requestUserData{
			mockResponse: rule.MockResp,
			entryID:      entry.ID,
		}
		return req, nil
	}

	entry := &history.Entry{
		ID:            entryID,
		Request:       originalRequest,
		AppliedAction: string(rule.Action),
		RuleName:      rule.Name,
		Origin:        origin,
		AgentCallID:   agentCallID,
	}
	ic.capture(entry)
	ctx.UserData = &entryUserData{entryID: entry.ID}
	LogRequest(entry.ID, req.Method, url)
	return req, nil
}

func (ic *Interceptor) HandleResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	// Defensive: HandleRequest rejects everything pre-session, so no proxied
	// response can reach this point. Guard anyway against a session ending
	// mid-flight.
	if resp == nil || ctx.Req == nil || ic.hist() == nil {
		return resp
	}

	if resp.StatusCode == http.StatusSwitchingProtocols {
		return resp
	}

	reqURL := ctx.Req.URL.Scheme + "://" + ctx.Req.Host + ctx.Req.URL.Path
	if ctx.Req.URL.RawQuery != "" {
		reqURL += "?" + ctx.Req.URL.RawQuery
	}

	// Streaming responses (text/event-stream) never finish: buffering the
	// body would hold the client response open forever. Record status and
	// headers, then capture the stream incrementally to a body file so the
	// full response is kept without buffering it in memory. The entry
	// references the file before any bytes arrive, so a killed process never
	// loses captured data. A response-mock rule is bypassed for streaming.
	if isStreamingResponse(resp) {
		if ud, ok := ctx.UserData.(*entryUserData); ok {
			entryID := ud.entryID
			if entry, err := ic.hist().Get(entryID); err == nil {
				entry.Response = &history.ResponseRecord{
					Status:   resp.StatusCode,
					Headers:  resp.Header,
					BodyFile: entryID + "-stream.bin",
					Stream:   true,
				}
				_ = ic.hist().Update(entry)
				LogResponse(entry.ID, ctx.Req.Method, reqURL, resp.StatusCode, resp.Header.Get("Content-Type"))

				if resp.Body != nil {
					resp.Body = newStreamCapture(ic, entryID, resp.Body)
				}
			}
		}
		return resp
	}

	if ud, ok := ctx.UserData.(*requestUserData); ok {
		entry, err := ic.hist().Get(ud.entryID)
		if err == nil {
			sresp := &history.ResponseRecord{
				Status:  resp.StatusCode,
				Headers: resp.Header,
			}
			if resp.Body != nil {
				bodyData, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if len(bodyData) > 0 {
					ce := resp.Header.Get("Content-Encoding")
					ct := resp.Header.Get("Content-Type")
					if filename, saveErr := ic.hist().SaveBinaryBody(entry.ID, "sresp", bodyData); saveErr == nil {
						sresp.BodyFile = filename
						sresp.BodySize = int64(len(bodyData))
					}
					if !history.IsBinaryBody(bodyData, ce, ct) {
						sresp.RawBody = string(bodyData)
					} else {
						sresp.IsBinaryBody = true
					}
				}
			}
			entry.ServerResponse = sresp
			fakeResp := rules.BuildMockResponse(ctx.Req, ud.mockResponse)
			entry.Response = &history.ResponseRecord{
				Status:  fakeResp.StatusCode,
				Headers: fakeResp.Header,
				Body:    ReadBodyString(fakeResp.Body),
			}
			_ = ic.hist().Update(entry)
			LogResponse(entry.ID, ctx.Req.Method, reqURL, fakeResp.StatusCode, fakeResp.Header.Get("Content-Type"))
		}
		return rules.BuildMockResponse(ctx.Req, ud.mockResponse)
	}

	if ud, ok := ctx.UserData.(*entryUserData); ok {
		entry, err := ic.hist().Get(ud.entryID)
		if err == nil {
			entry.Response = &history.ResponseRecord{
				Status:  resp.StatusCode,
				Headers: resp.Header,
			}
			_ = ic.hist().Update(entry)
			LogResponse(entry.ID, ctx.Req.Method, reqURL, resp.StatusCode, resp.Header.Get("Content-Type"))

			if resp.Body != nil {
				ce := resp.Header.Get("Content-Encoding")
				ct := resp.Header.Get("Content-Type")
				resp.Body = newStreamingBody(ic, entry.ID, "resp", resp.Body, ct, ce)
			}
		}
		return resp
	}

	return resp
}

// HandleConnect decides how HTTPS CONNECT tunnels are handled. With no active
// session (record auto mode before the first session start) the tunnel is
// rejected with the same 503 + X-Gospy-Replay: nosession used for plain HTTP,
// so the client gets a clear response instead of a silent close. Otherwise the
// tunnel is MITM'd and recorded.
func (ic *Interceptor) HandleConnect(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
	if ic.hist() == nil {
		ctx.Resp = noSessionResponse(ctx.Req)
		return goproxy.RejectConnect, host
	}
	LogConnect(host)
	LogMITM(host)
	return goproxy.MitmConnect, host
}

// noSessionResponse builds the response for any request that reaches the proxy
// before a recording session exists (record auto mode). The X-Gospy-Replay
// header mirrors the replay contract with a distinct value so callers can tell
// "no session" apart from hit/miss/exhausted.
func noSessionResponse(req *http.Request) *http.Response {
	const body = "no session recording active\n"
	resp := &http.Response{
		StatusCode:    http.StatusServiceUnavailable,
		Status:        "503 Service Unavailable",
		Header:        make(http.Header),
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
		Request:       req,
	}
	resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp.Header.Set("X-Gospy-Replay", "nosession")
	return resp
}

// recordingStoppedResponse rejects any request after the record max-duration
// was reached. The X-Gospy-Recording header tells the client (e.g. gostream)
// that recording ended, mirroring the X-Gospy-Replay contract of replay mode.
func recordingStoppedResponse(req *http.Request) *http.Response {
	const body = "recording stopped (max duration)\n"
	resp := &http.Response{
		StatusCode:    http.StatusGone,
		Status:        "410 Gone",
		Header:        make(http.Header),
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
		Request:       req,
	}
	resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp.Header.Set("X-Gospy-Recording", "stopped")
	return resp
}

// isStreamingResponse reports whether the response is a server-sent event
// stream whose body never ends (unlike 101 upgrades, which are handled
// separately by the caller).
func isStreamingResponse(resp *http.Response) bool {
	return strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

// streamCaptureReader passes a live SSE response through to the client while
// appending every chunk to the entry's body file, so an unbounded stream is
// captured in full without buffering it in memory. It checkpoints the entry
// (BodySize/Stream + fsync) for durability and notifies the webui hub so an
// open detail panel can render the body incrementally as it arrives.
type streamCaptureReader struct {
	ic      *Interceptor
	entryID string
	r       io.Reader

	mu             sync.Mutex
	file           *os.File
	size           int64
	finalized      bool
	lastCheckpoint int64

	stopCh chan struct{}
	once   sync.Once
}

func newStreamCapture(ic *Interceptor, entryID string, r io.Reader) io.ReadCloser {
	sc := &streamCaptureReader{
		ic:      ic,
		entryID: entryID,
		r:       r,
		stopCh:  make(chan struct{}),
	}
	go sc.run()
	return sc
}

func (sc *streamCaptureReader) Read(p []byte) (int, error) {
	n, err := sc.r.Read(p)
	if n > 0 {
		sc.append(p[:n])
	}
	if err == io.EOF {
		sc.finish()
	}
	return n, err
}

func (sc *streamCaptureReader) Close() error {
	sc.finish()
	if rc, ok := sc.r.(io.Closer); ok {
		return rc.Close()
	}
	return nil
}

func (sc *streamCaptureReader) append(chunk []byte) {
	sc.mu.Lock()
	if sc.finalized {
		sc.mu.Unlock()
		return
	}
	if sc.file == nil {
		if _, f, err := sc.ic.hist().CreateStreamBody(sc.entryID); err == nil {
			sc.file = f
		}
	}
	if sc.file != nil {
		if _, err := sc.file.Write(chunk); err == nil {
			sc.size += int64(len(chunk))
		}
	}
	needCheckpoint := sc.ic.streamCheckpointBytes > 0 &&
		sc.size-sc.lastCheckpoint >= sc.ic.streamCheckpointBytes
	sc.mu.Unlock()

	if needCheckpoint {
		sc.checkpoint()
	}
}

func (sc *streamCaptureReader) run() {
	notifyTicker := time.NewTicker(sc.ic.streamNotifyInterval)
	defer notifyTicker.Stop()
	cpTicker := time.NewTicker(sc.ic.streamCheckpointInterval)
	defer cpTicker.Stop()
	for {
		select {
		case <-sc.stopCh:
			return
		case <-notifyTicker.C:
			sc.notify()
		case <-cpTicker.C:
			sc.checkpoint()
		}
	}
}

func (sc *streamCaptureReader) checkpoint() {
	sc.mu.Lock()
	if sc.finalized {
		sc.mu.Unlock()
		return
	}
	size := sc.size
	if size == sc.lastCheckpoint {
		sc.mu.Unlock()
		return
	}
	sc.lastCheckpoint = size
	sc.mu.Unlock()

	if entry, err := sc.ic.hist().Get(sc.entryID); err == nil {
		if entry.Response != nil {
			entry.Response.BodySize = size
			entry.Response.Stream = true
			_ = sc.ic.hist().Update(entry)
		}
	}
	sc.syncFile()
}

func (sc *streamCaptureReader) notify() {
	sc.mu.Lock()
	if sc.finalized {
		sc.mu.Unlock()
		return
	}
	size := sc.size
	sc.mu.Unlock()

	if sc.ic.StreamNotifier != nil && size > 0 {
		sc.ic.StreamNotifier(sc.entryID, size, false)
	}
}

func (sc *streamCaptureReader) finish() {
	sc.once.Do(func() {
		close(sc.stopCh)

		sc.mu.Lock()
		sc.finalized = true
		size := sc.size
		file := sc.file
		sc.file = nil
		sc.mu.Unlock()

		if file != nil {
			_ = file.Sync()
			_ = file.Close()
		}

		if entry, err := sc.ic.hist().Get(sc.entryID); err == nil {
			if entry.Response != nil {
				entry.Response.BodySize = size
				entry.Response.Stream = false
				_ = sc.ic.hist().Update(entry)
			}
		}

		if sc.ic.StreamNotifier != nil {
			sc.ic.StreamNotifier(sc.entryID, size, true)
		}
	})
}

func (sc *streamCaptureReader) syncFile() {
	sc.mu.Lock()
	file := sc.file
	sc.mu.Unlock()
	if file != nil {
		_ = file.Sync()
	}
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

// streamingBody wraps an io.ReadCloser to capture body bytes to an in-memory
// buffer while the consumer (goproxy) reads them. On Close, the captured data
// is written to a binary file and the entry is updated with body metadata.
// This avoids the full-body buffer-then-forward delay of the old approach.
type streamingBody struct {
	r               io.ReadCloser
	ic              *Interceptor
	entryID         string
	contentType     string
	contentEncoding string
	phase           string // "req" or "resp"

	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func newStreamingBody(ic *Interceptor, entryID, phase string, body io.ReadCloser, contentType, contentEncoding string) *streamingBody {
	return &streamingBody{
		r:               body,
		ic:              ic,
		entryID:         entryID,
		contentType:     contentType,
		contentEncoding: contentEncoding,
		phase:           phase,
	}
}

func (sb *streamingBody) Read(p []byte) (int, error) {
	n, err := sb.r.Read(p)
	if n > 0 {
		sb.mu.Lock()
		sb.buf.Write(p[:n])
		sb.mu.Unlock()
	}
	return n, err
}

func (sb *streamingBody) Close() error {
	sb.mu.Lock()
	if sb.closed {
		sb.mu.Unlock()
		return sb.r.Close()
	}
	sb.closed = true
	data := make([]byte, sb.buf.Len())
	copy(data, sb.buf.Bytes())
	sb.mu.Unlock()

	if err := sb.r.Close(); err != nil {
		return err
	}

	if len(data) == 0 || sb.entryID == "" {
		return nil
	}

	entry, err := sb.ic.hist().Get(sb.entryID)
	if err != nil {
		return nil
	}

	var bodyFile string
	var bodySize int64
	if filename, saveErr := sb.ic.hist().SaveBinaryBody(sb.entryID, sb.phase, data); saveErr == nil {
		bodyFile = filename
		bodySize = int64(len(data))
	}

	isBinary := history.IsBinaryBody(data, sb.contentEncoding, sb.contentType)
	var body, rawBody, compression string
	if !isBinary {
		result := history.DecompressBody(data, sb.contentEncoding)
		body = result.Decoded
		rawBody = result.Raw
		compression = result.Compression
	}

	switch sb.phase {
	case "req":
		entry.Request.BodyFile = bodyFile
		entry.Request.BodySize = bodySize
		entry.Request.IsBinaryBody = isBinary
		entry.Request.Body = body
		entry.Request.RawBody = rawBody
		entry.Request.Compression = compression
	case "resp":
		if entry.Response != nil {
			entry.Response.BodyFile = bodyFile
			entry.Response.BodySize = bodySize
			entry.Response.IsBinaryBody = isBinary
			entry.Response.Body = body
			entry.Response.RawBody = rawBody
			entry.Response.Compression = compression
		}
	}

	_ = sb.ic.hist().Update(entry)
	return nil
}
