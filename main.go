package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Balancer struct {
	server  *goproxy.ProxyHttpServer
	proxies []*Proxy
}

type Proxy struct {
	Name        string
	Url         *url.URL
	Limiters    sync.Map
	HttpClient  *http.Client
	Connections int
}

type ByConnectionCount []*Proxy

func (a ByConnectionCount) Len() int {
	return len(a)
}

func (a ByConnectionCount) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ByConnectionCount) Less(i, j int) bool {
	return a[i].Connections < a[j].Connections
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

		every, _ := time.ParseDuration("1ms")
		limiter = rate.NewLimiter(rate.Every(every), 1)
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

	sort.Sort(ByConnectionCount(b.proxies))

	return b.proxies[0]
}

func New() *Balancer {

	balancer := new(Balancer)

	balancer.server = goproxy.NewProxyHttpServer()

	balancer.server.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	balancer.server.OnRequest().Do(LogRequestMiddleware(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

			sHost := simplifyHost(r.Host)
			p := balancer.chooseProxy(sHost)

			p.Connections += 1
			logrus.WithFields(logrus.Fields{
				"proxy":      p.Name,
				"connexions": p.Connections,
			}).Trace("Routing request")

			limiter := p.getLimiter(sHost)
			reservation := limiter.Reserve()
			if !reservation.OK() {
				logrus.Warn("Could not reserve")
			}
			delay := reservation.Delay()
			if delay > 0 {
				logrus.WithFields(logrus.Fields{
					"time": delay,
				}).Trace("Sleeping")
				time.Sleep(delay)
			}

			proxyReq := preprocessRequest(cloneRequest(r))
			resp, err := p.HttpClient.Do(proxyReq)
			p.Connections -= 1

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

	loadConfig()
	balancer := New()

	for _, proxyConf := range config.Proxies {
		proxy, err := NewProxy(proxyConf.Name, proxyConf.Url)
		handleErr(err)
		balancer.proxies = append(balancer.proxies, proxy)

		applyConfig(proxy)

		logrus.WithFields(logrus.Fields{
			"name": proxy.Name,
			"url":  proxy.Url,
		}).Info("Proxy")
	}

	balancer.Run()
}
