package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"github.com/sirupsen/logrus"
	"net/http"
	"regexp"
	"sync"
)

type WebProxy struct {
	server   *goproxy.ProxyHttpServer
	Limiters sync.Map
}

func LogRequestMiddleware(h goproxy.FuncReqHandler) goproxy.ReqHandler {
	return goproxy.FuncReqHandler(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

		logrus.WithFields(logrus.Fields{
			"host": string(r.Host),
		}).Trace(r.Method)

		return h(r, ctx)
	})
}

func New() *WebProxy {

	proxy := new(WebProxy)

	proxy.server = goproxy.NewProxyHttpServer()

	proxy.server.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile("^.*$"))).
		HandleConnect(goproxy.AlwaysMitm)

	proxy.server.OnRequest().Do(
		LogRequestMiddleware(
			func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

				logrus.Warn("TEST")
				return r, nil
			},
		),
	)

	return proxy
}

func (proxy *WebProxy) Run() {

	logrus.Infof("Started web proxy at address %s", "localhost:5050")

	addr := flag.String("addr", ":5050", "proxy listen address")
	flag.Parse()
	//proxy.Verbose = true

	go logrus.Fatal(http.ListenAndServe(*addr, proxy.server))
}

func main() {
	logrus.SetLevel(logrus.TraceLevel)

	proxy := New()
	proxy.Run()
}
