package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Zuhause is the household's address (#561), once for every template that
// needs it: Home Assistant's home, a solar forecast, a waste calendar. The
// coordinates are entered, never looked up: geocoding is an outside call.
type Zuhause struct {
	Street    string   `yaml:"street,omitempty" json:"street,omitempty"`
	Postcode  string   `yaml:"postcode,omitempty" json:"postcode,omitempty"`
	City      string   `yaml:"city,omitempty" json:"city,omitempty"`
	Country   string   `yaml:"country,omitempty" json:"country,omitempty"` // ISO 3166-1, DE
	Latitude  *float64 `yaml:"latitude,omitempty" json:"latitude,omitempty"`
	Longitude *float64 `yaml:"longitude,omitempty" json:"longitude,omitempty"`
	Elevation *int     `yaml:"elevation,omitempty" json:"elevation,omitempty"` // metres
}

var (
	postcodeRe = regexp.MustCompile(`^[A-Za-z0-9 -]{2,10}$`)
	countryRe  = regexp.MustCompile(`^[A-Z]{2}$`)
)

func (z Zuhause) validate() []error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf("box.yml zuhause: "+format, a...)) }
	for k, v := range map[string]string{"street": z.Street, "postcode": z.Postcode, "city": z.City, "country": z.Country} {
		if strings.ContainsFunc(v, func(r rune) bool { return r < ' ' }) {
			bad("%s must be one line", k)
		}
	}
	if z.Postcode != "" && !postcodeRe.MatchString(z.Postcode) {
		bad("postcode %q: letters, digits, blanks and dashes, 2 to 10", z.Postcode)
	}
	if z.Country != "" && !countryRe.MatchString(z.Country) {
		bad("country %q: two capital letters (ISO 3166-1, DE)", z.Country)
	}
	switch {
	case (z.Latitude == nil) != (z.Longitude == nil):
		bad("latitude and longitude go together")
	case z.Latitude != nil && (*z.Latitude < -90 || *z.Latitude > 90):
		bad("latitude %v is not between -90 and 90", *z.Latitude)
	case z.Longitude != nil && (*z.Longitude < -180 || *z.Longitude > 180):
		bad("longitude %v is not between -180 and 180", *z.Longitude)
	}
	if z.Elevation != nil && (*z.Elevation < -500 || *z.Elevation > 9000) {
		bad("elevation %d m is not between -500 and 9000", *z.Elevation)
	}
	return errs
}

// zuhauseWarnings: an address without coordinates while a service reads
// them; Laib looks nothing up, that service leaves its location alone.
func (st *State) zuhauseWarnings() []string {
	if z := st.Box.Zuhause; z.Latitude != nil || z.Street == "" && z.City == "" && z.Postcode == "" {
		return nil
	}
	var out []string
	for _, name := range st.ServiceNames() {
		if st.Services[name].Uses("ZUHAUSE_") {
			out = append(out, fmt.Sprintf("box.yml zuhause has an address but no latitude/longitude: %s leaves its location alone; Laib does not look an address up (#561)", name))
		}
	}
	return out
}

// ZuhauseVars are the builtins ZUHAUSE_*, empty where unset; ZUHAUSE_REV
// changes with any of them, so a unit carrying it is applied again.
func (st *State) ZuhauseVars() map[string]string {
	z := st.Box.Zuhause
	num := func(f *float64) string {
		if f == nil {
			return ""
		}
		return strconv.FormatFloat(*f, 'f', -1, 64)
	}
	v := map[string]string{"ZUHAUSE_STREET": z.Street, "ZUHAUSE_POSTCODE": z.Postcode, "ZUHAUSE_CITY": z.City, "ZUHAUSE_COUNTRY": z.Country,
		"ZUHAUSE_LATITUDE": num(z.Latitude), "ZUHAUSE_LONGITUDE": num(z.Longitude), "ZUHAUSE_ELEVATION": ""}
	if z.Elevation != nil {
		v["ZUHAUSE_ELEVATION"] = strconv.Itoa(*z.Elevation)
	}
	b, _ := json.Marshal(v) // map keys sorted
	h := sha256.Sum256(b)
	v["ZUHAUSE_REV"] = hex.EncodeToString(h[:])[:12]
	return v
}
