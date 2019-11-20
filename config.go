package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/ryanuber/go-glob"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func (a HostRuleAction) String() string {
	switch a {
	case DontRetry:
		return "dont_retry"
	case ForceRetry:
		return "force_retry"
	case ShouldRetry:
		return "should_retry"
	}
	return "???"
}

func parseRule(raw *RawHostRule) (*HostRule, error) {

	rule := &HostRule{}

	switch raw.Action {
	case "should_retry":
		rule.Action = ShouldRetry
	case "dont_retry":
		rule.Action = DontRetry
	case "force_retry":
		rule.Action = ForceRetry
	default:
		return nil, errors.Errorf("Invalid argument for action: %s", raw.Action)
	}

	switch {
	case strings.Contains(raw.Condition, "!="):
		op1Str, op2Str := split(raw.Condition, "!=")
		op1Func := parseOperand1(op1Str)
		if op1Func == nil {
			return nil, errors.Errorf("Invalid rule: %s", raw.Condition)
		}

		if isGlob(op2Str) {
			rule.Matches = func(ctx *ResponseCtx) bool {
				return !glob.Glob(op2Str, op1Func(ctx))
			}
		} else {
			op2Str = strings.Replace(op2Str, "\\*", "*", -1)
			rule.Matches = func(ctx *ResponseCtx) bool {
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
			rule.Matches = func(ctx *ResponseCtx) bool {
				return glob.Glob(op2Str, op1Func(ctx))
			}
		} else {
			op2Str = strings.Replace(op2Str, "\\*", "*", -1)
			rule.Matches = func(ctx *ResponseCtx) bool {
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

		rule.Matches = func(ctx *ResponseCtx) bool {
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

		rule.Matches = func(ctx *ResponseCtx) bool {
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

func parseOperand1(op string) func(ctx *ResponseCtx) string {
	switch {
	case op == "body":
		return func(ctx *ResponseCtx) string {

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
		return func(ctx *ResponseCtx) string {
			if ctx.Response == nil {
				return ""
			}
			return strconv.Itoa(ctx.Response.StatusCode)
		}
	case op == "response_time":
		return func(ctx *ResponseCtx) string {
			return strconv.FormatFloat(ctx.ResponseTime, 'f', 6, 64)
		}
	case strings.HasPrefix(op, "header:"):
		header := op[strings.Index(op, ":")+1:]
		return func(ctx *ResponseCtx) string {
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

func (a *Architeuthis) reloadConfig() {
	_ = loadConfig()
	logrus.Info("Reloaded config")
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}
