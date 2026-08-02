package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
)

// An interactive shell on one running instance.
//
// This is the only part of the CLI that is a conversation rather than a
// request. Bytes travel both ways for as long as somebody keeps typing, and
// neither end knows in advance how many there will be, which is why the
// transport is a socket and not a response body.
//
// Three facts about the far end shape everything here:
//
//   - It is always a terminal. The server allocates one for every session,
//     which is what makes a prompt, an editor and a colour work. It is also why
//     there is no exit status to report: a terminal reports none, and the
//     server turns the exec's own status into a line of red text on its way
//     past. Nothing here can tell a command that failed from one that did not.
//   - Input is a control frame and output is raw bytes. The asymmetry is the
//     server's: it has to tell a keystroke from a resize, and it has nothing to
//     add to output that the bytes do not already say.
//   - The command is split by the server, quotes respected, and executed
//     directly rather than through a shell. Shell syntax needs an explicit
//     `sh -c "..."`, which is what somebody types rather than something this
//     wraps around them.

const (
	// DefaultShellCommand is what the platform runs when nothing is asked for.
	//
	// The choice is the server's and this is only a copy of it, kept so that a
	// message written before the connection exists can still name what will
	// run. Nothing is sent when the command is empty, so if the two ever
	// disagree the server's answer is the one that happens.
	DefaultShellCommand = "sh"

	// shellSubprotocol is what the API answers the handshake with. It is
	// negotiated rather than assumed: offering something else fails the
	// handshake instead of connecting to a bridge that speaks another protocol.
	shellSubprotocol = "outplane.shell.v1"

	// shellKeepalive is how often an idle session proves it is still there.
	//
	// A shell can sit untouched for an hour and has to still work afterwards.
	// Nothing in the protocol says anything during that hour, and something in
	// between will eventually reclaim a connection that looks abandoned. The
	// first sign would be a terminal that quietly stops answering.
	shellKeepalive = 30 * time.Second
)

// Shell is a live session on one instance.
type Shell struct {
	conn *websocket.Conn

	// pending holds a character that arrived in pieces. See Send.
	pending []byte
}

// clientFrame is what this end sends. The server understands two kinds and
// ignores everything else without saying so, which is reason enough to declare
// the shape once here rather than assemble it at each call.
type clientFrame struct {
	T string `json:"t"`
	D string `json:"d,omitempty"`
	C int    `json:"c,omitempty"`
	R int    `json:"r,omitempty"`
}

// OpenShell starts a session on one instance of an application.
//
// instance is a name from AppInstances. The server checks that it belongs to
// this application before it execs into anything, so an instance borrowed from
// somewhere else is refused there rather than trusted here.
//
// command is what to run, empty meaning the platform's default shell. An image
// that ships no shell fails the exec, and the server explains that in the
// session's own output rather than in an error: by then the socket is open and
// there is nowhere else to say it.
func OpenShell(ctx context.Context, c *api.Client, appID, instance, command string) (*Shell, error) {
	query := url.Values{}
	query.Set("pod", instance)
	if strings.TrimSpace(command) != "" {
		query.Set("command", command)
	}

	conn, err := c.Dial(ctx, "/AppShell/Connect/"+appID, query, shellSubprotocol)
	if err != nil {
		return nil, err
	}

	// A terminal produces bursts larger than the library's default limit: the
	// server forwards whatever it read from the instance in one go, and `cat`
	// of a large file is one go. There is nothing to protect by capping it. The
	// far end is the caller's own process and the bytes are on their way to the
	// caller's own screen.
	conn.SetReadLimit(-1)

	return &Shell{conn: conn}, nil
}

// Send forwards keystrokes to the far end.
//
// The frame carries them as a JSON string, so what arrives has to be valid
// UTF-8. A read from a terminal can end in the middle of a character: paste a
// paragraph of anything but ASCII and eventually one will straddle the buffer
// boundary. The trailing piece is held back and sent with the bytes that
// complete it, because encoding it as it stands would turn it into a
// replacement character and the far end would receive something nobody typed.
//
// Send keeps that leftover, so it is the one method here that is not safe to
// call concurrently. Nothing needs to: keystrokes come from the one goroutine
// reading the terminal.
func (s *Shell) Send(ctx context.Context, keys []byte) error {
	buf := keys
	if len(s.pending) > 0 {
		buf = append(s.pending, keys...)
		s.pending = nil
	}

	if n := incompleteRune(buf); n > 0 {
		s.pending = append([]byte(nil), buf[len(buf)-n:]...)
		buf = buf[:len(buf)-n]
	}
	if len(buf) == 0 {
		return nil
	}
	return s.write(ctx, clientFrame{T: "i", D: string(buf)})
}

