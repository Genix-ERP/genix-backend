package handler

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Zaxiralarni baholash — the cost algorithms, as pure functions.
//
// Nothing here touches the database or a gin.Context: these are the rules from
// §3 of the plan and nothing else, so they can be tested exhaustively without a
// fixture. Phase 2 wires them to documents and journal entries.
//
// MONEY IS int64 MINOR UNITS (tiyin). §3.5 forbids float outright, and it is
// right to: binary fractions introduce their own error before the business
// logic has done anything. Go has no decimal in the standard library and this
// module has no decimal dependency, so the engine works in whole tiyin and
// converts at the database boundary, where the columns are NUMERIC.
//
// QUANTITY IS *big.Rat. Quantities are genuinely fractional (kilograms,
// litres, metres) at NUMERIC(20,4), and the proportion take × RV / r has to be
// exact before it is rounded once. A float here would reintroduce exactly the
// drift the tiyin arithmetic exists to prevent.

// CostMethod is the valuation method in force for a product.
type CostMethod string

const (
	CostMethodFIFO     CostMethod = "fifo"
	CostMethodAVCO     CostMethod = "avco"
	CostMethodStandard CostMethod = "standard"
)

// ParseCostMethod normalises a stored or user-supplied method name.
//
// "aveco" is accepted because that misspelling is what the existing
// tenant_settings key has stored since inventory_settings.go was written;
// rejecting it would silently flip live tenants to the fallback. LIFO is not
// accepted at all — BHMS № 4 and IAS 2 prohibit it (plan §0) — and it is
// reported as an error rather than quietly falling back, so an import that
// tries to set it fails loudly.
func ParseCostMethod(raw string) (CostMethod, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "fifo":
		return CostMethodFIFO, nil
	case "avco", "aveco", "average", "weighted_average":
		return CostMethodAVCO, nil
	case "standard", "standard_price":
		return CostMethodStandard, nil
	case "lifo":
		return "", errors.New("LIFO is not a permitted valuation method under BHMS No. 4 / IAS 2")
	case "":
		return "", errNoMethod
	}
	return "", fmt.Errorf("unknown valuation method %q", raw)
}

var errNoMethod = errors.New("no valuation method given")

// EffectiveCostMethod resolves the hierarchy from §0: the product category
// overrides the company accounting policy, and the product card never chooses.
//
// categoryMethod is the raw product_categories.cost_method, empty when the
// category inherits. companyMethod is the tenant's default. An unreadable value
// at either level falls back to AVCO rather than failing the read — a movement
// must not become impossible because a settings row is malformed — but
// ParseCostMethod still rejects it at the point of WRITING, which is where the
// mistake can actually be corrected.
func EffectiveCostMethod(categoryMethod, companyMethod string) CostMethod {
	if m, err := ParseCostMethod(categoryMethod); err == nil {
		return m
	}
	if m, err := ParseCostMethod(companyMethod); err == nil {
		return m
	}
	return CostMethodAVCO
}

// Layer is one open valuation layer, in the form the engine needs.
//
// RemainingValue is money in tiyin; RemainingQty is an exact rational. Both are
// primary — the unit cost is derived when displayed and never used to compute
// anything (§3.5, first golden rule).
type Layer struct {
	ID             string
	SeqNo          int64 // tiebreaker within a date; the row's insertion order
	DateOrdinal    int64 // days since epoch, so ordering needs no time package
	RemainingQty   *big.Rat
	RemainingValue int64
}

// Consumption records that an issue took Qty out of layer LayerID for Value.
type Consumption struct {
	LayerID string
	Qty     *big.Rat
	Value   int64
}

// IssueResult is what an issue costs and how it got there.
type IssueResult struct {
	Cost         int64
	Consumptions []Consumption
}

var (
	// ErrInsufficientStock is the §2.5 rule: an issue larger than the balance
	// is rejected at posting time. Allowing it would force every method to
	// invent a cost for goods that are not there, and it is what makes the
	// three algorithms as simple as they are.
	ErrInsufficientStock = errors.New("issue quantity exceeds the quantity on hand")
	ErrNonPositiveQty    = errors.New("quantity must be positive")
)

