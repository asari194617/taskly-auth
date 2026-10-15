#!/usr/bin/env python3
"""Exercise the local authorization-code, PKCE, rotation, and revocation flow."""

import base64
import csv
import datetime
import hashlib
import hmac
import http.cookiejar
import io
import json
import os
import secrets
import struct
import time
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
internal_token = os.environ.get(
    "AUTH_INTERNAL_TOKEN", "replace-with-an-independent-random-internal-token"
)


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


def internal_block_request(user_id, payload, token=internal_token):
    req = urllib.request.Request(
        api + f"/internal/users/{user_id}/block",
        json.dumps(payload).encode(),
        {"Authorization": "Bearer " + token, "Content-Type": "application/json"},
        method="PUT",
    )
    try:
        return opener.open(req)
    except urllib.error.HTTPError as exc:
        return exc


def internal_lifecycle_request(user_id, payload=None, token=internal_token):
    method = "DELETE" if payload is None else "PUT"
    data = None if payload is None else json.dumps(payload).encode()
    headers = {"Authorization": "Bearer " + token}
    if data is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(
        api + f"/internal/users/{user_id}" + ("" if payload is None else "/lifecycle"),
        data,
        headers,
        method=method,
    )
    try:
        return opener.open(req)
    except urllib.error.HTTPError as exc:
        return exc


def internal_user_request(method, path, payload=None, token=internal_token):
    data = None if payload is None else json.dumps(payload).encode()
    headers = {"Authorization": "Bearer " + token}
    if data is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(api + path, data, headers, method=method)
    try:
        return opener.open(req)
    except urllib.error.HTTPError as exc:
        return exc


def totp_code(secret):
    padding = "=" * ((8 - len(secret) % 8) % 8)
    key = base64.b32decode(secret + padding)
    counter = int(time.time()) // 30
    digest = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    value = struct.unpack(">I", digest[offset:offset + 4])[0] & 0x7FFFFFFF
    return f"{value % 1000000:06d}"


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
assert b'id="app"' in response.read()
response = request(ui + "/password")
assert response.status == 200, response.status
assert b'id="app"' in response.read()
passkey_begin = ui_json_request("/auth/passkey/begin", {"email": email})
assert passkey_begin.status == 401, passkey_begin.read()
assert ui_json_request("/auth/passkeys").status == 401
assert ui_json_request("/auth/passkey/register/begin", {}).status == 401
assert ui_json_request("/auth/email-code/settings").status == 401
assert ui_json_request("/auth/recovery/status").status == 401
assert ui_json_request("/auth/recovery/generate", {}).status == 401
assert ui_json_request("/auth/totp/status").status == 401
assert ui_json_request("/auth/totp/setup", {}).status == 401
assert ui_json_request("/auth/security/step-up/status").status == 401
assert ui_json_request("/auth/login-history").status == 401
assert ui_json_request("/auth/login-history/export").status == 401

login_started_at = time.time()
body = json.dumps({"email": email, "password": password}).encode()
response = opener.open(urllib.request.Request(
    api + "/auth/login", body,
    {"Content-Type": "application/json", "Origin": ui, "X-Auth-UI": "1"},
))
assert response.status == 200, response.status

response = ui_json_request("/auth/login-history")
assert response.status == 200, response.read()
history = json.load(response)["history"]
assert history and history[0]["method"] == "password", history
assert {"ip_address", "location", "device"}.issubset(history[0]), history[0]
logged_in_at = datetime.datetime.fromisoformat(
    history[0]["logged_in_at"].replace("Z", "+00:00")
).timestamp()
assert login_started_at - 5 <= logged_in_at <= time.time() + 5, history[0]
response = ui_json_request("/auth/login-history/export")
assert response.status == 200, response.read()
assert response.headers["Content-Type"].startswith("text/csv"), response.headers
export_rows = list(csv.reader(io.StringIO(response.read().decode("utf-8-sig"))))
assert export_rows[0][:4] == ["account_user_id", "event_id", "logged_in_at_utc", "method"]
assert export_rows[-1][3] == "password", export_rows[-1]

response = ui_json_request("/auth/profile")
assert response.status == 200, response.read()
profile = json.load(response)
assert profile["email"] == email
security_status = json.load(ui_json_request("/auth/security/step-up/status"))
assert security_status == {"required": False, "verified": False, "methods": ["email"]}, security_status
assert json.load(ui_json_request("/auth/recovery/status")) == {"issued": False, "remaining": 0}
response = ui_json_request("/auth/profile", {"display_name": profile["display_name"]})
assert response.status == 204, response.read()

