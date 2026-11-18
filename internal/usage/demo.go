package usage

import (
	"math/rand"
	"time"
)

// Demo builds a believable snapshot for previews and design work, without touching real data.
func Demo(now time.Time) *Snapshot {
	rng := rand.New(rand.NewSource(7))
	store, _ := OpenStore("")
	var events []Event
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for d := 6; d >= 0; d-- {
		day := today.AddDate(0, 0, -d)
		if day.Weekday() == time.Saturday && d > 0 {
			continue // a day off
		}
		for h := 8; h < 24; h++ {
			t := day.Add(time.Duration(h) * time.Hour)
			if t.After(now) || (h > 12 && h < 14) || (h > 19 && rng.Float64() < 0.6) {
				continue
			}
			for m := 0; m < 60; m += 3 {
				if rng.Float64() < 0.35 {
					continue
				}
				model := "claude-opus-5-5"
				if rng.Float64() < 0.22 {
					model = "claude-sonnet-5-5"
				}
				ev := Event{Time: t.Add(time.Duration(m) * time.Minute), Model: model, Project: "claude-tv",
					Usage: Usage{Input: 3, Output: int64(400 + rng.Intn(1800)), CacheWrite: int64(rng.Intn(4000)),
						CacheWrite1h: 0, CacheRead: int64(40000 + rng.Intn(90000))}}
				ev.Usage.CacheWrite1h = ev.Usage.CacheWrite
				if ev.Time.After(now) {
					continue
				}
				events = append(events, ev)
			}
		}
	}
	store.Merge(events)
	lim := Limits{
		Session:   Window{Known: true, Util: 62, ResetsAt: now.Add(108 * time.Minute), Length: 5 * time.Hour},
		Week:      Window{Known: true, Util: 38, ResetsAt: now.Add(3*24*time.Hour + 5*time.Hour), Length: 7 * 24 * time.Hour},
		FetchedAt: now,
	}
	return Build(now, store, events, lim, nil)
}
