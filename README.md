# Mokka

<img src="assets/logo.svg" alt="Mokka bean record logo" width="96" height="96" />

A lightweight Discord music bot written in Go. Runs without Lavalink or a
streaming platform API key.

Join a voice channel and type `m!p One More Time` or use `/play` with a song
title or YouTube link. The Now Playing card shows the track artwork and gives
you buttons for the queue, lyrics, skip, and stop.
Playback pauses when Mokka is alone in voice and resumes from the same position when someone returns before the idle timeout.

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
| Autoplay when the queue ends | `m!autoplay [on or off]` | `/autoplay` |
| Show playback details | `m!detail` | `/detail` |
| Show or set volume (0–100%) | `m!v [level]` or `m!volume [level]` | `/volume [level]` |
| List commands | | `/help` |
| About Mokka | `!info` or `m!info` | `/info` |

Skip bypasses repeat for the current track. If the queue is empty and autoplay is on, Mokka plays recommendations in batches of up to five. Requested tracks take priority and start a fresh batch when the queue runs out.

For example, `m!v 70` or `/volume level:70` changes the volume during playback.
Omit the level to show the current setting. Autoplay and volume are saved per server across restarts.

## Self-host

1. **Create and invite your bot.** Enable **Message Content Intent** under
   **Bot** for prefix commands. Use **Guild Install** with the `bot` and
   `applications.commands` scopes. In the channels Mokka will use, grant
   **View Channel**, **Send Messages**, **Read Message History**,
   **Attach Files**, **Connect**, and **Speak**. [Discord Developer Portal](https://discord.com/developers/applications)
2. **Install Mokka.** FFmpeg needs the `libopus` encoder and `azmq` filter.
   On Debian or Ubuntu, install it with `sudo apt update && sudo apt install ffmpeg`.
   Mokka also needs outbound HTTPS and Discord voice UDP.

   **Prebuilt Linux x86-64 (Ubuntu 22.04 or compatible).** Download
   `mokka-linux-amd64.tar.gz` and `SHA256SUMS` from
   [GitHub Releases](https://github.com/J1yann/Mokka/releases). Verify and extract
   them in the download directory.

   ```sh
   sha256sum -c SHA256SUMS
   tar -xzf mokka-linux-amd64.tar.gz
   cd mokka-linux-amd64
   ```

   The archive contains both binaries. Its example config points to the
   bundled extractor. The release tag includes the extractor source, and
   [Cargo.toml](extractor/Cargo.toml) pins the exact Strawcore source revision.

   **Build from source.** In the repository, use Go 1.26, Cargo, and a C compiler.

   ```sh
   GOEXPERIMENT=jsonv2 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o mokka .
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
| `MOKKA_STATE_DIR` | `mokka-state` | Writable directory for per-server autoplay and volume settings. |
| `MOKKA_EXTRACTOR` | `mokka-extractor` on PATH | The example config points to the bundled extractor or the Cargo build output. Update the path if you move the helper. |
| `MOKKA_IDLE_TIMEOUT` | `1h` | Leave voice when the queue is empty or the bot is alone for this long. Use a duration such as `15m`, or `0` to disable. |
| `MOKKA_OPUS_BITRATE_KBPS` | `96` | Stereo Opus target bitrate in kbps. Use a whole number such as `128`. |
| `MOKKA_MEDIA_PROXY` | Direct connection | An optional HTTP proxy for YouTube search and audio downloads. |

If Mokka logs an invalid preferences file, it uses defaults for that server and leaves the file intact. Fix the file and restart Mokka to re-enable saving.

Each server has its own queue of up to 25 waiting tracks. Queues are stored in
memory and reset on restart. Up to three servers can play at once. Other servers
wait for a playback slot. If playback is interrupted, Mokka tries once to
resume from the last audio it sent. Autoplay is off by default and uses
YouTube recommendations up to ten minutes long.

## Under the hood

Go handles Discord voice, commands, and the queue. Mokka uses these projects
for search, audio, and lyrics.

- **Strawcore** searches YouTube and resolves the audio URL. [Strawcore repository](https://git.sulkta.com/Sulkta-OSS/strawcore)
- **FFmpeg** turns the stream into Opus for Discord voice. [FFmpeg](https://ffmpeg.org/)
- **LRCLIB** looks up song lyrics. [LRCLIB](https://lrclib.net/)

Made with ❤️ by [J1yann](https://github.com/J1yann)