// ratIsZero reports whether r is exactly zero.
func ratIsZero(r *big.Rat) bool { return r.Sign() == 0 }

// roundHalfUp turns an exact rational amount of tiyin into whole tiyin,
// rounding halves away from zero.
//
// math.Round would mean going through float64 — the one thing §3.5 forbids —
// so this is done on the rational directly: add a half of the denominator's
// sign and truncate.
func roundHalfUp(r *big.Rat) int64 {
	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())
	neg := num.Sign() < 0
	if neg {
		num.Neg(num)
	}
	// (2*num + den) / (2*den) is |r| + 1/2 truncated, i.e. half-up on |r|.
	twice := new(big.Int).Lsh(num, 1)
	twice.Add(twice, den)
	den2 := new(big.Int).Lsh(den, 1)
	q := new(big.Int).Quo(twice, den2)
	if neg {
		q.Neg(q)
	}
	return q.Int64()
}

// proportion computes part × total / whole as exact rational tiyin, then rounds
// once. This is the second golden rule: round the operation's final sum via the
// VALUE proportion, never via a rounded unit price — round(V/Q) × q
// accumulates error, q × V / Q does not.
func proportion(part, whole *big.Rat, total int64) int64 {
	if ratIsZero(whole) {
		return 0
	}
	r := new(big.Rat).SetInt64(total)
	r.Mul(r, part)
	r.Quo(r, whole)
	return roundHalfUp(r)
}

// SortLayersFIFO orders layers into the drain order: receipt date ascending,
// then insertion order.
//
// The tiebreaker is not cosmetic. Several receipts commonly land on the same
// date — an import, a split delivery — and without a unique second key the
// order is whatever the planner returns, so two runs of the same issue can
// consume different layers and produce different costs for identical inputs.
func SortLayersFIFO(layers []Layer) {
	sort.SliceStable(layers, func(i, j int) bool {
		if layers[i].DateOrdinal != layers[j].DateOrdinal {
			return layers[i].DateOrdinal < layers[j].DateOrdinal
		}
		return layers[i].SeqNo < layers[j].SeqNo
	})
}

// FIFOIssue consumes `qty` from the given layers oldest-first and returns the
// cost together with the per-layer consumption records (§3.1).
//
// `layers` is mutated in place: each drained layer's RemainingQty and
// RemainingValue are reduced by what was taken, so the caller writes back
// exactly what the engine decided.
//
// The rounding nuance that makes this correct: a layer consumed IN FULL yields
// its whole remaining value, not a recomputed proportion. Otherwise the last
// fraction of a tiyin stays behind on a layer whose quantity is zero, and those
// orphans accumulate until the Σ invariant against account 2910 fails.
func FIFOIssue(layers []Layer, qty *big.Rat) (IssueResult, error) {
	if qty.Sign() <= 0 {
		return IssueResult{}, ErrNonPositiveQty
	}
	SortLayersFIFO(layers)

	available := new(big.Rat)
	for i := range layers {
		available.Add(available, layers[i].RemainingQty)
	}
	if available.Cmp(qty) < 0 {
		return IssueResult{}, ErrInsufficientStock
	}

	remaining := new(big.Rat).Set(qty)
	out := IssueResult{}
	for i := range layers {
		if remaining.Sign() == 0 {
			break
		}
		l := &layers[i]
		if l.RemainingQty.Sign() <= 0 {
			continue
		}
		take := new(big.Rat).Set(l.RemainingQty)
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}

		var part int64
		if take.Cmp(l.RemainingQty) == 0 {
			part = l.RemainingValue // whole layer — take every last tiyin
		} else {
			part = proportion(take, l.RemainingQty, l.RemainingValue)
		}

		l.RemainingQty = new(big.Rat).Sub(l.RemainingQty, take)
		l.RemainingValue -= part
		remaining.Sub(remaining, take)

		out.Cost += part
		out.Consumptions = append(out.Consumptions, Consumption{
			LayerID: l.ID, Qty: take, Value: part,
		})
	}
	return out, nil
}

