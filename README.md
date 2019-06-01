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

### Example usage with wget
```bash
export http_proxy="http://localhost:5050"
# --no-check-certificates is necessary for https mitm
# You don't need to specify user-agent if it's already in your config.json
wget -m -np -c --no-check-certificate -R index.html* http http://ca.releases.ubuntu.com/
```

With `"every": "500ms"` and a single proxy, you should see
```
...
level=trace msg=Sleeping wait=414.324437ms
level=trace msg="Routing request" conns=0 proxy=p0 url="http://ca.releases.ubuntu.com/12.04/SHA1SUMS.gpg"
level=trace msg=Sleeping wait=435.166127ms
level=trace msg="Routing request" conns=0 proxy=p0 url="http://ca.releases.ubuntu.com/12.04/SHA256SUMS"
level=trace msg=Sleeping wait=438.657784ms
level=trace msg="Routing request" conns=0 proxy=p0 url="http://ca.releases.ubuntu.com/12.04/SHA256SUMS.gpg"
level=trace msg=Sleeping wait=457.06543ms
level=trace msg="Routing request" conns=0 proxy=p0 url="http://ca.releases.ubuntu.com/12.04/ubuntu-12.04.5-alternate-amd64.iso"
level=trace msg=Sleeping wait=433.394361ms
...
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

