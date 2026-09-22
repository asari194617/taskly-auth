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
<button id="submit" type="submit">ログイン</button>{{if .Passkeys}} <button id="passkey" type="button">パスキーでログイン</button>{{end}}</form><p><a href="/security">パスワードを管理</a> · <a href="/passkeys">パスキーを管理</a></p><p>認証できない場合は <a href="{{.Legacy}}">従来のログイン画面</a> を利用できます。</p></main><script src="/login.js" defer></script></body></html>`))

func (a *app) loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src "+a.issuer+"; base-uri 'none'; form-action 'self'")
	legacy := env("LEGACY_LOGIN_URL", "https://taskly.iy0306.com/login")
	_ = loginTemplate.Execute(w, map[string]any{"API": a.issuer, "Legacy": legacy, "Passkeys": a.webAuthn != nil})
}

func (a *app) loginJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(loginJavaScript))
}

var passkeysTemplate = template.Must(template.New("passkeys").Parse(`<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="api-base" content="{{.API}}"><title>Taskly パスキー管理</title>
<style>body{font:16px system-ui,sans-serif;background:#f6f8fb;color:#17202c;margin:0}main{max-width:560px;margin:8vh auto;padding:28px;background:white;border-radius:12px;box-shadow:0 8px 30px #15243a16}h1{font-size:1.5rem}button{font:inherit;background:#185db6;color:white;border:0;border-radius:6px;padding:10px 16px;cursor:pointer}button:disabled{opacity:.6}li{margin:14px 0}li button{margin-left:8px}#error{color:#ad2020;min-height:1.5em}#notice{min-height:1.5em}a{color:#185db6}</style>
</head><body><main><h1>パスキーを管理</h1><p id="error" role="alert"></p><p id="notice" role="status"></p>
<ul id="keys"></ul><button id="register" type="button">パスキーを登録</button><p><a href="{{.Legacy}}">Taskly に戻る</a></p>
</main><script src="/passkeys.js" defer></script></body></html>`))

var securityTemplate = template.Must(template.New("security").Parse(`<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="api-base" content="{{.API}}"><title>Taskly 認証設定</title>
<style>body{font:16px system-ui,sans-serif;background:#f6f8fb;color:#17202c;margin:0}main{max-width:480px;margin:8vh auto;padding:28px;background:white;border-radius:12px;box-shadow:0 8px 30px #15243a16}h1{font-size:1.5rem}label{display:block;margin:16px 0 6px}input{box-sizing:border-box;width:100%;padding:12px;font:inherit;border:1px solid #aeb8c5;border-radius:6px}input[type=checkbox]{width:auto}button{font:inherit;background:#185db6;color:white;border:0;border-radius:6px;padding:12px 18px;margin-top:18px;cursor:pointer}button:disabled{opacity:.6}#error{color:#ad2020;min-height:1.5em}a{color:#185db6}</style>
</head><body><main><h1>認証設定</h1><p id="error" role="alert"></p><p id="notice" role="status"></p>
<section id="email-section" hidden><h2>メールコード</h2><p>無効にするにはパスワードかパスキーが必要です。</p>
<form id="email-form"><label><input id="email-enabled" type="checkbox"> メールコードを使う</label><button id="email-submit" type="submit">メールコード設定を保存</button></form></section>
<section><h2>復旧コード</h2><p>再発行すると以前の未使用コードは使えなくなります。表示されるコードを安全な場所に保存してください。</p>
<button id="recovery-generate" type="button">復旧コードを再発行</button><pre id="recovery-codes" hidden></pre></section>
<h2>パスワード</h2>
<form id="form" hidden><div id="current-field"><label for="current">現在のパスワード</label><input id="current" type="password" autocomplete="current-password"></div>
<label for="next">新しいパスワード（8文字以上）</label><input id="next" type="password" autocomplete="new-password" required minlength="8">
<button id="submit" type="submit">更新する</button></form><p><a id="login-again" href="/login?next=security" hidden>ログインし直す</a></p><p><a href="/passkeys">パスキーを管理</a></p>
</main><script src="/security.js" defer></script></body></html>`))

func (a *app) passkeysPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src "+a.issuer+"; base-uri 'none'; form-action 'none'")
	_ = passkeysTemplate.Execute(w, map[string]string{"API": a.issuer, "Legacy": env("LEGACY_LOGIN_URL", "https://taskly.iy0306.com/login")})
}

func (a *app) passkeysJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(passkeysJavaScript))
}

func (a *app) securityPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src "+a.issuer+"; base-uri 'none'; form-action 'none'")
	_ = securityTemplate.Execute(w, map[string]string{"API": a.issuer})
}

func (a *app) securityJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(securityJavaScript))
}

const securityJavaScript = `
const api = document.querySelector('meta[name="api-base"]').content;
const error = document.getElementById('error');
const notice = document.getElementById('notice');
const form = document.getElementById('form');
const submit = document.getElementById('submit');
const emailForm = document.getElementById('email-form');
const emailSubmit = document.getElementById('email-submit');
const recoveryGenerate = document.getElementById('recovery-generate');
async function request(path, body) {
  const response = await fetch(api + path, {
    method: body === undefined ? 'GET' : 'POST', credentials: 'include',
    headers: {'X-Auth-UI': '1', ...(body === undefined ? {} : {'Content-Type': 'application/json'})},
    ...(body === undefined ? {} : {body: JSON.stringify(body)})
  });
  if (response.status === 401 && body === undefined) {
    location.assign('/login?next=security');
    return null;
  }
  return response;
}
async function loadStatus() {
  try {
    const response = await request('/auth/password/status');
    if (!response) return;
    if (!response.ok) throw new Error('status');
    const status = await response.json();
    document.getElementById('current-field').hidden = !status.has_password;
    form.hidden = false;
  } catch (_) { error.textContent = 'パスワード設定を読み込めませんでした。'; }
}
async function loadEmailSettings() {
  try {
    const response = await request('/auth/email-code/settings');
    if (!response) return;
    if (!response.ok) throw new Error('status');
    const settings = await response.json();
    document.getElementById('email-enabled').checked = settings.enabled;
    document.getElementById('email-section').hidden = false;
  } catch (_) { error.textContent = 'メールコード設定を読み込めませんでした。'; }
}
emailForm.addEventListener('submit', async event => {
  event.preventDefault(); error.textContent = ''; notice.textContent = ''; emailSubmit.disabled = true;
  try {
    const response = await request('/auth/email-code/settings', {enabled: document.getElementById('email-enabled').checked});
    if (response.status === 401 && (await response.json()).error === 'login_required') {
      location.assign('/login?next=security'); return;
    }
    if (!response.ok) {
      await loadEmailSettings();
      error.textContent = '設定を更新できませんでした。メールコードを無効にするにはパスワードかパスキーが必要です。';
      return;
    }
    notice.textContent = 'メールコード設定を更新しました。';
  } catch (_) { error.textContent = '通信に失敗しました。'; }
  finally { emailSubmit.disabled = false; }
});
recoveryGenerate.addEventListener('click', async () => {
  if (!confirm('以前の未使用コードは使えなくなります。復旧コードを再発行しますか？')) return;
  error.textContent = ''; notice.textContent = ''; recoveryGenerate.disabled = true;
  document.getElementById('recovery-codes').hidden = true;
  try {
    const response = await request('/auth/recovery/generate', {});
    if (response.status === 401) { location.assign('/login?next=security'); return; }
    if (!response.ok) throw new Error('generate');
    const result = await response.json();
    const output = document.getElementById('recovery-codes');
    output.textContent = result.codes.join('\n');
    output.hidden = false;
    notice.textContent = '復旧コードを再発行しました。この画面を閉じると再表示できません。';
  } catch (_) { error.textContent = '復旧コードを再発行できませんでした。'; }
  finally { recoveryGenerate.disabled = false; }
});
form.addEventListener('submit', async event => {
  event.preventDefault(); error.textContent = ''; notice.textContent = ''; submit.disabled = true;
  try {
    const response = await request('/auth/password', {
      current: document.getElementById('current').value,
      next: document.getElementById('next').value
    });
    if (response.status === 401 && (await response.json()).error === 'login_required') {
      location.assign('/login?next=security'); return;
    }
    if (!response.ok) { error.textContent = 'パスワードを更新できませんでした。現在のパスワードと入力内容を確認してください。'; return; }
    form.hidden = true;
    notice.textContent = 'パスワードを更新しました。再度ログインしてください。';
    document.getElementById('login-again').hidden = false;
  } catch (_) { error.textContent = '通信に失敗しました。'; }
  finally { submit.disabled = false; }
});
loadStatus();
loadEmailSettings();
`

var logoutTemplate = template.Must(template.New("logout").Parse(`<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="api-base" content="{{.API}}"><meta name="legacy-login" content="{{.Legacy}}">
<title>Taskly ログアウト</title>
<style>body{font:16px system-ui,sans-serif;background:#f6f8fb;color:#17202c;margin:0}main{max-width:420px;margin:8vh auto;padding:28px;background:white;border-radius:12px;box-shadow:0 8px 30px #15243a16}h1{font-size:1.5rem}#error{color:#ad2020;min-height:1.5em}button{font:inherit;background:#185db6;color:white;border:0;border-radius:6px;padding:12px 18px;cursor:pointer}</style>
</head><body><main><h1>ログアウトしています</h1><p id="error" role="alert"></p><button id="retry" hidden>再試行</button></main><script src="/logout.js" defer></script></body></html>`))

func (a *app) logoutPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src "+a.issuer+"; base-uri 'none'; form-action 'none'")
	legacy := env("LEGACY_LOGIN_URL", "https://taskly.iy0306.com/login")
	_ = logoutTemplate.Execute(w, map[string]string{"API": a.issuer, "Legacy": legacy})
}

func (a *app) logoutJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(logoutJavaScript))
}

const logoutJavaScript = `
const api = document.querySelector('meta[name="api-base"]').content;
const target = document.querySelector('meta[name="legacy-login"]').content;
const error = document.getElementById('error');
const retry = document.getElementById('retry');
async function finishLogout() {
  retry.hidden = true;
  error.textContent = '';
  try {
    const response = await fetch(api + '/auth/logout', {
      method: 'POST', credentials: 'include', headers: {'X-Auth-UI': '1'}
    });
    if (!response.ok) throw new Error('logout failed');
    location.replace(target);
  } catch (_) {
    error.textContent = 'ログアウトを完了できませんでした。再試行してください。';
    retry.hidden = false;
  }
}
retry.addEventListener('click', finishLogout);
finishLogout();
`

const loginJavaScript = `
const api = document.querySelector('meta[name="api-base"]').content;
const requestID = new URLSearchParams(location.search).get('request_id');
const next = new URLSearchParams(location.search).get('next');
const managementTarget = !requestID && (next === 'passkeys' || next === 'security') ? next : '';
const form = document.getElementById('form');
const error = document.getElementById('error');
const submit = document.getElementById('submit');
let challenge = '';
function decodeBase64url(value) {
  const raw = atob(value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4));
  return Uint8Array.from(raw, character => character.charCodeAt(0));
}
function encodeBase64url(value) {
  const bytes = new Uint8Array(value);
  let raw = '';
  for (const byte of bytes) raw += String.fromCharCode(byte);
  return btoa(raw).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
if (!managementTarget && (!requestID || !/^[A-Za-z0-9._~-]{43,128}$/.test(requestID))) {
  error.textContent = 'Taskly からログインを開始してください。';
  form.hidden = true;
}
function afterLogin() {
  location.assign(managementTarget ? '/' + managementTarget : api + '/oauth/authorize?request_id=' + encodeURIComponent(requestID));
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
const passkeyButton = document.getElementById('passkey');
if (passkeyButton) passkeyButton.addEventListener('click', async () => {
  error.textContent = '';
  const email = document.getElementById('email').value;
  if (!email) { error.textContent = 'メールアドレスを入力してください。'; return; }
  if (!navigator.credentials || !navigator.credentials.get) { error.textContent = 'このブラウザはパスキーに対応していません。'; return; }
  passkeyButton.disabled = true;
  try {
    const begin = await post('/auth/passkey/begin', {email});
    if (!begin.response.ok) throw new Error('begin failed');
    const publicKey = begin.payload.options.publicKey;
    publicKey.challenge = decodeBase64url(publicKey.challenge);
    publicKey.allowCredentials = (publicKey.allowCredentials || []).map(item => ({...item, id: decodeBase64url(item.id)}));
    const credential = await navigator.credentials.get({publicKey});
    if (!credential) throw new Error('no credential');
    const response = credential.response;
    const finish = await post('/auth/passkey/finish', {
      request_id: begin.payload.request_id,
      credential: {
        id: credential.id, rawId: encodeBase64url(credential.rawId), type: credential.type,
        response: {
          clientDataJSON: encodeBase64url(response.clientDataJSON),
          authenticatorData: encodeBase64url(response.authenticatorData),
          signature: encodeBase64url(response.signature),
          userHandle: response.userHandle ? encodeBase64url(response.userHandle) : null
        },
        clientExtensionResults: credential.getClientExtensionResults()
      }
    });
    if (!finish.response.ok) throw new Error('finish failed');
    afterLogin();
  } catch (_) {
    error.textContent = 'パスキーで認証できませんでした。従来のログインも利用できます。';
  } finally { passkeyButton.disabled = false; }
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
    afterLogin();
  } catch (_) { error.textContent = '通信に失敗しました。'; }
  finally { submit.disabled = false; }
});
`

const passkeysJavaScript = `
const api = document.querySelector('meta[name="api-base"]').content;
const keys = document.getElementById('keys');
const error = document.getElementById('error');
const notice = document.getElementById('notice');
const register = document.getElementById('register');
function decodeBase64url(value) {
  const raw = atob(value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4));
  return Uint8Array.from(raw, character => character.charCodeAt(0));
}
function encodeBase64url(value) {
  const bytes = new Uint8Array(value);
  let raw = '';
  for (const byte of bytes) raw += String.fromCharCode(byte);
  return btoa(raw).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
async function request(path, body) {
  const response = await fetch(api + path, {
    method: body === undefined ? 'GET' : 'POST', credentials: 'include',
    headers: {'X-Auth-UI': '1', ...(body === undefined ? {} : {'Content-Type': 'application/json'})},
    ...(body === undefined ? {} : {body: JSON.stringify(body)})
  });
  if (response.status === 401) {
    location.assign('/login?next=passkeys');
    throw new Error('login required');
  }
  let payload = {};
  try { payload = await response.json(); } catch (_) {}
  if (!response.ok) throw new Error(payload.error || 'request failed');
  return payload;
}
async function loadKeys() {
  const result = await request('/auth/passkeys');
  keys.replaceChildren();
  for (const key of result.passkeys) {
    const item = document.createElement('li');
    item.textContent = key.device_name + ' (' + key.created_at + ')';
    const rename = document.createElement('button');
    rename.type = 'button'; rename.textContent = '名前を変更';
    rename.addEventListener('click', async () => {
      const name = prompt('新しい名前', key.device_name);
      if (name === null) return;
      error.textContent = '';
      try { await request('/auth/passkey/rename', {id: key.id, name}); await loadKeys(); }
      catch (_) { error.textContent = '名前を変更できませんでした。'; }
    });
    const remove = document.createElement('button');
    remove.type = 'button'; remove.textContent = '削除';
    remove.addEventListener('click', async () => {
      if (!confirm('このパスキーを削除しますか？')) return;
      error.textContent = '';
      try { await request('/auth/passkey/delete', {id: key.id}); await loadKeys(); }
      catch (_) { error.textContent = '削除できませんでした。最後のログイン手段は削除できません。'; }
    });
    item.append(rename, remove); keys.append(item);
  }
  if (result.passkeys.length === 0) {
    const item = document.createElement('li'); item.textContent = '登録済みのパスキーはありません。'; keys.append(item);
  }
}
register.addEventListener('click', async () => {
  error.textContent = ''; notice.textContent = '';
  if (!navigator.credentials || !navigator.credentials.create) {
    error.textContent = 'このブラウザはパスキーに対応していません。'; return;
  }
  const name = prompt('パスキーの名前', 'Passkey');
  if (name === null) return;
  register.disabled = true;
  try {
    const begin = await request('/auth/passkey/register/begin', {});
    const publicKey = begin.options.publicKey;
    publicKey.challenge = decodeBase64url(publicKey.challenge);
    publicKey.user.id = decodeBase64url(publicKey.user.id);
    publicKey.excludeCredentials = (publicKey.excludeCredentials || []).map(item => ({...item, id: decodeBase64url(item.id)}));
    const credential = await navigator.credentials.create({publicKey});
    if (!credential) throw new Error('no credential');
    const response = credential.response;
    await request('/auth/passkey/register/finish', {
      request_id: begin.request_id, device_name: name,
      credential: {
        id: credential.id, rawId: encodeBase64url(credential.rawId), type: credential.type,
        response: {
          attestationObject: encodeBase64url(response.attestationObject),
          clientDataJSON: encodeBase64url(response.clientDataJSON),
          transports: response.getTransports ? response.getTransports() : []
        },
        clientExtensionResults: credential.getClientExtensionResults()
      }
    });
    notice.textContent = 'パスキーを登録しました。';
    await loadKeys();
  } catch (_) { error.textContent = 'パスキーを登録できませんでした。再ログインしてからお試しください。'; }
  finally { register.disabled = false; }
});
loadKeys().catch(() => { error.textContent = 'パスキーを読み込めませんでした。'; });
`