// Resize tells the far end how large the terminal is now.
//
// A session starts at whatever size the runtime picked, which is not the
// reader's, so this is sent once on connecting and again whenever the window
// changes. A non-positive size is dropped rather than sent: the server would
// substitute 80x24 for it, and a window that briefly reports zero rows during a
// resize would leave the far end wrapping at a width nobody has.
func (s *Shell) Resize(ctx context.Context, cols, rows int) error {
	if cols < 1 || rows < 1 {
		return nil
	}
	return s.write(ctx, clientFrame{T: "r", C: cols, R: rows})
}

// Stream copies everything the far end writes to w, until the session ends.
//
// What arrives is already formatted for a terminal: escape sequences, carriage
// returns, colour, cursor movement. It goes through untouched and unbuffered,
// because anything else would be a second terminal emulator with its own bugs,
// and the reader already has a working one.
func (s *Shell) Stream(ctx context.Context, w io.Writer) error {
	for {
		// The message type is not checked. The bridge sends binary and a
		// terminal's bytes are bytes either way; refusing text would break a
		// session over a distinction that changes nothing.
		_, r, err := s.conn.Reader(ctx)
		if err != nil {
			if closedNormally(err) {
				return nil
			}
			return shellError(ctx, err, "the session ended unexpectedly")
		}
		if _, err := io.Copy(w, r); err != nil {
			return clierr.New(clierr.KindInternal, "could not write to the terminal: %v", err)
		}
	}
}

// Keepalive holds an idle session open until the context ends.
//
// A ping that goes unanswered ends the session with an error, which is the
// point: the alternative is a terminal that keeps accepting input nobody is
// receiving. Failure needs no report here, because the read loop is about to
// make the same discovery and it is the one talking to the reader.
func (s *Shell) Keepalive(ctx context.Context) {
	ticker := time.NewTicker(shellKeepalive)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.conn.Ping(ctx); err != nil {
				return
			}
		}
	}
}

// Close ends the session, so the far end sees somebody leaving rather than a
// connection that died.
func (s *Shell) Close() error {
	return s.conn.Close(websocket.StatusNormalClosure, "")
}

func (s *Shell) write(ctx context.Context, frame clientFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return clierr.New(clierr.KindInternal, "could not encode a terminal frame: %v", err)
	}
	if err := s.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		if closedNormally(err) {
			return nil
		}
		return shellError(ctx, err, "the session ended while sending")
	}
	return nil
}

// closedNormally reports a session that ended by itself: the shell exited, or
// the instance went away and the server closed the bridge behind it. Both are
// how a session is meant to end, so neither is a failure.
//
// An abnormal closure is not included. A connection that died without saying
// goodbye is exactly the case worth reporting, because from the reader's side
// it is indistinguishable from a shell that hung.
func closedNormally(err error) bool {
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return true
	}
	return errors.Is(err, io.EOF)
}

// shellError explains a broken session, unless the reader broke it.
func shellError(ctx context.Context, err error, what string) error {
	if e := clierr.Cancelled(ctx, "interrupted"); e != nil {
		return e
	}
	return clierr.New(clierr.KindUpstream, "%s: %v", what, err).
		WithCode("shell.disconnected").
		WithHint("The instance may have restarted or been replaced by a deployment. " +
			"Nothing typed after the disconnection reached it.")
}

// incompleteRune reports how many trailing bytes begin a character that has not
// arrived in full. See Send.
//
// It walks back to the last byte that starts a character and compares the
// length that byte promises with the number of bytes actually present. A byte
// that starts nothing valid promises one and is passed straight through:
// censoring what somebody typed is not this function's job, and the far end
// will make of it exactly what a local terminal would.
func incompleteRune(b []byte) int {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		start := len(b) - i
		if !utf8.RuneStart(b[start]) {
			continue
		}
		if runeLen(b[start]) > i {
			return i
		}
		return 0
	}
	return 0
}

// runeLen is how many bytes the character beginning with this byte occupies.
func runeLen(first byte) int {
	switch {
	case first&0x80 == 0x00:
		return 1
	case first&0xE0 == 0xC0:
		return 2
	case first&0xF0 == 0xE0:
		return 3
	case first&0xF8 == 0xF0:
		return 4
	}
	return 1
}
