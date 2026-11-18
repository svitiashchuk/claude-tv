package usage

import "strings"

// Price is USD per million tokens.
type Price struct {
	Input, Output, CacheWrite5m, CacheWrite1h, CacheRead float64
}

func price(in, out, read float64) Price {
	return Price{Input: in, Output: out, CacheWrite5m: in * 1.25, CacheWrite1h: in * 2, CacheRead: read}
}

// Ordered most-specific first: "claude-opus-5-5" must match before "claude-opus-5".
var priceTable = []struct {
	prefix string
	p      Price
}{
	{"claude-fable-5", price(10, 50, 0.25)},
	{"claude-mythos-5", price(10, 50, 0.25)},
	{"claude-opus-5-5", price(4, 20, 0.20)},
	{"claude-opus-5", price(5, 25, 0.50)},
	{"claude-opus-4-8", price(5, 25, 0.50)},
	{"claude-opus-4-7", price(5, 25, 0.50)},
	{"claude-opus-4-6", price(5, 25, 0.50)},
	{"claude-opus-4-5", price(5, 25, 0.50)},
	{"claude-opus-4", price(15, 75, 1.50)},
	{"claude-sonnet-5", price(2, 10, 0.20)},
	{"claude-sonnet-4", price(3, 15, 0.30)},
	{"claude-3-7-sonnet", price(3, 15, 0.30)},
	{"claude-haiku-5", price(0.10, 0.50, 0.01)},
	{"claude-haiku-4", price(1, 5, 0.10)},
	{"claude-3-5-haiku", price(0.80, 4, 0.08)},
}

// PriceFor returns API list prices for a model id, falling back to Sonnet-class pricing.
func PriceFor(model string) Price {
	for _, e := range priceTable {
		if strings.HasPrefix(model, e.prefix) {
			return e.p
		}
	}
	return price(3, 15, 0.30)
}

// Family groups model ids for charts: opus, sonnet, haiku, fable, other.
func Family(model string) string {
	for _, f := range []string{"fable", "mythos", "opus", "sonnet", "haiku"} {
		if strings.Contains(model, f) {
			if f == "mythos" {
				return "fable"
			}
			return f
		}
	}
	return "other"
}

// Cost is the API-equivalent USD cost of one message's usage.
func (u Usage) Cost(model string) float64 {
	p := PriceFor(model)
	w5, w1 := u.CacheWrite5m, u.CacheWrite1h
	if w5+w1 == 0 {
		w5 = u.CacheWrite // older logs have no TTL split; assume 5m
	}
	c := float64(u.Input)*p.Input + float64(u.Output)*p.Output +
		float64(w5)*p.CacheWrite5m + float64(w1)*p.CacheWrite1h +
		float64(u.CacheRead)*p.CacheRead
	if u.Fast {
		c *= 2
	}
	return c / 1e6
}
