package loop

import "testing"

func TestRepeatTrips(t *testing.T) {
	d := New(Config{})
	args := map[string]any{"file_path": "a.go"}
	if d.ToolCall("Read", args) != nil || d.ToolCall("Read", args) != nil {
		t.Fatal("tripped too early")
	}
	if v := d.ToolCall("Read", args); v == nil || v.Count != 3 {
		t.Fatalf("want loop at 3, got %+v", v)
	}
}

func TestWindowTrips(t *testing.T) {
	d := New(Config{MaxRepeat: 10, MaxInWin: 3})
	for i := 0; i < 2; i++ {
		d.ToolCall("Read", "x")
		d.ToolCall("Write", i)
	}
	if v := d.ToolCall("Read", "x"); v == nil {
		t.Fatal("want window loop")
	}
}

func TestDistinctCallsPass(t *testing.T) {
	d := New(Config{})
	for i := 0; i < 50; i++ {
		if v := d.ToolCall("Read", i); v != nil {
			t.Fatalf("false positive at %d: %+v", i, v)
		}
	}
}
