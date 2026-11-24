# Mokka

A lightweight Discord music bot written in Go. Runs without Lavalink or a
streaming platform API key.

Join a voice channel and type `m!p One More Time` or use `/play` with a song
title or YouTube link. The Now Playing card shows the track artwork and gives
you buttons for the queue, lyrics, skip, and stop.
When the last song ends, Mokka shows a Playback finished card in the request channel.

## Commands

| What you want to do | Prefix command | Slash command |
| --- | --- | --- |
| Play or queue a song | `m!p <title or link>` or `m!play <title or link>` | `/play` |
| See what's next | `m!queue` | `/queue` |
| Read lyrics | `m!lyrics [title and artist]` | `/lyrics` |
| Skip the current song | `m!skip` | `/skip` |
| Stop and clear the queue | `m!stop` | `/stop` |
| Edit the queue | `m!remove <position>`, `m!move <from> <to>` | `/remove`, `/move` |
| Shuffle or repeat | `m!shuffle`, `m!repeat [on or off]` | `/shuffle`, `/repeat` |
| About Mokka | `!info` or `m!info` | `/info` |

## Self-host

1. **Create and invite your bot.** Enable **Message Content Intent** under
   **Bot** for prefix commands. Use **Guild Install** with the `bot` and
   `applications.commands` scopes. In the channels Mokka will use, grant
   **View Channel**, **Send Messages**, **Read Message History**,
   **Attach Files**, **Connect**, and **Speak**. [Discord Developer Portal](https://discord.com/developers/applications)
2. **Build Mokka and its extractor.** You'll need Go 1.26, Cargo, and a C
   compiler on the build machine. The runtime needs FFmpeg with libopus and
   outbound access to HTTPS and Discord voice UDP.

   ```sh
   CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o mokka .
   cargo build --locked --release --manifest-path extractor/Cargo.toml
   ```

3. **Configure and start.** Copy the [example config](mokka.conf.example) and
   add your bot token and Discord server ID. Mokka reads `mokka.conf` from its
   working directory and registers slash commands in the configured server.
   Keep `mokka.conf` private. Environment variables override file values.

   ```sh
   install -m 600 mokka.conf.example mokka.conf
   # Fill in DISCORD_TOKEN and MOKKA_GUILD_ID before starting.
   ./mokka
   ```

| Optional setting | Default | What it controls |
| --- | --- | --- |
| `MOKKA_GUILD_IDS` | `MOKKA_GUILD_ID` | A comma-separated list of Discord server IDs. Invite Mokka to each listed server. |
| `MOKKA_EXTRACTOR` | `mokka-extractor` on PATH | The example config points to the Cargo build output. Update the path if you move the helper. |
| `MOKKA_IDLE_TIMEOUT` | `1h` | Leave voice when the queue is empty or the bot is alone for this long. Use a duration such as `15m`, or `0` to disable. |
| `MOKKA_OPUS_BITRATE_KBPS` | `96` | Stereo Opus target bitrate in kbps. Use a whole number such as `128`. |
| `MOKKA_MEDIA_PROXY` | Direct connection | An optional HTTP proxy for YouTube search and audio downloads. |

Each server has its own queue of up to 25 waiting tracks. Queues are stored in
memory and reset on restart. Up to three servers can play at once. Other servers
wait for a playback slot. If playback is interrupted, Mokka tries once to
resume from the last audio it sent.

## Under the hood

Go handles Discord voice, commands, and the queue. Mokka uses these projects
for search, audio, and lyrics.

- **Strawcore** searches YouTube and resolves the audio URL. [Strawcore repository](https://git.sulkta.com/Sulkta-OSS/strawcore)
- **FFmpeg** turns the stream into Opus for Discord voice. [FFmpeg](https://ffmpeg.org/)
- **LRCLIB** looks up song lyrics. [LRCLIB](https://lrclib.net/)

Made with ❤️ by [J1yann](https://github.com/J1yann)
