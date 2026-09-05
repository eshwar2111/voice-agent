package tools

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/yourname/voice-agent/internal/ui"
	"google.golang.org/api/gmail/v1"
)

// contact is a resolved recipient (display name + email).
type contact struct {
	Name  string
	Email string
}

// resolveRecipient turns a spoken recipient into an email address. If `to` is
// already an email it's used as-is. Otherwise it's treated as a NAME and
// resolved from the user's Gmail correspondence (ask-don't-guess): 0 matches
// errors with a helpful message, 1 is used, and 2+ prompt the user to pick which
// contact via a compact island choice. Uses only the existing gmail scope — no
// People/Contacts scope or re-link required.
func resolveRecipient(ctx context.Context, srv *gmail.Service, to string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", fmt.Errorf("no recipient given")
	}
	if strings.Contains(to, "@") {
		return to, nil // already an address
	}

	cands := searchContacts(ctx, srv, to)
	switch len(cands) {
	case 0:
		return "", fmt.Errorf("couldn't find a contact named %q — say the full email address instead", to)
	case 1:
		return cands[0].Email, nil
	default:
		opts := make([]ui.Option, 0, len(cands))
		for _, c := range cands {
			label := c.Name
			if label == "" {
				label = c.Email
			}
			opts = append(opts, ui.Option{ID: c.Email, Label: label, Sub: c.Email})
		}
		id, ok := ui.AskChoice(fmt.Sprintf("Which %q do you mean?", to), opts)
		if !ok {
			return "", fmt.Errorf("recipient not chosen")
		}
		return id, nil
	}
}

// searchContacts finds distinct people the user has corresponded with whose name
// or address matches `name`, by scanning recent Gmail headers. Capped so it stays
// fast and the choice list stays short.
func searchContacts(ctx context.Context, srv *gmail.Service, name string) []contact {
	q := strings.ToLower(strings.TrimSpace(name))
	list, err := srv.Users.Messages.List("me").
		Q(fmt.Sprintf("from:%s OR to:%s", name, name)).
		MaxResults(25).Do()
	if err != nil || list == nil {
		return nil
	}

	byEmail := map[string]contact{}
	order := []string{}
	for _, m := range list.Messages {
		if len(byEmail) >= 6 {
			break
		}
		msg, err := srv.Users.Messages.Get("me", m.Id).
			Format("metadata").MetadataHeaders("From", "To", "Cc").Do()
		if err != nil || msg.Payload == nil {
			continue
		}
		for _, h := range msg.Payload.Headers {
			addrs, perr := mail.ParseAddressList(h.Value)
			if perr != nil {
				continue
			}
			for _, a := range addrs {
				email := strings.ToLower(strings.TrimSpace(a.Address))
				if email == "" {
					continue
				}
				// Only keep addresses whose display name or address matches.
				if !strings.Contains(strings.ToLower(a.Name), q) && !strings.Contains(email, q) {
					continue
				}
				if _, seen := byEmail[email]; !seen {
					byEmail[email] = contact{Name: strings.TrimSpace(a.Name), Email: a.Address}
					order = append(order, email)
				}
			}
		}
	}

	out := make([]contact, 0, len(order))
	for _, e := range order {
		out = append(out, byEmail[e])
	}
	return out
}
