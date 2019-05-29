package main

import (
	"encoding/json"
	"golang.org/x/time/rate"
	"io/ioutil"
	"os"
	"time"
)

type HostConfig struct {
	Every   string            `json:"every"`
	Burst   int               `json:"burst"`
	Headers map[string]string `json:"headers"`
}

type ProxyConfig struct {
	Name string `json:"name"`
	Url  string `json:"url"`
}

var config struct {
	Addr    string                `json:"addr"`
	Hosts   map[string]HostConfig `json:"hosts"`
	Proxies []ProxyConfig         `json:"proxies"`
}

func loadConfig() {

	configFile, err := os.Open("config.json")
	handleErr(err)

	configBytes, err := ioutil.ReadAll(configFile)
	handleErr(err)

	err = json.Unmarshal(configBytes, &config)
	handleErr(err)
}

func applyConfig(proxy *Proxy) {

	for host, conf := range config.Hosts {
		duration, err := time.ParseDuration(conf.Every)
		handleErr(err)
		proxy.Limiters[host] = &ExpiringLimiter{
			rate.NewLimiter(rate.Every(duration), conf.Burst),
			time.Now(),
		}
	}
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}
