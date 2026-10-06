# omagram

A minimalist, keyboard-driven Telegram client for [Omarchy](https://omarchy.org).

Native Wayland app written in Go with [Gio](https://gioui.org) — no webview, no
bubbles, no avatars. Monospace text, a vim-style status bar, and colors taken
live from your active Omarchy theme.

```
── today ─────────────────────────────────────────────
10:07      you │ building a telegram client that fits omarchy
10:09      dhh │ ↳ you: building a telegram client that…
               │ love it. bubbles are a crime against text
10:19      dhh │ shipped 3.2 🎉
```

## Features

- QR-code login (scan from the Telegram mobile app), with two-step verification
- Chat list with unread counts, pinned and muted chats, incremental search
- Read history with infinite scroll, send, reply, and edit messages
- Reactions: see them under each message, right-click (or `+`) to react, click one to toggle it
- Desktop notifications via `notify-send` (mako / Omarchy shell)
- Follows `omarchy theme set` without restarting
- Uses your system monospace font, with fallbacks for symbols and color emoji

Photos are shown inline (`o` or a click opens the full size in your image viewer); other media is shown as text labels (`[voice 0:12]`, `[file report.pdf]`) for now.

## Install

Requires Go 1.27+ and the usual Wayland development libraries (present on
Omarchy / Arch by default).

```sh
git clone https://github.com/brunoenten/omagram
cd omagram
make install   # installs to ~/.local/bin and adds a desktop entry
```

`make run` builds and starts it without installing.

## Configuration

Telegram requires every client to use its own API credentials:

1. Sign in at <https://my.telegram.org> and open **API development tools**.
2. Create an application (any name) and note the `api_id` and `api_hash`.
3. Write them to `~/.config/omagram/config.toml`:

```toml
api_id   = 1234567
api_hash = "0123456789abcdef0123456789abcdef"

# optional
font          = "JetBrainsMono Nerd Font"  # default: fontconfig's monospace
font_size     = 14
notifications = true
```

`OMAGRAM_API_ID` and `OMAGRAM_API_HASH` override the file. The session is
stored in `~/.local/share/omagram/session.json`.

## Keys

| Context  | Keys |
|----------|------|
| Chats    | `j`/`k` move · `g`/`G` top/bottom · `enter` open · `/` search · `tab` messages |
| Messages | `j`/`k` select · `r` reply · `e` edit · `+` react · `o` open photo · `y` copy · `i` write · `h` back |
| Reacting | `1`–`8` pick · `h`/`l` move · `enter` pick · `esc` cancel |
| Writing  | `enter` send · `shift+enter` newline · `esc` cancel / back |
| Global   | `ctrl+k` search · `ctrl+=`/`ctrl+-`/`ctrl+0` font size · `ctrl+q` quit |

## Hyprland

The window class is `omagram`, e.g. for a keybinding or window rule.

## License

[MIT](LICENSE)