// AVCOState is (Q, V) for a product, company-wide.
//
// The average is derived on demand and never stored: storing it and multiplying
// back is what turns a few tiyin of rounding into a permanent gap between the
// warehouse and the ledger (§3.2).
type AVCOState struct {
	Qty   *big.Rat
	Value int64
}

// AVCOReceipt adds a receipt to the running state. Only receipts move the
// average; issues never do.
func AVCOReceipt(s AVCOState, qty *big.Rat, value int64) (AVCOState, error) {
	if qty.Sign() <= 0 {
		return s, ErrNonPositiveQty
	}
	return AVCOState{
		Qty:   new(big.Rat).Add(orZero(s.Qty), qty),
		Value: s.Value + value,
	}, nil
}

// AVCOIssue costs an issue at the running average and returns the new state.
//
// Taking the balance to zero yields the WHOLE remaining value rather than a
// recomputed proportion — the same rule as a fully-consumed FIFO layer, and the
// reason product_avco_state carries a CHECK that Q = 0 implies V = 0. Without
// it a long series of awkward divisions leaves a few tiyin sitting against zero
// quantity, which no report can explain.
func AVCOIssue(s AVCOState, qty *big.Rat) (int64, AVCOState, error) {
	if qty.Sign() <= 0 {
		return 0, s, ErrNonPositiveQty
	}
	have := orZero(s.Qty)
	if have.Cmp(qty) < 0 {
		return 0, s, ErrInsufficientStock
	}

	var cost int64
	if have.Cmp(qty) == 0 {
		cost = s.Value
	} else {
		cost = proportion(qty, have, s.Value)
	}
	return cost, AVCOState{
		Qty:   new(big.Rat).Sub(have, qty),
		Value: s.Value - cost,
	}, nil
}

// StandardIssue costs an issue at the standard price: cost = qty × standard.
// standardCost is in tiyin per unit.
func StandardIssue(qty *big.Rat, standardCost int64) (int64, error) {
	if qty.Sign() <= 0 {
		return 0, ErrNonPositiveQty
	}
	r := new(big.Rat).SetInt64(standardCost)
	r.Mul(r, qty)
	return roundHalfUp(r), nil
}

// StandardReceiptVariance is the §3.3 receipt: stock enters at the standard
// price and the difference against what was actually paid goes straight to the
// variance account. A positive result is a debit to variances (paid more than
// standard), a negative one a credit.
func StandardReceiptVariance(qty *big.Rat, actualUnitCost, standardCost int64) (layerValue int64, variance int64, err error) {
	if qty.Sign() <= 0 {
		return 0, 0, ErrNonPositiveQty
	}
	std := new(big.Rat).SetInt64(standardCost)
	std.Mul(std, qty)
	layerValue = roundHalfUp(std)

	act := new(big.Rat).SetInt64(actualUnitCost)
	act.Mul(act, qty)
	// The variance is the difference of the two ROUNDED sums, not the rounding
	// of the difference. Only then does layerValue + variance equal the actual
	// amount payable to the tiyin, which is what the journal entry has to
	// balance against.
	variance = roundHalfUp(act) - layerValue
	return layerValue, variance, nil
}

// StandardRevaluation is the delta a standard-price change produces on the
// stock currently held: Δ = Q × (new − old). Zero when nothing is on hand, in
// which case no document and no posting are produced at all.
func StandardRevaluation(qtyOnHand *big.Rat, oldCost, newCost int64) int64 {
	if qtyOnHand == nil || qtyOnHand.Sign() <= 0 {
		return 0
	}
	d := new(big.Rat).SetInt64(newCost - oldCost)
	d.Mul(d, qtyOnHand)
	return roundHalfUp(d)
}

