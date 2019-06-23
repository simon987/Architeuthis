package main

import (
	"fmt"
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
	"sync"
	"sync/atomic"
	"time"
)

type Balancer struct {
	server     *goproxy.ProxyHttpServer
	proxies    []*Proxy
	proxyMutex *sync.RWMutex
}

type ExpiringLimiter struct {
	HostGlob  string
	IsGlob    bool
	CanDelete bool
	Limiter   *rate.Limiter
	LastRead  time.Time
}

type Proxy struct {
	Name        string
	Url         *url.URL
	Limiters    []*ExpiringLimiter
	HttpClient  *http.Client
	Connections *int32
}

type RequestCtx struct {
	RequestTime time.Time
	Response    *http.Response
}

type ByConnectionCount []*Proxy

func (a ByConnectionCount) Len() int {
	return len(a)
}

func (a ByConnectionCount) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ByConnectionCount) Less(i, j int) bool {
	return *a[i].Connections < *a[j].Connections
}

func (p *Proxy) getLimiter(host string) *rate.Limiter {

	for _, limiter := range p.Limiters {
		if limiter.IsGlob {
			if glob.Glob(limiter.HostGlob, host) {
				limiter.LastRead = time.Now()
				return limiter.Limiter
			}
		} else if limiter.HostGlob == host {
			limiter.LastRead = time.Now()
			return limiter.Limiter
		}
	}

	newExpiringLimiter := p.makeNewLimiter(host)
	return newExpiringLimiter.Limiter
}

func (p *Proxy) makeNewLimiter(host string) *ExpiringLimiter {

	newExpiringLimiter := &ExpiringLimiter{
		CanDelete: false,
		HostGlob:  host,
		IsGlob:    false,
		LastRead:  time.Now(),
		Limiter:   rate.NewLimiter(rate.Every(config.DefaultConfig.Every), config.DefaultConfig.Burst),
	}

	p.Limiters = append([]*ExpiringLimiter{newExpiringLimiter}, p.Limiters...)

	logrus.WithFields(logrus.Fields{
		"host":  host,
		"every": config.DefaultConfig.Every,
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

	proxyWithLeastConns := b.proxies[0]
	proxiesWithSameConnCount := b.getProxiesWithSameConnCountAs(proxyWithLeastConns)

	if len(proxiesWithSameConnCount) > 1 {
		return proxiesWithSameConnCount[rand.Intn(len(proxiesWithSameConnCount))]
	} else {
		return proxyWithLeastConns
	}
}

func (b *Balancer) getProxiesWithSameConnCountAs(p0 *Proxy) []*Proxy {

	proxiesWithSameConnCount := make([]*Proxy, 0)
	for _, p := range b.proxies {
		if p.Connections != p0.Connections {
			break
		}
		proxiesWithSameConnCount = append(proxiesWithSameConnCount, p)
	}
	return proxiesWithSameConnCount
}

func New() *Balancer {

	balancer := new(Balancer)

	balancer.proxyMutex = &sync.RWMutex{}
	balancer.server = goproxy.NewProxyHttpServer()

	balancer.server.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	balancer.server.OnRequest().DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

			balancer.proxyMutex.RLock()
			p := balancer.chooseProxy()

			logrus.WithFields(logrus.Fields{
				"proxy": p.Name,
				"conns": *p.Connections,
				"url":   r.URL,
			}).Trace("Routing request")

			resp, err := p.processRequest(r)
			balancer.proxyMutex.RUnlock()

			if err != nil {
				logrus.WithError(err).Trace("Could not complete request")
				return nil, goproxy.NewResponse(r, "text/plain", 500, err.Error())
			}

			return nil, resp
		})

	balancer.server.NonproxyHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if r.URL.Path == "/reload" {
			balancer.reloadConfig()
			_, _ = fmt.Fprint(w, "Reloaded\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, "{\"name\":\"Architeuthis\",\"version\":1.0}")
		}
	})
	return balancer
}

func getConfsMatchingRequest(r *http.Request) []*HostConfig {

	sHost := simplifyHost(r.Host)

	configs := make([]*HostConfig, 0)

	for _, conf := range config.Hosts {
		if glob.Glob(conf.Host, sHost) {
			configs = append(configs, conf)
		}
	}

	return configs
}

