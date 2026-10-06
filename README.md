# AJ FASHION AND DESIGN

AJ FASHION AND DESIGN is a fashion community where local tailors can share sewn outfits and designs. Members sign in before browsing the gallery, and can post photos and descriptions under their own profile.

## Run locally

Requires Go 1.24 or newer.

```sh
go run .
```

Open <http://localhost:8080/>. Create an account to enter the gallery. Existing members can sign in with their email address and password. For template and CSS changes, run `go run . -dev` to load those files from disk without rebuilding.

## Member features

- Account registration and sign-in with salted PBKDF2 password hashes.
- Sign-in history on the member profile.
- Password recovery through one-time, expiring email codes.
- A gallery of outfit photographs and descriptions.
- Tailors can publish a design with up to five photographs and a description.
- A private profile page lists a member's own designs.
- Visitors must sign in before viewing the gallery or individual designs.
- The green theme has been replaced with a pink background and red accents.

The first visit to an empty gallery shows six sample designs. They are placeholders; their photo credits are in `static/img/lookbook/CREDITS.md`.

## Stored data

The `data` directory contains `members.json`, `catalogue.json`, and uploaded photos in `media/`. Back up this folder to retain member accounts and designs. Passwords are stored as salted hashes. Sessions expire after eight hours and are cleared when the server restarts.

### Password recovery email

Set these environment variables to enable forgotten-password emails. The SMTP
server must offer STARTTLS; the default port is `587`. Until these are
configured, the app cannot deliver recovery codes.

```text
AJUMA_SMTP_HOST=smtp.example.com
AJUMA_SMTP_PORT=587
AJUMA_SMTP_USER=mailer@example.com
AJUMA_SMTP_PASSWORD=your-mail-provider-password
AJUMA_SMTP_FROM=mailer@example.com
```

Static export is not supported because a static host cannot enforce member sign-in. Run the Go server to keep the gallery behind accounts.
