package proxy

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strings"

	"gospy/internal/ca"
	"gospy/internal/history"
	"gospy/internal/logging"
	"gospy/internal/netbind"
	"gospy/internal/rules"

	"github.com/elazarl/goproxy"
)

type Server struct {
	proxy       *goproxy.ProxyHttpServer
	interceptor *Interceptor
	ca          *ca.CA
	addr        string
	resolver    *ClientResolver
	sigCache    *SignatureCache
}

func NewServer(addr string, uiAddr string, caCert *ca.CA, hist *history.Store, ruleEngine *rules.Engine, ignoreStore *IgnoreStore, dataDir string, bindIface string, dns string) (*Server, error) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false

	if bindIface != "" || dns != "" {
		d, err := netbind.BuildDialer(bindIface, dns, logging.Log)
		if err != nil {
			return nil, err
		}
		if d != nil {
			// Mirror goproxy's default outbound transport (v1.8.4:
			// TLSClientConfig=skipVerify, Proxy=ProxyFromEnvironment) with the
			// bound DialContext swapped in. Constructed explicitly instead of
			// copying http.Transport (contains a sync.Mutex; vet-clean).
			proxy.Tr = &http.Transport{
				TLSClientConfig: proxy.Tr.TLSClientConfig,
				Proxy:           proxy.Tr.Proxy,
				DialContext:     d.DialContext,
			}
		}
	}

	caTLSCert := caCert.TLSCert()
	goproxy.MitmConnect = &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		TLSConfig: goproxy.TLSConfigFromCA(&caTLSCert),
	}

	proxy.CertStore = ca.NewCertStorage(caCert)

	var skipPorts []string
	for _, a := range []string{addr, uiAddr} {
		if _, port, err := net.SplitHostPort(a); err == nil {
			skipPorts = append(skipPorts, port)
		}
	}

	resolver := NewClientResolver(addr)
	sigCache := NewSignatureCache(dataDir)

	interceptor := NewInterceptor(hist, ignoreStore, ruleEngine, skipPorts, resolver, sigCache)

	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	proxy.OnRequest().DoFunc(interceptor.HandleRequest)

	proxy.OnResponse().DoFunc(interceptor.HandleResponse)

	return &Server{
		proxy:       proxy,
		interceptor: interceptor,
		ca:          caCert,
		addr:        addr,
		resolver:    resolver,
		sigCache:    sigCache,
	}, nil
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.addr, flushStreamingHeaders(s.proxy))
}

// SetStreamNotifier wires the interceptor's stream-growth callback (used to
// push live SSE response bodies to the webui). Nil-safe: without it the
// capture still checkpoints entries, just no live UI updates are pushed.
func (s *Server) SetStreamNotifier(fn func(entryID string, size int64, done bool)) {
	s.interceptor.StreamNotifier = fn
}

// streamingResponseWriter flushes the response headers immediately after
// WriteHeader for streaming responses. Without the explicit Flush, goproxy's
// headers sit in the connection buffer until the first body chunk is written
// or the handler returns - which never happens for a server-sent event stream
// waiting for events before emitting any data, so the client hangs without
// ever receiving the status line.
type streamingResponseWriter struct {
	http.ResponseWriter
}

func (w streamingResponseWriter) WriteHeader(code int) {
	ct := strings.ToLower(w.Header().Get("Content-Type"))
	te := strings.ToLower(w.Header().Get("Transfer-Encoding"))
	w.ResponseWriter.WriteHeader(code)
	if strings.HasPrefix(ct, "text/event-stream") || strings.Contains(te, "chunked") {
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func (w streamingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w streamingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijacking not supported")
}

func (w streamingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func flushStreamingHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(streamingResponseWriter{ResponseWriter: w}, r)
	})
}

func (s *Server) Proxy() *goproxy.ProxyHttpServer {
	return s.proxy
}

func (s *Server) Resolver() *ClientResolver {
	return s.resolver
}

func (s *Server) SigCache() *SignatureCache {
	return s.sigCache
}
