package tele

import (
	"context"

	"github.com/gotd/td/tg"
)

type Reaction struct {
	Emoji string
	Count int
	Mine  bool
}

func reactionEmoji(r tg.ReactionClass) string {
	switch r := r.(type) {
	case *tg.ReactionEmoji:
		return r.Emoticon
	case *tg.ReactionCustomEmoji:
		return "✦"
	case *tg.ReactionPaid:
		return "⭐"
	}
	return ""
}

// convertReactions maps server reactions. Min results omit which ones are
// ours, so those flags are carried over from prev.
func convertReactions(mr tg.MessageReactions, prev []Reaction) []Reaction {
	mine := map[string]bool{}
	for _, r := range prev {
		mine[r.Emoji] = r.Mine
	}
	var out []Reaction
	for _, rc := range mr.Results {
		e := reactionEmoji(rc.Reaction)
		if e == "" || rc.Count == 0 {
			continue
		}
		r := Reaction{Emoji: e, Count: rc.Count}
		if mr.Min {
			r.Mine = mine[e]
		} else {
			_, r.Mine = rc.GetChosenOrder()
		}
		out = append(out, r)
	}
	return out
}

func (c *Client) onReactions(u *tg.UpdateMessageReactions) {
	k := keyOf(u.Peer)
	s := c.Store
	s.update(func() {
		if m, ok := s.findLocked(k, u.MsgID); ok {
			m.Reactions = convertReactions(u.Reactions, m.Reactions)
			s.upsertLocked(k, m)
		}
	})
}

// React toggles our reaction on a message: picking the reaction we already
// set removes it, anything else replaces it.
func (c *Client) React(k PeerKey, id int, emoji string) {
	input, api, ok := c.inputPeer(k)
	if !ok {
		return
	}
	s := c.Store
	var send []tg.ReactionClass
	s.update(func() {
		m, ok := s.findLocked(k, id)
		if !ok {
			return
		}
		removing := false
		var next []Reaction
		for _, r := range m.Reactions {
			if r.Mine {
				removing = removing || r.Emoji == emoji
				r.Count--
				r.Mine = false
			}
			if r.Count > 0 {
				next = append(next, r)
			}
		}
		if !removing {
			send = []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: emoji}}
			found := false
			for i := range next {
				if next[i].Emoji == emoji {
					next[i].Count++
					next[i].Mine, found = true, true
				}
			}
			if !found {
				next = append(next, Reaction{Emoji: emoji, Count: 1, Mine: true})
			}
		}
		m.Reactions = next
		s.upsertLocked(k, m)
	})
	go func() {
		_, err := api.MessagesSendReaction(context.WithoutCancel(c.ctx), &tg.MessagesSendReactionRequest{
			Peer: input, MsgID: id, Reaction: send, AddToRecent: true,
		})
		if err != nil {
			s.SetStatus("reaction: " + err.Error())
		}
	}()
}
