package main

import (
	influx "github.com/influxdata/influxdb1-client/v2"
	"github.com/sirupsen/logrus"
	"strconv"
	"time"
)

const InfluxDbBufferSize = 100
const InfluxDbDatabase = "architeuthis"
const InfluxDBRetentionPolicy = ""

func (a *Architeuthis) asyncWriter(metrics chan *influx.Point) {

	logrus.Trace("Started async influxdb writer")

	bp, _ := influx.NewBatchPoints(influx.BatchPointsConfig{
		Database:        InfluxDbDatabase,
		RetentionPolicy: InfluxDBRetentionPolicy,
	})

	for point := range metrics {
		bp.AddPoint(point)

		if len(bp.Points()) >= InfluxDbBufferSize {
			flushQueue(a.influxdb, &bp)
		}
	}
	flushQueue(a.influxdb, &bp)
}

func flushQueue(influxdb influx.Client, bp *influx.BatchPoints) {

	err := influxdb.Write(*bp)

	if err != nil {
		logrus.WithError(err).Error("influxdb write")
		return
	}

	logrus.WithFields(logrus.Fields{
		"size": len((*bp).Points()),
	}).Trace("Wrote points")

	*bp, _ = influx.NewBatchPoints(influx.BatchPointsConfig{
		Database:        InfluxDbDatabase,
		RetentionPolicy: InfluxDBRetentionPolicy,
	})
}

func (a *Architeuthis) writeMetricProxyCount(newCount int) {
	point, _ := influx.NewPoint(
		"add_proxy",
		nil,
		map[string]interface{}{
			"newCount": newCount,
		},
		time.Now(),
	)
	a.points <- point
}

func (a *Architeuthis) writeMetricRequest(ctx ResponseCtx) {

	var fields map[string]interface{}

	if ctx.Response != nil {

		size, _ := strconv.ParseInt(ctx.Response.Header.Get("Content-Length"), 10, 64)

		fields = map[string]interface{}{
			"status":  ctx.Response.StatusCode,
			"latency": ctx.ResponseTime,
			"size":    size,
		}
	} else {
		fields = map[string]interface{}{}
	}

	var ok string
	if ctx.Error == nil {
		ok = "true"
	} else {
		ok = "false"
	}

	point, _ := influx.NewPoint(
		"request",
		map[string]string{
			"ok": ok,
		},
		fields,
		time.Now(),
	)
	a.points <- point
}

func (a *Architeuthis) writeMetricSleep(duration time.Duration, tag string) {
	point, _ := influx.NewPoint(
		"sleep",
		map[string]string{
			"context": tag,
		},
		map[string]interface{}{
			"duration": duration.Seconds(),
		},
		time.Now(),
	)
	a.points <- point
}
