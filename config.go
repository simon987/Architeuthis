package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/ryanuber/go-glob"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	"io/ioutil"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type HostConfig struct {
	Host     string            `json:"host"`
	EveryStr string            `json:"every"`
	Burst    int               `json:"burst"`
	Headers  map[string]string `json:"headers"`
	RawRules []*RawHostRule    `json:"rules"`
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
	DontRetry     HostRuleAction = 0
	MultiplyEvery HostRuleAction = 1
	SetEvery      HostRuleAction = 2
	ForceRetry    HostRuleAction = 3
	ShouldRetry   HostRuleAction = 4
)

func (a HostRuleAction) String() string {
	switch a {
	case DontRetry:
		return "dont_retry"
	case MultiplyEvery:
		return "multiply_every"
	case SetEvery:
		return "set_every"
	case ForceRetry:
		return "force_retry"
	case ShouldRetry:
		return "should_retry"
	}
	return "???"
}

type HostRule struct {
	Matches func(r *RequestCtx) bool
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
	RetriesHard   int           `json:"retries_hard"`
	Hosts         []*HostConfig `json:"hosts"`
	Proxies       []ProxyConfig `json:"proxies"`
	Wait          int64
	Timeout       time.Duration
	DefaultConfig *HostConfig
}

func parseRule(raw *RawHostRule) (*HostRule, error) {
	//TODO: for the love of god someone please refactor this func

	rule := &HostRule{}
	var err error

	switch raw.Action {
	case "should_retry":
		rule.Action = ShouldRetry
	case "dont_retry":
		rule.Action = DontRetry
	case "multiply_every":
		rule.Action = MultiplyEvery
		rule.Arg, err = strconv.ParseFloat(raw.Arg, 64)
	case "set_every":
		rule.Action = SetEvery
		var duration time.Duration
		duration, err = time.ParseDuration(raw.Arg)
		if err != nil {
			return nil, err
		}
		rule.Arg = 1 / duration.Seconds()
	case "force_retry":
		rule.Action = ForceRetry
	default:
		return nil, errors.Errorf("Invalid argument for action: %s", raw.Action)
	}

	if err != nil {
		return nil, err
	}

	switch {
	case strings.Contains(raw.Condition, "!="):
		op1Str, op2Str := split(raw.Condition, "!=")
		op1Func := parseOperand1(op1Str)
		if op1Func == nil {
			return nil, errors.Errorf("Invalid rule: %s", raw.Condition)
		}

		if isGlob(op2Str) {
			rule.Matches = func(ctx *RequestCtx) bool {
				return !glob.Glob(op2Str, op1Func(ctx))
			}
		} else {
			op2Str = strings.Replace(op2Str, "\\*", "*", -1)
			rule.Matches = func(ctx *RequestCtx) bool {
				return op1Func(ctx) != op2Str
			}
		}
	case strings.Contains(raw.Condition, "="):
		op1Str, op2Str := split(raw.Condition, "=")
		op1Func := parseOperand1(op1Str)
		if op1Func == nil {
			return nil, errors.Errorf("Invalid rule: %s", raw.Condition)
		}

		if isGlob(op2Str) {
			rule.Matches = func(ctx *RequestCtx) bool {
				return glob.Glob(op2Str, op1Func(ctx))
			}
		} else {
			op2Str = strings.Replace(op2Str, "\\*", "*", -1)
			rule.Matches = func(ctx *RequestCtx) bool {
				return op1Func(ctx) == op2Str
			}
		}
	case strings.Contains(raw.Condition, ">"):
		op1Str, op2Str := split(raw.Condition, ">")
		op1Func := parseOperand1(op1Str)
		if op1Func == nil {
			return nil, errors.Errorf("Invalid rule: %s", raw.Condition)
		}
		op2Num, err := parseOperand2(op1Str, op2Str)
		if err != nil {
			return nil, err
		}

		rule.Matches = func(ctx *RequestCtx) bool {
			op1Num, err := strconv.ParseFloat(op1Func(ctx), 64)
			handleRuleErr(err)
			return op1Num > op2Num
		}
	case strings.Contains(raw.Condition, "<"):
		op1Str, op2Str := split(raw.Condition, "<")
		op1Func := parseOperand1(op1Str)
		if op1Func == nil {
			return nil, errors.Errorf("Invalid rule: %s", raw.Condition)
		}
		op2Num, err := parseOperand2(op1Str, op2Str)
		if err != nil {
			return nil, err
		}

		rule.Matches = func(ctx *RequestCtx) bool {
			op1Num, err := strconv.ParseFloat(op1Func(ctx), 64)
			handleRuleErr(err)
			return op1Num < op2Num
		}
	}

	return rule, nil
}

func handleRuleErr(err error) {
	if err != nil {
		logrus.WithError(err).Warn("Error computing rule")
	}
}

func split(str, subStr string) (string, string) {

	str1 := str[:strings.Index(str, subStr)]
	str2 := str[strings.Index(str, subStr)+len(subStr):]

	return str1, str2
}

