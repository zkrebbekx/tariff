//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
)

// A scenario is a named preset: a small, accurate configuration for one panel
// that teaches a single idea in about fifteen seconds. tariff is a stateless
// rating core, so unlike a stateful demo a scenario carries no history — it is
// just the inputs the page loads into a panel's controls, plus which panel to
// focus and a one-line note on what to watch. The numbers here are pinned to
// the library's golden vectors, so loading one and reading the panel reproduces
// a figure from the README exactly.
type scenario struct {
	Name   string         `json:"name"`
	Title  string         `json:"title"`
	Blurb  string         `json:"blurb"`
	Panel  string         `json:"panel"`
	Note   string         `json:"note"`
	Config map[string]any `json:"config"`
}

// scenarios are the loadable presets, keyed by name.
var scenarios = map[string]*scenario{
	"graduated-vs-volume": {
		Name:  "graduated-vs-volume",
		Title: "Graduated vs volume",
		Blurb: "The most-confused billing concept. One tiered schedule, two totals — $41.50 marginal vs $39.00 landed — at the same quantity.",
		Panel: "graduated",
		Note: "Schedule 1–5 @ $7, 6–10 @ $6.50, 11+ @ $6 at quantity 6: graduated sums each tier's " +
			"units at its own rate (5×$7 + 1×$6.50 = $41.50); volume charges the whole 6 at the one " +
			"landed rate (6×$6.50 = $39.00). Drag the quantity to watch them diverge.",
		Config: map[string]any{
			"rounding": "half_up",
			"quantity": 6,
			"tiers": []map[string]any{
				{"upTo": 5, "unitRate": "7"},
				{"upTo": 10, "unitRate": "13/2"},
				{"last": true, "unitRate": "6"},
			},
		},
	},

	"order-matters": {
		Name:  "order-matters",
		Title: "Order matters",
		Blurb: "The same three operations, two orders, two totals. A 10% discount before or after a $95 floor is not the same invoice.",
		Panel: "compose",
		Note: "Charge $100, then 10% off, then a $95 minimum → $95 (discount to $90, then floored up to $95). " +
			"Drag the minimum above the discount → $90 (floored to $100, unchanged, then 10% off). " +
			"tariff bakes in no order; the sequence you build is the invoice you get.",
		Config: map[string]any{
			"rounding": "half_up",
			"steps": []map[string]any{
				{"type": "charge", "label": "Base charge", "value": 100},
				{"type": "percent_off", "label": "10% off", "value": 10},
				{"type": "minimum", "label": "Minimum $95", "value": 95},
			},
		},
	},

	"mid-cycle-upgrade": {
		Name:  "mid-cycle-upgrade",
		Title: "Mid-cycle upgrade (proration)",
		Blurb: "Upgrade halfway through the month: credit the unused old price, charge the new one for the time left, net the two.",
		Panel: "proration",
		Note: "$10/mo → $20/mo, upgraded exactly halfway through January. Credit −$5.00 for the unused half of the " +
			"old plan, charge +$10.00 for the remaining half of the new plan, net +$5.00 — the verified cross-vendor " +
			"model, not a true-forward. Flip the basis to whole days and drag the change marker to watch the fraction move.",
		Config: map[string]any{
			"rounding":  "half_up",
			"oldAmount": 10.0,
			"newAmount": 20.0,
			"start":     "2024-01-01T00:00:00Z",
			"end":       "2024-02-01T00:00:00Z",
			"at":        "2024-01-16T12:00:00Z",
			"basis":     "second",
		},
	},
}

// order fixes the presentation order in the UI, the two flagship ideas first.
var scenarioOrder = []string{"graduated-vs-volume", "order-matters", "mid-cycle-upgrade"}

func scenarioList() []map[string]any {
	out := make([]map[string]any, 0, len(scenarioOrder))
	for _, name := range scenarioOrder {
		sc, ok := scenarios[name]
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"name":  sc.Name,
			"title": sc.Title,
			"blurb": sc.Blurb,
			"panel": sc.Panel,
		})
	}
	return out
}

// handleLoadScenario returns a scenario's full preset by name. The name may
// arrive as a bare string or as {"name": ...}.
func handleLoadScenario(in []byte) (any, error) {
	name := scenarioName(in)
	sc, ok := scenarios[name]
	if !ok {
		return nil, badArg("unknown_scenario", fmt.Sprintf("unknown scenario %q", name))
	}
	return sc, nil
}

// scenarioName extracts the requested name from a bare string or a {name}
// object, so loadScenario("order-matters") and loadScenario({name:"..."}) both
// work.
func scenarioName(in []byte) string {
	s := string(in)
	if s == "" || s == "{}" {
		return ""
	}
	if s[0] != '{' {
		return s
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(in, &obj); err == nil {
		return obj.Name
	}
	return ""
}
