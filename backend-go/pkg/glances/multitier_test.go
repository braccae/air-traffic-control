package glances

import "testing"

// These tests pin the pure parsing logic ported from
// src/utils/telemetryCollector.ts. They use genuine command/REST output shapes;
// no synthetic metric values are asserted into production code paths.

func TestParseRestData(t *testing.T) {
	payload := map[string]any{
		"cpu":  map[string]any{"total": 12.4},
		"mem":  map[string]any{"percent": 31.2, "used": 5.0, "total": 16.0},
		"load": map[string]any{"min1": 0.52, "min5": 0.41, "min15": 0.33},
	}

	got := ParseRestData(payload)
	if got.Source != SourceHTTPRest {
		t.Fatalf("source = %q, want %q", got.Source, SourceHTTPRest)
	}
	if got.CPU.Total != 12.4 {
		t.Fatalf("cpu.total = %v, want 12.4", got.CPU.Total)
	}
	if got.Mem.Percent != 31.2 || got.Mem.Used != 5.0 || got.Mem.Total != 16.0 {
		t.Fatalf("mem = %+v", got.Mem)
	}
	if got.Load.Min1 != 0.52 || got.Load.Min5 != 0.41 || got.Load.Min15 != 0.33 {
		t.Fatalf("load = %+v", got.Load)
	}

	// Glances /api/3/quicklook returns cpu as a bare number.
	bare := ParseRestData(map[string]any{"cpu": 7.5})
	if bare.CPU.Total != 7.5 {
		t.Fatalf("bare cpu total = %v, want 7.5", bare.CPU.Total)
	}
}

func TestParseCLIJSON(t *testing.T) {
	stdout := "Starting Glances...\n{\"cpu\": {\"total\": 4.1}, \"mem\": {\"percent\": 20.0, " +
		"\"used\": 1024, \"total\": 8192}, \"load\": {\"min1\": 0.1, \"min5\": 0.2, \"min15\": 0.3}}\ndone\n"

	got, err := ParseCLIJSON(stdout)
	if err != nil {
		t.Fatalf("ParseCLIJSON error: %v", err)
	}
	if got.Source != SourceCLIStdoutJSON {
		t.Fatalf("source = %q", got.Source)
	}
	if got.CPU.Total != 4.1 || got.Mem.Percent != 20.0 || got.Load.Min15 != 0.3 {
		t.Fatalf("payload = %+v", got)
	}

	if _, err := ParseCLIJSON("no json here"); err == nil {
		t.Fatal("expected an error for output without a JSON object")
	}
}

func TestParseSysfs(t *testing.T) {
	raw := "0.52 0.41 0.33 2/512 12345\n" +
		"MemTotal:       16384000 kB\n" +
		"MemFree:         8192000 kB\n" +
		"MemAvailable:   12288000 kB\n" +
		"Buffers:          100000 kB\n" +
		"Cached:          2000000 kB\n" +
		"cpu  1000 50 300 8000 100 0 50 0 0 0\n"

	got := ParseSysfs(raw)
	if got.Source != SourcePCPFallback {
		t.Fatalf("source = %q", got.Source)
	}
	if got.Load.Min1 != 0.52 || got.Load.Min5 != 0.41 || got.Load.Min15 != 0.33 {
		t.Fatalf("load = %+v", got.Load)
	}
	if got.Mem.Total != 16384000*1024 {
		t.Fatalf("mem.total = %v", got.Mem.Total)
	}
	if got.Mem.Used != (16384000-12288000)*1024 {
		t.Fatalf("mem.used = %v", got.Mem.Used)
	}
	if got.Mem.Percent != 25.0 {
		t.Fatalf("mem.percent = %v, want 25.0", got.Mem.Percent)
	}
	// total = 1000+50+300+8000+100+0+50 = 9500 ticks; idle+iowait = 8100
	// => busy 1400/9500 = 14.7%
	if got.CPU.Total != 14.7 {
		t.Fatalf("cpu.total = %v, want 14.7", got.CPU.Total)
	}
}

func TestParseSysfsEmpty(t *testing.T) {
	got := ParseSysfs("")
	if got.Mem.Total != 0 || got.CPU.Total != 0 || got.Load.Min1 != 0 {
		t.Fatalf("empty input must not fabricate metrics: %+v", got)
	}
}
