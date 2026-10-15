package agent

import (
	"encoding/binary"
	"io"
	"net/http"
	"os/exec"

	"github.com/creack/pty"
)

// Terminal stream framing (client -> agent):
//
//	[type:1][length:2 big endian][payload]
//
// type 0 carries keyboard input, type 1 a resize with cols and rows as
// two big-endian uint16. The agent sends raw terminal output back.
const (
	FrameData   = 0
	FrameResize = 1
)

func (s *Server) ctConsole(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	argv, err := s.LXC.ConsoleCommand(r.Context(), name)
	if err != nil {
		fail(w, err)
		return
	}
	conn, rw, err := hijack(w)
	if err != nil {
		return
	}
	defer conn.Close()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm-256color"}
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 32})
	if err != nil {
		_, _ = conn.Write([]byte("\r\nconsole error: " + err.Error() + "\r\n"))
		return
	}
	defer func() {
		tty.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, tty)
		done <- struct{}{}
	}()
	go func() {
		readFrames(rw.Reader, func(typ byte, payload []byte) {
			switch typ {
			case FrameData:
				_, _ = tty.Write(payload)
			case FrameResize:
				if len(payload) == 4 {
					cols := binary.BigEndian.Uint16(payload[0:2])
					rows := binary.BigEndian.Uint16(payload[2:4])
					if cols > 0 && rows > 0 && cols < 1000 && rows < 1000 {
						_ = pty.Setsize(tty, &pty.Winsize{Cols: cols, Rows: rows})
					}
				}
			}
		})
		done <- struct{}{}
	}()
	<-done
}

func readFrames(r io.Reader, fn func(typ byte, payload []byte)) {
	hdr := make([]byte, 3)
	buf := make([]byte, 65535)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[1:3]))
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return
		}
		fn(hdr[0], buf[:n])
	}
}

// EncodeFrame builds one client frame (used by the control plane).
func EncodeFrame(typ byte, payload []byte) []byte {
	out := make([]byte, 3+len(payload))
	out[0] = typ
	binary.BigEndian.PutUint16(out[1:3], uint16(len(payload)))
	copy(out[3:], payload)
	return out
}
