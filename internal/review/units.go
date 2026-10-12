package review

import (
	"strings"
)

// entry is one key=value line of a unit file, with its section.
type entry struct {
	Section, Key, Value string
}

// parseUnit reads a systemd/Quadlet unit into its entries, in order.
// Repeated keys (Volume, Environment, PublishPort) stay repeated.
func parseUnit(text string) []entry {
	var out []entry
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out = append(out, entry{Section: section, Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
	}
	return out
}

// diffUnits returns the entries added to or changed in `after` and the ones
// removed from `before`. An entry counts by section, key and value: a Volume
// line with a new source is "added" and the old one "removed".
func diffUnits(before, after []entry) (added, removed []entry) {
	count := func(es []entry) map[entry]int {
		m := map[entry]int{}
		for _, e := range es {
			m[e]++
		}
		return m
	}
	b, a := count(before), count(after)
	for _, e := range after {
		if b[e] == 0 {
			added = append(added, e)
		} else {
			b[e]--
		}
	}
	for _, e := range before {
		if a[e] == 0 {
			removed = append(removed, e)
		} else {
			a[e]--
		}
	}
	return
}

// imageRepo is the image reference without its tag or digest:
// docker.io/traefik/whoami:v1.10 → docker.io/traefik/whoami.
func imageRepo(ref string) string {
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndexByte(ref, '/')
	if colon := strings.LastIndexByte(ref, ':'); colon > slash {
		ref = ref[:colon]
	}
	return ref
}

// publishHost is the host side of a PublishPort value: the ip (may be empty)
// and the host port.
func publishHost(v string) (ip, port string) {
	parts := strings.Split(v, ":")
	switch len(parts) {
	case 1:
		return "", parts[0] // containerPort only: podman picks a host port on every interface
	case 2:
		return "", parts[0]
	}
	// ip:hostPort:containerPort — an IPv6 ip has more colons; the last two are the ports
	return strings.Join(parts[:len(parts)-2], ":"), parts[len(parts)-2]
}

func loopback(ip string) bool {
	ip = strings.Trim(ip, "[]")
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost"
}
