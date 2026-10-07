package main

import (
	"crypto/subtle"
	"encoding/csv"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const loginHistoryPageSize = 50

type loginLocation struct {
	City    string `json:"city"`
	Region  string `json:"region"`
	Country string `json:"country"`
}

type loginHistoryEntry struct {
	ID         uint64         `json:"-"`
	Method     string         `json:"method"`
	LoggedInAt time.Time      `json:"logged_in_at"`
	IPAddress  *string        `json:"ip_address"`
	Location   *loginLocation `json:"location"`
	Device     *string        `json:"device"`
	IPSource   *string        `json:"-"`
}

// The private reverse proxy overwrites X-Auth-* headers after checking the
// Cloudflare ingress address. Direct requests have no location and use their
// socket peer only when that peer is a public address.
func loginRequestEvidence(r *http.Request, proxyToken string) (*string, *loginLocation, *string, *string) {
	var ip, source *string
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	peerIP, peerErr := netip.ParseAddr(peer)
	proxyPeer := peerErr == nil && (peerIP.IsLoopback() || peerIP.IsPrivate()) &&
		len(proxyToken) >= 32 && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Auth-Proxy-Token")), []byte(proxyToken)) == 1
	if parsed, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Auth-Client-IP"))); proxyPeer && err == nil && parsed.IsGlobalUnicast() && !parsed.IsPrivate() {
		ipValue := parsed.Unmap().String()
		ip = &ipValue
		sourceValue := strings.TrimSpace(r.Header.Get("X-Auth-IP-Source"))
		if sourceValue != "cloudflare" && sourceValue != "origin_socket" {
			sourceValue = "reverse_proxy_unspecified"
		}
		source = &sourceValue
	} else {
		if peerErr == nil && peerIP.IsGlobalUnicast() && !peerIP.IsPrivate() {
			ipValue := peerIP.Unmap().String()
			ip = &ipValue
			sourceValue := "direct_socket"
			source = &sourceValue
		}
	}
	var location *loginLocation
	if proxyPeer && ip != nil && source != nil && *source == "cloudflare" {
		city := cleanHistoryText(r.Header.Get("X-Auth-Geo-City"), 128)
		region := cleanHistoryText(r.Header.Get("X-Auth-Geo-Region"), 128)
		country := strings.ToUpper(cleanHistoryText(r.Header.Get("X-Auth-Geo-Country"), 2))
		if country == "XX" || len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
			country = ""
		}
		if city != "" || region != "" || country != "" {
			location = &loginLocation{City: city, Region: region, Country: country}
		}
	}
	var device *string
	if value := cleanHistoryText(r.UserAgent(), 1024); value != "" {
		device = &value
	}
	return ip, location, device, source
}

func cleanHistoryText(value string, maxRunes int) string {
	value = strings.ToValidUTF8(value, "")
	var out strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		out.WriteRune(r)
		maxRunes--
		if maxRunes == 0 {
			break
		}
	}
	return strings.TrimSpace(out.String())
}

func (a *app) historyUser(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	if !a.requireUI(w, r) {
		return 0, false
	}
	userID, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return 0, false
	}
	return userID, true
}

func (a *app) loginHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.historyUser(w, r)
	if !ok {
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,method,logged_in_at,ip_address,geo_city,geo_region,geo_country,user_agent,ip_source FROM auth_login_history WHERE user_id=? ORDER BY logged_in_at DESC,id DESC LIMIT ?`, userID, loginHistoryPageSize)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "history_unavailable")
		return
	}
	defer rows.Close()
	history := make([]loginHistoryEntry, 0)
	for rows.Next() {
		entry, err := scanLoginHistory(rows)
		if err != nil {
			oauthError(w, http.StatusServiceUnavailable, "history_unavailable")
			return
		}
		history = append(history, entry)
	}
	if err := rows.Err(); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "history_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"history": history})
}

type historyScanner interface {
	Scan(dest ...any) error
}

func scanLoginHistory(row historyScanner) (loginHistoryEntry, error) {
	var entry loginHistoryEntry
	var ip, city, region, country, device, source *string
	err := row.Scan(&entry.ID, &entry.Method, &entry.LoggedInAt, &ip, &city, &region, &country, &device, &source)
	if err != nil {
		return entry, err
	}
	entry.IPAddress, entry.Device, entry.IPSource = ip, device, source
	if city != nil || region != nil || country != nil {
		entry.Location = &loginLocation{City: stringValue(city), Region: stringValue(region), Country: stringValue(country)}
	}
	return entry, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// Export every retained event. Query rows and CSV output are streamed, keeping
// memory bounded even when an account has a long history.
func (a *app) exportLoginHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.historyUser(w, r)
	if !ok {
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,method,logged_in_at,ip_address,geo_city,geo_region,geo_country,user_agent,ip_source FROM auth_login_history WHERE user_id=? ORDER BY logged_in_at ASC,id ASC`, userID)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "history_unavailable")
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="taskly-login-history.csv"`)
	w.WriteHeader(http.StatusOK)
	// Excel recognizes UTF-8 reliably when the CSV starts with a BOM.
	if _, err := w.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		return
	}
	csvWriter := csv.NewWriter(w)
	if err := csvWriter.Write([]string{"account_user_id", "event_id", "logged_in_at_utc", "method", "ip_address", "ip_source", "city", "region", "country_code", "location_source", "device_user_agent"}); err != nil {
		return
	}
	for rows.Next() {
		entry, err := scanLoginHistory(rows)
		if err != nil {
			log.Printf("login history CSV scan failed: %v", err)
			panic(http.ErrAbortHandler)
		}
		var city, region, country string
		var locationSource string
		if entry.Location != nil {
			city, region, country = entry.Location.City, entry.Location.Region, entry.Location.Country
			locationSource = "cloudflare_ip_geolocation"
		}
		if err := csvWriter.Write([]string{
			strconv.FormatUint(userID, 10), strconv.FormatUint(entry.ID, 10),
			entry.LoggedInAt.UTC().Format(time.RFC3339Nano), csvSafe(entry.Method),
			stringValue(entry.IPAddress), csvSafe(stringValue(entry.IPSource)),
			csvSafe(city), csvSafe(region), csvSafe(country), locationSource, csvSafe(stringValue(entry.Device)),
		}); err != nil {
			return
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("login history CSV query failed: %v", err)
		panic(http.ErrAbortHandler)
	}
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		log.Printf("login history CSV write failed: %v", err)
	}
}

func csvSafe(value string) string {
	first := strings.TrimLeftFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
	if first != "" && strings.ContainsRune("=+-@", []rune(first)[0]) {
		return "'" + value
	}
	return value
}
