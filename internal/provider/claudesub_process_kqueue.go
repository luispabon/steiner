//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package provider

import (
	"fmt"
	"sync"

	"golang.org/x/sys/unix"
)

// newClaudeSubExitObserver registers a kqueue EVFILT_PROC NOTE_EXIT watch on
// pid. kqueue reports the process exit without reaping it, and the NOTE_EXIT
// event refers to this process, so it can never observe a reused PID.
func newClaudeSubExitObserver(pid int) (claudeSubExitObserver, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("create claude CLI kqueue observer: %w", err)
	}
	change := unix.Kevent_t{
		Ident:  uint64(pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}
	if _, err := unix.Kevent(kq, []unix.Kevent_t{change}, nil, nil); err != nil {
		_ = unix.Close(kq)
		return nil, fmt.Errorf("register claude CLI exit observer: %w", err)
	}
	o := &claudeSubKqueueObserver{
		kq:       kq,
		exitedCh: make(chan struct{}),
		stop:     make(chan struct{}),
	}
	go o.watch()
	return o, nil
}

type claudeSubKqueueObserver struct {
	kq       int
	exitedCh chan struct{}
	stop     chan struct{}

	exitOnce sync.Once
	stopOnce sync.Once
}

func (o *claudeSubKqueueObserver) exited() <-chan struct{} { return o.exitedCh }

func (o *claudeSubKqueueObserver) close() {
	o.stopOnce.Do(func() { close(o.stop) })
}

// watch owns the kqueue descriptor: it closes it on return.
func (o *claudeSubKqueueObserver) watch() {
	defer unix.Close(o.kq)
	events := make([]unix.Kevent_t, 1)
	timeout := unix.NsecToTimespec(int64(claudeSubObserverPoll))
	for {
		n, err := unix.Kevent(o.kq, nil, events, &timeout)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if n > 0 {
			o.exitOnce.Do(func() { close(o.exitedCh) })
			return
		}
		select {
		case <-o.stop:
			return
		default:
		}
	}
}
