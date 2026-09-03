package fileindex

import (
	"math"
	"testing"
)

func TestFloat16RoundTrip(t *testing.T) {
	in := []float32{0, 1, -1, 0.5, 0.123, -2.75}
	out := float16Decode(float16Encode(in))
	for i := range in {
		if math.Abs(float64(in[i]-out[i])) > 0.01 {
			t.Fatalf("f16 drift at %d: %v vs %v", i, in[i], out[i])
		}
	}
}

func TestCosine(t *testing.T) {
	if c := cosine([]float32{1, 0}, []float32{1, 0}); math.Abs(c-1) > 1e-6 {
		t.Fatalf("parallel=%f", c)
	}
	if c := cosine([]float32{1, 0}, []float32{0, 1}); math.Abs(c) > 1e-6 {
		t.Fatalf("orthogonal=%f", c)
	}
}
