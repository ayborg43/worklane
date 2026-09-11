package chat

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
)

// Client wires one WebSocket connection into a channel's broadcast group.
// onMessage is set by the HTTP handler that created the client, closing
// over the repo/renderer/hub/user needed to persist + rebroadcast.
type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	userID    int64
	channelID int64
	onMessage func(body string)
}

type registration struct {
	client *Client
	add    bool
}

// Hub tracks which clients are connected to which channel. Registration
// (add/remove) always goes through Run()'s single-goroutine loop, but
// Broadcast is called directly from request-handling goroutines, so the map
// itself is still guarded by mu — the loop takes it for writes, Broadcast
// takes a read lock.
type Hub struct {
	mu       sync.RWMutex
	channels map[int64]map[*Client]bool
	register chan registration
}

func NewHub() *Hub {
	return &Hub{
		channels: make(map[int64]map[*Client]bool),
		register: make(chan registration),
	}
}

func (h *Hub) Run() {
	for reg := range h.register {
		h.mu.Lock()
		if reg.add {
			if h.channels[reg.client.channelID] == nil {
				h.channels[reg.client.channelID] = make(map[*Client]bool)
			}
			h.channels[reg.client.channelID][reg.client] = true
		} else if clients, ok := h.channels[reg.client.channelID]; ok {
			if _, ok := clients[reg.client]; ok {
				delete(clients, reg.client)
				close(reg.client.send)
				if len(clients) == 0 {
					delete(h.channels, reg.client.channelID)
				}
			}
		}
		h.mu.Unlock()
	}
}

func (h *Hub) Broadcast(channelID int64, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.channels[channelID] {
		select {
		case c.send <- msg:
		default:
			go func(c *Client) { h.register <- registration{client: c, add: false} }(c)
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump parses the JSON payload htmx's ws extension sends on form
// submission (e.g. {"chat_message":"hi","HEADERS":{...}}) and hands the
// message text to onMessage.
func (c *Client) readPump() {
	defer func() {
		c.hub.register <- registration{client: c, add: false}
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var payload struct {
			ChatMessage string `json:"chat_message"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		body := strings.TrimSpace(payload.ChatMessage)
		if body == "" || c.onMessage == nil {
			continue
		}
		c.onMessage(body)
	}
}
