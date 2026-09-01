from flask import Flask, render_template

app = Flask(__name__)

@app.route("/")
def home():
    dresses = [
        {"name": "Red Gown", "image": "dress1.jpg"},
        {"name": "Blue Dress", "image": "dress2.jpg"},
    ]
    return render_template("index.html", dresses=dresses)

if __name__ == "__main__":
    app.run(debug=True)