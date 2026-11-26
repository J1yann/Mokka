package main

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/disgoorg/snowflake/v2"
)

const preferenceSaveMessage = "Mokka couldn’t save that setting. Your previous setting is still active."

func (p *player) editQueue(name, input string, user snowflake.ID) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.channel == 0 || p.leaving {
		return "Mokka isn't playing or waiting in voice."
	}
	state, ok := p.client.Caches.VoiceState(p.guild, user)
	if !ok || state.ChannelID == nil || *state.ChannelID != p.channel {
		return joinMokkaVoiceMessage
	}
	if name == "volume" {
		if input != "" {
			level, err := strconv.Atoi(input)
			if err != nil || level < 0 || level > 100 {
				return "Use a volume from 0 to 100."
			}
			if p.volume != level {
				if !p.savePreferenceChange(preferences{Autoplay: p.autoplay, Volume: level}) {
					return preferenceSaveMessage
				}
				p.volume = level
				if updates := p.volumeUpdates; updates != nil {
					select {
					case <-updates:
					default:
					}
					updates <- level
				}
			}
		}
		if input != "" {
			return fmt.Sprintf("Volume set to %d%%.", p.volume)
		}
		return fmt.Sprintf("Volume is %d%%.", p.volume)
	}
	args := strings.Fields(input)
	switch name {
	case "remove", "move":
		return p.reorderQueue(name, args)
	case "shuffle":
		if len(p.queue) < 2 {
			return "Add at least two tracks to the queue before shuffling."
		}
		rand.Shuffle(len(p.queue), func(i, j int) { p.queue[i], p.queue[j] = p.queue[j], p.queue[i] })
		return fmt.Sprintf("Shuffled %d queued tracks.", len(p.queue))
	case "repeat", "autoplay":
		setting, title := &p.repeat, "Repeat one"
		if name == "autoplay" {
			setting, title = &p.autoplay, "Autoplay"
		}
		next := *setting
		switch strings.ToLower(strings.TrimSpace(input)) {
		case "":
			next = !next
		case "on":
			next = true
		case "off":
			next = false
		default:
			return "Use `m!" + name + " on` or `m!" + name + " off`."
		}
		if name == "autoplay" && next != p.autoplay && !p.savePreferenceChange(preferences{Autoplay: next, Volume: p.volume}) {
			return preferenceSaveMessage
		}
		*setting = next
		if *setting {
			return title + " is on."
		}
		return title + " is off."
	default:
		return "Unknown queue command."
	}
}

// reorderQueue runs with p.mu held. Positions refer to waiting tracks only.
func (p *player) reorderQueue(name string, args []string) string {
	want := 1
	usage := "Use `m!remove <position>`."
	if name == "move" {
		want = 2
		usage = "Use `m!move <from> <to>`."
	}
	if len(args) != want {
		return usage
	}
	if len(p.queue) == 0 {
		return "The queue is empty."
	}
	var positions [2]int
	for i, arg := range args {
		position, err := strconv.Atoi(arg)
		if err != nil || position < 1 || position > len(p.queue) {
			return fmt.Sprintf("Use a queue position from 1 to %d.", len(p.queue))
		}
		positions[i] = position - 1
	}
	from, to := positions[0], positions[1]
	t := p.queue[from]
	title := escapeCardText(label(t.Title))
	p.queue = slices.Delete(p.queue, from, from+1)
	if name == "remove" {
		return "Removed " + title + " from the queue."
	}
	p.queue = slices.Insert(p.queue, to, t)
	return fmt.Sprintf("Moved %s to position %d.", title, to+1)
}

func (p *player) playbackDetails() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	onOff := func(enabled bool) string {
		if enabled {
			return "On"
		}
		return "Off"
	}
	lines := []string{
		"**Autoplay:** " + onOff(p.autoplay),
		"**Repeat one:** " + onOff(p.repeat),
		fmt.Sprintf("**Volume:** %d%%", p.volume),
		fmt.Sprintf("**Tracks queued:** %d", len(p.queue)),
	}
	if p.current.id != "" {
		lines = append(lines, "**Current track:** "+escapeCardText(label(p.current.Title)))
	}
	return strings.Join(lines, "\n")
}
