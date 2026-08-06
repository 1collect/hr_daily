package app

import (
	"net/http"

	"github.com/gorilla/websocket"
)

func (a *App) reportWebsocket(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if !validDate(date) {
		problem(w, 400, "Некорректная дата")
		return
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	a.reports.add(date, c)
	defer a.reports.remove(date, c)
	for {
		if _, _, err = c.ReadMessage(); err != nil {
			return
		}
	}
}
func (h *reportHub) add(date string, c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[date] == nil {
		h.clients[date] = map[*websocket.Conn]struct{}{}
	}
	h.clients[date][c] = struct{}{}
}
func (h *reportHub) remove(date string, c *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients[date], c)
	if len(h.clients[date]) == 0 {
		delete(h.clients, date)
	}
	h.mu.Unlock()
	_ = c.Close()
}
func (h *reportHub) send(date string, v any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients[date] {
		_ = c.WriteJSON(v)
	}
}
