package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/go-webauthn/webauthn/webauthn"
)

type app struct {
	db            *sql.DB
	issuer        string
	uiURL         string
	clientID      string
	clientSecret  string
	redirectURI   string
	publicClients map[string]string
	secure        bool
	ready         bool
	alerts        *alerter
	webAuthn      *webauthn.WebAuthn
	internalToken string
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	alerts := newAlerter(os.Getenv("DISCORD_WEBHOOK_URL"))
	dsn := os.Getenv("AUTH_DSN")
	if dsn == "" {
		alerts.sendSync("Taskly auth startup failed: missing database configuration")
		log.Fatal("AUTH_DSN is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		alerts.sendSync("Taskly auth startup failed: database configuration invalid")
		log.Fatal("auth DB configuration invalid")
	}
	defer db.Close()
	db.SetMaxOpenConns(20)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = db.PingContext(ctx)
	cancel()
	if err != nil {
		alerts.sendSync("Taskly auth startup failed: database unavailable")
		log.Fatal("auth DB unavailable")
	}
	a := &app{
		db:            db,
		issuer:        strings.TrimRight(env("AUTH_ISSUER", "http://localhost:3067"), "/"),
		uiURL:         strings.TrimRight(env("AUTH_UI_URL", "http://localhost:3068"), "/"),
		clientID:      env("OAUTH_CLIENT_ID", "taskly-web"),
		clientSecret:  os.Getenv("OAUTH_CLIENT_SECRET"),
		redirectURI:   os.Getenv("OAUTH_REDIRECT_URI"),
		alerts:        alerts,
		internalToken: os.Getenv("AUTH_INTERNAL_TOKEN"),
	}
	a.publicClients, err = parsePublicClients(os.Getenv("OAUTH_PUBLIC_CLIENTS_JSON"), a.clientID)
	if err != nil {
		alerts.sendSync("Taskly auth startup failed: public client registration invalid")
		log.Fatal(err)
	}
	a.secure = strings.HasPrefix(a.issuer, "https://")
	a.ready = env("NODE_ENV", "development") != "production" || os.Getenv("AUTH_MIGRATION_READY") == "1"
	if rpID := os.Getenv("WEBAUTHN_RP_ID"); rpID != "" {
		a.webAuthn, err = webauthn.New(&webauthn.Config{RPDisplayName: "Taskly", RPID: rpID, RPOrigins: []string{a.uiURL}})
		if err != nil {
			alerts.sendSync("Taskly auth startup failed: WebAuthn configuration invalid")
			log.Fatal("WebAuthn configuration invalid: ", err)
		}
	}
	if a.clientSecret == "" || a.redirectURI == "" {
		alerts.sendSync("Taskly auth startup failed: OAuth configuration missing")
		log.Fatal("OAUTH_CLIENT_SECRET and OAUTH_REDIRECT_URI are required")
	}
	if env("NODE_ENV", "development") == "production" && (!a.secure || !strings.HasPrefix(a.uiURL, "https://") || !strings.HasPrefix(a.redirectURI, "https://")) {
		alerts.sendSync("Taskly auth startup failed: HTTPS configuration invalid")
		log.Fatal("production OAuth endpoints must use HTTPS")
	}
	if env("NODE_ENV", "development") == "production" && (len(a.clientSecret) < 32 || os.Getenv("TOTP_ENC_KEY") == "" || strings.EqualFold(env("SMTP_TLS", "starttls"), "none")) {
		alerts.sendSync("Taskly auth startup failed: security configuration invalid")
		log.Fatal("production client secret, TOTP key, and SMTP TLS configuration are required")
	}
	if env("NODE_ENV", "development") == "production" && a.ready && len(a.internalToken) < 32 {
		alerts.sendSync("Taskly auth startup failed: internal API configuration invalid")
		log.Fatal("AUTH_INTERNAL_TOKEN must contain at least 32 characters when migration is ready")
	}
	if err := a.ensureOAuthSchema(context.Background()); err != nil {
		alerts.sendSync("Taskly auth startup failed: database schema unavailable")
		log.Fatal("OAuth schema: ", err)
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /healthz", a.health)
	api.HandleFunc("GET /.well-known/oauth-authorization-server", a.metadata)
	api.HandleFunc("GET /oauth/authorize", a.authorize)
	api.HandleFunc("POST /oauth/token", a.token)
	api.HandleFunc("POST /oauth/introspect", a.introspect)
	api.HandleFunc("POST /oauth/revoke", a.revoke)
	api.HandleFunc("GET /oauth/userinfo", a.userinfo)
	api.HandleFunc("POST /auth/login", a.login)
	api.HandleFunc("POST /auth/logout", a.logout)
	api.HandleFunc("GET /auth/password/status", a.passwordStatus)
	api.HandleFunc("POST /auth/password", a.changePassword)
	api.HandleFunc("GET /auth/email-code/settings", a.emailCodeSettings)
	api.HandleFunc("POST /auth/email-code/settings", a.updateEmailCodeSettings)
	api.HandleFunc("POST /auth/recovery/generate", a.generateRecoveryCodes)
	api.HandleFunc("GET /auth/totp/status", a.totpStatus)
	api.HandleFunc("POST /auth/totp/setup", a.totpSetup)
	api.HandleFunc("POST /auth/totp/activate", a.totpActivate)
	api.HandleFunc("POST /auth/totp/disable", a.totpDisable)
	api.HandleFunc("POST /auth/request-code", a.requestCode)
	api.HandleFunc("POST /auth/passkey/begin", a.passkeyBegin)
	api.HandleFunc("POST /auth/passkey/finish", a.passkeyFinish)
	api.HandleFunc("GET /auth/passkeys", a.passkeyList)
	api.HandleFunc("POST /auth/passkey/register/begin", a.passkeyRegisterBegin)
	api.HandleFunc("POST /auth/passkey/register/finish", a.passkeyRegisterFinish)
	api.HandleFunc("POST /auth/passkey/rename", a.passkeyRename)
	api.HandleFunc("POST /auth/passkey/delete", a.passkeyDelete)
	api.HandleFunc("PUT /internal/users/{id}/block", a.syncBlock)
	api.HandleFunc("PUT /internal/users/{id}/lifecycle", a.syncUserLifecycle)
	api.HandleFunc("DELETE /internal/users/{id}", a.eraseUser)
	ui := http.NewServeMux()
	ui.HandleFunc("GET /healthz", a.health)
	ui.HandleFunc("GET /login", a.loginPage)
	ui.HandleFunc("GET /login.js", a.loginJS)
	ui.HandleFunc("GET /passkeys", a.passkeysPage)
	ui.HandleFunc("GET /passkeys.js", a.passkeysJS)
	ui.HandleFunc("GET /security", a.securityPage)
	ui.HandleFunc("GET /security.js", a.securityJS)
	ui.HandleFunc("GET /logout", a.logoutPage)
	ui.HandleFunc("GET /logout.js", a.logoutJS)
	ui.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/login", http.StatusFound) })
	apiServer := &http.Server{Addr: env("AUTH_ADDR", "127.0.0.1:3067"), Handler: a.wrapAPI(api), ReadHeaderTimeout: 5 * time.Second}
	uiServer := &http.Server{Addr: env("UI_ADDR", "127.0.0.1:3068"), Handler: ui, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := uiServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
			a.alerts.send("Taskly auth UI unavailable")
		}
	}()
	log.Printf("auth API listening on %s", apiServer.Addr)
	if err := apiServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		alerts.sendSync("Taskly auth API unavailable")
		log.Fatal(err)
	}
}

func (a *app) wrapAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" {
			if origin != a.uiURL {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", a.uiURL)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Auth-UI")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !a.ready && (strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/oauth/") || strings.HasPrefix(r.URL.Path, "/internal/")) {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		observed := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(observed, r)
		if observed.status >= 500 {
			a.alerts.send("Taskly auth API returned a server error")
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.db.PingContext(ctx); err != nil {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		a.alerts.send("Taskly auth database health check failed")
		return
	}
	w.Write([]byte("ok"))
}