func parseOperand2(op1, op2 string) (float64, error) {
	if op1 == "response_time" {
		res, err := time.ParseDuration(op2)
		if err != nil {
			return -1, err
		}
		return res.Seconds(), nil
	}

	return strconv.ParseFloat(op2, 64)
}

func parseOperand1(op string) func(ctx *RequestCtx) string {
	switch {
	case op == "body":
		return func(ctx *RequestCtx) string {

			if ctx.Response == nil {
				return ""
			}
			bodyBytes, err := ioutil.ReadAll(ctx.Response.Body)
			if err != nil {
				return ""
			}
			err = ctx.Response.Body.Close()
			if err != nil {
				return ""
			}
			ctx.Response.Body = ioutil.NopCloser(bytes.NewReader(bodyBytes))

			return string(bodyBytes)
		}
	case op == "status":
		return func(ctx *RequestCtx) string {
			if ctx.Response == nil {
				return ""
			}
			return strconv.Itoa(ctx.Response.StatusCode)
		}
	case op == "response_time":
		return func(ctx *RequestCtx) string {
			return strconv.FormatFloat(time.Now().Sub(ctx.RequestTime).Seconds(), 'f', 6, 64)
		}
	case strings.HasPrefix(op, "header:"):
		header := op[strings.Index(op, ":")+1:]
		return func(ctx *RequestCtx) string {
			if ctx.Response == nil {
				return ""
			}
			return ctx.Response.Header.Get(header)
		}
	default:
		return nil
	}
}

func isGlob(op string) bool {
	tmpStr := strings.Replace(op, "\\*", "_", -1)

	return strings.Contains(tmpStr, "*")
}

func loadConfig() error {

	configFile, err := os.Open("config.json")
	if err != nil {
		return err
	}

	configBytes, err := ioutil.ReadAll(configFile)
	if err != nil {
		return err
	}

	err = json.Unmarshal(configBytes, &config)
	if err != nil {
		return err
	}

	validateConfig()

	config.Timeout, err = time.ParseDuration(config.TimeoutStr)
	wait, err := time.ParseDuration(config.WaitStr)
	config.Wait = int64(wait)

	for i, conf := range config.Hosts {
		if conf.EveryStr == "" {
			// Look 'upwards' for every
			for _, prevConf := range config.Hosts[:i] {
				if glob.Glob(prevConf.Host, conf.Host) {
					conf.Every = prevConf.Every
				}
			}
		} else {
			conf.Every, err = time.ParseDuration(conf.EveryStr)
			handleErr(err)
		}

		if conf.Burst == 0 {
			// Look 'upwards' for burst
			for _, prevConf := range config.Hosts[:i] {
				if glob.Glob(prevConf.Host, conf.Host) {
					conf.Burst = prevConf.Burst
				}
			}
		}
		if conf.Burst == 0 {
			return errors.Errorf("Burst must be > 0 (Host: %s)", conf.Host)
		}

		for _, rawRule := range conf.RawRules {
			r, err := parseRule(rawRule)
			handleErr(err)
			conf.Rules = append(conf.Rules, r)

			logrus.WithFields(logrus.Fields{
				"arg":       r.Arg,
				"action":    r.Action,
				"matchFunc": runtime.FuncForPC(reflect.ValueOf(r.Matches).Pointer()).Name(),
			}).Info("Rule")
		}

		logrus.WithFields(logrus.Fields{
			"every":   conf.Every,
			"burst":   conf.Burst,
			"headers": conf.Headers,
			"host":    conf.Host,
		}).Info("Host")
	}

	return nil
}

func validateConfig() {

	for _, conf := range config.Hosts {

		if conf.Host == "*" {
			config.DefaultConfig = conf
		}

		for k := range conf.Headers {
			if strings.ToLower(k) == "accept-encoding" {
				panic(fmt.Sprintf("headers config for '%s':"+
					" Do not set the Accept-Encoding header, it breaks goproxy", conf.Host))
			}
		}
	}

	if config.DefaultConfig == nil {
		panic("config.json: You must specify a default host ('*')")
	}
}

func applyConfig(proxy *Proxy) {

	//Reverse order
	for i := len(config.Hosts) - 1; i >= 0; i-- {

		conf := config.Hosts[i]

		proxy.Limiters = append(proxy.Limiters, &ExpiringLimiter{
			HostGlob:  conf.Host,
			IsGlob:    isGlob(conf.Host),
			Limiter:   rate.NewLimiter(rate.Every(conf.Every), conf.Burst),
			LastRead:  time.Now(),
			CanDelete: false,
		})
	}
}

func (b *Balancer) reloadConfig() {

	b.proxyMutex.Lock()
	err := loadConfig()
	if err != nil {
		panic(err)
	}

	if b.proxies != nil {
		b.proxies = b.proxies[:0]
	}

	for _, proxyConf := range config.Proxies {
		proxy, err := NewProxy(proxyConf.Name, proxyConf.Url)
		handleErr(err)
		b.proxies = append(b.proxies, proxy)

		applyConfig(proxy)

		logrus.WithFields(logrus.Fields{
			"name": proxy.Name,
			"url":  proxy.Url,
		}).Info("Proxy")
	}
	b.proxyMutex.Unlock()

	logrus.Info("Reloaded config")
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}
