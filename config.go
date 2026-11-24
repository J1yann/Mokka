package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/disgoorg/snowflake/v2"
)

type config map[string]string

func loadConfig(path string) (config, error) {
	cfg := make(config)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%s line %d must use KEY=value", path, line)
		}
		cfg[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return cfg, scanner.Err()
}

func (cfg config) value(key string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return cfg[key]
}

func configuredGuilds(plural, single string) ([]snowflake.ID, error) {
	if strings.TrimSpace(plural) == "" {
		plural = single
	}
	var ids []snowflake.ID
	seen := make(map[snowflake.ID]bool)
	for i, raw := range strings.Split(plural, ",") {
		id, err := snowflake.Parse(strings.TrimSpace(raw))
		if err != nil || id == 0 {
			return nil, fmt.Errorf("set a valid Discord server ID at position %d in MOKKA_GUILD_IDS or MOKKA_GUILD_ID", i+1)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