response = request(ui + "/passkeys")
assert response.status == 200, response.status
assert b'id="app"' in response.read()
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
user_info = json.load(response)
assert user_info["email"] == email
user_id = user_info["sub"]

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

response = request(ui + "/security")
assert response.status == 200
assert b'id="app"' in response.read()
response = ui_json_request("/auth/email-code/settings")
assert response.status == 200
assert json.load(response)["enabled"] is False
assert ui_json_request("/auth/email-code/settings", {}).status == 400
assert ui_json_request("/auth/email-code/settings", {"enabled": True}).status == 204
assert json.load(ui_json_request("/auth/email-code/settings"))["enabled"] is True
assert ui_json_request("/auth/email-code/settings", {"enabled": False}).status == 204
assert json.load(ui_json_request("/auth/email-code/settings"))["enabled"] is False
response = ui_json_request("/auth/password/status")
assert response.status == 200
assert json.load(response)["has_password"] is True
response = request(api + "/oauth/authorize?" + params)
assert response.status == 302
password_code = urllib.parse.parse_qs(urllib.parse.urlparse(response.headers["Location"]).query)["code"][0]
response = token_request({"grant_type": "authorization_code", "code": password_code,
                          "redirect_uri": redirect_uri, "code_verifier": verifier})
assert response.status == 200
password_tokens = json.load(response)
assert ui_json_request("/auth/password", {"current": "wrong", "next": "new-password123"}).status == 401
response = ui_json_request("/auth/password", {"current": password, "next": "new-password123"})
assert response.status == 204, response.read()
assert ui_json_request("/auth/password/status").status == 401
response = request(api + "/oauth/introspect", {"token": password_tokens["access_token"]},
                   {"Authorization": "Basic " + basic})
assert json.load(response)["active"] is False
assert token_request({"grant_type": "refresh_token", "refresh_token": password_tokens["refresh_token"]}).status == 400
assert ui_json_request("/auth/passkey/begin", {"email": email}).status == 401
assert ui_json_request("/auth/login", {"email": email, "password": "new-password123"}).status == 200
assert ui_json_request("/auth/password", {"current": "new-password123", "next": password}).status == 204

assert ui_json_request("/auth/login", {"email": email, "password": password}).status == 200
assert ui_json_request("/auth/recovery/generate", {"unexpected": True}).status == 400
response = ui_json_request("/auth/recovery/generate", {})
assert response.status == 200, response.read()
first_codes = json.load(response)["codes"]
assert len(first_codes) == 10 and len(set(first_codes)) == 10
assert all(len(code) == 19 and code[4] == code[9] == code[14] == "-" for code in first_codes)
assert json.load(ui_json_request("/auth/recovery/status")) == {"issued": True, "remaining": 10}
response = ui_json_request("/auth/recovery/generate", {})
assert response.status == 200, response.read()
second_codes = json.load(response)["codes"]
assert len(second_codes) == 10 and set(first_codes).isdisjoint(second_codes)
assert json.load(ui_json_request("/auth/recovery/status")) == {"issued": True, "remaining": 10}
response = ui_json_request("/auth/login", {"email": email, "password": password})
assert response.status == 202, response.read()
old_challenge = json.load(response)["auth_challenge"]
assert ui_json_request("/auth/login", {"email": email, "auth_challenge": old_challenge,
                                       "recovery_code": first_codes[0]}).status == 401
response = ui_json_request("/auth/login", {"email": email, "password": password})
assert response.status == 202, response.read()
new_challenge = json.load(response)["auth_challenge"]
response = ui_json_request("/auth/login", {"email": email, "auth_challenge": new_challenge,
                                           "recovery_code": second_codes[0]})
assert response.status == 200, response.read()
assert json.load(ui_json_request("/auth/recovery/status")) == {"issued": True, "remaining": 9}

response = ui_json_request("/auth/totp/status")
assert response.status == 200, response.read()
assert json.load(response)["enabled"] is False
assert ui_json_request("/auth/totp/setup", {"unexpected": True}).status == 400
response = ui_json_request("/auth/totp/setup", {})
assert response.status == 200, response.read()
totp_setup = json.load(response)
assert totp_setup["secret"] in totp_setup["otpauth"]
code = totp_code(totp_setup["secret"])
assert ui_json_request("/auth/totp/activate", {"code": "invalid"}).status == 400
response = ui_json_request("/auth/totp/activate", {"code": code})
assert response.status == 204, response.read()
assert json.load(ui_json_request("/auth/totp/status"))["enabled"] is True
response = ui_json_request("/auth/login", {"email": email, "password": password})
assert response.status == 202, response.read()
totp_challenge = json.load(response)["auth_challenge"]
response = ui_json_request("/auth/login", {"email": email, "auth_challenge": totp_challenge,
                                           "totp": totp_code(totp_setup["secret"])})
