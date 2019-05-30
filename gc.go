package main

import (
	"github.com/robfig/cron"
	"github.com/sirupsen/logrus"
	"time"
)

func (b *Balancer) setupGarbageCollector() {

	const gcInterval = time.Minute * 5

	gcCron := cron.New()
	gcSchedule := cron.Every(gcInterval)
	gcCron.Schedule(gcSchedule, cron.FuncJob(b.cleanAllExpiredLimits))

	go gcCron.Run()

	logrus.WithFields(logrus.Fields{
		"every": gcInterval,
	}).Info("Started task cleanup cron")
}

func (b *Balancer) cleanAllExpiredLimits() {
	before := 0
	after := 0
	for _, p := range b.proxies {
		before += len(p.Limiters)
		cleanExpiredLimits(p)
		after += len(p.Limiters)
	}

	logrus.WithFields(logrus.Fields{
		"removed": before - after,
	}).Info("Cleaned up limiters")
}

func cleanExpiredLimits(proxy *Proxy) {

	const ttl = time.Hour

	limits := make(map[string]*ExpiringLimiter, 0)
	now := time.Now()

	for host, limiter := range proxy.Limiters {
		if now.Sub(limiter.LastRead) > ttl && shouldPruneLimiter(host) {
			logrus.WithFields(logrus.Fields{
				"proxy":     proxy.Name,
				"limiter":   host,
				"last_read": now.Sub(limiter.LastRead),
			}).Trace("Pruning limiter")
		} else {
			limits[host] = limiter
		}
	}

	proxy.Limiters = limits
}

func shouldPruneLimiter(host string) bool {

	// Don't remove hosts that are coming from the config
	for _, conf := range config.Hosts {
		if conf.Host == host {
			return false
		}
	}

	return true
}