// RescaleLayersToTotal distributes `total` across the open layers in proportion
// to quantity, so Σ remaining_value comes out at exactly `total`.
//
// This is what keeps §1.3 true for the two methods whose cost does NOT come
// from the layers themselves. Under standard costing the stock is worth
// Q × standard by definition, and under AVCO it is worth the running
// avco_value; in both cases the layers are drained FIFO for the audit trail, so
// without a rescale their sum would be the FIFO value and disagree with both
// the ledger and the method's own definition of stock value.
//
// The last open layer absorbs the rounding remainder for the same reason a full
// consumption takes the whole layer: distributing it evenly would leave the sum
// a tiyin or two away from the posting.
func RescaleLayersToTotal(layers []Layer, total int64) {
	SortLayersFIFO(layers)

	qtyTotal := new(big.Rat)
	lastOpen := -1
	for i := range layers {
		if layers[i].RemainingQty.Sign() > 0 {
			qtyTotal.Add(qtyTotal, layers[i].RemainingQty)
			lastOpen = i
		}
	}
	if lastOpen < 0 {
		return
	}

	assigned := int64(0)
	for i := range layers {
		if layers[i].RemainingQty.Sign() <= 0 {
			layers[i].RemainingValue = 0
			continue
		}
		if i == lastOpen {
			layers[i].RemainingValue = total - assigned
			continue
		}
		v := proportion(layers[i].RemainingQty, qtyTotal, total)
		layers[i].RemainingValue = v
		assigned += v
	}
}

// RescaleLayersToStandard rewrites open layers to a new standard price so that
// Σ remaining_value equals Q × standard.
//
// Expressed in terms of RescaleLayersToTotal rather than repeating the
// distribution, so a fix to the rounding rule cannot land in one and not the
// other.
func RescaleLayersToStandard(layers []Layer, newCost int64) {
	qtyTotal := new(big.Rat)
	for i := range layers {
		if layers[i].RemainingQty.Sign() > 0 {
			qtyTotal.Add(qtyTotal, layers[i].RemainingQty)
		}
	}
	if qtyTotal.Sign() == 0 {
		for i := range layers {
			layers[i].RemainingValue = 0
		}
		return
	}
	std := new(big.Rat).SetInt64(newCost)
	RescaleLayersToTotal(layers, roundHalfUp(new(big.Rat).Mul(std, qtyTotal)))
}

// StockValue is Σ remaining_value over the open layers — the number that must
// equal both the stock balance and the 2910 account balance (§1.3).
func StockValue(layers []Layer) int64 {
	var total int64
	for i := range layers {
		if layers[i].RemainingQty.Sign() > 0 || layers[i].RemainingValue != 0 {
			total += layers[i].RemainingValue
		}
	}
	return total
}

// StockQty is Σ remaining_qty over the open layers.
func StockQty(layers []Layer) *big.Rat {
	total := new(big.Rat)
	for i := range layers {
		total.Add(total, layers[i].RemainingQty)
	}
	return total
}

// ReturnValue prices a customer return at the ORIGINAL issue cost, in
// proportion to the quantity coming back — not at today's price (§3.1, §3.2).
//
// Returning goods at the current cost is how a business books a profit or a
// loss purely by selling and un-selling the same item across a price change.
func ReturnValue(returnQty, originalQty *big.Rat, originalCost int64) (int64, error) {
	if returnQty.Sign() <= 0 || originalQty.Sign() <= 0 {
		return 0, ErrNonPositiveQty
	}
	if returnQty.Cmp(originalQty) > 0 {
		return 0, errors.New("return quantity exceeds the quantity originally issued")
	}
	if returnQty.Cmp(originalQty) == 0 {
		return originalCost, nil
	}
	return proportion(returnQty, originalQty, originalCost), nil
}

func orZero(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return r
}

// Rat is a small helper for callers and tests: Rat(3, 1) is three units,
// Rat(1, 3) is a third.
func Rat(num, den int64) *big.Rat { return big.NewRat(num, den) }
