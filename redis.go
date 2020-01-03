package main

import (
	"errors"
	"github.com/go-redis/redis/v7"
	"github.com/go-redis/redis_rate/v8"
	"github.com/sirupsen/logrus"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
)

const KeyProxyList = "proxies"
const KeyDeadProxyList = "deadProxies"
const PrefixProxy = "proxy:"

const KeyConnectionCount = "conn"
const KeyRequestTime = "reqtime"
const KeyBadRequestCount = "bad"
const KeyGoodRequestCount = "good"
const KeyRevived = "revived"
const KeyUrl = "url"

func (a *Architeuthis) getLimiter(rCtx *RequestCtx) *RedisLimiter {

	var hostConfig *HostConfig
	if len(rCtx.configs) == 0 {
		hostConfig = config.DefaultConfig
	} else {
		hostConfig = rCtx.configs[len(rCtx.configs)-1]
	}

	return &RedisLimiter{
		Key:     hostConfig.Host + ":" + rCtx.p.Name,
		Limiter: redis_rate.NewLimiter(a.redis),
		Limit: &redis_rate.Limit{
			Rate:   1,
			Period: hostConfig.Every,
			Burst:  hostConfig.Burst,
		},
	}
}

func (a *Architeuthis) UpdateProxy(p *Proxy) {

	key := PrefixProxy + p.Name
	pipe := a.redis.Pipeline()

	if p.incrBad != 0 {
		pipe.HIncrBy(key, KeyBadRequestCount, p.incrBad)
		p.BadRequestCount += p.incrBad
	} else {
		pipe.HIncrBy(key, KeyGoodRequestCount, p.incrGood)
		p.GoodRequestCount += p.incrGood

		if p.KillOnError {
			pipe.HSet(key, KeyRevived, 0)
		}
	}

	pipe.HIncrByFloat(key, KeyRequestTime, p.incrReqTime)
	p.TotalRequestTime += p.incrReqTime

	pipe.HIncrBy(key, KeyConnectionCount, -1)

	pipe.ZAddXX(KeyProxyList, &redis.Z{
		Score:  p.Score(),
		Member: p.Name,
	})

	_, _ = pipe.Exec()

	newBadRatio := float64(p.BadRequestCount) / float64(p.GoodRequestCount)

	if p.incrBad > 0 && (p.KillOnError || (newBadRatio > config.MaxErrorRatio && p.BadRequestCount >= 5)) {
		a.setDead(p.Name)
	}
}

func (a *Architeuthis) AddProxy(name, stringUrl string) error {

	_, err := url.Parse(stringUrl)
	if err != nil {
		return err
	}

	pipe := a.redis.Pipeline()

	pipe.HMSet(PrefixProxy+name, map[string]interface{}{
		KeyUrl:              stringUrl,
		KeyRequestTime:      0,
		KeyGoodRequestCount: 0,
		KeyBadRequestCount:  0,
		KeyConnectionCount:  0,
		KeyRevived:          0,
	})

	zadd := pipe.ZAdd(KeyProxyList, &redis.Z{
		Score:  1000,
		Member: name,
	})

	zcard := pipe.ZCard(KeyProxyList)

	_, _ = pipe.Exec()

	if zadd.Val() != 0 {
		logrus.WithFields(logrus.Fields{
			KeyUrl: stringUrl,
		}).Info("Add proxy")

		a.writeMetricProxyCount(int(zcard.Val()))
	}

	return nil
}

func (a *Architeuthis) incConns(name string) int64 {
	res, _ := a.redis.HIncrBy(PrefixProxy+name, KeyConnectionCount, 1).Result()
	return res
}

func (a *Architeuthis) setDead(name string) {

	pipe := a.redis.Pipeline()

	pipe.ZRem(KeyProxyList, name)
	pipe.SAdd(KeyDeadProxyList, name)
	count := pipe.ZCard(KeyProxyList)

	_, _ = pipe.Exec()

	logrus.WithFields(logrus.Fields{
		"proxy": name,
	}).Trace("dead")

	a.writeMetricProxyCount(int(count.Val()))
}

