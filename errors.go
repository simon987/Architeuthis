package main

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	"log"
	"math"
	"net"
	"net/url"
	"os"
	"syscall"
	"time"
)

func shouldBlameProxy(rCtx *ResponseCtx) bool {

	if rCtx.Response != nil {
		return shouldBlameProxyHttpCode(rCtx.Response.StatusCode)
	} else {
		//TODO: don't blame proxy for timeout?
		return true
	}
}

func isProxyError(err error) bool {

	urlErr, ok := err.(*url.Error)
	if ok {
		opErr, ok := urlErr.Err.(*net.OpError)
		if ok {
			if opErr.Op == "proxyconnect" {
				return true
			}
			if opErr.Op == "local error" {
				return true
			}
		}
	}
	return false
}

func isPermanentError(err error) bool {

	var opErr *net.OpError

	urlErr, ok := err.(*url.Error)
	if ok {
		opErr, ok = urlErr.Err.(*net.OpError)
		if !ok {
			if urlErr.Err != nil && urlErr.Err.Error() == "Proxy Authentication Required" {
				logrus.Warn("Got 'Proxy Authentication Required'. Did you forget to configure the password for a proxy?")
				return true
			}
			return false
		}

		if opErr.Err.Error() == "Internal Privoxy Error" {
			return true
		}

	} else {
		_, ok := err.(net.Error)
		if ok {
			return false
		}
	}

	//This should not happen...
	if opErr == nil {
		logrus.Error("FIXME: isPermanentError; opErr == nil")
		return false
	}

	if opErr.Timeout() {
		// Usually means that there is no route to host
		return true
	}

	switch t := opErr.Err.(type) {
	case *net.DNSError:
		return true
	case *os.SyscallError:
		if errno, ok := t.Err.(syscall.Errno); ok {
			switch errno {
			case syscall.ECONNREFUSED:
				log.Println("connect refused")
				return true
			case syscall.ETIMEDOUT:
				log.Println("timeout")
				return false
			case syscall.ECONNRESET:
				log.Println("connection reset by peer")
				return false
			}
		}
	}

	fmt.Println("fixme: None of the above")

	return false
}

func getWaitTime(retries int) time.Duration {
	return time.Duration(config.Wait * int64(math.Pow(config.Multiplier, float64(retries))))
}

func (p *Proxy) waitRateLimit(limiter *rate.Limiter) {

	reservation := limiter.Reserve()
	delay := reservation.Delay()

	if delay > 0 {
		time.Sleep(delay)
	}
}

func isHttpSuccessCode(code int) bool {
	return code >= 200 && code < 300
}

func shouldBlameProxyHttpCode(code int) bool {

	switch {
	case code >= 500:
		return false
	default:
		return true
	}
}

func shouldRetryHttpCode(code int) bool {

	switch {
	case code == 403:
		fallthrough
	case code == 408:
		fallthrough
	case code == 429:
		fallthrough
	case code == 444:
		fallthrough
	case code == 499:
		fallthrough
	case code >= 500:
		return true
	}

	return false
}
