package main

import (
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	"io/ioutil"
	"os"
	"strings"
	"time"
)

type HostConfig struct {
	Host     string            `json:"host"`
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
	Addr          string        `json:"addr"`
	TimeoutStr    string        `json:"timeout"`
	WaitStr       string        `json:"wait"`
	Multiplier    float64       `json:"multiplier"`
	Retries       int           `json:"retries"`
	Hosts         []*HostConfig `json:"hosts"`
	Proxies       []ProxyConfig `json:"proxies"`
	Wait          int64
	Timeout       time.Duration
	DefaultConfig *HostConfig
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
		if conf.EveryStr == "" {
			conf.Every = config.DefaultConfig.Every
		} else {
			conf.Every, err = time.ParseDuration(conf.EveryStr)
			handleErr(err)
		}

		if config.DefaultConfig != nil && conf.Burst == 0 {
			conf.Burst = config.DefaultConfig.Burst
		}
	}
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

	for _, conf := range config.Hosts {
		proxy.Limiters[conf.Host] = &ExpiringLimiter{
			rate.NewLimiter(rate.Every(conf.Every), conf.Burst),
			time.Now(),
		}
	}
}

func (b *Balancer) reloadConfig() {

	b.proxyMutex.Lock()
	loadConfig()

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
