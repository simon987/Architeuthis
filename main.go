package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Balancer struct {
	server  *goproxy.ProxyHttpServer
	proxies []*Proxy
}

type Proxy struct {
	Name       string
	Url        *url.URL
	Limiters   sync.Map
	HttpClient *http.Client
}

func LogRequestMiddleware(h goproxy.FuncReqHandler) goproxy.ReqHandler {
	return goproxy.FuncReqHandler(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

		logrus.WithFields(logrus.Fields{
			"host": r.Host,
		}).Trace(strings.ToUpper(r.URL.Scheme) + " " + r.Method)

		return h(r, ctx)
	})
}

//TODO: expiration ?
func (p *Proxy) getLimiter(host string) *rate.Limiter {

	limiter, ok := p.Limiters.Load(host)
	if !ok {

		every, _ := time.ParseDuration("100ms")
		limiter = rate.NewLimiter(rate.Every(every), 0)
		p.Limiters.Store(host, limiter)

		logrus.WithFields(logrus.Fields{
			"host": host,
		}).Trace("New limiter")
	}

	return limiter.(*rate.Limiter)
}

func simplifyHost(host string) string {
	if strings.HasPrefix(host, "www.") {
		return host[4:]
	}

	return host
}

func (b *Balancer) chooseProxy(host string) *Proxy {

	_ = simplifyHost(host)

	return b.proxies[0]
}

func New() *Balancer {

	balancer := new(Balancer)

	balancer.server = goproxy.NewProxyHttpServer()

	balancer.server.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	balancer.server.OnRequest().Do(LogRequestMiddleware(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

			p := balancer.chooseProxy(r.Host)

			logrus.WithFields(logrus.Fields{
				"proxy": p.Name,
			}).Trace("Routing request")

			proxyReq := preprocessRequest(cloneRequest(r))
			resp, err := p.HttpClient.Do(proxyReq)

			//TODO: handle err
			if err != nil {
				panic(err)
			}

			return nil, resp
		}))
	return balancer
}

func (b *Balancer) Run() {

	addr := flag.String("addr", "localhost:5050", "listen address")
	flag.Parse()

	//b.Verbose = true
	logrus.WithFields(logrus.Fields{
		"addr": *addr,
	}).Info("Listening")

	go logrus.Fatal(http.ListenAndServe(*addr, b.server))
}

func preprocessRequest(r *http.Request) *http.Request {
	return r
}

func cloneRequest(r *http.Request) *http.Request {

	proxyReq := &http.Request{
		Method:     r.Method,
		URL:        r.URL,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     r.Header,
		Body:       r.Body,
		Host:       r.URL.Host,
	}

	return proxyReq
}

func NewProxy(name, stringUrl string) (*Proxy, error) {

	var parsedUrl *url.URL
	var err error
	if stringUrl != "" {
		parsedUrl, err = url.Parse(stringUrl)
		if err != nil {
			return nil, err
		}
	} else {
		parsedUrl = nil
	}

	var httpClient *http.Client
	//TODO: setup extra headers & qargs here
	if parsedUrl == nil {
		httpClient = &http.Client{}
	} else {
		httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(parsedUrl),
			},
		}
	}

	return &Proxy{
		Name:       name,
		Url:        parsedUrl,
		HttpClient: httpClient,
	}, nil
}

func main() {
	logrus.SetLevel(logrus.TraceLevel)

	balancer := New()

	p0, _ := NewProxy("p0", "http://localhost:3128")
	//p0, _ := NewProxy("p0", "")

	balancer.proxies = []*Proxy{p0}

	balancer.Run()
}
