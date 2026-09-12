# Design decisions — `webtyp/server`

This file explains **why** parts of `webtyp/server` are built the way they are,
including the options that were considered and dropped. It is the place to look
when you read the code, think "this could have been done more simply," and want
to know whether that was already tried.

It is a companion to [`ARCHITECTURE.md`](ARCHITECTURE.md), which describes *what*
the pieces are and how they fit together. This one carries the arguments, so that
document can stay short.

---

## Why the dev certificate uses `smallstep/truststore` and not `mkcert`

### The situation this comes from

Start a WebTyp app in development:

```bash
webtyp dev
```

A browser opens on **`https://localhost:8080`** — note the `s` — and shows the
page with no security warning, as if the site had a certificate bought from a
real provider.

That is unusual, and it is the thing this section explains. Two questions hide
behind it:

- **Why HTTPS at all in development?** Because serving plain HTTP locally and
  HTTPS in production hides bugs that only surface after deploying — `Secure`
  cookies that never get sent, `SameSite=None`, HSTS, mixed content — and because
  a phone on the same Wi-Fi cannot install a PWA from an `http://` address. The
  full reasoning lives in [ARCHITECTURE.md → Development TLS](ARCHITECTURE.md#development-tls).
- **Why no warning?** Normally a certificate a program made for itself produces
  *"Your connection is not private"* in every browser. Avoiding that is what
  brought the `github.com/smallstep/truststore` dependency into
  [`../go.mod`](../go.mod), and this section justifies that choice.

### Three words you need first

- **Certificate** — a file the server shows the browser saying "I am
  `localhost`," signed by somebody else so it cannot be forged.
- **Certificate Authority (CA)** — whoever does that signing. Public websites pay
  a commercial CA; in development the server invents its own, a one-off CA that
  exists only on your machine.
- **Trust store** — the list of CAs your operating system believes. Firefox,
  Chrome, macOS, Windows and Linux each keep one, in different formats and
  different places. A certificate signed by a CA that is *not* in that list is
  what triggers the browser warning.

So the warning disappears only if the dev CA is added to that list. That single
sentence is the whole problem this dependency solves.

### What the Go standard library already covers, and what it does not

`httpd` builds the certificates itself, with nothing but `crypto/x509` from the
standard library, all of it in
[`../httpd/devcert.go`](../httpd/devcert.go): a CA called `WebTyp Dev CA`, and a
server certificate signed by it whose list of valid addresses covers `localhost`,
the loopback addresses and every LAN address the machine currently has. Both land
in `~/.webtyp/httpd/certs`.

The standard library stops there. **Adding a CA to the operating system's trust
store is not a Go problem — it is five different problems**, one per platform:

| Platform | What has to happen |
|---|---|
| macOS | run `security add-trusted-cert` |
| Linux | drop the file in the system anchors and run `update-ca-certificates` or `trust anchor` |
| Firefox & Chrome on Linux | separately, edit each NSS database with `certutil` |
| Windows | call the `CertAddEncodedCertificateToStore` syscall |
| Java apps | add it to the JVM keystore |

That list, not certificate generation, is the gap a dependency is being paid to
fill.

### The two candidates, and why they are not comparable

[`mkcert`](https://github.com/FiloSottile/mkcert) is the well-known tool for
exactly this, and it is the first thing anyone suggests.
[`truststore`](https://github.com/smallstep/truststore) is the library that
Smallstep (the authors of `step-ca`) produced by lifting mkcert's
`truststore_darwin.go` / `_linux.go` / `_windows.go` / `_nss.go` / `_java.go`
files out of it and publishing them as an importable module. Its README says so
directly: *"Based on https://github.com/FiloSottile/mkcert."*

So the per-platform logic in the table above is **the same code** in both. What
differs is the shape it is delivered in:

| | `FiloSottile/mkcert` | `smallstep/truststore` |
|---|---|---|
| Kind | a **command-line program** you run | a **library** you import |
| Go package | `package main` | `package truststore` |
| Does | generate a CA, generate certificates, install them, print a friendly CLI | **only** install/uninstall a certificate in the trust stores |
| Usable from Go code | **No** | Yes — `Install`, `Uninstall`, `InstallFile`, plus options like `WithJava()`, `WithFirefox()`, `WithNoSystem()` |
| Non-stdlib dependencies | its own CLI stack | `howett.net/plist`, and nothing else |
| License | BSD-3 | Apache-2.0 |

### The decision, in three reasons

1. **mkcert cannot be imported.** Everything in it lives in `package main`, which
   in Go means no other program can call into it. Using it would mean running it
   as a subprocess — `exec.Command("mkcert", ...)` — which requires the developer
   to have installed mkcert first through Homebrew, apt or `go install`, plus
   code to parse its output and to handle the "it is not installed" case. That
   destroys the property that `webtyp dev` is one binary that needs no other
   tooling on the machine.

2. **Only a fifth of mkcert is wanted.** `httpd` already mints its own chain, in
   its own directory, with its own naming, and with a list of addresses it
   recomputes when the laptop changes network. It also publishes the server
   certificate's public-key fingerprint for the browser launch flag and serves
   the CA file at `CAPath` so a phone can install it. mkcert has its own opinions
   about all of that; adopting it would mean fighting it, not reusing it. The one
   piece genuinely missing is "install this CA," and that is the entirety of what
   `truststore` does — the codebase calls exactly **one** function from it,
   [`truststore.Install`](../httpd/devcert.go#L173).

3. **Writing it again is not worth it.** It is on the order of a thousand lines
   of per-platform code — NSS databases, the Java keystore, Windows syscalls —
   that mkcert has been proving in the field since 2018. Nothing about WebTyp
   would be better for having its own copy.

### What this costs us

- **`truststore` is a quiet project** (v0.13.0, infrequent releases). The
  exposure is one function call against an API with no reason to change, and the
  module is five files under Apache-2.0 — if it were ever abandoned, vendoring it
  is a small, available exit.
- **On Linux the install runs `sudo`** internally and may ask for your password
  the first time. Because of that the step is best-effort: a failure is logged as
  a warning and the server keeps going. Setting the environment variable
  `WEBTYP_DEVCERT_SKIP_TRUSTSTORE` to any value skips it altogether — the test
  runner and CI set it, so `gotest` never stops waiting for a password prompt.
  TLS itself never depends on this step.

### Options that were considered and dropped

- **Run `mkcert` as a subprocess.** Adds a prerequisite the developer must
  install by hand, and hands control of the CA name, the certificate directory
  and the address list to a tool that has its own conventions.
- **Implement trust-store installation inside WebTyp.** A large, platform-specific
  surface to maintain, with no advantage over the existing implementation.
- **Never touch the trust store at all.** This already exists as the fallback
  path: the browser WebTyp launches is told to accept this one certificate, and
  any other device can install the CA by hand from `CAPath`. It works — but every
  client WebTyp did not launch itself (a second browser, `curl`, a native app)
  goes back to showing the warning. Removing that friction is precisely what the
  dependency buys.
