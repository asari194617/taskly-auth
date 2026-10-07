package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
	"golang.org/x/sys/unix"
)

const (
	geoIPSource = "dbip_city_lite"
	geoIPURL    = "https://db-ip.com"
)

// cityGeoDB is loaded once at startup. The reader memory-maps the read-only
// database, so a monthly file replacement takes effect after auth restarts.
type cityGeoDB struct {
	reader  *maxminddb.Reader
	mapping []byte
	release string
	sha256  string
}

func openCityGeoDB(path string) (*cityGeoDB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open DB-IP City Lite database: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > int64(^uint(0)>>1) {
		return nil, errors.New("AUTH_GEOIP_MMDB_PATH must point to a nonempty regular MMDB file")
	}
	// Map a single opened inode and hash exactly the bytes the reader sees.
	// This also keeps the database outside the Go heap on a small host.
	mapping, err := unix.Mmap(int(file.Fd()), 0, int(stat.Size()), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("map DB-IP City Lite database: %w", err)
	}
	reader, err := maxminddb.OpenBytes(mapping)
	if err != nil {
		unix.Munmap(mapping)
		return nil, fmt.Errorf("read DB-IP City Lite database: %w", err)
	}
	if reader.Metadata.DatabaseType != "DBIP-City-Lite" || reader.Metadata.BuildEpoch == 0 {
		reader.Close()
		unix.Munmap(mapping)
		return nil, errors.New("AUTH_GEOIP_MMDB_PATH must point to a DB-IP City Lite MMDB with a build date")
	}
	if err := reader.Verify(); err != nil {
		reader.Close()
		unix.Munmap(mapping)
		return nil, fmt.Errorf("verify DB-IP City Lite database: %w", err)
	}
	hash := sha256.Sum256(mapping)
	return &cityGeoDB{
		reader: reader, mapping: mapping,
		release: reader.Metadata.BuildTime().UTC().Format("2006-01"),
		sha256:  fmt.Sprintf("%x", hash),
	}, nil
}

func (db *cityGeoDB) Close() error {
	if db == nil {
		return nil
	}
	return errors.Join(db.reader.Close(), unix.Munmap(db.mapping))
}

type cityGeoRecord struct {
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Subdivisions []struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"subdivisions"`
}

func (db *cityGeoDB) lookup(ip netip.Addr) (*loginLocation, error) {
	if db == nil || !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() {
		return nil, nil
	}
	var record cityGeoRecord
	if err := db.reader.Lookup(ip.Unmap()).Decode(&record); err != nil {
		return nil, err
	}
	city := cleanHistoryText(record.City.Names["en"], 128)
	if city == "" {
		city = cleanHistoryText(record.City.Names["ja"], 128)
	}
	var region string
	if len(record.Subdivisions) > 0 {
		region = cleanHistoryText(record.Subdivisions[0].Names["en"], 128)
		if region == "" {
			region = cleanHistoryText(record.Subdivisions[0].Names["ja"], 128)
		}
	}
	country := strings.ToUpper(record.Country.ISOCode)
	if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
		country = ""
	}
	if city == "" && region == "" && country == "" {
		return nil, nil
	}
	return &loginLocation{City: city, Region: region, Country: country}, nil
}

type loginGeoEvidence struct {
	Location loginLocation
	Source   string
	Release  string
	SHA256   string
}

func (a *app) resolveLoginLocation(ip, ipSource *string) *loginGeoEvidence {
	if a.geoDB == nil || ip == nil || ipSource == nil || (*ipSource != "cloudflare" && *ipSource != "direct_socket") {
		return nil
	}
	parsed, err := netip.ParseAddr(*ip)
	if err != nil {
		return nil
	}
	location, err := a.geoDB.lookup(parsed)
	if err != nil {
		// Keep login available when a single record cannot be decoded. The
		// database itself is verified at startup.
		return nil
	}
	if location == nil {
		return nil
	}
	return &loginGeoEvidence{Location: *location, Source: geoIPSource, Release: a.geoDB.release, SHA256: a.geoDB.sha256}
}
