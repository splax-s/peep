package ws

import "sync"

// Subscriber abstracts a streaming client.
type Subscriber interface {
	Send([]byte) error
	Close()
}

// Hub manages stream subscriptions by project ID.
type Hub struct {
	mu        sync.RWMutex
	clients   map[string]map[Subscriber]struct{}
	register  chan subscription
	unreg     chan subscription
	broadcast chan message
}

// message couples payload with project identifier.
type message struct {
	projectID string
	payload   []byte
}

// subscription defines register/unregister requests.
type subscription struct {
	projectID string
	client    Subscriber
}

// NewHub creates an initialized Hub.
func NewHub() *Hub {
	h := &Hub{
		clients:   make(map[string]map[Subscriber]struct{}),
		register:  make(chan subscription),
		unreg:     make(chan subscription),
		broadcast: make(chan message),
	}
	go h.run()
	return h
}

func (h *Hub) run() {
	for {
		select {
		case sub := <-h.register:
			if _, ok := h.clients[sub.projectID]; !ok {
				h.clients[sub.projectID] = make(map[Subscriber]struct{})
			}
			h.clients[sub.projectID][sub.client] = struct{}{}
		case sub := <-h.unreg:
			if clients, ok := h.clients[sub.projectID]; ok {
				delete(clients, sub.client)
				if len(clients) == 0 {
					delete(h.clients, sub.projectID)
				}
			}
		case msg := <-h.broadcast:
			if clients, ok := h.clients[msg.projectID]; ok {
				for c := range clients {
					if err := c.Send(msg.payload); err != nil {
						c.Close()
						delete(clients, c)
					}
				}
				if len(clients) == 0 {
					delete(h.clients, msg.projectID)
				}
			}
		}
	}
}

// Register adds a client to a project stream.
func (h *Hub) Register(projectID string, client Subscriber) {
	h.register <- subscription{projectID: projectID, client: client}
}

// Unregister removes a client.
func (h *Hub) Unregister(projectID string, client Subscriber) {
	h.unreg <- subscription{projectID: projectID, client: client}
}

// Broadcast sends payload to all project clients.
func (h *Hub) Broadcast(projectID string, payload []byte) {
	h.broadcast <- message{projectID: projectID, payload: payload}
}
