package proxy

import (
	"net"
	"net/http"

	"gospy/internal/ca"
	"gospy/internal/history"
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

func NewServer(addr string, uiAddr string, caCert *ca.CA, hist *history.Store, ruleEngine *rules.Engine, ignoreStore *IgnoreStore, dataDir string) *Server {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false

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
	}
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.addr, s.proxy)
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
