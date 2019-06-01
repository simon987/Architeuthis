package main

import (
	"github.com/elazarl/goproxy"
	"github.com/pkg/errors"
	"github.com/ryanuber/go-glob"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Balancer struct {
	server  *goproxy.ProxyHttpServer
	proxies []*Proxy
}

type ExpiringLimiter struct {
	Limiter  *rate.Limiter
	LastRead time.Time
}

type Proxy struct {
	Name        string
	Url         *url.URL
	Limiters    map[string]*ExpiringLimiter
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

func (p *Proxy) getLimiter(host string) *rate.Limiter {

	for hostGlob, limiter := range p.Limiters {
		if glob.Glob(hostGlob, host) {
			limiter.LastRead = time.Now()
			return limiter.Limiter
		}
	}

	newExpiringLimiter := p.makeNewLimiter(host)
	return newExpiringLimiter.Limiter
}

func (p *Proxy) makeNewLimiter(host string) *ExpiringLimiter {

	newExpiringLimiter := &ExpiringLimiter{
		LastRead: time.Now(),
		Limiter:  rate.NewLimiter(rate.Every(config.DefaultConfig.Every), config.DefaultConfig.Burst),
	}

	p.Limiters[host] = newExpiringLimiter

	logrus.WithFields(logrus.Fields{
		"host": host,
	}).Trace("New limiter")

	return newExpiringLimiter
}

func simplifyHost(host string) string {

	col := strings.LastIndex(host, ":")
	if col > 0 {
		host = host[:col]
	}

	return "." + host
}

func (b *Balancer) chooseProxy() *Proxy {

	if len(b.proxies) == 0 {
		return b.proxies[0]
	}

	sort.Sort(ByConnectionCount(b.proxies))

	p0 := b.proxies[0]
	proxiesWithSameConnCount := make([]*Proxy, 0)
	for _, p := range b.proxies {
		if p.Connections != p0.Connections {
			break
		}
		proxiesWithSameConnCount = append(proxiesWithSameConnCount, p)
	}

	if len(proxiesWithSameConnCount) > 1 {
		return proxiesWithSameConnCount[rand.Intn(len(proxiesWithSameConnCount))]
	} else {
		return p0
	}
}

func New() *Balancer {

	balancer := new(Balancer)

	balancer.server = goproxy.NewProxyHttpServer()

	balancer.server.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	balancer.server.OnRequest().DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

			p := balancer.chooseProxy()

			logrus.WithFields(logrus.Fields{
				"proxy": p.Name,
				"conns": p.Connections,
				"url":   r.URL,
			}).Trace("Routing request")

			resp, err := p.processRequest(r)

			if err != nil {
				logrus.WithError(err).Trace("Could not complete request")
				return nil, goproxy.NewResponse(r, "text/plain", 500, err.Error())
			}

			return nil, resp
		})
	return balancer
}

func applyHeaders(r *http.Request) *http.Request {

	sHost := simplifyHost(r.Host)

	for _, conf := range config.Hosts {
		if glob.Glob(conf.Host, sHost) {
			for k, v := range conf.Headers {
				r.Header.Set(k, v)
			}
		}
	}

	return r
}

func (p *Proxy) processRequest(r *http.Request) (*http.Response, error) {

	p.Connections += 1
	defer func() {
		p.Connections -= 1
	}()
	retries := 0

	p.waitRateLimit(r)
	proxyReq := applyHeaders(cloneRequest(r))

	for {

		if retries >= config.Retries {
			return nil, errors.Errorf("giving up after %d retries", config.Retries)
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

	//b.Verbose = true
	logrus.WithFields(logrus.Fields{
		"addr": config.Addr,
	}).Info("Listening")

	err := http.ListenAndServe(config.Addr, b.server)
	logrus.Fatal(err)
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
	if parsedUrl == nil {
		httpClient = &http.Client{}
	} else {
		httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(parsedUrl),
			},
		}
	}

	httpClient.Timeout = config.Timeout

	return &Proxy{
		Name:       name,
		Url:        parsedUrl,
		HttpClient: httpClient,
		Limiters:   make(map[string]*ExpiringLimiter),
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

	balancer.setupGarbageCollector()
	balancer.Run()
}
