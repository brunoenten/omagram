# Personal assistant (PA) — integration notes

Status: design notes only. Nothing here is implemented in omagram yet. The PA
core lives in `~/Work/pa-core` (milestone 1 implemented there).

## Target pipeline

```
channel adapters ─▶ interaction/event store ─▶ PA decision layer ─▶ (Pythia adviser)
                                                      │
                     Bruno (conversational turns) ◀──▶ approval / executor ─▶ audit log
```

## Where the PA lives

The PA core is a separate long-running service (its own repo/module), not part of
omagram. omagram is one client of it, and optionally one source of signals.

Why:

- It must run when the omagram window is closed (24/7 triage, reminders, commitments).
- Most channels (email, WhatsApp, SMS/phone) have nothing to do with a Telegram GUI.
- The event store, approvals and audit log need durable storage. omagram stores
  nothing apart from `session.json` and a photo cache.
- LLM credentials, prompts and policies stay out of a minimalist chat client.
- `internal/` packages cannot be imported from another module anyway.

## omagram today (seams relevant to the PA)

| Concern | Where | Notes |
|---|---|---|
| Telegram protocol | `internal/tele/client.go` | `gotd/td` (pure-Go MTProto), **not TDLib**. Reconnect loop in `Run`, gap manager in `run`. |
| Session | `client.go` `session.FileStorage` | `~/.local/share/omagram/session.json`. Do not share it with another process; give the PA its own device session. |
| Update handlers | `client.go` `registerHandlers` | new/edit message, read inbox. No deletes, reactions, typing or presence. |
| Update state | `gaps.Run(..., updates.AuthOptions{})` | No persisted pts/qts storage, so updates missed while offline are never replayed. |
| Domain model | `internal/tele/store.go` | `PeerKey`, `Chat`, `Message`. Lossy: media are flattened to labels, no entities, forwards or topics, and `SenderID` has no peer kind. |
| Conversion | `internal/tele/convert.go` | `keyOf`, `userName`, `mediaLabel`, `actionLabel` can be reused/copied into an adapter. |
| State store | `Store` (in-memory, `RWMutex`, `onChange` → `w.Invalidate`) | Rebuilt on every start from `loadDialogs` (≤1000 dialogs) plus on-demand history. |
| Outbound | `Client.Send`/`Edit` | Fire-and-forget goroutines; errors only reach the status line. An executor needs a synchronous variant that returns the message ID or an error. |
| Event hook | `Client.OnIncoming` | Single callback, used for notifications (`ui/app.go` `startClient`). The only existing extension point. |
| UI loop | `ui/app.go` `Run`/`frame` | Gio immediate mode; `refresh()` pulls snapshots from `Store` every frame. |
| Input | `ui/app.go` `handleKeys`/`onKey`, `submit` | Modal (CHATS/MESSAGES/INSERT/SEARCH). The composer submits plain text; there is no command parser. |
| UI | `ui/view.go` | A native Wayland GUI with a terminal look (monospace, vim modes), not a terminal TUI. |

## omagram as a PA client (later)

The PA is shown as a pinned virtual chat (`◆ pa`) in the chat list:

- Proposals render as messages: draft, rationale, target thread.
- Bruno answers in the normal composer ("yes but don't mention price yet",
  "why?", "ask Pythia", "not today"). The PA interprets the reply; omagram does no
  NLU itself.
- Transport: local unix socket (`$XDG_RUNTIME_DIR/pa.sock`), request/response
  plus an event stream. omagram adds a `pa` package and a second "source"
  next to `tele.Store`. No changes to `tele` are required.

## First milestone (in the PA repo)

"Telegram DM triage with one approved-reply loop, fully audited."

1. Store: SQLite tables `interactions`, `threads`, `proposals`, `turns`
   (Bruno's utterances plus their interpreted intent), `executions`, and an
   append-only `audit`.
2. Telegram adapter: its own gotd session (second device) with persisted update
   state. Ingests private chats only and writes normalized interactions plus
   raw JSON.
3. Decision layer: for each new inbound DM it classifies as reply-needed,
   FYI or ignore. For reply-needed it creates a proposal with a draft and a
   rationale.
4. Turn interpreter: free text about a proposal becomes one of
   `approve | revise(instructions) | reject | defer(until) | explain | ask_pythia | handle_it`.
   `revise` re-drafts and asks again; `handle_it` means approve and send (only
   this once in M1).
5. Executor: the single action is `telegram.send_message`. It requires an
   approved proposal, carries an idempotency key, and is dry-run by default.
6. Pythia: an interface that returns "not configured" for now.
7. Client: a terminal REPL (`pa chat`). omagram integration comes in M2.

Exit criteria: a DM arrives, a proposal appears in the REPL, Bruno revises it in
natural language, approves, it is sent, and every step is visible in `audit`.
