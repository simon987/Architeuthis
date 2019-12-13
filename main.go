package main

import (
	"fmt"
	"github.com/elazarl/goproxy"
	"github.com/go-redis/redis"
	influx "github.com/influxdata/influxdb1-client/v2"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"html/template"
	"net/http"
	"strings"
	"time"
)

func New() *Architeuthis {

	a := new(Architeuthis)

	a.redis = redis.NewClient(&redis.Options{
		Addr:     config.RedisUrl,
		Password: "",
		DB:       0,
	})

	a.setupProxyReviver()

	var err error
	const InfluxDBUrl = "http://localhost:8086"
	a.influxdb, err = influx.NewHTTPClient(influx.HTTPConfig{
		Addr: InfluxDBUrl,
	})

	_, err = http.Post(InfluxDBUrl+"/query", "application/x-www-form-urlencoded", strings.NewReader("q=CREATE DATABASE \"architeuthis\""))
	if err != nil {
		panic(err)
	}

	a.points = make(chan *influx.Point, InfluxDbBufferSize)

	go a.asyncWriter(a.points)

	a.server = goproxy.NewProxyHttpServer()
	a.server.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	a.server.OnRequest().DoFunc(
		func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {

			resp, err := a.processRequest(r)

			if err != nil {
				logrus.WithError(err).Trace("Could not complete request")
				return nil, goproxy.NewResponse(r, "text/plain", http.StatusInternalServerError, err.Error())
			}

			return nil, resp
		})

	mux := http.NewServeMux()
	a.server.NonproxyHandler = mux

	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		a.reloadConfig()
		_, _ = fmt.Fprint(w, "Reloaded\n")
	})

	templ, _ := template.ParseFiles("templates/stats.html")

	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		err = templ.Execute(w, a.getStats())
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, "{\"name\":\"Architeuthis\",\"version\":2.0}")
	})

	mux.HandleFunc("/add_proxy", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		url := r.URL.Query().Get("url")

		if name == "" || url == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		err := a.AddProxy(name, url)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"name": name,
				"url":  url,
			}).Error("Could not add proxy")

			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	return a
}

func (a *Architeuthis) processRequest(r *http.Request) (*http.Response, error) {

	sHost := normalizeHost(r.Host)
	configs := getConfigsMatchingHost(sHost)

	options := parseOptions(&r.Header)
	proxyReq := applyHeaders(cloneRequest(r), configs)

	requestCtx := RequestCtx{
		Request:     proxyReq,
		Retries:     0,
		RequestTime: time.Now(),
		options:     options,
		configs:     configs,
	}

	for {
		responseCtx := a.processRequestWithCtx(&requestCtx)

		a.writeMetricRequest(responseCtx)

		if requestCtx.p != nil {
			a.UpdateProxy(requestCtx.p)
		}

		if responseCtx.Error == nil {
			return responseCtx.Response, nil
		}

		if !responseCtx.ShouldRetry {
			return nil, responseCtx.Error
		}
	}
}

func (lim *RedisLimiter) waitRateLimit() (time.Duration, error) {
	result, err := lim.Limiter.Allow(lim.Key)
	if err != nil {
		return 0, err
	}

	if result.RetryAfter > 0 {
		time.Sleep(result.RetryAfter)
	}
	return result.RetryAfter, nil
}

func (a *Architeuthis) processRequestWithCtx(rCtx *RequestCtx) ResponseCtx {

	if !rCtx.LastErrorWasProxyError && rCtx.Retries > config.Retries {
		return ResponseCtx{Error: errors.Errorf("Giving up after %d retries", rCtx.Retries)}
	}

	name, err := a.ChooseProxy(rCtx)
	if err != nil {
		return ResponseCtx{Error: err}
	}

	logrus.WithFields(logrus.Fields{
		"proxy": name,
		"host":  rCtx.Request.Host,
	}).Info("Routing request")

	p, err := a.GetProxy(name)
	if err != nil {
		return ResponseCtx{Error: err}
	}

	rCtx.p = p
	response, err := a.processRequestWithProxy(rCtx)

	responseCtx := ResponseCtx{
		Response:     response,
		ResponseTime: time.Now().Sub(rCtx.RequestTime).Seconds(),
		Error:        err,
	}

	p.incrReqTime = responseCtx.ResponseTime

	if response != nil && isHttpSuccessCode(response.StatusCode) {
		p.incrGood += 1
		return responseCtx
	}

	rCtx.LastFailedProxy = p.Name

	if isProxyError(err) {
		a.handleFatalProxyError(p)
		rCtx.LastErrorWasProxyError = true
		responseCtx.ShouldRetry = true
		return responseCtx
	}

	if err != nil {
		if isPermanentError(err) {
			a.handleProxyError(p, &responseCtx)
			return responseCtx
		}

		a.waitAfterFail(rCtx)
		a.handleProxyError(p, &responseCtx)
		responseCtx.ShouldRetry = true
	}

	dontRetry, forceRetry, shouldRetry := computeRules(rCtx, responseCtx)

	if forceRetry {
		responseCtx.ShouldRetry = true
		return responseCtx

	} else if dontRetry {
		responseCtx.Error = errors.Errorf("Applied dont_retry rule")
		return responseCtx
	}

	if response == nil {
		return responseCtx
	}

	// Handle HTTP errors
	responseCtx.Error = errors.Errorf("HTTP error: %d", response.StatusCode)

	if shouldRetry || shouldRetryHttpCode(response.StatusCode) {
		responseCtx.ShouldRetry = true
	}

	return responseCtx
}

func (a *Architeuthis) waitAfterFail(rCtx *RequestCtx) {
	wait := getWaitTime(rCtx.Retries)
	time.Sleep(wait)

	a.writeMetricSleep(wait, "retry")

	rCtx.Retries += 1
}

func isRemoteProxy(p *Proxy) bool {
	return p.HttpClient.Transport != nil
}

func (a *Architeuthis) handleProxyError(p *Proxy, rCtx *ResponseCtx) {

	if isRemoteProxy(p) && shouldBlameProxy(rCtx) {
		p.incrBad += 1
		p.BadRequestCount += 1
	}
}

func (a *Architeuthis) handleFatalProxyError(p *Proxy) {
	a.setDead(p.Name)
}

func (a *Architeuthis) processRequestWithProxy(rCtx *RequestCtx) (r *http.Response, e error) {

	a.incConns(rCtx.p.Name)

	limiter := a.getLimiter(rCtx)
	duration, err := limiter.waitRateLimit()
	if err != nil {
		return nil, err
	}

	if duration > 0 {
		a.writeMetricSleep(duration, "rate")
	}

	r, e = rCtx.p.HttpClient.Do(rCtx.Request)

	return
}

func (a *Architeuthis) Run() {

	logrus.WithFields(logrus.Fields{
		"addr": config.Addr,
	}).Info("Listening")

	err := http.ListenAndServe(config.Addr, a.server)
	logrus.Fatal(err)
}

func main() {
	logrus.SetLevel(logrus.TraceLevel)

	balancer := New()
	balancer.reloadConfig()

	var err error
	balancer.influxdb, err = influx.NewHTTPClient(influx.HTTPConfig{
		Addr:     config.InfluxUrl,
		Username: config.InfluxUser,
		Password: config.InfluxPass,
	})

	_, err = http.Post(config.InfluxUrl+"/query", "application/x-www-form-urlencoded", strings.NewReader("q=CREATE DATABASE \"architeuthis\""))
	if err != nil {
		panic(err)
	}

	balancer.points = make(chan *influx.Point, InfluxDbBufferSize)

	go balancer.asyncWriter(balancer.points)

	balancer.Run()
}
