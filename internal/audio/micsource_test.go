package audio

import "testing"

func TestFrameRingReadsFixedFramesFIFO(t *testing.T) {
	r := newFrameRing(4)
	r.push([]int16{1, 2, 3, 4, 5, 6}) // 6 samples buffered
	f1, err := r.read()
	if err != nil || len(f1) != 4 || f1[0] != 1 || f1[3] != 4 {
		t.Fatalf("frame1 = %v err=%v", f1, err)
	}
	r.push([]int16{7, 8}) // now 4 buffered: 5,6,7,8
	f2, _ := r.read()
	if f2[0] != 5 || f2[3] != 8 {
		t.Fatalf("frame2 = %v", f2)
	}
}

func TestFrameRingReadUnblocksOnClose(t *testing.T) {
	r := newFrameRing(4)
	done := make(chan error, 1)
	go func() { _, err := r.read(); done <- err }() // blocks: no data
	r.close()
	if err := <-done; err == nil {
		t.Fatal("read must return an error once the ring is closed")
	}
}