assert response.status == 200, response.read()
response = ui_json_request("/auth/totp/disable", {"code": totp_code(totp_setup["secret"])})
assert response.status == 204, response.read()
assert json.load(ui_json_request("/auth/totp/status"))["enabled"] is False

assert internal_block_request(user_id, {"active": True}, "wrong-token").status == 401
response = internal_block_request(user_id, {"active": True, "reason": "smoke test"})
assert response.status == 200, response.read()
assert json.load(response)["active"] is True
assert ui_json_request("/auth/password/status").status == 401
response = internal_block_request(user_id, {"active": False})
assert response.status == 200, response.read()
assert json.load(response)["active"] is False

response = request(ui + "/logout")
assert response.status == 200, response.status
assert b"/src/logout-react.tsx" in response.read()
response = request(api + "/auth/logout", {}, {"Origin": ui, "X-Auth-UI": "1"})
assert response.status == 204, response.read()
response = request(api + "/oauth/authorize?" + params)
assert response.status == 302, response.status
assert response.headers["Location"].startswith(ui + "/login?")

deleted_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
assert internal_lifecycle_request(
    user_id, {"deleted": True, "deleted_at": deleted_at}, "wrong-token"
).status == 401
response = internal_lifecycle_request(user_id, {"deleted": True, "deleted_at": deleted_at})
assert response.status == 200, response.read()
assert json.load(response)["deleted_at"] == deleted_at
assert ui_json_request("/auth/login", {"email": email, "password": password}).status == 401
response = internal_lifecycle_request(user_id, {"deleted": True, "deleted_at": deleted_at})
assert response.status == 200, response.read()
# Restoration reopens the existing account and keeps its old session invalid.
# Fresh login is covered by the isolated MySQL test to avoid this fixture's login rate limit.
response = internal_lifecycle_request(user_id, {"deleted": False})
assert response.status == 200, response.read()
assert json.load(response)["deleted"] is False
assert ui_json_request("/auth/password/status").status == 401
response = internal_lifecycle_request(user_id, {"deleted": False})
assert response.status == 200, response.read()
response = internal_lifecycle_request(user_id, {"deleted": True, "deleted_at": deleted_at})
assert response.status == 200, response.read()
assert ui_json_request("/auth/password/status").status == 401
response = internal_lifecycle_request(user_id)
assert response.status == 204, response.read()
response = internal_lifecycle_request(user_id)
assert response.status == 204, response.read()

response = internal_lifecycle_request(user_id, {"deleted": False})
assert response.status == 404, response.read()

projection_email = "projection-smoke@example.test"
response = internal_user_request("POST", "/internal/users", {
    "email": projection_email, "display_name": "Projection Smoke", "notification_lang": "ja",
})
assert response.status == 201, response.read()
projection = json.load(response)
assert projection["created"] is True
projection_id = projection["id"]
response = internal_user_request("POST", "/internal/users", {
    "email": projection_email, "display_name": "Ignored Retry", "notification_lang": "en",
})
assert response.status == 200, response.read()
retry = json.load(response)
assert retry == {"created": False, "id": projection_id}, retry
response = internal_user_request("PATCH", f"/internal/users/{projection_id}", {
    "email": "projection-updated@example.test", "display_name": "Updated Projection",
    "notification_lang": "en", "email_code_allowed": False,
})
assert response.status == 200, response.read()
response = internal_user_request("POST", "/internal/users", {
    "email": "projection-conflict@example.test", "display_name": "Conflict", "notification_lang": "ja",
})
assert response.status == 201, response.read()
conflict_id = json.load(response)["id"]
response = internal_user_request("PATCH", f"/internal/users/{conflict_id}", {
    "email": "projection-updated@example.test",
})
assert response.status == 409, response.read()
projection_deleted_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
response = internal_lifecycle_request(
    projection_id, {"deleted": True, "deleted_at": projection_deleted_at}
)
assert response.status == 200, response.read()
assert internal_lifecycle_request(projection_id).status == 204
response = internal_lifecycle_request(
    conflict_id, {"deleted": True, "deleted_at": projection_deleted_at}
)
assert response.status == 200, response.read()
assert internal_lifecycle_request(conflict_id).status == 204

print("OAuth 2.1 local smoke passed")
