import asyncio
import sys

import requests
from proxybroker import Broker, Checker

if len(sys.argv) < 2:
    print("Architeuthis url required")
    quit(0)

ARCHITEUTHIS_URL = sys.argv[1]


def add_to_architeuthis(name, url):
    r = requests.get(ARCHITEUTHIS_URL + "/add_proxy?name=%s&url=%s" % (name, url))
    print("ADD %s <%d>" % (name, r.status_code))


async def add(proxies):
    while True:
        proxy = await proxies.get()
        if proxy is None:
            break

        url = "http://%s:%d" % (proxy.host, proxy.port)
        name = "%s_%d" % (proxy.host, proxy.port)

        add_to_architeuthis(name, url)


proxies = asyncio.Queue()
broker = Broker(proxies)
tasks = asyncio.gather(broker.find(types=['HTTPS'], limit=300), add(proxies))

loop = asyncio.get_event_loop()
loop.run_until_complete(tasks)
