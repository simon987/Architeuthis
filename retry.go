package main

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"
)

func isPermanentError(err error) bool {

	var opErr *net.OpError

	urlErr, ok := err.(*url.Error)
	if ok {
		opErr, ok = urlErr.Err.(*net.OpError)
		if !ok {
			return false
		}
	} else {
		netErr, ok := err.(net.Error)
		if ok {
			if netErr.Timeout() {
				return false
			}

			opErr, ok = netErr.(*net.OpError)
			if !ok {
				return false
			}
		}
	}

	//This should not happen...
	if opErr == nil {
		logrus.Error("FIXME: isPermanentError; opErr == nil")
		return false
	}

	if opErr.Op == "proxyconnect" {
		logrus.Error("Error connecting to the proxy!")
		return true
	}

	if opErr.Timeout() {
		// Usually means that there is no route to host
		return true
	}

	switch t := opErr.Err.(type) {
	case *net.DNSError:
		return true
	case *os.SyscallError:

		logrus.Printf("os.SyscallError:%+v", t)

		if errno, ok := t.Err.(syscall.Errno); ok {
			switch errno {
			case syscall.ECONNREFUSED:
				log.Println("connect refused")
				return true
			case syscall.ETIMEDOUT:
				log.Println("timeout")
				return false
			}
		}
	}

	//todo: handle the other error types
	fmt.Println("fixme: None of the above")

	return false
}

func waitTime(retries int) time.Duration {

	const multiplier = 1.5
	const wait = int64(5 * time.Second)

	return time.Duration(wait * int64(math.Pow(multiplier, float64(retries))))
}

func (p *Proxy) waitRateLimit(r *http.Request) {

	sHost := simplifyHost(r.Host)

	limiter := p.getLimiter(sHost)
	reservation := limiter.Reserve()
	if !reservation.OK() {
		logrus.WithFields(logrus.Fields{
			"host": sHost,
		}).Warn("Could not get reservation, make sure that burst is > 0")
	}

	delay := reservation.Delay()
	if delay > 0 {
		logrus.WithFields(logrus.Fields{
			"time": delay,
		}).Trace("Sleeping")
		time.Sleep(delay)
	}
}

func isHttpSuccessCode(code int) bool {
	return code >= 200 && code < 300
}

func shouldRetryHttpCode(code int) bool {

	switch {
	case code == 403:
	case code == 408:
	case code == 429:
	case code == 444:
	case code == 499:
	case code >= 500:
		return true
	}

	return false
}
