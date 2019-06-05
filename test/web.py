import time

from flask import Flask, Response

app = Flask(__name__)


@app.route("/")
def slow():
    time.sleep(90)
    return "Hello World!"


@app.route("/echo/<text>")
def echo(text):
    return text


@app.route("/echoh/<text>")
def echoh(text):
    return Response(response="see X-Test header", status=404, headers={
        "X-Test": text,
    })


@app.route("/500")
def e500():
    return Response(status=500)


@app.route("/404")
def e404():
    return Response(status=404)


@app.route("/403")
def e403():
    return Response(status=403)


if __name__ == "__main__":
    app.run(port=9999)
