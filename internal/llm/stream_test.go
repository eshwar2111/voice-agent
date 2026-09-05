package llm

import (
	"context"
	"testing"
)

func TestStreamViaGenerateEmitsWholeAnswer(t *testing.T) {
	ch := make(chan string, 4)
	got, err := streamViaGenerate(func() (string, error) { return "hello world", nil }, ch)
	if err != nil || got != "hello world" {
		t.Fatalf("got %q err=%v", got, err)
	}
	var acc string
	for d := range ch { // must be closed so range ends
		acc += d
	}
	if acc != "hello world" {
		t.Fatalf("streamed %q, want whole answer", acc)
	}
}

func TestStreamViaGenerateClosesOnError(t *testing.T) {
	ch := make(chan string, 4)
	if _, err := streamViaGenerate(func() (string, error) { return "", context.Canceled }, ch); err == nil {
		t.Fatal("want error")
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel must be closed and empty on error")
	}
}
