package main

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

func makeTestGeoDB(t *testing.T) *cityGeoDB {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: "DBIP-City-Lite",
		BuildEpoch:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix(),
		Languages:    []string{"en", "ja"},
		Description:  map[string]string{"en": "DB-IP City Lite test fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, network, err := net.ParseCIDR("8.8.8.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.Insert(network, mmdbtype.Map{
		"city":         mmdbtype.Map{"names": mmdbtype.Map{"en": mmdbtype.String("Mountain View")}},
		"country":      mmdbtype.Map{"iso_code": mmdbtype.String("US")},
		"subdivisions": mmdbtype.Slice{mmdbtype.Map{"names": mmdbtype.Map{"en": mmdbtype.String("California")}}},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test-city.mmdb")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.WriteTo(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := openCityGeoDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if expected := fmt.Sprintf("%x", sha256.Sum256(contents)); db.sha256 != expected {
		t.Fatalf("database hash %q != file hash %q", db.sha256, expected)
	}
	return db
}

func TestCityGeoDBLookupAndProvenance(t *testing.T) {
	db := makeTestGeoDB(t)
	if db.release != "2026-10" || len(db.sha256) != 64 {
		t.Fatalf("wrong DB provenance: release=%q hash=%q", db.release, db.sha256)
	}
	location, err := db.lookup(netip.MustParseAddr("8.8.8.8"))
	if err != nil || location == nil || location.City != "Mountain View" || location.Region != "California" || location.Country != "US" {
		t.Fatalf("DB-IP lookup: location=%+v err=%v", location, err)
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "1.1.1.1", "203.0.113.10"} {
		location, err := db.lookup(netip.MustParseAddr(ip))
		if err != nil || location != nil {
			t.Errorf("unknown/private IP %s got %+v, %v", ip, location, err)
		}
	}
	a := &app{geoDB: db}
	ip, source := "8.8.8.8", "nginx_remote_addr"
	if evidence := a.resolveLoginLocation(&ip, &source); evidence != nil {
		t.Fatalf("ambiguous Nginx IP was geolocated: %+v", evidence)
	}
	source = "cloudflare"
	if evidence := a.resolveLoginLocation(&ip, &source); evidence == nil || evidence.Source != geoIPSource || evidence.Release != "2026-10" || evidence.SHA256 != db.sha256 {
		t.Fatalf("verified visitor IP lacks provenance: %+v", evidence)
	}
	ip = "1.1.1.1"
	if evidence := a.resolveLoginLocation(&ip, &source); evidence != nil {
		t.Fatalf("lookup miss should not have provenance: %+v", evidence)
	}
}

func TestCityGeoDBConfiguredInvalidFileFails(t *testing.T) {
	if db, err := openCityGeoDB(""); err != nil || db != nil {
		t.Fatalf("empty path should disable geolocation: db=%v err=%v", db, err)
	}
	if _, err := openCityGeoDB(filepath.Join(t.TempDir(), "missing.mmdb")); err == nil {
		t.Fatal("missing configured MMDB was accepted")
	}
	badPath := filepath.Join(t.TempDir(), "bad.mmdb")
	if err := os.WriteFile(badPath, []byte("not an MMDB"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openCityGeoDB(badPath); err == nil || !strings.Contains(err.Error(), "DB-IP City Lite") {
		t.Fatalf("invalid MMDB was accepted: %v", err)
	}
}
