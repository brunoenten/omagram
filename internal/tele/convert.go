package tele

import (
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

func keyOf(p tg.PeerClass) PeerKey {
	switch p := p.(type) {
	case *tg.PeerUser:
		return PeerKey{KindUser, p.UserID}
	case *tg.PeerChat:
		return PeerKey{KindChat, p.ChatID}
	case *tg.PeerChannel:
		return PeerKey{KindChannel, p.ChannelID}
	}
	return PeerKey{}
}

func userName(u *tg.User) string {
	if u == nil {
		return "unknown"
	}
	if u.Deleted {
		return "deleted account"
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = u.Username
	}
	if name == "" {
		name = fmt.Sprintf("user %d", u.ID)
	}
	return name
}

// chatLocked builds a Chat for k from known entities. ok is false if the
// entity (and therefore its access hash) is unknown.
func (s *Store) chatLocked(k PeerKey) (*Chat, bool) {
	c := &Chat{Key: k}
	switch k.Kind {
	case KindUser:
		u, ok := s.users[k.ID]
		if !ok {
			return nil, false
		}
		c.Title = userName(u)
		if u.Self {
			c.Title = "Saved Messages"
		}
		c.input = &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
	case KindChat:
		g, ok := s.groups[k.ID]
		if !ok {
			return nil, false
		}
		c.Title = g.Title
		c.input = &tg.InputPeerChat{ChatID: g.ID}
	case KindChannel:
		ch, ok := s.channels[k.ID]
		if !ok {
			return nil, false
		}
		c.Title = ch.Title
		c.Broadcast = ch.Broadcast
		c.input = &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	default:
		return nil, false
	}
	return c, true
}

func (s *Store) convertLocked(mc tg.MessageClass) (Message, bool) {
	switch m := mc.(type) {
	case *tg.Message:
		out := Message{
			ID:     m.ID,
			Peer:   keyOf(m.PeerID),
			Out:    m.Out,
			Date:   time.Unix(int64(m.Date), 0),
			Text:   m.Message,
			Edited: m.EditDate != 0 && !m.EditHide,
		}
		if h, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			out.ReplyTo = h.ReplyToMsgID
		}
		if mr, ok := m.GetReactions(); ok {
			prev, _ := s.findLocked(out.Peer, m.ID)
			out.Reactions = convertReactions(mr, prev.Reactions)
		}
		if p := inlinePhoto(m.Media); p != nil {
			out.Photo = p
		} else if f := attachFile(m.Media); f != nil {
			out.File = f
		} else if m.Media != nil {
			if label := mediaLabel(m.Media); label != "" {
				out.Text = strings.TrimSpace(label + " " + out.Text)
			}
		}
		s.fillSenderLocked(&out, m.FromID)
		return out, true
	case *tg.MessageService:
		out := Message{
			ID:      m.ID,
			Peer:    keyOf(m.PeerID),
			Out:     m.Out,
			Date:    time.Unix(int64(m.Date), 0),
			Service: true,
		}
		s.fillSenderLocked(&out, m.FromID)
		out.Text = actionLabel(m.Action)
		return out, true
	}
	return Message{}, false
}

func (s *Store) fillSenderLocked(m *Message, from tg.PeerClass) {
	switch {
	case m.Out:
		m.SenderID, m.Sender = s.selfID, "you"
		return
	case from == nil && m.Peer.Kind == KindUser:
		from = &tg.PeerUser{UserID: m.Peer.ID}
	case from == nil:
		from = &tg.PeerChannel{ChannelID: m.Peer.ID}
		if m.Peer.Kind == KindChat {
			from = &tg.PeerChat{ChatID: m.Peer.ID}
		}
	}
	k := keyOf(from)
	m.SenderID = k.ID
	switch k.Kind {
	case KindUser:
		m.Sender = userName(s.users[k.ID])
	case KindChat:
		if g, ok := s.groups[k.ID]; ok {
			m.Sender = g.Title
		}
	case KindChannel:
		if c, ok := s.channels[k.ID]; ok {
			m.Sender = c.Title
		}
	}
	if m.Sender == "" {
		m.Sender = "unknown"
	}
}

