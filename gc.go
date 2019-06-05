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

	b.proxyMutex.RLock()
	for _, p := range b.proxies {
		before += len(p.Limiters)
		cleanExpiredLimits(p)
		after += len(p.Limiters)
	}
	b.proxyMutex.RUnlock()

	logrus.WithFields(logrus.Fields{
		"removed": before - after,
	}).Info("Cleaned up limiters")
}

func cleanExpiredLimits(proxy *Proxy) {

	const ttl = time.Hour

	var limits []*ExpiringLimiter
	now := time.Now()

	for host, limiter := range proxy.Limiters {
		if now.Sub(limiter.LastRead) > ttl && limiter.CanDelete {
			logrus.WithFields(logrus.Fields{
				"proxy":     proxy.Name,
				"limiter":   host,
				"last_read": now.Sub(limiter.LastRead),
			}).Trace("Pruning limiter")
		} else {
			limits = append(limits, limiter)
		}
	}

	proxy.Limiters = limits
}
