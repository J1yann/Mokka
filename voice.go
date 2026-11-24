package main

import (
	"context"
	"net"
	"sync"

	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

type managedVoiceConn struct {
	voice.Conn
	update voice.StateUpdateFunc
	mu     sync.Mutex
	closed bool
}

func newManagedVoiceConn(guild, user snowflake.ID, update voice.StateUpdateFunc, remove func(), opts ...voice.ConnConfigOpt) voice.Conn {
	conn := &managedVoiceConn{update: update}
	conn.Conn = voice.NewConn(guild, user, conn.updateWhileOpen, remove, opts...)
	return conn
}

func (c *managedVoiceConn) updateWhileOpen(ctx context.Context, guild snowflake.ID, channel *snowflake.ID, mute, deaf bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	return c.update(ctx, guild, channel, mute, deaf)
}

func (c *managedVoiceConn) Close(ctx context.Context) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	// The library can still be attempting to reopen the old session. Its
	// eventual close must not disconnect the replacement session.
	_ = c.update(ctx, c.GuildID(), nil, false, false)
	c.Conn.Close(ctx)
}
