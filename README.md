# claude-tv

Your Claude Code usage on a GeekMagic SmallTV desk clock. A small Go server on your laptop
draws 240×240 screens and pushes them to the clock's **stock firmware** over your Wi‑Fi:
no flashing, nothing installed on the clock.

| Screen | Shows |
|---|---|
| `now` | 5-hour session % as the outer ring, weekly % as the inner ring, time to reset, even-pace marker |
| `today` | tokens per hour, today's total, API-equivalent cost, model split |
| `pace` | the session so far as a curve, projected to the limit: "limit at 16:40" or "on pace" |
| `week` | 7 × 24 heat grid of hourly tokens, weekly total and cost |
| `clock` | time and date, whether you're working right now (and in which project), both limits as bars |

Two looks: **aurora** (default; glowing gradients on deep plum) and **signal** (light Swiss
poster, condensed numerals). Switch from the preview page or with `-theme`.

## Run

```sh
go build -o claude-tv ./cmd/claude-tv
./claude-tv -device 192.168.1.50          # the clock's IP (shown on its screen at boot)
```

Open the preview page at <http://127.0.0.1:8787> to see every screen live and switch themes.
Without `-device`, only the preview page runs, which is handy for trying it out.

Useful flags (`./claude-tv -h` for all):

| Flag | Default | |
|---|---|---|
| `-theme` | `aurora` | `aurora` or `signal` |
| `-screens` | `now,today,pace,week,clock` | which screens rotate, in order |
| `-rotate` | `20s` | how long each screen stays up |
| `-min-upload` | `60s` | re-send a changed screen at most this often |
| `-brightness` | `-1` | set brightness 0–100 on start; `-1` leaves it |
| `-album-theme` | `3` | firmware's Photo Album mode: `3` SmallTV Ultra, `4` Pro |
| `-limits` | `true` | `false`: never contact Anthropic; local history only |
| `-demo` | | sample data |

Render PNGs without a clock: `./claude-tv render -demo -at 15:20 -out screens/`.

To run it in the background on login, see [`contrib/claude-tv.service`](contrib/claude-tv.service).

## Where the numbers come from

- **Plan limits** (session and weekly %, reset times): the same endpoint Claude Code and
  claude.ai's Settings → Usage use. claude-tv reads Claude Code's login token from
  `~/.claude/.credentials.json` **read-only**; it never refreshes or rewrites it. If the token
  has expired, the screens say so until you next use Claude Code.
- **Tokens and cost**: parsed from Claude Code's transcripts in `~/.claude/projects/`.
  Messages are de-duplicated (Claude Code repeats usage on every content block). Cost is
  API list price, which is useful for comparison but isn't what a subscription charges you.
  Prices live in `internal/usage/pricing.go`.
- **History**: Claude Code deletes old transcripts (30 days by default), so claude-tv keeps
  hourly totals in `~/.local/share/claude-tv/hours.json`. Charts keep working after cleanup.

## Keeping the clock off the internet

The stock firmware has no password and phones out for weather, time and possibly updates.
claude-tv only needs the clock reachable from your laptop:

1. In your router, give the clock a fixed IP (DHCP reservation) and **block that device from
   the internet** (often under "parental controls" or "access control").
2. Turn off weather in the clock's settings page.

Without internet the built-in clock can't set the time. That doesn't matter here: claude-tv
draws the time from your laptop.

## How it talks to the clock

Plain HTTP to the stock firmware (documented by
[geekmagic-stats](https://docs.rs/crate/geekmagic-stats/0.1.0/source/device-protocol.md),
tested there on `Ultra-V9.0.43`): switch to Photo Album mode, stop its slideshow, upload
`ctv-<screen>.jpg` to `/image/`, then show it. Your own album images are left alone.

Every upload writes to the clock's flash. claude-tv skips uploads when a screen hasn't
changed and never re-sends one screen more than once per `-min-upload`. For the gentlest
setup, use fewer screens or a longer `-min-upload`.

## Development

```sh
go test ./...
```

Fonts (SIL OFL, see `internal/render/fonts/LICENSES.txt`): Unbounded, Manrope, Archivo,
cut to static weights from Google Fonts' variable files.
