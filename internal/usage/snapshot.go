package usage

import (
	"sort"
	"time"
)

// Day is one local calendar day of usage.
type Day struct {
	Date   time.Time
	Hours  [24]Bucket
	Tokens int64
	Cost   float64
}

// Snapshot is everything a screen may draw. Screens must cope with any field being empty.
type Snapshot struct {
	Now    time.Time
	Limits Limits
	// LimitsErr is set when the latest fetch failed; Limits then holds the last good values.
	LimitsErr error

	Today    Day
	Week     []Day // the last 7 days, oldest first, Week[6] == Today
	ByFamily map[string]int64

	BurnTokensPerHour int64   // last 60 minutes
	BurnCostPerHour   float64 // last 60 minutes
	Active            bool    // a message within the last 5 minutes
	Project           string  // project of the latest message

	// SessionCurve traces the 5-hour window so far: X is the fraction of the window elapsed,
	// Y the percent used. Its shape comes from local spend, scaled to end at Limits.Session.Util.
	SessionCurve []Point
}

type Point struct{ X, Y float64 }

// FamilyShare returns families sorted by today's token share, largest first.
func (s *Snapshot) FamilyShare() (names []string, share []float64) {
	var total int64
	for _, v := range s.ByFamily {
		total += v
	}
	for k := range s.ByFamily {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return s.ByFamily[names[i]] > s.ByFamily[names[j]] })
	for _, n := range names {
		share = append(share, float64(s.ByFamily[n])/float64(max(total, 1)))
	}
	return
}

// Build assembles a snapshot from the hourly store and the raw recent events.
func Build(now time.Time, store *Store, events []Event, lim Limits, limErr error) *Snapshot {
	s := &Snapshot{Now: now, Limits: lim, LimitsErr: limErr, ByFamily: map[string]int64{}}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for d := 6; d >= 0; d-- {
		date := today.AddDate(0, 0, -d)
		day := Day{Date: date}
		for h := 0; h < 24; h++ {
			b := store.Hour(date.Add(time.Duration(h) * time.Hour))
			day.Hours[h] = b
			day.Tokens += b.Tokens
			day.Cost += b.Cost
		}
		s.Week = append(s.Week, day)
	}
	s.Today = s.Week[6]
	for _, b := range s.Today.Hours {
		for f, t := range b.ByFamily {
			s.ByFamily[f] += t
		}
	}
	var latest time.Time
	for _, ev := range events {
		if age := now.Sub(ev.Time); age >= 0 && age < time.Hour {
			s.BurnTokensPerHour += ev.Usage.Tokens()
			s.BurnCostPerHour += ev.Usage.Cost(ev.Model)
		}
		if ev.Time.After(latest) {
			latest, s.Project = ev.Time, ev.Project
		}
	}
	s.Active = !latest.IsZero() && now.Sub(latest) < 5*time.Minute
	s.SessionCurve = sessionCurve(now, lim.Session, events)
	return s
}

func sessionCurve(now time.Time, w Window, events []Event) []Point {
	if !w.Known || w.ResetsAt.IsZero() {
		return nil
	}
	start := w.ResetsAt.Add(-w.Length)
	var in []Event
	for _, ev := range events {
		if !ev.Time.Before(start) && !ev.Time.After(now) {
			in = append(in, ev)
		}
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Time.Before(in[j].Time) })
	xOf := func(t time.Time) float64 { return t.Sub(start).Seconds() / w.Length.Seconds() }
	if xOf(now) <= 0 {
		return nil
	}
	pts := []Point{{0, 0}}
	var total float64
	for _, ev := range in {
		total += ev.Usage.Cost(ev.Model)
		pts = append(pts, Point{xOf(ev.Time), total})
	}
	pts = append(pts, Point{xOf(now), total})
	for i := range pts {
		if total > 0 {
			pts[i].Y = pts[i].Y / total * w.Util
		} else {
			pts[i].Y = pts[i].X / xOf(now) * w.Util // no local data: straight line
		}
	}
	return pts
}
