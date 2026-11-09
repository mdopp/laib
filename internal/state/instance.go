package state

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// PersonSuffix ends a person's instance's secret names (#986): _<PERSON>,
// upper case, '-' as '_'.
func PersonSuffix(person string) string {
	return "_" + strings.ToUpper(strings.ReplaceAll(person, "-", "_"))
}

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
		c.Data[i].Path, c.Data[i].Was = r.Replace(c.Data[i].Path), r.Replace(c.Data[i].Was)
	}
	if c.Status != nil {
		c.Status.File = r.Replace(c.Status.File)
	}
	for i := range c.Secrets {
		c.Secrets[i].Account = r.Replace(c.Secrets[i].Account)
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
	suffix, own := PersonSuffix(c.Person), map[string]bool{}
	for i := range c.Secrets {
		own[c.Secrets[i].Name] = true
		c.Secrets[i].Name += suffix
		if c.Secrets[i].Was != "" {
			c.Secrets[i].Was += suffix
		}
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
// instance (Remote-User) and, where its person switched it on, /p/<person>/
// to an agent past Authelia (AgentPaths).
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

// AgentPaths: the instances whose values.yml sets AGENT_PATH, the only ones
// with /p/<person>/, an agent's way past Authelia with the instance's key.
// Off by default: the archive has one door, the person's login (plan 12).
func AgentPaths(is []Instance) []Instance {
	return slices.DeleteFunc(slices.Clone(is), func(i Instance) bool { return !i.Service.Set("AGENT_PATH") })
}

// Account is a service account the identity service keeps for a service
// (Secret.Account): its name, the secret holding its password, the service.
type Account struct {
	User    string `json:"user"`
	Secret  string `json:"secret"`
	Service string `json:"service"`
}

// Accounts: every declared service account, in service order.
func (st *State) Accounts() []Account {
	var out []Account
	for _, name := range st.ServiceNames() {
		for _, s := range st.Services[name].Contract.Secrets {
			if s.Account != "" {
				out = append(out, Account{s.Account, s.Name, name})
			}
		}
	}
	return out
}

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// reach wires the instances a service names under instances: into its
// container (laib#1016), right after [Container]: one line with every
// instance's address, and for each instance its secret as a value of the
// service, so the renderer hands it over as a podman secret like any
// !secret (never in the unit), a review names it as another service's
// secret, and a change of it applies the service again (SecretUsers).
// Adding or removing an instance changes the unit: the service applies
// again. After validate: the secret values are the instances' own.
func (st *State) reach() []error {
	var errs []error
	for _, name := range st.ServiceNames() {
		svc := st.Services[name]
		for _, r := range svc.Contract.Instances {
			unit, ok := svc.Units[r.Unit]
			if r.Template == "" || !envName.MatchString(r.URL) || (r.Secret == "") != (r.Env == "") || r.Env != "" && !envName.MatchString(r.Env) ||
				!ok || !strings.HasSuffix(r.Unit, ".container") || !strings.Contains(unit, "\n[Container]\n") {
				errs = append(errs, fmt.Errorf("services/%s/laib.yml: instances: %s wants a template, a .container of the service as unit, url as an environment name, and secret with env or neither (laib#1016)", name, r.Template))
				continue
			}
			var urls, lines []string
			for _, other := range st.ServiceNames() {
				c := st.Services[other].Contract
				if c.Person == "" || c.Template.Name != r.Template {
					continue
				}
				if len(c.Routes) == 0 {
					errs = append(errs, fmt.Errorf("services/%s/laib.yml: instances: %s has no route to reach", name, other))
					continue
				}
				urls = append(urls, fmt.Sprintf("%s=http://host.containers.internal:%d", c.Person, c.Routes[0].Port))
				if r.Secret == "" {
					continue
				}
				secret, key := r.Secret+PersonSuffix(c.Person), r.Env+PersonSuffix(c.Person)
				if _, taken := svc.Values[key]; taken || !slices.ContainsFunc(c.Secrets, func(s Secret) bool { return s.Name == secret }) {
					errs = append(errs, fmt.Errorf("services/%s/laib.yml: instances: %s does not declare %s, or values.yml has %s already", name, other, r.Secret, key))
					continue
				}
				svc.Values[key] = Value{Secret: secret}
				lines = append(lines, "Environment="+key+"=${"+key+"}")
			}
			lines = append([]string{"# each person's " + r.Template + " (laib#1016: instances:)", `Environment="` + r.URL + "=" + strings.Join(urls, " ") + `"`}, lines...)
			svc.Units[r.Unit] = strings.Replace(unit, "\n[Container]\n", "\n[Container]\n"+strings.Join(lines, "\n")+"\n", 1)
		}
	}
	return errs
}
