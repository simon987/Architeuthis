package main

import (
	"github.com/ryanuber/go-glob"
	"net/http"
	"strings"
)

func normalizeHost(host string) string {

	col := strings.LastIndex(host, ":")
	if col > 0 {
		host = host[:col]
	}

	return "." + host
}

func parseOptions(header *http.Header) RequestOptions {

	opts := RequestOptions{}

	cfParam := header.Get("X-Architeuthis-CF-Bypass")
	if cfParam != "" {
		header.Del("X-Architeuthis-CF-Bypass")
		opts.DoCloudflareBypass = true
	}

	return opts
}

func getConfigsMatchingHost(sHost string) []*HostConfig {

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

func computeRules(requestCtx *RequestCtx, responseCtx ResponseCtx) (dontRetry, forceRetry bool, shouldRetry bool) {
	dontRetry = false
	forceRetry = false
	shouldRetry = false

	for _, conf := range requestCtx.configs {
		for _, rule := range conf.Rules {
			if rule.Matches(&responseCtx) {
				switch rule.Action {
				case DontRetry:
					dontRetry = true
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
