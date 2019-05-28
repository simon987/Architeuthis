package main

import (
	"bytes"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
	"sync"
)

type WebProxy struct {
	server         *fasthttp.Server
	Limiters sync.Map
}

var proxyClient = &fasthttp.Client{
}


func LogRequestMiddleware(h fasthttp.RequestHandler) fasthttp.RequestHandler {
	return fasthttp.RequestHandler(func(ctx *fasthttp.RequestCtx) {

		logrus.WithFields(logrus.Fields{
			"path":   string(ctx.Path()),
			"header": ctx.Request.Header.String(),
		}).Trace(string(ctx.Method()))

		h(ctx)
	})
}

func Index(ctx *fasthttp.RequestCtx) {

	if bytes.Equal([]byte("localhost:5050"), ctx.Host())  {
		logrus.Warn("Ignoring same host request")
		_, _ = fmt.Fprintf(ctx, "Ignoring same host request")
		ctx.Response.Header.SetStatusCode(400)
		return
	}

	req := &ctx.Request
	resp := &ctx.Response

	prepareRequest(req)

	if err := proxyClient.Do(req, resp); err != nil {
		logrus.WithError(err).Error("error when proxying request")
	}

	postprocessResponse(resp)
}

func prepareRequest(req *fasthttp.Request) {
	// do not proxy "Connection" header.
	req.Header.Del("Connection")

	// strip other unneeded headers.

	// alter other request params before sending them to upstream host
}

func postprocessResponse(resp *fasthttp.Response) {
	// do not proxy "Connection" header
	resp.Header.Del("Connection")
	// strip other unneeded headers

	// alter other response data if needed
}


func New() *WebProxy {

	proxy := new(WebProxy)

	proxy.server = &fasthttp.Server{
		Handler: LogRequestMiddleware(Index),

	}

	return proxy
}

func (proxy *WebProxy) Run() {

	logrus.Infof("Started web proxy at address %s", "localhost:5050")

	err := proxy.server.ListenAndServe("localhost:5050")
	if err != nil {
		logrus.Fatalf("Error in ListenAndServe: %s", err)
	}
}

func main() {

	logrus.SetLevel(logrus.TraceLevel)

	p := New()
	p.Run()
}