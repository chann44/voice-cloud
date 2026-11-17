package main

import (
	"strings"
	"testing"
)

func TestUsageObserver(t *testing.T) {
	for _, stream := range []bool{false, true} {
		o := &usageObserver{sse: stream}
		payload := `{"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}}`
		if stream {
			payload = "data: " + payload + "\r\n\r\ndata: [DONE]\n\n"
		}
		for _, b := range []byte(payload) {
			o.Write([]byte{b})
		}
		o.finish()
		if o.metrics["input_tokens"] != 12 || o.metrics["output_tokens"] != 4 || o.metrics["total_tokens"] != 16 || (stream && !o.done) {
			t.Fatalf("incorrect usage: %+v", o)
		}
	}
	o := &usageObserver{sse: true}
	o.Write([]byte("data: " + strings.Repeat("x", usageInspectionLimit+1) + "\ndata: {\"usage\":{\"input_tokens\":3}}\n"))
	o.finish()
	if o.metrics["input_tokens"] != 3 {
		t.Fatal("large event prevented subsequent usage inspection")
	}
	o = &usageObserver{}
	o.Write([]byte(`{"usage":{"prompt_tokens":-1,"completion_tokens":null,"seconds":1.25}}`))
	o.finish()
	if _, ok := o.metrics["input_tokens"]; ok {
		t.Fatal("negative quantity accepted")
	}
	if _, ok := o.metrics["output_tokens"]; ok {
		t.Fatal("null quantity accepted")
	}
	if o.metrics["audio_ms"] != 1250 {
		t.Fatal("audio duration lost")
	}
	o = &usageObserver{sse: true}
	o.Write([]byte("data: {\"error\":{\"message\":\"failed\"}}\n\ndata: [DONE]\n\n"))
	if !o.failed || !o.done {
		t.Fatal("in-band error not observed")
	}
}

func TestCanonicalUsageWinsOverAliases(t *testing.T) {
	observer := &usageObserver{}
	observer.inspect([]byte(`{"usage":{"input_tokens":4,"prompt_tokens":3,"output_tokens":6,"completion_tokens":5}}`))
	if observer.metrics["input_tokens"] != 4 || observer.metrics["output_tokens"] != 6 {
		t.Fatalf("usage aliases changed canonical counts: %+v", observer.metrics)
	}
}
