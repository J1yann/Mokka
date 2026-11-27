package player

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func SlashCommands() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "play", Description: "Play a title or YouTube link",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{Name: "query", Description: "Song title or YouTube link", Required: true}}},
		discord.SlashCommandCreate{Name: "lyrics", Description: "Find lyrics for a title or the current song",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{Name: "query", Description: "Song title and optional artist"}}},
		discord.SlashCommandCreate{Name: "queue", Description: "Show the music queue"},
		discord.SlashCommandCreate{Name: "info", Description: "About Mokka"},
		discord.SlashCommandCreate{Name: "help", Description: "Show Mokka’s commands"},
		discord.SlashCommandCreate{Name: "skip", Description: "Skip the current track"},
		discord.SlashCommandCreate{Name: "stop", Description: "Stop playback and clear the queue"},
		discord.SlashCommandCreate{Name: "remove", Description: "Remove a waiting track", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: "position", Description: "Position in the queue", Required: true},
		}},
		discord.SlashCommandCreate{Name: "move", Description: "Move a waiting track", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: "from", Description: "Current position", Required: true},
			discord.ApplicationCommandOptionInt{Name: "to", Description: "New position", Required: true},
		}},
		discord.SlashCommandCreate{Name: "detail", Description: "Show playback settings and queue status"},
		discord.SlashCommandCreate{Name: "volume", Description: "Show or set playback volume", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: "level", Description: "Volume from 0 to 100 percent"},
		}},
		discord.SlashCommandCreate{Name: "shuffle", Description: "Shuffle the waiting tracks"},
		discord.SlashCommandCreate{Name: "repeat", Description: "Set repeat one or toggle it", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "mode", Description: "On or off"},
		}},
		discord.SlashCommandCreate{Name: "autoplay", Description: "Play a recommendation when the queue ends", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "mode", Description: "On or off"},
		}},
	}
}

func (p *Player) Command(name, query string, user, channel snowflake.ID) (string, string) {
	if !p.inVoice(user) {
		return joinVoiceMessage, ""
	}
	switch name {
	case "info":
		return "Thanks for listening with Mokka. Hope you come across a song you’ll want to play again.\n\nMade with ❤️ by [J1yann](https://github.com/J1yann)\n\nUse `/help` to see Mokka’s commands.", ""
	case "help":
		var lines []string
		for _, command := range SlashCommands() {
			if slash, ok := command.(discord.SlashCommandCreate); ok {
				lines = append(lines, "`/"+slash.Name+"` "+slash.Description)
			}
		}
		lines = append(lines, "", fmt.Sprintf("Use `%[1]sp` for `%[1]splay` and `%[1]sv` for `%[1]svolume`.", p.prefix))
		return strings.Join(lines, "\n"), ""
	case "detail":
		return p.playbackDetails(), ""
	case "play":
		return p.enqueue(user, channel, query), ""
	case "lyrics":
		return p.lookupLyrics(query, "")
	case "queue", "skip", "stop":
		return p.controls(name, user, ""), ""
	case "remove", "move", "shuffle", "repeat", "autoplay", "volume":
		return p.editQueue(name, query, user), ""
	default:
		return "Unknown command.", ""
	}
}

func label(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if chars := []rune(text); len(chars) > 72 {
		return string(chars[:72]) + "…"
	}
	return text
}
