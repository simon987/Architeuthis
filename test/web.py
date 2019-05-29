from flask import Flask, Response
import time

app = Flask(__name__)


@app.route("/")
def hello():
    time.sleep(90)
    return "Hello World!"


@app.route("/500")
def e500():
    return Response(status=500)


@app.route("/404")
def e404():
    time.sleep(0.5)
    return Response(status=404)


if __name__ == "__main__":
    app.run(port=9999)
