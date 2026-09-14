# Ajuma Fashion Hub

A small online shop for a dressmaker. It shows the dresses she makes, and turns
every order into a WhatsApp message she already knows how to answer.

Nothing is charged on the site. A buyer chooses a size, a quantity and adds a
note if they want one; pressing **Order on WhatsApp** hands them to WhatsApp
with the whole message already written, so all they do is press send. Payment
and delivery are settled in the chat, which is how this shop already works.

It is the Go standard library and nothing else — no framework, no database
server, no build step, no dependencies:

```sh
go run .
```

The storefront is on <http://localhost:8080/> and the studio on
<http://localhost:8080/admin>. A password for the studio is printed in the
terminal on first run.

It can also hand the whole shop over as flat files, for a host that will not run
a Go process — see [publishing it without a server](#publishing-it-without-a-server).

---

## Quick start

Go 1.24 or newer is the only requirement. Nothing to install, no database to
create, no build step.

**1. Start it.**

```sh
cd ajuma
go run .
```

To keep it off your network while you look at it, bind it to your own machine
only — the default `:8080` listens on every interface:

```sh
go run . -addr 127.0.0.1:8080
```

**2. Copy the password it prints.** With no password configured, one is invented
for that run and printed to the terminal inside a box. It is not written down
anywhere, and it changes every time you restart.

**3. Open the shop.** <http://localhost:8080/> — empty on a first run, which is
the point of the next step.

**4. Sign in to the studio.** <http://localhost:8080/admin>, with the password
from step 2. The lookbook screen offers **Load the samples**: six dresses with
photographs, prices and descriptions, so there is something to look at while you
find your way around.

**5. Set the WhatsApp number.** **Settings → WhatsApp number**, country code
first and no `+` or spaces — a Nigerian number begins `234` and drops the leading
zero, so `0815 560 4988` is typed `2348155604988`. Until a number is there,
every order button is switched off on purpose.

**6. Send yourself an order.** Back on the storefront, open a dress, choose a
size and quantity, and press **Order on WhatsApp**. It hands you to WhatsApp with
the message already written. (Running on `localhost`, the photograph will not
render a preview in the chat — see
[About the photograph, honestly](#about-the-photograph-honestly). Everything else
in the message is there.)

**7. Add one of your own.** **Add a dress** takes a name, a description, a price,
sizes and up to five photographs. They are re-encoded on the way in, so a
phone-camera JPEG straight off a handset is fine.

Everything you enter is kept in `./data` — `catalogue.json` and a `media/`
directory of photographs. Delete that directory to start over with an empty
shop. Stop the server with `Ctrl-C`.

While you are working on the templates or the CSS, `go run . -dev` re-reads them
from disk on every request, so a reload is enough to see a change.

---

## What it does

**For a buyer**

- A front page with a hero piece, the atelier's story, and a lookbook that can
  be narrowed by category or a search word.
- A page per dress: photographs, price, fabric, sizes, delivery note, the
  description the owner wrote, and the order form.
- A live preview of the message they are about to send, above the button that
  sends it.
- A sold-out piece still takes orders — a one-woman atelier can usually cut it
  again — and says so in the message.

**For the owner (the studio, at `/admin`)**

- Add a dress: name, description, price, category, fabric, sizes, up to five
  photographs, and whether it is featured or sold out.
- Reorder the lookbook, change the cover photograph, edit or take a dress down.
- Set the WhatsApp number that every order button opens.
- Write the order message herself, with placeholders filled in per dress, and
  see it previewed as she types.
- Shop name, tagline, story, announcement ribbon, currency sign, location,
  delivery note, email, Instagram and TikTok.
- Six sample dresses on one button, so the shop is never empty while it is
  being set up.

### The sample photographs

The six samples are real photographs, not illustrations: freely-licensed
pictures from Wikimedia Commons — museum studio shots, and a length of achi, the
handwoven cloth of Igalaland — resized and re-encoded by this project's own
image pipeline rather than by an outside tool, so they are the same shape of
file an owner's upload produces. Every photographer and licence is named in
`static/img/lookbook/CREDITS.md`.

They are **placeholders**. The names, prices and descriptions in `seed.go` are
invented sample copy and have nothing to do with the designers whose work is
photographed. Delete the samples once the shop has its own photographs — the
studio's **Load the samples** button refuses to run on a catalogue that already
has dresses in it, so it can never overwrite real work.

## How the order hand-off works

1. The buyer presses **Order on WhatsApp**. The form is a plain `POST` to
   `/dress/{slug}/order`.
2. The server composes the message from the owner's template and the dress,
   percent-encodes it, and replies `303 See Other` to
   `https://wa.me/<number>?text=<message>`.
3. WhatsApp opens — the app on a phone, web on a desktop — with the message in
   the box, addressed to the shop.

The owner's template is filled in from these placeholders. A placeholder that
resolves to nothing takes its whole line with it, so a message never carries an
orphaned label:

| Token | Becomes |
| --- | --- |
| `{brand}` | the shop name |
| `{name}` | the name of the dress |
| `{price}` | the price, formatted with the shop's currency sign |
| `{ref}` | the reference code, e.g. `AJM-004` |
| `{options}` | the size, quantity and note the buyer chose |
| `{image}` | the address of the cover photograph |
| `{link}` | the address of the dress on this site |
| `{fabric}`, `{category}`, `{sizes}` | the dress's own details |
| `{delivery}`, `{location}` | the shop's delivery note and location |

### About the photograph, honestly

**A WhatsApp click-to-chat link can only carry text.** There is no way to
attach a file to `wa.me/...?text=...`; that is WhatsApp's design, not a
shortcut taken here. What the message carries instead is `{image}` — the
absolute address of the cover photograph — and WhatsApp renders a link preview
for it, so the dress appears as a picture in the chat before either side types
anything else.

Two things follow from that:

- The shop has to be reachable from the internet for the preview to appear, and
  **Settings → Web address** must be filled in, because a preview cannot be
  fetched from `localhost`.
- The picture in the chat is a preview of a link, not an attachment. The order
  message names the dress and its reference code as well, so the enquiry is
  unambiguous even where a preview does not render.

---

## Running it in earnest

Every command the project has:

```sh
go run .                      # the shop on :8080, data in ./data
go run . -addr 127.0.0.1:8080 # ... on this machine only
go run . -dev                 # re-read templates and CSS on every request
go run . -hash "a password"   # print a credential for the environment, then exit
go run . -export dist -base … # the shop as flat files, for a static host
go test ./...                 # the suite
go build -o ajuma .           # one binary, templates and assets inside it
```

### The studio password

On first run, with nothing configured, a password is invented and printed once:

```
  ┌─ Ajuma Fashion Hub ─────────────────────────────────────────┐
  │  No admin password was set, so here is one for this run:     │
  │                                                              │
  │      cedar-lantern-quiet-84                                  │
  │                                                              │
  │  It changes on every restart. To keep one password, run      │
  │  'go run . -hash "your password"' and put the line it        │
  │  prints in AJUMA_ADMIN_PASSWORD_HASH.                        │
  └──────────────────────────────────────────────────────────────┘
```

It is never written to disk, so it lasts only as long as that process. To keep a
password, hash it and put the hash in the environment:

```sh
go run . -hash "the password you want"
# pbkdf2-sha256$600000$<salt>$<key>
```

```sh
export AJUMA_ADMIN_PASSWORD_HASH='pbkdf2-sha256$600000$...'
```

`AJUMA_ADMIN_PASSWORD` also works — the plain password, hashed at startup — but
the hash is the better of the two, because it means the password itself is not
sitting in your shell history or your process list.

### Flags and environment

| Flag | Environment | Default | What it does |
| --- | --- | --- | --- |
| `-addr` | `AJUMA_ADDR` | `:8080` | address to listen on |
| `-data` | `AJUMA_DATA` | `data` | where the catalogue and photographs live |
| `-dev` | | off | re-read templates on every request |
| `-trust-proxy` | `AJUMA_TRUST_PROXY=1` | off | believe `X-Forwarded-For`, `-Proto` and `-Host` |
| `-hash` | | | print a credential for the given password and exit |
| `-export` | | | write the shop to this folder as flat files and exit |
| `-base` | | | the address an exported shop will be served from |
| `-v` | | off | log every request, assets included |
| | `AJUMA_ADMIN_PASSWORD_HASH` | | the studio credential |
| | `AJUMA_ADMIN_PASSWORD` | | a plain password, hashed at startup |

`SIGINT` or `SIGTERM` stops the server, with fifteen seconds for requests in
flight to finish.

### Behind a reverse proxy

```nginx
location / {
    proxy_pass         http://127.0.0.1:8080;
    proxy_set_header   Host              $host;
    proxy_set_header   X-Forwarded-Proto $scheme;
    proxy_set_header   X-Forwarded-Host  $host;
    proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
}
```

Start it with `-trust-proxy` so it knows the request arrived over TLS and which
client it came from — that is what puts `Secure` on the session cookie, `https://`
in the addresses it writes into order messages, and the real address in the
sign-in throttle. Without a proxy in front, leave the flag off: those headers are
trivial for a client to forge, so they are ignored unless you say otherwise.

Raise the proxy's body limit as well — five photographs of 8 MB each is the most
one submission can carry, so `client_max_body_size 48m;`.

Keep `./data` on a real disk and back it up; it is the whole shop. Set
**Settings → Web address** to the public address once it has one.

---

## Publishing it without a server

Nothing a visitor does needs Go running. Every public page is a `GET` that reads
the catalogue and writes HTML, and the one thing that looks like a transaction —
ordering — ends at WhatsApp, on someone else's servers. So the shop can be
exported to flat files and handed to any static host, while the studio stays on
the owner's own machine, next to the catalogue and the photographs.

```sh
go run . -export dist -base https://ajuma-fashion-hub.netlify.app
```

```
  exporting Ajuma Fashion Hub to dist

  index.html                                 19 KB
  dress/ojoma-achi-set/index.html            16 KB
  ...
  c/wrapper/index.html                       11 KB
  robots.txt                                 116 B
  sitemap.xml                                851 B
  404.html                                    4 KB
  static/                                   1.9 MB  (12 files)
  media/                                       0 B  (0 files)
  _headers                                   580 B

  28 files, 2.1 MB, in dist
  the shop will be served from https://ajuma-fashion-hub.netlify.app
```

`media/` is the photographs the studio has been given; a shop still on the sample
set has none of its own, and its pictures travel inside `static/`.

`-base` is not optional, and the export stops rather than guess. An order message
carries absolute links to the dress and to its photograph — that link is what
makes WhatsApp show the picture — and a folder of files has no request to work
the address out from. Record it once under **Settings → Web address** and the
flag can be left off.

The folder is safe to re-export over: the first run leaves a `.ajuma-export`
marker in it, and later runs refuse to clear a folder that has no marker, so a
mistyped `-export .` cannot eat the source tree.

### Netlify, by dragging the folder

1. Go to [app.netlify.com/drop](https://app.netlify.com/drop) and drop any empty
   folder on it, just to make the site and find out its address. Rename it under
   **Site configuration → Change site name** to something you want to keep, and
   note the full `https://…netlify.app`.
2. Export with that address: `go run . -export dist -base https://your-name.netlify.app`
3. Drag `dist` onto the same drop page. Every later change to the shop is the
   same two steps — export, drag.

If you attach a custom domain, export again with the new address, or the links
inside the WhatsApp messages will keep pointing at the old one.

Two files in the folder are for the host rather than the visitor. `404.html` is
what Netlify serves for an address matching no file, so a wrong URL still lands
on the shop's own error page. `_headers` carries the security headers the Go
server sets for itself — the same content security policy, minus the per-request
nonce a CDN has no way to mint. Both are conventions plenty of static hosts
share; on one that ignores `_headers` the shop still works, just without the
headers.

### What is different in an exported shop

Three things a server was doing, and what happens in its absence:

- **The category chips** were a query string the home page read for itself.
  Exported, each becomes a real page: `/c/wrapper/`.
- **The search box** is left out. It asked the server a question, and there is no
  server to ask; a box that silently does nothing is worse than no box.
- **The order button** becomes a plain link to `wa.me` carrying the default
  message, instead of a form the server answers. With JavaScript on, the page
  rewrites that link as the buyer picks a size or types a note — so the size and
  quantity fields only appear when the script is there to carry them, and the
  page never offers a choice it cannot pass on. Sizes are still listed on the
  page and still travel in the message either way.

The studio itself is not exported — no `/admin` page and no session is in the
folder, and the binary that made it never even asks for a password. To edit the
shop, run it locally, work in the studio, and export again.

---

## How it is built

The Go standard library only. `net/http` routing with Go 1.22 patterns,
`html/template` for the pages, `image/*` for the photographs — no third-party
modules, so `go.mod` has no `require` block at all.

**It works with JavaScript switched off.** Every feature is HTML and CSS: the
order form is a `POST` that redirects, the photograph gallery is radio inputs
and a sibling selector, and "are you sure?" is a `<details>` element rather than
a dialogue box. The one script in the project — a hundred and twenty lines —
redraws the message preview as the buyer changes size or quantity. Turn it off
and the preview simply shows the message for one of each; the server composes
the message that is actually sent, either way. Exported to a static host that
same script keeps the WhatsApp link in step instead, and the fields it carries
stay hidden until it has run — so a page without it still orders, it just orders
the piece rather than a particular size of it.

**Photographs are re-encoded, not stored.** An upload is decoded, its EXIF
orientation baked in, flattened onto white if it has transparency, downscaled to
fit 1400x2000 with a box filter averaged in linear light, and written out as
JPEG under a name the server chose. Nothing the browser sent — filename,
metadata, or the bytes themselves — reaches the disk. The average colour is kept
as a hex tint so a card has something to show while its photograph loads.

**Storage is one JSON document**, `data/catalogue.json`, held in memory behind a
`sync.RWMutex` and written by creating a temporary file and renaming it over the
old one, so a crash halfway through a save cannot leave a half-written
catalogue. Prices are `int64` minor units — kobo, cents — never floats.

### Security

- Passwords are PBKDF2-HMAC-SHA256, 600,000 iterations, compared in constant
  time.
- Sessions are held in memory (`ajuma_session`, scoped to `/admin`, eight
  hours), `HttpOnly`, `SameSite=Lax`, and `Secure` when the request arrived over
  TLS.
- Every studio form carries a per-session CSRF token; every studio `POST` is
  also checked against `Sec-Fetch-Site` and `Origin`. The sign-in form is the
  one exception, since there is no session yet to bind a token to — it relies on
  the origin check.
- Failed sign-ins are throttled.
- Each response carries a Content Security Policy with a fresh nonce, so the
  one script on the page is the only thing allowed to run: `default-src
  'self'`, `object-src 'none'`, `frame-ancestors 'none'`, and `form-action
  'self' https://wa.me` — the order redirect is the only place a form may go.
- Uploads are capped at 8 MB and accepted on what decodes, not on what the
  filename claims. `/media/` serves files out of the uploads directory and
  cannot be walked out of it.

### The files

```
main.go             flags, credentials, routes, graceful shutdown
app.go              the App itself and what it hangs on to
middleware.go       the nonce, the security headers, request logging
public.go           the storefront: home, dress, the order redirect, sitemap
admin.go            sign in, sign out, the lookbook screen
admin_dress.go      the add and edit forms, and the photograph actions
admin_settings.go   the settings screen and the message tokens
seed.go             the six sample dresses
export.go           writing the whole storefront out as flat files
flash.go            the one-shot messages above a studio screen
store.go            the JSON catalogue
models.go           Dress, Image, and the shapes around them
settings.go         the shop's own settings
whatsapp.go         composing the message and the wa.me link
media.go            decode, orient, flatten, downscale, encode
exif.go             reading orientation out of a JPEG
auth.go             PBKDF2, sessions, CSRF
render.go           embedded templates and their functions
templates/          layouts, pages, partials  (1,141 lines)
static/             base.css, site.css, admin.css, one 120-line script
static/img/lookbook/ the six sample photographs, and CREDITS.md
data/               catalogue.json and media/ — created on first run
```

### Tests

```
go test -cover .
ok  ajuma  2.740s  coverage: 87.0% of statements
```

Table-driven, standard library only, and behavioural where it matters: the
suite signs in through `httptest` with a cookie jar and drives the real forms,
uploads real multipart photographs, checks that a traversal path cannot reach
the catalogue through `/media/`, that "Exif" appearing inside image data cannot
fool the orientation reader, and that all eight orientations come out the right
way up. What is left uncovered is process lifecycle and disk-error branches.

---

Built by hand with Go, HTML and CSS.