func applyHeaders(r *http.Request, configs []*HostConfig) *http.Request {

	for _, conf := range configs {
		for k, v := range conf.Headers {
			r.Header.Set(k, v)
		}
	}

	return r
}

func computeRules(ctx *RequestCtx, configs []*HostConfig) (dontRetry, forceRetry bool,
	limitMultiplier, newLimit float64, shouldRetry bool) {
	dontRetry = false
	forceRetry = false
	shouldRetry = false
	limitMultiplier = 1

	for _, conf := range configs {
		for _, rule := range conf.Rules {
			if rule.Matches(ctx) {
				switch rule.Action {
				case DontRetry:
					dontRetry = true
				case MultiplyEvery:
					limitMultiplier = rule.Arg
				case SetEvery:
					newLimit = rule.Arg
				case ForceRetry:
					forceRetry = true
				case ShouldRetry:
					shouldRetry = true
				}
			}
		}
	}

	return
}

func (p *Proxy) processRequest(r *http.Request) (*http.Response, error) {

	atomic.AddInt32(p.Connections, 1)
	defer func() {
		atomic.AddInt32(p.Connections, -1)
	}()
	retries := 0
	additionalRetries := 0

	configs := getConfsMatchingRequest(r)
	sHost := simplifyHost(r.Host)
	limiter := p.getLimiter(sHost)

	proxyReq := applyHeaders(cloneRequest(r), configs)

	for {
		p.waitRateLimit(limiter)

		if retries >= config.Retries+additionalRetries || retries > config.RetriesHard {
			return nil, errors.Errorf("giving up after %d retries", retries)
		}

		ctx := &RequestCtx{
			RequestTime: time.Now(),
		}
		var err error
		ctx.Response, err = p.HttpClient.Do(proxyReq)

		if err != nil {
			if isPermanentError(err) {
				return nil, err
			}

			dontRetry, forceRetry, limitMultiplier, newLimit, _ := computeRules(ctx, configs)
			if forceRetry {
				additionalRetries += 1
			} else if dontRetry {
				return nil, errors.Errorf("Applied dont_retry rule for (%s)", err)
			}
			p.applyLimiterRules(newLimit, limiter, limitMultiplier)

			wait := waitTime(retries)
			logrus.WithError(err).WithFields(logrus.Fields{
				"wait": wait,
			}).Trace("Temporary error during request")
			time.Sleep(wait)

			retries += 1
			continue
		}

		// Compute rules
		dontRetry, forceRetry, limitMultiplier, newLimit, shouldRetry := computeRules(ctx, configs)

		if forceRetry {
			additionalRetries += 1
		} else if dontRetry {
			return nil, errors.Errorf("Applied dont_retry rule")
		}
		p.applyLimiterRules(newLimit, limiter, limitMultiplier)

		if isHttpSuccessCode(ctx.Response.StatusCode) {
			return ctx.Response, nil

		} else if forceRetry || shouldRetry || shouldRetryHttpCode(ctx.Response.StatusCode) {

			wait := waitTime(retries)

			logrus.WithFields(logrus.Fields{
				"wait":   wait,
				"status": ctx.Response.StatusCode,
			}).Trace("HTTP error during request")

			time.Sleep(wait)
			retries += 1
			continue
		} else {
			return nil, errors.Errorf("HTTP error: %d", ctx.Response.StatusCode)
		}
	}
}

func (p *Proxy) applyLimiterRules(newLimit float64, limiter *rate.Limiter, limitMultiplier float64) {
	if newLimit != 0 {
		limiter.SetLimit(rate.Limit(newLimit))
	} else if limitMultiplier != 1 {
		limiter.SetLimit(limiter.Limit() * rate.Limit(1/limitMultiplier))
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

	p := &Proxy{
		Name:       name,
		Url:        parsedUrl,
		HttpClient: httpClient,
	}

	conns := int32(0)
	p.Connections = &conns
	return p, nil
}

func main() {
	logrus.SetLevel(logrus.TraceLevel)

	balancer := New()
	balancer.reloadConfig()

	balancer.setupGarbageCollector()
	balancer.Run()
}
