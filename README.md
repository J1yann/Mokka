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

1. Create a bot and invite it with **Guild Install** and the `bot` and
   `applications.commands` scopes. Enable **Message Content Intent** for
   prefix commands. Grant **View Channel**, **Send Messages**, **Read Message
   History**, **Attach Files**, **Connect**, and **Speak**. [Discord Developer Portal](https://discord.com/developers/applications)
2. Install FFmpeg on Debian or Ubuntu with `sudo apt update && sudo apt install ffmpeg`.
   Mokka needs the `libopus` encoder, the `azmq` filter, outbound HTTPS, and Discord voice UDP.
3. For Linux x86-64 (Ubuntu 22.04 or compatible), download
   `mokka-linux-amd64.tar.gz`. It includes Mokka and the extractor.
   `SHA256SUMS` is optional if you want to verify the download. [GitHub Releases](https://github.com/J1yann/Mokka/releases)

   ```sh
   tar -xzf mokka-linux-amd64.tar.gz
   cd mokka-linux-amd64
   ```

4. Copy the [example config](mokka.conf.example), fill in your bot token and
   server ID, then start Mokka. Keep `mokka.conf` private.

   ```sh
   install -m 600 mokka.conf.example mokka.conf
   # Fill in DISCORD_TOKEN and MOKKA_GUILD_ID before starting.
   ./mokka
   ```

<details>
<summary>Build from source</summary>

Run these in the repository with Go 1.26, Cargo, and a C compiler, then follow
step 4. The release tag includes the extractor source. [Pinned Strawcore version](extractor/Cargo.toml)

```sh
GOEXPERIMENT=jsonv2 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o mokka .
cargo build --locked --release --manifest-path extractor/Cargo.toml
```

</details>

<details>
<summary>Optional settings</summary>

Environment variables override `mokka.conf`.

| Optional setting | Default | What it controls |
| --- | --- | --- |
| `MOKKA_PREFIX` | `m!` | Text command prefix. Set it to `.` for `.play` or `!` for `!play`, then restart Mokka. |
| `MOKKA_GUILD_IDS` | `MOKKA_GUILD_ID` | A comma-separated list of Discord server IDs. Invite Mokka to each listed server. |
| `MOKKA_STATE_DIR` | `mokka-state` | Writable directory for per-server autoplay and volume settings. |
| `MOKKA_EXTRACTOR` | `mokka-extractor` on PATH | The example config points to the bundled extractor or the Cargo build output. Update the path if you move the helper. |
| `MOKKA_IDLE_TIMEOUT` | `1h` | Leave voice when the queue is empty or the bot is alone for this long. Use a duration such as `15m`, or `0` to disable. |
| `MOKKA_OPUS_BITRATE_KBPS` | `96` | Stereo Opus target bitrate in kbps. Use a whole number such as `128`. |
| `MOKKA_MEDIA_PROXY` | Direct connection | An optional HTTP proxy for YouTube search and audio downloads. |

If Mokka logs an invalid preferences file, it uses defaults for that server and leaves the file intact. Fix the file and restart Mokka to re-enable saving.

Each server has its own queue of up to 25 waiting tracks. Queues are stored in
memory and reset on restart. Up to three servers can play at once. Other servers
wait for a playback slot. On a voice disconnect, Mokka tries to resume from
the last audio it sent. Autoplay is off by default and uses YouTube
recommendations up to ten minutes long.

</details>

## Under the hood

Go handles Discord voice, commands, and the queue. Mokka uses these projects
for search, audio, and lyrics.

- **Strawcore** searches YouTube and resolves the audio URL. [Strawcore repository](https://git.sulkta.com/Sulkta-OSS/strawcore)
- **FFmpeg** turns the stream into Opus for Discord voice. [FFmpeg](https://ffmpeg.org/)
- **LRCLIB** looks up song lyrics. [LRCLIB](https://lrclib.net/)

Made with ❤️ by [J1yann](https://github.com/J1yann)
