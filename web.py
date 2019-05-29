from flask import Flask
import time

app = Flask(__name__)


@app.route("/")
def hello():
    time.sleep(3)
    return "Hello World!"


if __name__ == "__main__":
    app.run(port=9999)
