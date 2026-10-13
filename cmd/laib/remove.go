package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// cmdRemove takes a service out of the state and off the box: its units are
// stopped, its folder leaves the state repo (a commit — `laib rollback`
// brings it back), the apply removes the rendered files. Data paths stay:
// deleting data is the human's own, separate act, and Laib says where it is.
func cmdRemove(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		return fail(errors.New("usage: laib remove <service>"))
	}
	if proposal.Hand(env.actor) != "human" {
		return exitRefusedMsg(env, "only a human removes a service")
	}
	name := args[0]
	st, svc, err := loadSvc(env, name)
	if err != nil {
		return fail(err)
	}
	// nobody may need it (#50): a dependant left behind makes the state
	// unloadable, and the refusal comes before anything is stopped or deleted
	for _, other := range st.ServiceNames() {
		for _, dep := range st.Services[other].Contract.After {
			if dep == name {
				return fail(fmt.Errorf("%s braucht %s (after:) — erst dort entfernen", other, name))
			}
		}
	}
	for role, holder := range map[string]string{"proxy": st.Box.Roles.Proxy, "sso": st.Box.Roles.SSO, "identity": st.Box.Roles.Identity, "dns": st.Box.Roles.DNS, "model": st.Box.Roles.Model} {
		if holder == name {
			return fail(fmt.Errorf("%s holds the %s role: point box.yml roles.%s elsewhere first", name, role, role))
		}
	}
	unlock, code := takeLock(env, st, "remove")
	if code != exitOK {
		return code
	}
	defer unlock()
	for _, unit := range svc.UnitNames() {
		sysd.Stop(ctx, unit)
	}
	kept := dataPaths(st, svc)
	if err := os.RemoveAll(filepath.Join(env.paths.State, "services", name)); err != nil {
		return fail(err)
	}
	// the service's Quadlet folder and its rendered files go with it: plan only
	// scans folders of services the state still has (an unknown folder is
	// "unmanaged", never deleted on its own)
	os.RemoveAll(filepath.Join(env.paths.Quadlet, name))
	os.RemoveAll(filepath.Join(env.paths.Render, name))
	st2, err := loadState(env)
	if err != nil {
		return fail(fmt.Errorf("state without %s does not load: %w (git checkout -- services/%s restores it)", name, err, name))
	}
	code = applyHeld(ctx, env, st2, "", "remove "+name)
	if len(kept) > 0 {
		fmt.Fprintf(env.w(), "Daten bleiben: %s  (löschen ist dein Schritt; laib rollback holt den Dienst zurück)\n", strings.Join(kept, ", "))
	}
	return code
}
