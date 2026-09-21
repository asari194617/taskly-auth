#!/usr/bin/env python3
"""Exercise the local authorization-code, PKCE, rotation, and revocation flow."""

import base64
import hashlib
import http.cookiejar
import json
import os
import secrets
import urllib.error
import urllib.parse
import urllib.request


api = os.environ.get("AUTH_ISSUER", "http://localhost:3067")
ui = os.environ.get("AUTH_UI_URL", "http://localhost:3068")
client_id = os.environ.get("OAUTH_CLIENT_ID", "taskly-web")
client_secret = os.environ.get("OAUTH_CLIENT_SECRET", "replace-with-a-long-random-secret")
redirect_uri = os.environ.get("OAUTH_REDIRECT_URI", "http://localhost:3063/auth/oauth/callback")
email = os.environ["AUTH_DEV_EMAIL"]
password = os.environ["AUTH_DEV_PASSWORD"]


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), NoRedirect())


def request(url, body=None, headers=None):
    if body is not None:
        body = urllib.parse.urlencode(body).encode()
    req = urllib.request.Request(url, body, headers or {})
    try:
        return opener.open(req)
    except urllib.error.HTTPError as exc:
        return exc


def token_request(body):
    basic = base64.b64encode(f"{client_id}:{client_secret}".encode()).decode()
    return request(api + "/oauth/token", body, {"Authorization": "Basic " + basic})


verifier = secrets.token_urlsafe(32)
challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
state = secrets.token_urlsafe(32)
params = urllib.parse.urlencode({
    "response_type": "code", "client_id": client_id, "redirect_uri": redirect_uri,
    "scope": "taskly:login", "state": state, "code_challenge": challenge,
    "code_challenge_method": "S256",
})
response = request(api + "/oauth/authorize?" + params)
assert response.status == 302, response.status
login_url = response.headers["Location"]
assert login_url.startswith(ui + "/login?"), login_url
request_id = urllib.parse.parse_qs(urllib.parse.urlparse(login_url).query)["request_id"][0]

body = json.dumps({"email": email, "password": password}).encode()
response = opener.open(urllib.request.Request(
    api + "/auth/login", body,
    {"Content-Type": "application/json", "Origin": ui, "X-Auth-UI": "1"},
))
assert response.status == 200, response.status

response = request(api + "/oauth/authorize?request_id=" + urllib.parse.quote(request_id))
assert response.status == 302, response.status
callback = urllib.parse.urlparse(response.headers["Location"])
assert response.headers["Location"].startswith(redirect_uri), response.headers["Location"]
callback_query = urllib.parse.parse_qs(callback.query)
assert callback_query["state"] == [state]
assert callback_query["iss"] == [api]
code = callback_query["code"][0]

response = token_request({"grant_type": "authorization_code", "code": code,
                          "redirect_uri": redirect_uri, "code_verifier": verifier})
assert response.status == 200, response.read()
tokens = json.load(response)
assert tokens["token_type"] == "Bearer"
assert token_request({"grant_type": "authorization_code", "code": code,
                      "redirect_uri": redirect_uri, "code_verifier": verifier}).status == 400

response = request(api + "/oauth/userinfo", headers={"Authorization": "Bearer " + tokens["access_token"]})
assert response.status == 200
assert json.load(response)["email"] == email

basic = base64.b64encode(f"{client_id}:{client_secret}".encode()).decode()
response = request(api + "/oauth/introspect", {"token": tokens["access_token"]},
                   {"Authorization": "Basic " + basic})
assert json.load(response)["active"] is True

response = token_request({"grant_type": "refresh_token", "refresh_token": tokens["refresh_token"]})
assert response.status == 200
rotated = json.load(response)
assert rotated["refresh_token"] != tokens["refresh_token"]
assert token_request({"grant_type": "refresh_token", "refresh_token": tokens["refresh_token"]}).status == 400
assert token_request({"grant_type": "refresh_token", "refresh_token": rotated["refresh_token"]}).status == 400

for access_token in (tokens["access_token"], rotated["access_token"]):
    response = request(api + "/oauth/introspect", {"token": access_token},
                       {"Authorization": "Basic " + basic})
    assert json.load(response)["active"] is False

response = request(api + "/oauth/revoke", {"token": tokens["access_token"]},
                   {"Authorization": "Basic " + basic})
assert response.status == 200
response = request(api + "/oauth/introspect", {"token": tokens["access_token"]},
                   {"Authorization": "Basic " + basic})
assert json.load(response)["active"] is False
print("OAuth 2.1 local smoke passed")
