package main

import (
	"github.com/robfig/cron"
	"github.com/sirupsen/logrus"
	"sync"
	"time"
)

func (a *Architeuthis) setupProxyReviver() {

	const gcInterval = time.Minute * 10

	gcCron := cron.New()
	gcSchedule := cron.Every(gcInterval)
	gcCron.Schedule(gcSchedule, cron.FuncJob(a.reviveProxies))

	go gcCron.Run()

	logrus.WithFields(logrus.Fields{
		"every": gcInterval,
	}).Info("Started proxy revive cron")
}

func (a *Architeuthis) testUrl(ch chan *Proxy, url string, wg sync.WaitGroup) {

	for p := range ch {
		r, _ := p.HttpClient.Get(url)

		if r != nil && isHttpSuccessCode(r.StatusCode) {
			a.setAlive(p.Name)
		}
	}
	wg.Done()
}

func (a *Architeuthis) reviveProxies() {

	wg := sync.WaitGroup{}
	const checkers = 50
	wg.Add(checkers)

	ch := make(chan *Proxy, checkers)

	for i := 0; i < checkers; i++ {
		go a.testUrl(ch, "https://google.com/", wg)
	}

	for _, p := range a.GetDeadProxies() {
		ch <- p
	}

	wg.Wait()
}
