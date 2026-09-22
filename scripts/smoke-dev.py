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


def ui_json_request(path, body=None):
    data = None if body is None else json.dumps(body).encode()
    headers = {"Origin": ui, "X-Auth-UI": "1"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(api + path, data, headers)
    try:
        return opener.open(req)
    except urllib.error.HTTPError as exc:
        return exc


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

response = request(login_url)
assert response.status == 200, response.status
assert b'id="passkey"' in response.read()
passkey_begin = ui_json_request("/auth/passkey/begin", {"email": email})
assert passkey_begin.status == 401, passkey_begin.read()
assert ui_json_request("/auth/passkeys").status == 401
assert ui_json_request("/auth/passkey/register/begin", {}).status == 401

body = json.dumps({"email": email, "password": password}).encode()
response = opener.open(urllib.request.Request(
    api + "/auth/login", body,
    {"Content-Type": "application/json", "Origin": ui, "X-Auth-UI": "1"},
))
assert response.status == 200, response.status

response = request(ui + "/passkeys")
assert response.status == 200, response.status
assert b"/passkeys.js" in response.read()
response = ui_json_request("/auth/passkeys")
assert response.status == 200, response.read()
assert json.load(response)["passkeys"] == []
response = ui_json_request("/auth/passkey/register/begin", {})
assert response.status == 200, response.read()
registration = json.load(response)
assert registration["options"]["publicKey"]["rp"]["id"] == "localhost"
assert registration["options"]["publicKey"]["challenge"]
assert len(registration["request_id"]) >= 43
response = ui_json_request("/auth/passkey/register/finish", {
    "request_id": registration["request_id"],
    "credential": {"id": "invalid", "rawId": "aW52YWxpZA", "type": "public-key", "response": {}},
})
assert response.status == 401, response.read()
assert json.load(ui_json_request("/auth/passkeys"))["passkeys"] == []

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

public_client_id = os.environ.get("OAUTH_DEV_PUBLIC_CLIENT_ID")
public_redirect_uri = os.environ.get("OAUTH_DEV_PUBLIC_REDIRECT_URI")
if public_client_id and public_redirect_uri:
    public_verifier = secrets.token_urlsafe(32)
    public_challenge = base64.urlsafe_b64encode(
        hashlib.sha256(public_verifier.encode()).digest()
    ).rstrip(b"=").decode()
    public_state = secrets.token_urlsafe(32)
    public_params = urllib.parse.urlencode({
        "response_type": "code", "client_id": public_client_id,
        "redirect_uri": public_redirect_uri, "scope": "taskly:login",
        "state": public_state, "code_challenge": public_challenge,
        "code_challenge_method": "S256",
    })
    response = request(api + "/oauth/authorize?" + public_params)
    assert response.status == 302, response.status
    public_callback = urllib.parse.urlparse(response.headers["Location"])
    assert response.headers["Location"].startswith(public_redirect_uri)
    public_query = urllib.parse.parse_qs(public_callback.query)
    assert public_query["state"] == [public_state]
    public_code = public_query["code"][0]

    public_exchange = {
        "grant_type": "authorization_code", "code": public_code,
        "redirect_uri": public_redirect_uri, "code_verifier": public_verifier,
    }
    assert token_request(public_exchange).status == 400
    response = request(api + "/oauth/token", {**public_exchange, "client_id": public_client_id})
    assert response.status == 200, response.read()
    public_tokens = json.load(response)
    response = request(api + "/oauth/introspect", {"token": public_tokens["access_token"]},
                       {"Authorization": "Basic " + basic})
    introspection = json.load(response)
    assert introspection["active"] is True
    assert introspection["client_id"] == public_client_id

    public_refresh = {"grant_type": "refresh_token", "client_id": public_client_id,
                      "refresh_token": public_tokens["refresh_token"]}
    assert token_request(public_refresh).status == 401
    response = request(api + "/oauth/token", public_refresh)
    assert response.status == 200, response.read()
    public_rotated = json.load(response)
    assert request(api + "/oauth/token", public_refresh).status == 400
    for access_token in (public_tokens["access_token"], public_rotated["access_token"]):
        response = request(api + "/oauth/introspect", {"token": access_token},
                           {"Authorization": "Basic " + basic})
        assert json.load(response)["active"] is False

response = request(ui + "/logout")
assert response.status == 200, response.status
assert b"/logout.js" in response.read()
response = request(api + "/auth/logout", {}, {"Origin": ui, "X-Auth-UI": "1"})
assert response.status == 204, response.read()
response = request(api + "/oauth/authorize?" + params)
assert response.status == 302, response.status
assert response.headers["Location"].startswith(ui + "/login?")

print("OAuth 2.1 local smoke passed")
