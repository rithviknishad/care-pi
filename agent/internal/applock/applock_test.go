package applock

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func short(t *testing.T) {
	t.Helper()
	n, p := NoticeAfter, Poll
	NoticeAfter, Poll = 50*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { NoticeAfter, Poll = n, p })
}

func TestSecondCommandWaitsForTheFirst(t *testing.T) {
	short(t)
	p := paths.New(t.TempDir())
	ctx := context.Background()
	release, err := Acquire(ctx, p, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p.Abs(paths.RunDir)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("%s: %v %v", paths.RunDir, st, err)
	}

	var released atomic.Bool
	var out syncBuffer
	var wg sync.WaitGroup
	wg.Add(1)
	errc := make(chan error, 1)
	go func() {
		defer wg.Done()
		r, err := Acquire(ctx, p, 10*time.Second, &out)
		if err == nil {
			if !released.Load() {
				err = errors.New("the second command took the lock while the first held it")
			}
			r()
		}
		errc <- err
	}()
	time.Sleep(150 * time.Millisecond)
	released.Store(true)
	release()
	wg.Wait()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != Waiting+"\n" {
		t.Fatalf("want one notice, got %q", got)
	}
}

func TestTimeout(t *testing.T) {
	short(t)
	p := paths.New(t.TempDir())
	release, err := Acquire(context.Background(), p, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var out syncBuffer
	_, err = Acquire(context.Background(), p, 100*time.Millisecond, &out)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Acquire(ctx, p, 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
