# Mokka

Mokka is a single-server Discord music bot. The Go process manages commands,
voice, and the in-memory queue. The Strawcore helper searches YouTube and resolves
media URLs when a track reaches the front of the queue. FFmpeg streams Opus to
the bot for Discord voice. Lyrics come from LRCLIB. Long lyrics are sent as a
text attachment.

| Prefix command | Slash command | Action |
| --- | --- | --- |
| `m!p <title or link>`, `m!play <title or link>` | `/play query` | Queue a YouTube video or the first matching search result |
| `m!queue` | `/queue` | Show the current track and upcoming tracks |
| `m!skip` | `/skip` | Skip the current track |
| `m!stop` | `/stop` | Stop and clear the queue |
| `m!lyrics [title and artist]` | `/lyrics [query]` | Look up lyrics for the current song or a supplied title |
| `m!remove <position>` | `/remove position` | Remove a waiting track |
| `m!move <from> <to>` | `/move from to` | Move a waiting track to another position |
| `m!shuffle` | `/shuffle` | Randomize the waiting tracks |
| `m!repeat [on or off]` | `/repeat [mode]` | Set repeat one, or toggle it without an argument |

The Now playing card shows the current track and its YouTube artwork. Its
buttons open the queue or lyrics, skip the track, or stop playback and clear the
queue. Button results share a separate response card in each channel. If the
chat has moved on, the next click replaces the response card at the bottom.
Moving the card uses Read Message History in that channel.

## Build and run

Build on a machine with Go 1.26, Cargo, and a C compiler. The VPS needs the
two compiled binaries and FFmpeg with libopus support. Build for the VPS's OS
and architecture.

```sh
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o mokka .
cargo build --locked --release --manifest-path extractor/Cargo.toml
```

In the [Discord Developer Portal](https://discord.com/developers/applications),
enable **Message Content Intent** under **Bot**. Under **Installation**, use
Guild Install with the `bot` and `applications.commands` scopes. Give Mokka
View Channel, Send Messages, Attach Files, Connect, and Speak in its intended
channels. Use Discord's install link to add it to the server.

Copy the config template, then set `DISCORD_TOKEN`, `MOKKA_GUILD_ID`, and
`MOKKA_EXTRACTOR` in `mokka.conf`. Use plain `KEY=value` lines. The local
config is ignored by Git. Mokka loads it from its working directory, and
environment variables can override individual settings.

```sh
install -m 600 mokka.conf.example mokka.conf
# Edit mokka.conf before starting.
./mokka
```

Mokka registers its slash commands in that server at startup. It uses the
Discord Gateway for commands and needs outbound HTTPS and Discord voice UDP.
The queue holds 25 waiting tracks, and playback uses one FFmpeg process at a
time. The queue resets on restart.

The idle timeout defaults to one hour. Change `MOKKA_IDLE_TIMEOUT=1h` in
`mokka.conf` to another duration, such as `90s` or `15m`. Mokka leaves voice
when the queue stays empty or everyone else stays out of the voice channel for
that long. Set it to `0` to disable automatic disconnection. Stop still
disconnects immediately.

If a track fails before the first audio frame, Mokka retries once. Repeat one
replays the current track when it finishes successfully.

Set `MOKKA_MEDIA_PROXY` to an HTTP proxy URL to route YouTube search, stream
resolution, and audio downloads through it. Discord and lyrics use their
normal connections.
