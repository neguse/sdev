package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryKeepsOneEntryPerAddressNewestFirst(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sdev", "history.json")
	at := func(n int) time.Time { return time.Unix(int64(n), 0).UTC() }
	for _, e := range []historyEntry{
		{Address: "a", Hostname: "old", LastConnected: at(1)},
		{Address: "b", LastConnected: at(2)},
		{Address: "a", Hostname: "new", LastConnected: at(3)},
	} {
		if err := recordConnection(file, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := readHistory(file, func(address string) bool { return address == "b" })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Address != "a" || got[0].Hostname != "new" || got[1].Address != "b" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Reachable == nil || *got[0].Reachable || got[1].Reachable == nil || !*got[1].Reachable {
		t.Errorf("reachable: %+v", got)
	}
	// An agent tells a sleeping device apart by this, so false must be written out.
	out, err := json.Marshal(got[0])
	if err != nil || !strings.Contains(string(out), `"reachable":false`) {
		t.Errorf("%s, %v", out, err)
	}
}

func TestHistoryIsEmptyAtFirst(t *testing.T) {
	got, err := readHistory(filepath.Join(t.TempDir(), "history.json"), func(string) bool { return true })
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("%v, %v", got, err)
	}
}

func TestHistoryDoesNotStoreReachability(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.json")
	answers := true
	if err := recordConnection(file, historyEntry{Address: "a", Reachable: &answers}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `[{"address":"a","last_connected":"0001-01-01T00:00:00Z"}]` {
		t.Errorf("got %s", data)
	}
}

func TestDescribeDevice(t *testing.T) {
	d, dev := setup(t)
	d.variant = "vr"
	e := dev.historyEntry()
	if e.Address != dev.host || e.Login != "steamos" || e.Hostname != "fakehost" || e.Variant != "vr" || e.LastConnected.IsZero() {
		t.Errorf("got %+v", e)
	}
}
