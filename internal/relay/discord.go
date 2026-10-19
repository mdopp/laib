package relay

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Discord is the relay's view of the owner's direct chat with the Bote's
// bot (#463, ADR 0016 amendment). The relay reads that DM itself over
// Discord's REST API with the bot token, never through Hermes, and decides
// only on a message whose author Discord names as the owner: a bot token
// can post as the bot, never as a person, so Hermes holding the same token
// cannot forge a verdict. Only what the chat may decide counts (#441): the
// kernel refuses the rest to the approver.
type Discord struct {
	API, Token, Channel string        // API default https://discord.com/api/v10; Channel the DM's id
	Every               time.Duration // poll interval, default 5 s (Discord's limit is 5 per 5 s per route)
	Retry               time.Duration // a failed Check is tried again after this, default 1 min
	H                   *http.Client
	self                string // the bot's own id, set by Check
}

func (d *Discord) call(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cmp.Or(d.API, "https://discord.com/api/v10"), "/")+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bot "+d.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cmp.Or(d.H, &http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 == 2 && out != nil {
		return resp.StatusCode, json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

// Check refuses what the relay cannot verify: no token, the owner being the
// bot itself, a channel that is not a direct chat (type 1) between the bot
// and the owner alone.
func (d *Discord) Check(ctx context.Context, admin string) error {
	if d.Token == "" {
		return fmt.Errorf("no Discord token (laib secret set BOTE_DISCORD_TOKEN)")
	}
	var me struct{ ID string }
	if code, err := d.call(ctx, "GET", "/users/@me", nil, &me); err != nil || code != 200 {
		return failed("discord users/@me", code, err)
	}
	if d.self = me.ID; admin == me.ID {
		return fmt.Errorf("ADMIN_DISCORD_ID %s is the bot itself", admin)
	}
	var ch struct {
		Type       int
		Recipients []struct{ ID string }
	}
	if code, err := d.call(ctx, "GET", "/channels/"+d.Channel, nil, &ch); err != nil || code != 200 {
		return failed("discord channel "+d.Channel, code, err)
	}
	if ch.Type != 1 || len(ch.Recipients) != 1 || ch.Recipients[0].ID != admin {
		return fmt.Errorf("channel %s is not the direct chat of %s with the bot", d.Channel, admin)
	}
	return nil
}

// Send one message to the DM, as the bot. to is "discord:<channel>".
func (d *Discord) Send(ctx context.Context, to, text string) error {
	code, err := d.call(ctx, "POST", "/channels/"+strings.TrimPrefix(to, "discord:")+"/messages", map[string]string{"content": text}, nil)
	if err != nil || code/100 != 2 {
		return failed("discord send", code, err)
	}
	return nil
}

// newer sorts snowflakes: a longer id is newer, then by digits.
func newer(a, b string) bool { return len(a) > len(b) || len(a) == len(b) && a > b }

// RunDiscord reads the DM from now on until the context ends or a read
// fails; history before the start is never acted on.
func (r *Relay) RunDiscord(ctx context.Context, d *Discord) {
	every, last := cmp.Or(d.Every, 5*time.Second), ""
	for ctx.Err() == nil {
		var page []struct {
			ID, Content string
			Author      struct {
				ID  string
				Bot bool
			}
		}
		q := "?limit=1" // from now on: the newest message is the mark
		if last != "" {
			q = "?limit=50&after=" + last
		}
		code, err := d.call(ctx, "GET", "/channels/"+d.Channel+"/messages"+q, nil, &page)
		if err != nil || code != 200 {
			if ctx.Err() == nil {
				log.Printf("relay: %v", failed("discord messages", code, err))
			}
			return
		}
		sort.Slice(page, func(i, j int) bool { return newer(page[j].ID, page[i].ID) })
		for _, m := range page {
			if last != "" && m.Author.ID != d.self && !m.Author.Bot {
				r.Handle(ctx, Message{Platform: "discord", From: m.Author.ID, Room: d.Channel, Text: strings.TrimSpace(m.Content)})
			}
			last = m.ID
		}
		last = cmp.Or(last, "0")
		select {
		case <-ctx.Done():
		case <-time.After(every):
		}
	}
}

// WatchDiscord keeps Discord verdicts going without taking the relay down,
// like WatchMatrix: a failed Check or read is journaled once per reason and
// tried again.
func (r *Relay) WatchDiscord(ctx context.Context, d *Discord, admin string) {
	last := ""
	for ctx.Err() == nil {
		if err := d.Check(ctx, admin); err != nil {
			if err.Error() != last && ctx.Err() == nil {
				last = err.Error()
				log.Printf("relay: Discord verdicts off: %v", err)
				r.laib(ctx, "note", fmt.Sprintf("--kind watch \"Discord-Freigaben aus: %s. Die Box-Seite geht weiter.\"", strings.ReplaceAll(last, "\"", "'")))
			}
		} else {
			last = ""
			log.Printf("relay: Discord verdicts on: %s in %s", admin, d.Channel)
			r.RunDiscord(ctx, d)
		}
		select {
		case <-ctx.Done():
		case <-time.After(cmp.Or(d.Retry, time.Minute)):
		}
	}
}
