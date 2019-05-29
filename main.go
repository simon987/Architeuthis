package main

import (
	"flag"
	"github.com/elazarl/goproxy"
	"github.com/pkg/errors"
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

func (b *Balancer) chooseProxy() *Proxy {

	sort.Sort(ByConnectionCount(b.proxies))
	return b.proxies[0]
}

func New() *Balancer {

	balancer := new(Balancer)

	balancer.server = goproxy.NewProxyHttpServer()

	balancer.server.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	balancer.server.OnRequest().Do(LogRequestMiddleware(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

			p := balancer.chooseProxy()

			logrus.WithFields(logrus.Fields{
				"proxy":      p.Name,
				"connexions": p.Connections,
			}).Trace("Routing request")

			resp, err := p.processRequest(r)

			if err != nil {
				logrus.WithError(err).Trace("Could not complete request")
				return nil, goproxy.NewResponse(r, "text/plain", 500, err.Error())
			}

			return nil, resp
		}))
	return balancer
}

func (p *Proxy) processRequest(r *http.Request) (*http.Response, error) {

	p.Connections += 1
	defer func() {
		p.Connections -= 1
	}()
	retries := 1
	const maxRetries = 5

	p.waitRateLimit(r)
	proxyReq := preprocessRequest(cloneRequest(r))

	for {

		if retries > maxRetries {
			return nil, errors.Errorf("giving up after %d retries", maxRetries)
		}

		resp, err := p.HttpClient.Do(proxyReq)

		if err != nil {
			if isPermanentError(err) {
				return nil, err
			}

			wait := waitTime(retries)

			logrus.WithError(err).WithFields(logrus.Fields{
				"wait": wait,
			}).Trace("Temporary error during request")
			time.Sleep(wait)

			retries += 1
			continue
		}

		if isHttpSuccessCode(resp.StatusCode) {

			return resp, nil
		} else if shouldRetryHttpCode(resp.StatusCode) {

			wait := waitTime(retries)

			logrus.WithFields(logrus.Fields{
				"wait":   wait,
				"status": resp.StatusCode,
			}).Trace("HTTP error during request")

			time.Sleep(wait)
			retries += 1
			continue
		} else {
			return nil, errors.Errorf("HTTP error: %d", resp.StatusCode)
		}
	}
}

func (b *Balancer) Run() {

	addr := flag.String("addr", "localhost:5050", "listen address")
	flag.Parse()

	//b.Verbose = true
	logrus.WithFields(logrus.Fields{
		"addr": *addr,
	}).Info("Listening")

	err := http.ListenAndServe(*addr, b.server)
	logrus.Fatal(err)
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
