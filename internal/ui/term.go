package ui

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// terminal is the controlling terminal in raw mode.
type terminal struct {
	f    *os.File
	orig syscall.Termios
	alt  bool
	once sync.Once
}

// openTerminal puts /dev/tty in raw mode and, when alt is set, switches to
// the alternate screen.
func openTerminal(alt bool) (*terminal, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	t := &terminal{f: f, alt: alt}
	if err := ioctl(f.Fd(), ioctlGetTermios, unsafe.Pointer(&t.orig)); err != nil {
		f.Close()
		return nil, err
	}
	raw := t.orig
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(f.Fd(), ioctlSetTermios, unsafe.Pointer(&raw)); err != nil {
		f.Close()
		return nil, err
	}
	if alt {
		f.WriteString("\x1b[?1049h")
	}
	return t, nil
}

// restore undoes openTerminal. It is safe to call more than once.
func (t *terminal) restore() {
	t.once.Do(func() {
		s := "\x1b[0m\x1b[?25h"
		if t.alt {
			s += "\x1b[?1049l"
		}
		t.f.WriteString(s)
		ioctl(t.f.Fd(), ioctlSetTermios, unsafe.Pointer(&t.orig))
		t.f.Close()
	})
}

// size returns the terminal width and height, or 80x24 when unknown.
func (t *terminal) size() (int, int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if ioctl(t.f.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)) != nil || ws.Col == 0 || ws.Row == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// WaitKey writes msg to the terminal and waits for a key press, so a message
// stays readable in a popup that closes when the program exits.
func WaitKey(msg string) {
	t, err := openTerminal(false)
	if err != nil {
		return
	}
	defer t.restore()
	t.f.WriteString(msg)
	t.f.Read(make([]byte, 64))
}
