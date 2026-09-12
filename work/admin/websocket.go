package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/proxy"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// adminStateMessage is the live-state frame pushed to connected admin sockets.
// Generation lets the client decide whether its cached channel list is stale.
type adminStateMessage struct {
	Type           string            `json:"type"`
	Stats          StatsResponse     `json:"stats"`
	ActiveChannels []ChannelResponse `json:"activeChannels"`
	Generation     uint64            `json:"generation"`
}

// adminSocketClient is one connected admin socket and its outbound frame queue.
type adminSocketClient struct {
	send chan []byte
}

// adminSocketHub tracks connected admin sockets and fans a single marshalled
// snapshot out to all of them.
type adminSocketHub struct {
	mu      sync.RWMutex
	clients map[*adminSocketClient]struct{}
}

var stateHub = &adminSocketHub{clients: make(map[*adminSocketClient]struct{})}

// add registers a client with the hub.
func (h *adminSocketHub) add(client *adminSocketClient) {
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
}

// remove deregisters a client from the hub.
func (h *adminSocketHub) remove(client *adminSocketClient) {
	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()
}

// hasClients reports whether any admin socket is currently connected.
func (h *adminSocketHub) hasClients() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients) > 0
}

// broadcast queues a frame for every connected client. A client whose queue is
// full is skipped rather than blocking the broadcaster on one slow consumer.
func (h *adminSocketHub) broadcast(payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		select {
		case client.send <- payload:
		default:
		}
	}
}

// buildAdminState assembles the current live-state snapshot.
func buildAdminState(sp *proxy.StreamProxy) adminStateMessage {
	return adminStateMessage{
		Type:           "state",
		Stats:          buildStats(sp),
		ActiveChannels: buildActiveChannels(sp),
		Generation:     sp.ImportGeneration(),
	}
}

// startAdminSocketBroadcaster runs the periodic live-state push. The snapshot is
// built and marshalled once per tick and only while a client is connected.
func startAdminSocketBroadcaster(sp *proxy.StreamProxy) {
	go func() {
		ticker := time.NewTicker(constants.Internal.AdminSocketInterval)
		defer ticker.Stop()

		for range ticker.C {
			if !stateHub.hasClients() {
				continue
			}

			payload, err := json.Marshal(buildAdminState(sp))
			if err != nil {
				addLogEntry("error", fmt.Sprintf("{admin - startAdminSocketBroadcaster} marshal failed: %v", err))
				continue
			}

			stateHub.broadcast(payload)
		}
	}()
}

// handleAdminSocket upgrades an authenticated request to a websocket and streams
// live state to it until the client disconnects.
func handleAdminSocket(sp *proxy.StreamProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			addLogEntry("error", fmt.Sprintf("{admin - handleAdminSocket} accept failed: %v", err))
			return
		}
		defer conn.CloseNow()

		// The client never sends anything; CloseRead drains and gives a context
		// that cancels as soon as the peer goes away.
		ctx := conn.CloseRead(context.Background())

		client := &adminSocketClient{send: make(chan []byte, constants.Internal.AdminSocketSendBuffer)}
		stateHub.add(client)
		defer stateHub.remove(client)

		if payload, err := json.Marshal(buildAdminState(sp)); err == nil {
			if !writeAdminFrame(ctx, conn, payload) {
				return
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case payload := <-client.send:
				if !writeAdminFrame(ctx, conn, payload) {
					return
				}
			}
		}
	}
}

// writeAdminFrame writes one frame under a bounded timeout, reporting whether the
// connection is still usable.
func writeAdminFrame(ctx context.Context, conn *websocket.Conn, payload []byte) bool {
	writeCtx, cancel := context.WithTimeout(ctx, constants.Internal.AdminSocketWriteTimeout)
	defer cancel()

	return conn.Write(writeCtx, websocket.MessageText, payload) == nil
}
