package state

import (
	"net/url"
	"strconv"
	"strings"
)

// instance expands ${SERVICE} and ${PERSON} in the data paths and the
// status file, and makes a person's instance of a per-person template
// (#986, ADR 0029) its own, so that two never meet: a unit file named
// <template>… is named <service>… (Quadlet names a unit by its file, and
// the container after the unit), a declared secret and every reference to
// it ends in _<PERSON> (secrets.age has one namespace), and every host
// port, published or declared, moves by the port offset. The template's
// units name each other through ${SERVICE}.
func (svc *Service) instance() {
	c := &svc.Contract
	r := strings.NewReplacer("${SERVICE}", svc.Name, "${PERSON}", c.Person)
	for i := range c.Data {
		c.Data[i].Path = r.Replace(c.Data[i].Path)
	}
	if c.Status != nil {
		c.Status.File = r.Replace(c.Status.File)
	}
	if c.Person == "" {
		return
	}
	rename := func(file string) string {
		if rest, ok := strings.CutPrefix(file, c.Template.Name); ok && c.Template.Name != "" && (strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "-")) {
			return svc.Name + rest
		}
		return file
	}
	units := map[string]string{}
	for file, content := range svc.Units {
		lines := strings.Split(content, "\n")
		for i, l := range lines {
			spec, ok := strings.CutPrefix(strings.TrimSpace(l), "PublishPort=")
			parts := strings.Split(spec, ":")
			if n, err := strconv.Atoi(parts[max(len(parts)-2, 0)]); ok && len(parts) > 1 && err == nil {
				parts[len(parts)-2] = strconv.Itoa(n + c.PortOffset)
				lines[i] = "PublishPort=" + strings.Join(parts, ":")
			}
		}
		units[rename(file)] = strings.Join(lines, "\n")
	}
	svc.Units = units
	if c.Status != nil {
		c.Status.Check = rename(c.Status.Check)
	}
	suffix, own := "_"+strings.ToUpper(strings.ReplaceAll(c.Person, "-", "_")), map[string]bool{}
	for i := range c.Secrets {
		own[c.Secrets[i].Name] = true
		c.Secrets[i].Name += suffix
	}
	for k, v := range svc.Values {
		if own[v.Secret] {
			svc.Values[k] = Value{Secret: v.Secret + suffix}
		}
	}
	for i := range c.Routes {
		c.Routes[i].Port += c.PortOffset
		if own[c.Routes[i].ProxySecret] {
			c.Routes[i].ProxySecret += suffix
		}
	}
	for i := range c.Ports {
		c.Ports[i].Port += c.PortOffset
	}
	if u, err := url.Parse(c.Health.HTTP); err == nil && u.Port() != "" && c.PortOffset != 0 {
		n, _ := strconv.Atoi(u.Port())
		u.Host = u.Hostname() + ":" + strconv.Itoa(n+c.PortOffset)
		c.Health.HTTP = u.String()
	}
}

// Instance: the person's instance of a per-person template whose route
// is at host, with that route (#986); the services in name order. A host
// shared this way is one Caddy site that hands a signed-in person to their
// instance (Remote-User) and /p/<person>/ to theirs past Authelia.
type Instance struct {
	Service *Service
	Route   Route
}

// Instances are the persons' instances whose routes share host; none for
// a plain service's host.
func (st *State) Instances(host string) []Instance {
	var out []Instance
	for _, name := range st.ServiceNames() {
		for _, r := range st.Services[name].Contract.Routes {
			if r.Host == host && st.Services[name].Contract.Person != "" {
				out = append(out, Instance{st.Services[name], r})
			}
		}
	}
	return out
}
