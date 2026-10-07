package plist

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMarshalLaunchdJob(t *testing.T) {
	job := map[string]any{
		"Label":            "com.example.test",
		"ProgramArguments": []string{"/usr/bin/say", "hi & <bye>"},
		"RunAtLoad":        true,
		"StartInterval":    float64(300), // as JSON would deliver it
		"StartCalendarInterval": map[string]any{
			"Hour": 9, "Minute": 30,
		},
		"Nice":      -5,
		"Threshold": 1.5,
		"When":      time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
		"Blob":      []byte{1, 2},
	}
	out, err := Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`<!DOCTYPE plist`, `<key>Label</key>`, `<string>com.example.test</string>`,
		`<string>hi &amp; &lt;bye&gt;</string>`, `<true/>`, `<integer>300</integer>`,
		`<key>Hour</key>`, `<integer>9</integer>`, `<integer>-5</integer>`, `<real>1.5</real>`,
		`<date>2026-10-07T12:00:00Z</date>`, `<data>AQI=</data>`, `</plist>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	// Keys are sorted so the output is stable.
	if strings.Index(s, "<key>Blob</key>") > strings.Index(s, "<key>Label</key>") {
		t.Error("keys must be emitted in sorted order")
	}
}

func TestMarshalRejectsUnsupported(t *testing.T) {
	if _, err := Marshal(map[string]any{"x": struct{}{}}); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("want ErrUnsupportedType, got %v", err)
	}
	if _, err := Marshal(nil); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("nil: %v", err)
	}
}

func TestMarshalValueIsAFragment(t *testing.T) {
	out, err := MarshalValue([]any{"a", 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "<plist") || !strings.HasPrefix(string(out), "<array>") {
		t.Fatalf("fragment: %s", out)
	}
}