// inlinePhoto extracts a photo that can be shown inline. Self-destructing
// and spoiler photos are left to the text label.
func inlinePhoto(media tg.MessageMediaClass) *Photo {
	mp, ok := media.(*tg.MessageMediaPhoto)
	if !ok || mp.Spoiler || mp.TTLSeconds != 0 {
		return nil
	}
	ph, ok := mp.Photo.(*tg.Photo)
	if !ok {
		return nil
	}
	type size struct {
		typ  string
		w, h int
	}
	var sizes []size
	for _, s := range ph.Sizes {
		switch s := s.(type) {
		case *tg.PhotoSize:
			sizes = append(sizes, size{s.Type, s.W, s.H})
		case *tg.PhotoSizeProgressive:
			sizes = append(sizes, size{s.Type, s.W, s.H})
		}
	}
	if len(sizes) == 0 {
		return nil
	}
	thumb, full := sizes[0], sizes[0]
	for _, s := range sizes {
		long := max(s.w, s.h)
		if long > max(full.w, full.h) {
			full = s
		}
		if tl := max(thumb.w, thumb.h); (long <= 800 && long > tl) || (tl > 800 && long < tl) {
			thumb = s
		}
	}
	return &Photo{
		ID: ph.ID, AccessHash: ph.AccessHash, FileRef: ph.FileReference,
		W: thumb.w, H: thumb.h, Thumb: thumb.typ, Full: full.typ,
	}
}

// attachFile extracts a downloadable document. Stickers, spoilers and
// self-destructing media stay as text labels.
func attachFile(media tg.MessageMediaClass) *File {
	md, ok := media.(*tg.MessageMediaDocument)
	if !ok || md.Spoiler || md.TTLSeconds != 0 {
		return nil
	}
	doc, ok := md.Document.(*tg.Document)
	if !ok {
		return nil
	}
	f := &File{
		ID: doc.ID, AccessHash: doc.AccessHash, FileRef: doc.FileReference,
		Mime: doc.MimeType, Size: doc.Size, Kind: FileDoc,
	}
	for _, a := range doc.Attributes {
		switch a := a.(type) {
		case *tg.DocumentAttributeSticker:
			return nil
		case *tg.DocumentAttributeAudio:
			f.Duration = float64(a.Duration)
			if a.Voice || md.Voice {
				f.Kind = FileVoice
			} else {
				f.Kind = FileAudio
				f.Title = strings.TrimSpace(a.Performer + " – " + a.Title)
			}
		case *tg.DocumentAttributeVideo:
			f.Duration = a.Duration
			f.Kind = FileVideo
		case *tg.DocumentAttributeAnimated:
			f.Kind = FileGIF
		case *tg.DocumentAttributeFilename:
			f.Name = a.FileName
		}
	}
	if md.Video && f.Kind == FileDoc {
		f.Kind = FileVideo
	}
	if md.Voice && f.Kind == FileDoc {
		f.Kind = FileVoice
	}
	if f.Name == "" {
		if f.Title != "" {
			f.Name = f.Title
		} else {
			f.Name = defaultName(f)
		}
	}
	return f
}

func defaultName(f *File) string {
	ext := ""
	if i := strings.LastIndex(f.Mime, "/"); i >= 0 && i+1 < len(f.Mime) {
		ext = "." + f.Mime[i+1:]
		if ext == ".mpeg" {
			ext = ".mp3"
		}
	}
	switch f.Kind {
	case FileVoice:
		return "voice" + orExt(ext, ".ogg")
	case FileAudio:
		return "audio" + orExt(ext, ".mp3")
	case FileVideo:
		return "video" + orExt(ext, ".mp4")
	case FileGIF:
		return "animation" + orExt(ext, ".mp4")
	}
	return "file" + orExt(ext, "")
}

func orExt(ext, fallback string) string {
	if ext == "" || ext == ".octet-stream" {
		return fallback
	}
	return ext
}

func mediaLabel(media tg.MessageMediaClass) string {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		return "[photo]"
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok {
			return "[file]"
		}
		for _, a := range doc.Attributes {
			switch a := a.(type) {
			case *tg.DocumentAttributeSticker:
				return "[sticker " + a.Alt + "]"
			}
		}
		return "[file]"
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
		return "[location]"
	case *tg.MessageMediaContact:
		return "[contact " + strings.TrimSpace(m.FirstName+" "+m.LastName) + "]"
	case *tg.MessageMediaPoll:
		return "[poll]"
	case *tg.MessageMediaWebPage, *tg.MessageMediaEmpty:
		return ""
	}
	return "[media]"
}

func actionLabel(a tg.MessageActionClass) string {
	switch a := a.(type) {
	case *tg.MessageActionChatCreate:
		return "created the group " + a.Title
	case *tg.MessageActionChannelCreate:
		return "created the channel " + a.Title
	case *tg.MessageActionChatEditTitle:
		return "changed the title to " + a.Title
	case *tg.MessageActionChatEditPhoto:
		return "changed the photo"
	case *tg.MessageActionChatAddUser, *tg.MessageActionChatJoinedByLink, *tg.MessageActionChatJoinedByRequest:
		return "joined"
	case *tg.MessageActionChatDeleteUser:
		return "left"
	case *tg.MessageActionPinMessage:
		return "pinned a message"
	case *tg.MessageActionPhoneCall:
		if a.Video {
			return "video call"
		}
		return "call"
	case *tg.MessageActionHistoryClear:
		return "history cleared"
	}
	return "service message"
}
