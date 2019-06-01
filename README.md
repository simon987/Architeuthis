# Architeuthis 🦑

[![CodeFactor](https://www.codefactor.io/repository/github/simon987/architeuthis/badge)](https://www.codefactor.io/repository/github/simon987/architeuthis)
![GitHub](https://img.shields.io/github/license/simon987/Architeuthis.svg)
[![Build Status](https://ci.simon987.net/buildStatus/icon?job=architeuthis_builds)](https://ci.simon987.net/job/architeuthis_builds/)

*NOTE: this is very WIP* 

HTTP(S) proxy with integrated load-balancing, rate-limiting
and error handling. Built for automated web scraping.

* Strictly obeys configured rate-limiting for each IP & Host
* Seamless exponential backoff retries on timeout or error HTTP codes
* Requires no additional configuration for integration into existing programs

### Typical use case
![user_case](use_case.png)

### Usage

```bash
wget https://simon987.net/data/architeuthis/11_architeuthis.tar.gz
tar -xzf 11_architeuthis.tar.gz

vim config.json # Configure settings here
./architeuthis
```

### Hot config reload

```bash
# Note: this will reset current rate limiters, if there are many active
# connections, this might cause a small request spike and go over
# the rate limits.
./reload.sh
```

### Sample configuration

```json
{
  "addr": "localhost:5050",
  "timeout": "15s",
  "wait": "4s",
  "multiplier": 2.5,
  "retries": 3,
  "proxies": [
    {
      "name": "squid_P0",
      "url": "http://user:pass@p0.exemple.com:8080"
    },
    {
      "name": "privoxy_P1",
      "url": "http://p1.exemple.com:8080"
    }
  ],
  "hosts": [
    {
      "host": "*",
      "every": "500ms",
      "burst": 25,
      "headers": {
        "User-Agent": "Some user agent",
        "X-Test": "Will be overwritten"
      }
    },
    {
      "host": "*.reddit.com",
      "every": "2s",
      "burst": 2,
      "headers": {
        "X-Test": "Will overwrite default"
      }
    }
  ]
}
```

