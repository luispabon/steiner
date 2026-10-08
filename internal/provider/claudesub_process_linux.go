//go:build linux || android

package provider

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// newClaudeSubExitObserver opens a pidfd for pid and reports the process exit
// by polling it. A pidfd refers to the process itself, so it can never observe
// a reused PID, and polling it does not reap the child.
func newClaudeSubExitObserver(pid int) (claudeSubExitObserver, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, fmt.Errorf("claude CLI transport requires pidfd support (Linux 5.3+) to observe exit safely: %w", err)
	}
	o := &claudeSubPidfdObserver{
		fd:       fd,
		exitedCh: make(chan struct{}),
		stop:     make(chan struct{}),
	}
	go o.watch()
	return o, nil
}

type claudeSubPidfdObserver struct {
	fd       int
	exitedCh chan struct{}
	stop     chan struct{}

	exitOnce sync.Once
	stopOnce sync.Once
}

func (o *claudeSubPidfdObserver) exited() <-chan struct{} { return o.exitedCh }

func (o *claudeSubPidfdObserver) close() {
	o.stopOnce.Do(func() { close(o.stop) })
}

// watch owns the pidfd: it closes it on return. The process exit is signalled
// as soon as the pidfd becomes readable, and stop is polled so a stopped
// observer returns without waiting for the process.
func (o *claudeSubPidfdObserver) watch() {
	defer unix.Close(o.fd)
	fds := []unix.PollFd{{Fd: int32(o.fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, int(claudeSubObserverPoll/time.Millisecond))
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
