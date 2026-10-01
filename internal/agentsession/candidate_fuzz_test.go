package agentsession

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func FuzzSessionCandidates(f *testing.F) {
	f.Add([]byte{4, 7, 2, 9, 1})
	f.Add([]byte{1})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 32 {
			return
		}
		cands := make([]candidate, len(input))
		var earliest candidate
		for i, value := range input {
			cands[i] = candidate{id: fmt.Sprintf("session-%d", i), modTime: time.Unix(0, int64(value)*64+int64(i))}
			if i == 0 || cands[i].modTime.Before(earliest.modTime) {
				earliest = cands[i]
			}
		}
		got, ok := pickEarliest(cands)
		if ok != (len(cands) != 0) || ok && got != earliest.id {
			t.Fatalf("pickEarliest = %q, %t, want %q", got, ok, earliest.id)
		}
		slices.Reverse(cands)
		if reversed, reversedOK := pickEarliest(cands); reversedOK != ok || reversed != got {
			t.Fatalf("candidate order changed unique earliest: %q vs %q", got, reversed)
		}
		snapshot := map[string]int64{}
		for i, cand := range cands {
			if i%2 == 0 {
				snapshot[cand.id] = cand.modTime.UnixNano()
			} else {
				snapshot[cand.id] = cand.modTime.UnixNano() - 1
			}
		}
		matched := recaptureCandidates(slices.Clone(cands), nil, snapshot)
		for _, cand := range matched {
			if !afterSnapshot(snapshot, cand.id, cand.modTime.UnixNano()) {
				t.Fatalf("recapture included unchanged %s", cand.id)
			}
		}
		if len(matched) != len(cands)/2 {
			t.Fatalf("recapture returned %d changed candidates, want %d", len(matched), len(cands)/2)
		}
	})
}

func FuzzOpencodeExportParser(f *testing.F) {
	f.Add([]byte(`{"info":{"directory":"/tmp/work","time":{"created":1,"updated":2}}}`))
	f.Add([]byte(`{"info":`))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 8<<10 {
			return
		}
		dir, created, updated, ok := parseOpencodeExport(input)
		otherDir, otherCreated, otherUpdated, otherOK := parseOpencodeExport(slices.Clone(input))
		if dir != otherDir || !created.Equal(otherCreated) || !updated.Equal(otherUpdated) || ok != otherOK {
			t.Fatal("export parsing depends on input buffer identity")
		}
	})
}
