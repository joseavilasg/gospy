package proxy

import (
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
}

func NewServer(addr string, caCert *ca.CA, hist *history.Store, ruleEngine *rules.Engine) *Server {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false

	caTLSCert := caCert.TLSCert()
	goproxy.MitmConnect = &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		TLSConfig: goproxy.TLSConfigFromCA(&caTLSCert),
	}

	proxy.CertStore = ca.NewCertStorage(caCert)

	interceptor := NewInterceptor(hist)

	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	proxy.OnRequest().DoFunc(interceptor.HandleRequest)

	proxy.OnResponse().DoFunc(interceptor.HandleResponse)

	_ = ruleEngine

	return &Server{
		proxy:       proxy,
		interceptor: interceptor,
		ca:          caCert,
		addr:        addr,
	}
}

func (s *Server) ListenAndServe() error {
	return http.ListenAndServe(s.addr, s.proxy)
}

func (s *Server) Proxy() *goproxy.ProxyHttpServer {
	return s.proxy
}
