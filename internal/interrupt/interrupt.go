// Package interrupt removes temporary files and directories when doszer is
// interrupted (SIGINT, SIGTERM), which would otherwise skip the deferred
// cleanup that removes them.
package interrupt

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	mu     sync.Mutex
	next   int
	paths  = map[int]string{}
	onExit []func()
)

// OnExit registers f to run when the process is interrupted, after the
// registered paths are removed and before it exits (a run log's last
// write).
func OnExit(f func()) {
	mu.Lock()
	onExit = append(onExit, f)
	mu.Unlock()
}

// Remove registers path for removal (with everything under it) if the
// process is interrupted. The returned func unregisters it, for once the
// caller has removed or kept it.
func Remove(path string) (done func()) {
	mu.Lock()
	defer mu.Unlock()
	id := next
	next++
	paths[id] = path
	return func() {
		mu.Lock()
		delete(paths, id)
		mu.Unlock()
	}
}

// Handle installs the handler: on the first interrupt it removes every
// registered path and exits with status 130.
func Handle() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		mu.Lock()
		for _, p := range paths {
			if err := os.RemoveAll(p); err != nil {
				fmt.Fprintf(os.Stderr, "interrupted: could not remove %s: %v\n", p, err)
			}
		}
		fs := onExit
		mu.Unlock()
		for _, f := range fs {
			f()
		}
		os.Exit(130)
	}()
}
