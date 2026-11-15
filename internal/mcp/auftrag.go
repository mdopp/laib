package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/cli"
)

// An Auftrag is the door's word for one call (ADR 0035, #1234): laib-haushalt
// runs the verb, and the one write it may not do itself (secrets.age, LLDAP,
// the state repo) it asks of the kernel with this, which names the caller and
// the verb and lasts as long as the door waits. The kernel signs it with a
// key derived from the age key, which the container never sees, so it can
// only hand back what the door gave it: the caller's own rights, for that
// verb's kernel part (Verb.Behind), for two minutes.
type auftrag struct{ actor, verb string }

type auftragKey struct{}

func auftragMAC(env *cli.Env, msg string) []byte {
	key, err := os.ReadFile(env.Paths.Key)
	if err != nil || len(key) == 0 {
		return nil
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte("laib-auftrag\n" + msg))
	return m.Sum(nil)
}

// Auftrag signs one call's caller and verb until a time; "" without an age key.
func Auftrag(env *cli.Env, actor, verb string, until time.Time) string {
	msg := actor + "\n" + verb + "\n" + strconv.FormatInt(until.Unix(), 10)
	mac := auftragMAC(env, msg)
	if mac == nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

// readAuftrag checks one: the kernel's signature, not expired.
func readAuftrag(env *cli.Env, s string, now time.Time) (auftrag, bool) {
	b64, sig, _ := strings.Cut(s, ".")
	msg, err1 := base64.RawURLEncoding.DecodeString(b64)
	mac, err2 := base64.RawURLEncoding.DecodeString(sig)
	want := auftragMAC(env, string(msg))
	f := strings.Split(string(msg), "\n")
	if err1 != nil || err2 != nil || want == nil || !hmac.Equal(mac, want) || len(f) != 3 {
		return auftrag{}, false
	}
	until, err := strconv.ParseInt(f[2], 10, 64)
	return auftrag{f[0], f[1]}, err == nil && now.Unix() <= until && f[0] != "" && f[1] != ""
}
