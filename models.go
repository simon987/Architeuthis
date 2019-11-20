package main

import (
	"github.com/elazarl/goproxy"
	redisPackage "github.com/go-redis/redis"
	"github.com/go-redis/redis_rate"
	influx "github.com/influxdata/influxdb1-client/v2"
	"math"
	"net/http"
	"net/url"
	"time"
)

type Architeuthis struct {
	server   *goproxy.ProxyHttpServer
	redis    *redisPackage.Client
	influxdb influx.Client
	points   chan *influx.Point
}

// Request/Response
type RequestCtx struct {
	Request *http.Request

	Retries int

	LastFailedProxy        string
	p                      *Proxy
	LastErrorWasProxyError bool

	RequestTime time.Time
	options     RequestOptions
	configs     []*HostConfig
}

type ResponseCtx struct {
	Response     *http.Response
	ResponseTime float64
	Error        error
	ShouldRetry  bool
}

type RequestOptions struct {
	DoCloudflareBypass bool
}

// Proxy
type Proxy struct {
	Name string
	Url  *url.URL

	HttpClient *http.Client

	GoodRequestCount int64
	incrGood         int64

	BadRequestCount int64
	incrBad         int64

	TotalRequestTime float64
	incrReqTime      float64

	Connections int64

	KillOnError bool
}

func (p *Proxy) AvgLatency() float64 {
	return p.TotalRequestTime / float64(p.GoodRequestCount+p.BadRequestCount)
}

func (p *Proxy) Score() float64 {

	if p.GoodRequestCount+p.BadRequestCount == 0 {
		return 1000
	}

	var errorMod float64
	var latencyMod float64

	if p.BadRequestCount == 0 {
		errorMod = 1
	} else {
		errorMod = math.Min(float64(p.GoodRequestCount)/float64(p.BadRequestCount), 1)
	}

	avgLatency := p.AvgLatency()

	switch {
	case avgLatency < 3:
		latencyMod = 1
	case avgLatency < 4:
		latencyMod = 0.8
	case avgLatency < 5:
		latencyMod = 0.7
	case avgLatency < 9:
		latencyMod = 0.6
	case avgLatency < 10:
		latencyMod = 0.5
	case avgLatency < 15:
		latencyMod = 0.3
	case avgLatency < 20:
		latencyMod = 0.1
	default:
		latencyMod = 0
	}

	return 600*errorMod + 400*latencyMod - 200*(math.Max(float64(p.Connections-1), 0))
}

func (p *Proxy) getStats() proxyStat {
	return proxyStat{
		Name:             p.Name,
		Url:              p.Url.String(),
		GoodRequestCount: p.GoodRequestCount,
		BadRequestCount:  p.BadRequestCount,
		AvgLatency:       p.AvgLatency(),
		Connections:      p.Connections,
		Score:            int64(p.Score()),
	}
}

type proxyStat struct {
	Name string
	Url  string

	GoodRequestCount int64
	BadRequestCount  int64
	AvgLatency       float64
	Connections      int64
	Score            int64
}

type statsData struct {
	TotalGood   int
	TotalBad    int
	Connections int
	AvgLatency  float64
	AvgScore    float64

	Proxies []proxyStat
}

type CheckMethod string

const (
	CheckIp CheckMethod = "check_ip"
	HttpOk  CheckMethod = "http_ok"
)

type ProxyJudge struct {
	url    *url.URL
	method CheckMethod
}

type RedisLimiter struct {
	Key     string
	Limiter *redis_rate.Limiter
}

// Config
type HostConfig struct {
	Host     string            `json:"host"`
	EveryStr string            `json:"every"`
	Burst    int               `json:"burst"`
	Headers  map[string]string `json:"headers"`
	RawRules []*RawHostRule    `json:"rules"`
	IsGlob   bool
	Every    time.Duration
	Rules    []*HostRule
}

type RawHostRule struct {
	Condition string `json:"condition"`
	Action    string `json:"action"`
	Arg       string `json:"arg"`
}

type HostRuleAction int

const (
	DontRetry   HostRuleAction = 0
	ForceRetry  HostRuleAction = 1
	ShouldRetry HostRuleAction = 2
)

type HostRule struct {
	Matches func(r *ResponseCtx) bool
	Action  HostRuleAction
	Arg     float64
}

type ProxyConfig struct {
	Name string `json:"name"`
	Url  string `json:"url"`
}

var config struct {
	Addr          string        `json:"addr"`
	TimeoutStr    string        `json:"timeout"`
	WaitStr       string        `json:"wait"`
	Multiplier    float64       `json:"multiplier"`
	Retries       int           `json:"retries"`
	MaxErrorRatio float64       `json:"max_error"`
	Hosts         []*HostConfig `json:"hosts"`
	Proxies       []ProxyConfig `json:"proxies"`
	RedisUrl      string        `json:"redis_url"`
	Wait          int64
	Timeout       time.Duration
	DefaultConfig *HostConfig
	Routing       bool
}
