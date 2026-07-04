package app

import (
	"bytes"
	"testing"
	"time"
)

func TestTimestampedLineWriterPrefixesContentLines(t *testing.T) {
	now := func() time.Time {
		return time.Date(2026, 7, 4, 12, 34, 56, 0, time.FixedZone("MDT", -6*60*60))
	}
	var out bytes.Buffer
	w := newTimestampedLineWriter(&out, now)

	if _, err := w.Write([]byte("first ")); err != nil {
		t.Fatalf("write first fragment: %v", err)
	}
	if _, err := w.Write([]byte("line\n\nsecond\nthird")); err != nil {
		t.Fatalf("write second fragment: %v", err)
	}
	if _, err := w.Write([]byte(" line\n")); err != nil {
		t.Fatalf("write final fragment: %v", err)
	}

	want := "2026-07-04T12:34:56-06:00 first line\n\n2026-07-04T12:34:56-06:00 second\n2026-07-04T12:34:56-06:00 third line\n"
	if got := out.String(); got != want {
		t.Fatalf("timestamped output = %q, want %q", got, want)
	}
}
