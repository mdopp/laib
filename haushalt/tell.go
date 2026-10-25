package haushalt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Bote is bote-notify as laib renders it into this program's unit (#676):
// its address on the host and its bearer; both empty without a Bote.
type Bote struct {
	URL, Token string
}

// maxTell is bote-notify's own cut (templates/bote/notify.py max_text):
// longer is refused here, not cut there without a word.
const maxTell = 1000

// Tell: `laib tell <person> "<text>"` (#676, haus#123), one message to one
// person of box.yml people through bote-notify, which takes that person's
// first reachable channel. Haus tells the subscriber of a podcast its new
// episode, later the shopping list's changes. The answer is
// {sent, message} whatever happened, exit 0 only when it went out; the
// journal names the person and the channel, never the text.
func Tell(ctx context.Context, env *Env, args []string) int {
	if len(args) < 2 {
		return env.fail(errors.New(`usage: laib tell <person> "<text>"`))
	}
	person, text := args[0], strings.TrimSpace(strings.Join(args[1:], " "))
	switch {
	case !scanPerson.MatchString(person):
		return env.fail(fmt.Errorf("%q: ein Name aus box.yml people, klein", person))
	case text == "":
		return env.fail(errors.New("die Nachricht ist leer"))
	case utf8.RuneCountInString(text) > maxTell:
		return env.fail(fmt.Errorf("höchstens %d Zeichen, ein kurzer Satz", maxTell))
	}
	told := func(sent bool, msg string) int {
		if env.JSON {
			env.emit(map[string]any{"sent": sent, "message": msg})
		} else {
			fmt.Fprintln(env.Out, msg)
		}
		if !sent {
			return 1
		}
		return exitOK
	}
	if env.Bote.URL == "" || env.Bote.Token == "" {
		return told(false, "Auf dieser Box ist kein Bote, der Nachrichten zustellt.")
	}
	body, _ := json.Marshal(map[string]string{"an": person, "text": text})
	ctx, cancel := context.WithTimeout(ctx, time.Minute) // bote-notify tries every channel within 50 s
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(env.Bote.URL, "/")+"/mitteilung", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.Bote.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return told(false, "Der Bote antwortet nicht; die Nachricht an "+person+" ist nicht verschickt.")
	}
	defer resp.Body.Close()
	var r struct {
		Kanal string `json:"kanal"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	switch resp.StatusCode {
	case http.StatusOK:
		env.journal("", "", "an "+person+" über "+r.Kanal)
		return told(true, fmt.Sprintf("An %s über %s geschickt.", person, r.Kanal))
	case http.StatusNotFound:
		return told(false, person+" steht nicht mit einem Kanal in box.yml people.")
	case http.StatusTooManyRequests:
		return told(false, "Zu viele Nachrichten in dieser Minute; später noch einmal.")
	case http.StatusBadGateway:
		env.journal("", "", "an "+person+": kein Kanal hat angenommen")
		return told(false, "Kein Kanal von "+person+" hat die Nachricht angenommen.")
	}
	return told(false, fmt.Sprintf("Der Bote lehnt ab (HTTP %d); die Nachricht an %s ist nicht verschickt.", resp.StatusCode, person))
}
