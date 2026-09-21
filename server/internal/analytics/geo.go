package analytics

import (
	"fmt"
	"net/netip"

	"github.com/oschwald/geoip2-golang/v2"
)

// Locator finds where an IP address is. It returns "" for anything it doesn't know.
type Locator interface {
	Locate(ip netip.Addr) (country, city string)
}

// GeoIP looks addresses up in a local MaxMind GeoLite2-City (or GeoIP2-City) database file,
// so no request leaves the server.
type GeoIP struct {
	reader *geoip2.Reader
}

func OpenGeoIP(path string) (*GeoIP, error) {
	reader, err := geoip2.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open geoip database: %w", err)
	}
	return &GeoIP{reader: reader}, nil
}

func (g *GeoIP) Locate(ip netip.Addr) (string, string) {
	record, err := g.reader.City(ip)
	if err != nil {
		return "", ""
	}
	return record.Country.ISOCode, record.City.Names.English
}

func (g *GeoIP) Close() error {
	return g.reader.Close()
}
