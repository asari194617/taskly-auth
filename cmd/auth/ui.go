package main

import (
	"html/template"
	"net/http"
)

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="api-base" content="{{.API}}"><meta name="legacy-login" content="{{.Legacy}}">
<title>Taskly 認証</title>
<style>body{font:16px system-ui,sans-serif;background:#f6f8fb;color:#17202c;margin:0}main{max-width:420px;margin:8vh auto;padding:28px;background:white;border-radius:12px;box-shadow:0 8px 30px #15243a16}h1{font-size:1.5rem}label{display:block;margin:16px 0 6px}input{box-sizing:border-box;width:100%;padding:12px;font:inherit;border:1px solid #aeb8c5;border-radius:6px}button{font:inherit;background:#185db6;color:white;border:0;border-radius:6px;padding:12px 18px;margin-top:18px;cursor:pointer}button:disabled{opacity:.6}#error{color:#ad2020;min-height:1.5em}a{color:#185db6}</style>
</head><body><main><h1>Taskly にログイン</h1><p id="error" role="alert"></p>
<form id="form"><label for="email">メールアドレス</label><input id="email" type="email" autocomplete="username" required>
<div id="primary"><label for="password">パスワード</label><input id="password" type="password" autocomplete="current-password"><p>パスワードを使わない場合は、下のコードを利用できます。</p></div>
<div id="secondary"><label for="code">メールコード</label><input id="code" inputmode="numeric" autocomplete="one-time-code"><button id="send-code" type="button">メールコードを送信</button>
<label for="totp">認証アプリのコード</label><input id="totp" inputmode="numeric" autocomplete="one-time-code">
<label for="recovery">復旧コード</label><input id="recovery" autocomplete="off"></div>
<button id="submit" type="submit">ログイン</button></form><p>既存のパスキーは <a href="{{.Legacy}}">従来のログイン画面</a> で利用できます。</p></main><script src="/login.js" defer></script></body></html>`))

func (a *app) loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src "+a.issuer+"; base-uri 'none'; form-action 'self'")
	legacy := env("LEGACY_LOGIN_URL", "https://taskly.iy0306.com/login")
	_ = loginTemplate.Execute(w, map[string]string{"API": a.issuer, "Legacy": legacy})
}

func (a *app) loginJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(loginJavaScript))
}

const loginJavaScript = `
const api = document.querySelector('meta[name="api-base"]').content;
const requestID = new URLSearchParams(location.search).get('request_id');
const form = document.getElementById('form');
const error = document.getElementById('error');
const submit = document.getElementById('submit');
let challenge = '';
if (!requestID || !/^[A-Za-z0-9._~-]{43,128}$/.test(requestID)) {
  error.textContent = 'Taskly からログインを開始してください。';
  form.hidden = true;
}
async function post(path, body) {
  const response = await fetch(api + path, {
    method: 'POST', credentials: 'include',
    headers: {'Content-Type': 'application/json', 'X-Auth-UI': '1'},
    body: JSON.stringify(body)
  });
  let payload = {};
  try { payload = await response.json(); } catch (_) {}
  return {response, payload};
}
document.getElementById('send-code').addEventListener('click', async () => {
  error.textContent = '';
  const email = document.getElementById('email').value;
  if (!email) { error.textContent = 'メールアドレスを入力してください。'; return; }
  const {response} = await post('/auth/request-code', {email});
  error.textContent = response.ok ? 'メールコードを送信しました。' : 'コードを送信できませんでした。';
});
form.addEventListener('submit', async event => {
  event.preventDefault(); error.textContent = ''; submit.disabled = true;
  const body = {
    email: document.getElementById('email').value,
    password: challenge ? '' : document.getElementById('password').value,
    code: document.getElementById('code').value,
    totp: document.getElementById('totp').value,
    recovery_code: document.getElementById('recovery').value,
    auth_challenge: challenge
  };
  try {
    const {response,payload} = await post('/auth/login', body);
    if (response.status === 202 && payload.mfa_required) {
      challenge = payload.auth_challenge;
      document.getElementById('primary').hidden = true;
      document.getElementById('code').value = '';
      document.getElementById('totp').value = '';
      document.getElementById('recovery').value = '';
      error.textContent = '2段階認証のコードを入力してください。';
      return;
    }
    if (!response.ok) { error.textContent = '認証できませんでした。入力内容を確認してください。'; return; }
    location.assign(api + '/oauth/authorize?request_id=' + encodeURIComponent(requestID));
  } catch (_) { error.textContent = '通信に失敗しました。'; }
  finally { submit.disabled = false; }
});
`
