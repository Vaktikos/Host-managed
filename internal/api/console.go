package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/vaktikos/host-managed/internal/agent"
)

// Consoles use WebSockets. The library rejects cross-origin upgrades by
// default, which together with the SameSite=Strict session cookie prevents
// cross-site WebSocket hijacking.

func (s *Server) vmConsole(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	backend, br, err := s.Agent.VNC(r.Context(), name)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	defer backend.Close()
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		return
	}
	s.audit(r, "vm.console", "vm/"+name, nil, "")
	ws.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	defer conn.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(backend, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, br); done <- struct{}{} }()
	<-done
}

// ctConsole bridges xterm.js and the agent's terminal stream. Binary
// messages carry keystrokes; text messages carry JSON control messages such
// as {"type":"resize","cols":120,"rows":32}.
func (s *Server) ctConsole(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	backend, br, err := s.Agent.ContainerConsole(r.Context(), name)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	defer backend.Close()
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	s.audit(r, "ct.console", "ct/"+name, nil, "")
	ws.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := br.Read(buf)
			if n > 0 {
				wctx, wc := context.WithTimeout(ctx, 30*time.Second)
				werr := ws.Write(wctx, websocket.MessageBinary, buf[:n])
				wc()
				if werr != nil {
					return
				}
			}
			if err != nil {
				_ = ws.Close(websocket.StatusNormalClosure, "console closed")
				return
			}
		}
	}()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageBinary:
			for len(data) > 0 {
				n := min(len(data), 65535)
				if _, err := backend.Write(agent.EncodeFrame(agent.FrameData, data[:n])); err != nil {
					return
				}
				data = data[n:]
			}
		case websocket.MessageText:
			var msg struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &msg) != nil || msg.Type != "resize" || msg.Cols <= 0 || msg.Rows <= 0 || msg.Cols > 999 || msg.Rows > 999 {
				continue
			}
			p := make([]byte, 4)
			binary.BigEndian.PutUint16(p[0:2], uint16(msg.Cols))
			binary.BigEndian.PutUint16(p[2:4], uint16(msg.Rows))
			if _, err := backend.Write(agent.EncodeFrame(agent.FrameResize, p)); err != nil {
				return
			}
		}
	}
}
