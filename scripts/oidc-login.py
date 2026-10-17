#!/usr/bin/env python3
"""A family member's "Mit Familienkonto anmelden", end to end, as a browser
does it: the app sends us to Authelia, we log in with LLDAP's password,
give consent, Authelia sends us back with a code, the app exchanges it at
Authelia's token endpoint. Test tooling for vm-install (#254), never the
box's: it proves the app's OIDC settings, Authelia's client and the path
between the app and auth.<domain> together.

Usage: oidc-login.py <app> <app base url>   (app: immich, audiobookshelf)
Env:   OIDC_USER, OIDC_PASSWORD, OIDC_CA (the CA file Caddy's certs chain to),
       OIDC_EMAIL (what Immich must say it logged in)."""
import http.cookiejar, json, os, ssl, sys, urllib.error, urllib.parse, urllib.request

E = os.environ


class Stay(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None  # every redirect is ours to look at


jar = http.cookiejar.CookieJar()
web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), Stay(),
                                  urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=E["OIDC_CA"])))


def req(method, url, body=None):
    """status, JSON body (or None), Location"""
    r = urllib.request.Request(url, data=json.dumps(body).encode() if body is not None else None, method=method,
                               headers={"Content-Type": "application/json", "Accept": "application/json"})
    try:
        resp = web.open(r, timeout=60)
    except urllib.error.HTTPError as e:
        resp = e
    raw = resp.read()
    try:
        data = json.loads(raw) if raw else None
    except ValueError:
        data = raw.decode(errors="replace")[:300]  # a text answer, shown when it is an error
    return resp.status if hasattr(resp, "status") else resp.code, data, resp.headers.get("Location")


def fail(msg):
    print("FAIL:", msg, file=sys.stderr)
    sys.exit(1)


def authelia(url, client_id):
    """from the authorization URL to the app's redirect URI with a code"""
    auth = urllib.parse.urlparse(url).netloc
    logged_in = False
    for _ in range(12):
        code, _, loc = req("GET", url)
        if not loc:
            fail(f"{url.split('?')[0]}: HTTP {code} and no redirect")
        loc = urllib.parse.urljoin(url, loc)
        u = urllib.parse.urlparse(loc)
        q = dict(urllib.parse.parse_qsl(u.query))
        if u.netloc != auth:
            return loc
        flow = q.get("flow_id")
        if not logged_in:
            body = {"username": E["OIDC_USER"], "password": E["OIDC_PASSWORD"], "keepMeLoggedIn": False, "targetURL": url}
            if flow:
                body.update(flow=q.get("flow", "openid_connect"), flowID=flow)
            code, res, _ = req("POST", f"https://{auth}/api/firstfactor", body)
            if code != 200 or not isinstance(res, dict) or res.get("status") != "OK":
                fail(f"Authelia refused the first factor: HTTP {code} {res}")
            print("authelia: logged in as", E["OIDC_USER"])
            logged_in = True
            continue
        if not flow:
            fail(f"Authelia sent us to {u.path} with no flow to consent to")
        code, res, _ = req("POST", f"https://{auth}/api/oidc/consent", {"flow_id": flow, "client_id": client_id, "consent": True, "pre_configure": False})
        if code != 200 or not isinstance(res, dict) or res.get("status") != "OK":
            fail(f"Authelia refused the consent: HTTP {code} {res}")
        print("authelia: consent given for", client_id)
        url = urllib.parse.urljoin(url, res["data"]["redirect_uri"])
    fail("Authelia never sent us back to the app")


def immich(base):
    code, body, _ = req("POST", base + "/api/oauth/authorize", {"redirectUri": base + "/auth/login"})
    if code not in (200, 201) or not isinstance(body, dict) or not body.get("url"):
        fail(f"immich /api/oauth/authorize: HTTP {code} {body} (Immich could not reach Authelia's discovery?)")
    print("immich: sends the browser to", body["url"].split("?")[0])
    back = authelia(body["url"], "immich")
    if "code=" not in back:
        fail(f"Authelia sent us back without a code: {back}")
    code, body, _ = req("POST", base + "/api/oauth/callback", {"url": back})
    if code not in (200, 201) or not isinstance(body, dict) or body.get("userEmail") != E["OIDC_EMAIL"]:
        fail(f"immich /api/oauth/callback (the code exchange): HTTP {code} {body}")
    print(f"immich: code exchanged, logged in as {body['userEmail']}")


def audiobookshelf(base):
    # as ABS's login page does it: ABS serves under its router base path, /audiobookshelf, and takes
    # only a callback below it (OidcAuthStrategy.isValidWebCallbackUrl)
    code, body, loc = req("GET", base + "/audiobookshelf/auth/openid?callback=" + urllib.parse.quote(base + "/audiobookshelf/login", safe=""))
    if not loc or not loc.startswith("https://auth."):
        fail(f"audiobookshelf /auth/openid: HTTP {code}, to {loc}: {body}")
    print("audiobookshelf: sends the browser to", loc.split("?")[0])
    back = authelia(loc, "audiobookshelf")
    if "code=" not in back:
        fail(f"Authelia sent us back without a code: {back}")
    code, body, loc = req("GET", back)  # ABS exchanges the code at Authelia's token endpoint
    if not loc or "accessToken=" not in loc:
        fail(f"audiobookshelf /auth/openid/callback (the code exchange): HTTP {code}, to {loc}: {body}")
    print("audiobookshelf: code exchanged, logged in")


{"immich": immich, "audiobookshelf": audiobookshelf}[sys.argv[1]](sys.argv[2].rstrip("/"))
