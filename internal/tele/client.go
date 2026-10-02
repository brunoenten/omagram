package tele

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/updates/hook"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const historyPage = 50

type Client struct {
	Store *Store

	// OnIncoming is called for every new incoming (not outgoing, not edited) message.
	OnIncoming func(Chat, Message)

	appID   int
	appHash string
	dataDir string

	ctx      context.Context
	api      *tg.Client
	password chan string
}

func New(appID int, appHash, dataDir string, store *Store) *Client {
	return &Client{
		Store:    store,
		appID:    appID,
		appHash:  appHash,
		dataDir:  dataDir,
		password: make(chan string),
	}
}

// Run connects, authenticates and processes updates until ctx is cancelled.
// It reconnects on failure.
func (c *Client) Run(ctx context.Context) {
	c.ctx = ctx
	for ctx.Err() == nil {
		err := c.run(ctx)
		if ctx.Err() != nil {
			return
		}
		c.Store.setOnline(false)
		if err != nil {
			c.Store.SetStatus("disconnected: " + err.Error())
			if a := c.Store.Auth(); a.State != AuthReady {
				c.Store.setAuth(Auth{State: AuthFailed, Error: err.Error()})
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *Client) run(ctx context.Context) error {
	if err := os.MkdirAll(c.dataDir, 0o700); err != nil {
		return err
	}

	d := tg.NewUpdateDispatcher()
	gaps := updates.New(updates.Config{Handler: d})
	loggedIn := qrlogin.OnLoginToken(d)
	c.registerHandlers(d)

	client := telegram.NewClient(c.appID, c.appHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: filepath.Join(c.dataDir, "session.json")},
		UpdateHandler:  gaps,
		Middlewares:    []telegram.Middleware{hook.UpdateHook(gaps.Handle), hook.AffectedHook(gaps)},
		OnConnectionState: func(s telegram.ConnectionState) {
			c.Store.setOnline(s == telegram.ConnectionStateReady)
		},
	})

	return client.Run(ctx, func(ctx context.Context) error {
		c.Store.mu.Lock()
		c.api = client.API()
		c.Store.mu.Unlock()
		c.Store.setOnline(true)

		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			if err := c.login(ctx, client, loggedIn); err != nil {
				return err
			}
		}

		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		c.Store.update(func() {
			c.Store.selfID = self.ID
			c.Store.users[self.ID] = self
		})
		c.Store.setAuth(Auth{State: AuthReady})

		if err := c.loadDialogs(ctx); err != nil {
			c.Store.SetStatus("loading chats: " + err.Error())
		}
		return gaps.Run(ctx, c.api, self.ID, updates.AuthOptions{})
	})
}

func (c *Client) login(ctx context.Context, client *telegram.Client, loggedIn qrlogin.LoggedIn) error {
	_, err := client.QR().Auth(ctx, loggedIn, func(ctx context.Context, t qrlogin.Token) error {
		c.Store.setAuth(Auth{State: AuthQR, QRURL: t.URL(), QRExpires: t.Expires()})
		return nil
	})
	if err == nil || !tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		return err
	}

	c.Store.setAuth(Auth{State: AuthPassword})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case pw := <-c.password:
			_, err := client.Auth().Password(ctx, pw)
			if err == nil {
				return nil
			}
			if errors.Is(err, auth.ErrPasswordInvalid) {
				c.Store.setAuth(Auth{State: AuthPassword, Error: "wrong password"})
				continue
			}
			return err
		}
	}
}

// SubmitPassword delivers the 2FA password to a pending login.
func (c *Client) SubmitPassword(pw string) {
	go func() {
		select {
		case c.password <- pw:
		case <-time.After(5 * time.Second):
		}
	}()
}

func (c *Client) registerHandlers(d tg.UpdateDispatcher) {
	d.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		c.onMessage(e, u.Message, false)
		return nil
	})
	d.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		c.onMessage(e, u.Message, false)
		return nil
	})
	d.OnEditMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditMessage) error {
		c.onMessage(e, u.Message, true)
		return nil
	})
	d.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		c.onMessage(e, u.Message, true)
		return nil
	})
	d.OnReadHistoryInbox(func(ctx context.Context, e tg.Entities, u *tg.UpdateReadHistoryInbox) error {
		c.setUnread(keyOf(u.Peer), u.StillUnreadCount)
		return nil
	})
	d.OnReadChannelInbox(func(ctx context.Context, e tg.Entities, u *tg.UpdateReadChannelInbox) error {
		c.setUnread(PeerKey{KindChannel, u.ChannelID}, u.StillUnreadCount)
		return nil
	})
}

