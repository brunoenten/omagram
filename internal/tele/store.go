package tele

import (
	"sort"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

type PeerKind uint8

const (
	KindUser PeerKind = iota + 1
	KindChat
	KindChannel
)

type PeerKey struct {
	Kind PeerKind
	ID   int64
}

func (k PeerKey) Zero() bool { return k.Kind == 0 }

type Chat struct {
	Key        PeerKey
	Title      string
	Unread     int
	Preview    string
	LastDate   int
	Pinned     bool
	MutedUntil int
	Broadcast  bool

	input tg.InputPeerClass
}

func (c Chat) Muted() bool { return c.MutedUntil > int(time.Now().Unix()) }

type Message struct {
	ID       int
	Peer     PeerKey
	SenderID int64
	Sender   string
	Out      bool
	Date     time.Time
	Text     string
	ReplyTo  int
	Edited   bool
	Service  bool
}

type AuthState int

const (
	AuthConnecting AuthState = iota
	AuthQR
	AuthPassword
	AuthReady
	AuthFailed
)

type Auth struct {
	State     AuthState
	QRURL     string
	QRExpires time.Time
	Error     string
}

// Store holds everything the UI renders. All access goes through its mutex;
// readers get copies.
type Store struct {
	mu       sync.RWMutex
	onChange func()

	auth    Auth
	selfID  int64
	online  bool
	status  string
	statusT time.Time

	users    map[int64]*tg.User
	groups   map[int64]*tg.Chat
	channels map[int64]*tg.Channel

	chats    map[PeerKey]*Chat
	messages map[PeerKey][]Message
	loading  map[PeerKey]bool
	loaded   map[PeerKey]bool
	complete map[PeerKey]bool
}

func NewStore(onChange func()) *Store {
	return &Store{
		onChange: onChange,
		users:    map[int64]*tg.User{},
		groups:   map[int64]*tg.Chat{},
		channels: map[int64]*tg.Channel{},
		chats:    map[PeerKey]*Chat{},
		messages: map[PeerKey][]Message{},
		loading:  map[PeerKey]bool{},
		loaded:   map[PeerKey]bool{},
		complete: map[PeerKey]bool{},
	}
}

func (s *Store) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

func (s *Store) update(f func()) {
	s.mu.Lock()
	f()
	s.mu.Unlock()
	s.changed()
}

func (s *Store) Auth() Auth {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.auth
}

func (s *Store) setAuth(a Auth) { s.update(func() { s.auth = a }) }

func (s *Store) Online() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.online
}

func (s *Store) setOnline(v bool) { s.update(func() { s.online = v }) }

// Status returns a transient status/error line, if one is still fresh.
func (s *Store) Status() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if time.Since(s.statusT) > 6*time.Second {
		return ""
	}
	return s.status
}

func (s *Store) SetStatus(msg string) {
	s.update(func() { s.status, s.statusT = msg, time.Now() })
}

// Chats returns all chats, pinned first, then by recency.
func (s *Store) Chats() []Chat {
	s.mu.RLock()
	out := make([]Chat, 0, len(s.chats))
	for _, c := range s.chats {
		out = append(out, *c)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if out[i].LastDate != out[j].LastDate {
			return out[i].LastDate > out[j].LastDate
		}
		return out[i].Key.ID < out[j].Key.ID
	})
	return out
}

func (s *Store) Chat(k PeerKey) (Chat, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.chats[k]
	if !ok {
		return Chat{}, false
	}
	return *c, true
}

func (s *Store) Messages(k PeerKey) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Message(nil), s.messages[k]...)
}

func (s *Store) Message(k PeerKey, id int) (Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ms := s.messages[k]
	i := sort.Search(len(ms), func(i int) bool { return ms[i].ID >= id })
	if i < len(ms) && ms[i].ID == id {
		return ms[i], true
	}
	return Message{}, false
}

func (s *Store) HistoryState(k PeerKey) (loaded, loading, complete bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded[k], s.loading[k], s.complete[k]
}

func (s *Store) TotalUnread() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, c := range s.chats {
		if !c.Muted() {
			n += c.Unread
		}
	}
	return n
}

// upsertLocked inserts or replaces messages, keeping them sorted by ID.
func (s *Store) upsertLocked(k PeerKey, msgs ...Message) {
	ms := s.messages[k]
	for _, m := range msgs {
		i := sort.Search(len(ms), func(i int) bool { return ms[i].ID >= m.ID })
		if i < len(ms) && ms[i].ID == m.ID {
			ms[i] = m
			continue
		}
		ms = append(ms, Message{})
		copy(ms[i+1:], ms[i:])
		ms[i] = m
	}
	s.messages[k] = ms
}

func (s *Store) addEntitiesLocked(users []tg.UserClass, chats []tg.ChatClass) {
	for _, u := range users {
		if u, ok := u.(*tg.User); ok {
			if old, ok := s.users[u.ID]; ok && u.Min && !old.Min {
				continue
			}
			s.users[u.ID] = u
		}
	}
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Chat:
			s.groups[c.ID] = c
		case *tg.Channel:
			if old, ok := s.channels[c.ID]; ok && c.Min && !old.Min {
				continue
			}
			s.channels[c.ID] = c
		}
	}
}

func (s *Store) addEntityMapsLocked(e tg.Entities) {
	for _, u := range e.Users {
		if old, ok := s.users[u.ID]; ok && u.Min && !old.Min {
			continue
		}
		s.users[u.ID] = u
	}
	for _, c := range e.Chats {
		s.groups[c.ID] = c
	}
	for _, c := range e.Channels {
		if old, ok := s.channels[c.ID]; ok && c.Min && !old.Min {
			continue
		}
		s.channels[c.ID] = c
	}
}