func (a *Architeuthis) setAlive(name string) {

	pipe := a.redis.Pipeline()

	pipe.SRem(KeyDeadProxyList, name)
	pipe.HMSet(KeyProxyList+name, map[string]interface{}{
		KeyRevived:          1,
		KeyRequestTime:      0,
		KeyGoodRequestCount: 0,
		KeyBadRequestCount:  0,
		KeyConnectionCount:  0,
	})
	pipe.ZAdd(KeyProxyList, &redis.Z{
		Score:  1000,
		Member: name,
	})
	count := pipe.ZCard(KeyProxyList)

	_, _ = pipe.Exec()

	logrus.WithFields(logrus.Fields{
		"proxy": name,
	}).Trace("revive")

	a.writeMetricProxyCount(int(count.Val()))
}

func (a *Architeuthis) GetDeadProxies() []*Proxy {

	result, err := a.redis.SMembers(KeyDeadProxyList).Result()
	if err != nil {
		return nil
	}

	return a.getProxies(result)
}

func (a *Architeuthis) GetAliveProxies() []*Proxy {

	result, err := a.redis.ZRange(KeyProxyList, 0, math.MaxInt64).Result()
	if err != nil {
		return nil
	}

	return a.getProxies(result)
}

func (a *Architeuthis) getProxies(names []string) []*Proxy {

	var proxies []*Proxy

	for _, name := range names {
		p, _ := a.GetProxy(name)
		if p != nil {
			proxies = append(proxies, p)
		}
	}

	return proxies
}

func (a *Architeuthis) getStats() statsData {

	data := statsData{}

	var totalTime float64 = 0
	var totalScore int64 = 0

	for _, p := range a.GetAliveProxies() {
		stat := p.getStats()
		data.Proxies = append(data.Proxies, stat)

		data.TotalBad += int(p.BadRequestCount)
		data.TotalGood += int(p.GoodRequestCount)
		data.Connections += int(p.Connections)

		totalTime += p.TotalRequestTime
		totalScore += stat.Score
	}

	data.AvgLatency = totalTime / float64(data.TotalGood+data.TotalBad)
	data.AvgScore = float64(totalScore) / float64(len(data.Proxies))

	return data
}

func (a *Architeuthis) GetProxy(name string) (*Proxy, error) {

	result, err := a.redis.HGetAll(PrefixProxy + name).Result()
	if err != nil {
		return nil, err
	}

	var parsedUrl *url.URL
	var httpClient *http.Client

	if result[KeyUrl] == "" {
		parsedUrl = nil
		httpClient = &http.Client{
			Timeout: config.Timeout,
		}
	} else {
		parsedUrl, err = url.Parse(result[KeyUrl])
		if err != nil {
			return nil, err
		}

		httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(parsedUrl),
			},
			Timeout: config.Timeout,
		}
	}

	conns, _ := strconv.ParseInt(result[KeyConnectionCount], 10, 64)
	good, _ := strconv.ParseInt(result[KeyGoodRequestCount], 10, 64)
	bad, _ := strconv.ParseInt(result[KeyBadRequestCount], 10, 64)
	reqtime, _ := strconv.ParseFloat(result[KeyRequestTime], 64)

	return &Proxy{
		Name:             name,
		Url:              parsedUrl,
		HttpClient:       httpClient,
		Connections:      conns,
		GoodRequestCount: good,
		BadRequestCount:  bad,
		TotalRequestTime: reqtime,
	}, nil
}

func (a *Architeuthis) ChooseProxy(rCtx *RequestCtx) (string, error) {
	results, err := a.redis.ZRevRangeWithScores(KeyProxyList, 0, 12).Result()
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return "", errors.New("no proxies available")
	}

	if len(results) == 1 {
		return results[0].Member.(string), nil
	}

	for {
		idx := rand.Intn(len(results))

		if results[idx].Member != rCtx.LastFailedProxy {
			return results[idx].Member.(string), nil
		}
	}
}