func (c *Client) setUnread(k PeerKey, n int) {
	c.Store.update(func() {
		if ch, ok := c.Store.chats[k]; ok {
			ch.Unread = n
		}
	})
}

func (c *Client) onMessage(e tg.Entities, mc tg.MessageClass, edited bool) {
	s := c.Store
	var (
		chat     Chat
		msg      Message
		incoming bool
	)
	s.mu.Lock()
	s.addEntityMapsLocked(e)
	m, ok := s.convertLocked(mc)
	if ok {
		ch, exists := s.chats[m.Peer]
		if !exists {
			if ch, exists = s.chatLocked(m.Peer); exists {
				s.chats[m.Peer] = ch
			}
		}
		if exists {
			old, seen := s.findLocked(m.Peer, m.ID)
			s.upsertLocked(m.Peer, m)
			if !edited && !seen {
				ch.Preview = preview(m)
				ch.LastDate = int(m.Date.Unix())
				if !m.Out {
					ch.Unread++
					chat, msg, incoming = *ch, m, true
				}
			} else if seen && old.ID == maxID(s.messages[m.Peer]) {
				ch.Preview = preview(m)
			}
		}
	}
	s.mu.Unlock()
	s.changed()

	if incoming && c.OnIncoming != nil {
		c.OnIncoming(chat, msg)
	}
}

func (s *Store) findLocked(k PeerKey, id int) (Message, bool) {
	for _, m := range s.messages[k] {
		if m.ID == id {
			return m, true
		}
	}
	return Message{}, false
}

func maxID(ms []Message) int {
	if len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].ID
}

func preview(m Message) string {
	if m.Out {
		return "you: " + m.Summary()
	}
	return m.Summary()
}

func (c *Client) loadDialogs(ctx context.Context) error {
	var (
		offsetDate int
		offsetID   int
		offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
	)
	for page := 0; page < 10; page++ {
		res, err := c.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			OffsetPeer: offsetPeer,
			Limit:      100,
		})
		if err != nil {
			return err
		}
		mod, ok := res.AsModified()
		if !ok {
			return nil
		}
		dialogs := mod.GetDialogs()
		var last *Chat
		var lastMsg Message

		s := c.Store
		s.mu.Lock()
		s.addEntitiesLocked(mod.GetUsers(), mod.GetChats())
		top := map[PeerKey]map[int]Message{}
		for _, mc := range mod.GetMessages() {
			if m, ok := s.convertLocked(mc); ok {
				if top[m.Peer] == nil {
					top[m.Peer] = map[int]Message{}
				}
				top[m.Peer][m.ID] = m
			}
		}
		for _, dc := range dialogs {
			d, ok := dc.(*tg.Dialog)
			if !ok {
				continue
			}
			k := keyOf(d.Peer)
			ch, ok := s.chatLocked(k)
			if !ok {
				continue
			}
			ch.Unread = d.UnreadCount
			ch.Pinned = d.Pinned
			ch.MutedUntil, _ = d.NotifySettings.GetMuteUntil()
			if m, ok := top[k][d.TopMessage]; ok {
				ch.Preview = preview(m)
				ch.LastDate = int(m.Date.Unix())
				lastMsg = m
			}
			s.chats[k] = ch
			last = ch
		}
		s.mu.Unlock()
		s.changed()

		if _, slice := res.(*tg.MessagesDialogsSlice); !slice || len(dialogs) < 100 || last == nil {
			return nil
		}
		offsetDate, offsetID, offsetPeer = int(lastMsg.Date.Unix()), lastMsg.ID, last.input
	}
	return nil
}

// LoadHistory fetches the latest page of a chat, or the page before the
// oldest loaded message if older is set.
func (c *Client) LoadHistory(k PeerKey, older bool) {
	s := c.Store
	s.mu.Lock()
	ch, ok := s.chats[k]
	if !ok || s.loading[k] || (older && s.complete[k]) || c.api == nil {
		s.mu.Unlock()
		return
	}
	s.loading[k] = true
	input, api := ch.input, c.api
	offset := 0
	if older && len(s.messages[k]) > 0 {
		offset = s.messages[k][0].ID
	}
	s.mu.Unlock()

	go func() {
		res, err := api.MessagesGetHistory(c.ctx, &tg.MessagesGetHistoryRequest{
			Peer:     input,
			OffsetID: offset,
			Limit:    historyPage,
		})
		s.mu.Lock()
		s.loading[k] = false
		if err == nil {
			if mod, ok := res.AsModified(); ok {
				s.addEntitiesLocked(mod.GetUsers(), mod.GetChats())
				msgs := mod.GetMessages()
				for _, mc := range msgs {
					if m, ok := s.convertLocked(mc); ok && m.Peer == k {
						s.upsertLocked(k, m)
					}
				}
				s.loaded[k] = true
				if len(msgs) < historyPage {
					s.complete[k] = true
				}
			}
		}
		s.mu.Unlock()
		if err != nil {
			s.SetStatus("history: " + err.Error())
		}
		s.changed()
	}()
}

