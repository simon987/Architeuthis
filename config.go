package main

import (
	"encoding/json"
	"fmt"
	"golang.org/x/time/rate"
	"io/ioutil"
	"os"
	"strings"
	"time"
)

type HostConfig struct {
	EveryStr string            `json:"every"`
	Burst    int               `json:"burst"`
	Headers  map[string]string `json:"headers"`
	Every    time.Duration
}

type ProxyConfig struct {
	Name string `json:"name"`
	Url  string `json:"url"`
}

var config struct {
	Addr       string                 `json:"addr"`
	TimeoutStr string                 `json:"timeout"`
	WaitStr    string                 `json:"wait"`
	Multiplier float64                `json:"multiplier"`
	Retries    int                    `json:"retries"`
	Hosts      map[string]*HostConfig `json:"hosts"`
	Proxies    []ProxyConfig          `json:"proxies"`
	Wait       int64
	Timeout    time.Duration
}

func loadConfig() {

	configFile, err := os.Open("config.json")
	handleErr(err)

	configBytes, err := ioutil.ReadAll(configFile)
	handleErr(err)

	err = json.Unmarshal(configBytes, &config)
	handleErr(err)

	validateConfig()

	config.Timeout, err = time.ParseDuration(config.TimeoutStr)
	wait, err := time.ParseDuration(config.WaitStr)
	config.Wait = int64(wait)

	for _, conf := range config.Hosts {
		conf.Every, err = time.ParseDuration(conf.EveryStr)
		handleErr(err)
	}
}

func validateConfig() {

	hasDefaultHost := false

	for host, conf := range config.Hosts {

		if host == "*" {
			hasDefaultHost = true
		}

		for k := range conf.Headers {
			if strings.ToLower(k) == "accept-encoding" {
				panic(fmt.Sprintf("headers config for '%s':"+
					" Do not set the Accept-Encoding header, it breaks goproxy", host))
			}
		}
	}

	if !hasDefaultHost {
		panic("config.json: You must specify a default host ('*')")
	}
}

func applyConfig(proxy *Proxy) {

	for host, conf := range config.Hosts {
		proxy.Limiters[host] = &ExpiringLimiter{
			rate.NewLimiter(rate.Every(conf.Every), conf.Burst),
			time.Now(),
		}
	}
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}
