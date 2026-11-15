package tview

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// runWithKeys queues keys before the event loop starts, so they are all waiting at once,
// and returns the number of draws that happened after the first one.
func runWithKeys(t *testing.T, keys int, handle func()) int64 {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	app := NewApplication().SetScreen(screen).SetRoot(NewBox(), true)

	var draws, handled atomic.Int64
	app.SetAfterDrawFunc(func(tcell.Screen) { draws.Add(1) })
	done := make(chan struct{})
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if handle != nil {
			handle()
		}
		if handled.Add(1) == int64(keys) {
			close(done)
		}
		return event
	})
	for i := 0; i < keys; i++ {
		app.QueueEvent(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	}

	stopped := make(chan error, 1)
	go func() { stopped <- app.Run() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("handled %d of %d keys", handled.Load(), keys)
	}
	// lets the draw that follows the last key happen
	app.QueueUpdate(func() {})
	app.QueueUpdate(func() {})
	app.Stop()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	return draws.Load() - 1
}

func TestKeysWaitingInTheQueueShareOneDraw(t *testing.T) {
	if draws := runWithKeys(t, 80, nil); draws > 3 {
		t.Fatalf("80 queued keys caused %d draws, want at most 3", draws)
	}
}

func TestTheLastQueuedKeyIsDrawn(t *testing.T) {
	if draws := runWithKeys(t, 80, nil); draws < 1 {
		t.Fatal("nothing was drawn after the queued keys")
	}
}

func TestSlowKeyHandlersStillDrawWhileKeysWait(t *testing.T) {
	draws := runWithKeys(t, 10, func() { time.Sleep(40 * time.Millisecond) })
	if draws < 4 {
		t.Fatalf("10 slow keys caused %d draws, want the screen to keep updating", draws)
	}
}

// The screen cannot deliver input while it is being drawn.
// Right after a draw the queue therefore looks empty although the rest of a burst is on its way,
// and drawing at once would spend a whole draw on every key or two.
func TestKeysArrivingRightAfterADrawShareTheNextDraw(t *testing.T) {
	const keys = 100
	screen := tcell.NewSimulationScreen("UTF-8")
	root := NewBox()
	root.SetDrawFunc(func(_ tcell.Screen, x, y, width, height int) (int, int, int, int) {
		time.Sleep(3 * time.Millisecond)
		return x, y, width, height
	})
	app := NewApplication().SetScreen(screen).SetRoot(root, true)

	var drawing sync.Mutex
	var draws, handled atomic.Int64
	app.SetBeforeDrawFunc(func(tcell.Screen) bool {
		drawing.Lock()
		return false
	})
	app.SetAfterDrawFunc(func(tcell.Screen) {
		draws.Add(1)
		drawing.Unlock()
	})
	done := make(chan struct{})
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if handled.Add(1) == keys {
			close(done)
		}
		return event
	})

	stopped := make(chan error, 1)
	go func() { stopped <- app.Run() }()
	go func() {
		for i := 0; i < keys; i++ {
			drawing.Lock() // input waits for a draw to end
			drawing.Unlock()
			app.QueueEvent(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
			time.Sleep(100 * time.Microsecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("handled %d of %d keys", handled.Load(), keys)
	}
	app.QueueUpdate(func() {})
	app.Stop()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if got := draws.Load(); got > 15 {
		t.Fatalf("%d keys arriving just after draws caused %d draws, want at most 15", keys, got)
	}
}