// MarkRead marks everything in the chat as read.
func (c *Client) MarkRead(k PeerKey) {
	s := c.Store
	s.mu.Lock()
	ch, ok := s.chats[k]
	if !ok || ch.Unread == 0 || c.api == nil {
		s.mu.Unlock()
		return
	}
	ch.Unread = 0
	input, api := ch.input, c.api
	max := maxID(s.messages[k])
	s.mu.Unlock()
	s.changed()

	go func() {
		var err error
		if p, ok := input.(*tg.InputPeerChannel); ok {
			_, err = api.ChannelsReadHistory(c.ctx, &tg.ChannelsReadHistoryRequest{
				Channel: &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash},
				MaxID:   max,
			})
		} else {
			_, err = api.MessagesReadHistory(c.ctx, &tg.MessagesReadHistoryRequest{Peer: input, MaxID: max})
		}
		if err != nil {
			s.SetStatus("mark read: " + err.Error())
		}
	}()
}

func (c *Client) inputPeer(k PeerKey) (tg.InputPeerClass, *tg.Client, bool) {
	c.Store.mu.RLock()
	defer c.Store.mu.RUnlock()
	ch, ok := c.Store.chats[k]
	if !ok || c.api == nil {
		return nil, nil, false
	}
	return ch.input, c.api, true
}

// Send sends a text message, optionally as a reply.
func (c *Client) Send(k PeerKey, text string, replyTo int) {
	input, api, ok := c.inputPeer(k)
	if !ok {
		return
	}
	go func() {
		req := &tg.MessagesSendMessageRequest{Peer: input, Message: text, RandomID: randomID()}
		if replyTo != 0 {
			req.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: replyTo}
		}
		res, err := api.MessagesSendMessage(c.ctx, req)
		if err != nil {
			c.Store.SetStatus("send: " + err.Error())
			return
		}
		// Short results carry no message body, so they never reach the dispatcher.
		if short, ok := res.(*tg.UpdateShortSentMessage); ok {
			s := c.Store
			s.update(func() {
				m := Message{
					ID: short.ID, Peer: k, Out: true, Date: time.Unix(int64(short.Date), 0),
					Text: text, ReplyTo: replyTo, SenderID: s.selfID, Sender: "you",
				}
				if short.Media != nil {
					if label := mediaLabel(short.Media); label != "" {
						m.Text = label + " " + m.Text
					}
				}
				s.upsertLocked(k, m)
				if ch, ok := s.chats[k]; ok {
					ch.Preview, ch.LastDate = preview(m), short.Date
				}
			})
		}
	}()
}

// Edit replaces the text of one of our messages.
func (c *Client) Edit(k PeerKey, id int, text string) {
	input, api, ok := c.inputPeer(k)
	if !ok {
		return
	}
	s := c.Store
	s.update(func() {
		if m, ok := s.findLocked(k, id); ok {
			m.Text, m.Edited = text, true
			s.upsertLocked(k, m)
		}
	})
	go func() {
		_, err := api.MessagesEditMessage(c.ctx, &tg.MessagesEditMessageRequest{Peer: input, ID: id, Message: text})
		if err != nil && !tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
			s.SetStatus("edit: " + err.Error())
		}
	}()
}

// DownloadPhoto saves the given size of a photo to path.
func (c *Client) DownloadPhoto(p Photo, size, path string) error {
	c.Store.mu.RLock()
	api := c.api
	c.Store.mu.RUnlock()
	if api == nil {
		return errors.New("not connected")
	}
	tmp := path + ".part"
	_, err := downloader.NewDownloader().Download(api, &tg.InputPhotoFileLocation{
		ID: p.ID, AccessHash: p.AccessHash, FileReference: p.FileRef, ThumbSize: size,
	}).ToPath(c.ctx, tmp)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func randomID() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]))
}
