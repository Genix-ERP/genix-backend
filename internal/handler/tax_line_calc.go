package handler

// One place for "what VAT does this document line carry".
//
// Three document paths (sales order, sales invoice, purchase invoice) each
// carried a private copy of the tax_rates lookup, and all three had the same
// two defects (mobile parity audit 2026-08-15, P4/P5):
//
//   - price_include was never read. tax_rates.price_include (migration 130)
//     marks a rate whose amount is already INSIDE the line price — the web
//     client honoured it, the server always computed on top, so on an
//     inclusive-tax tenant the header the web sent and the lines the server
//     stored disagreed, and mobile (which sends no header) was simply wrong.
//   - the Scan error was discarded (`_ = ... .Scan(&rate)`), so a tax_id that
//     did not exist, belonged to another tenant, or was soft-deleted resolved
//     to 0% and the document saved WITHOUT tax and without a word to the user.
//
// resolveTaxRate is the single lookup; lineTaxFor is the single formula.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// taxRateInfo is what a document line needs to know about its tax_id.
type taxRateInfo struct {
	Rate         float64 // percent, e.g. 12
	PriceInclude bool    // true → the rate is inside the price
}

// errTaxRateNotFound is returned for a tax_id that resolves to no usable
// row for this tenant. Callers turn it into a 400 naming the id.
var errTaxRateNotFound = errors.New("tax rate not found")

// taxRateResolver caches lookups for the lifetime of one request, so a
// document with many lines on the same rate makes one query per distinct id.
type taxRateResolver struct {
	q        dbQuerier
	tenantID uuid.UUID
	cache    map[string]taxRateInfo
}

func newTaxRateResolver(q dbQuerier, tenantID uuid.UUID) *taxRateResolver {
	return &taxRateResolver{q: q, tenantID: tenantID, cache: map[string]taxRateInfo{}}
}

// resolve returns the rate for a tax_id string. An unparsable id, a missing
// row, another tenant's row or a soft-deleted row all yield
// errTaxRateNotFound; the DB error case is passed through so a real outage is
// not mistaken for a bad id.
func (r *taxRateResolver) resolve(taxID string) (taxRateInfo, error) {
	if info, ok := r.cache[taxID]; ok {
		return info, nil
	}
	tid, err := uuid.Parse(taxID)
	if err != nil {
		return taxRateInfo{}, fmt.Errorf("%w: %s", errTaxRateNotFound, taxID)
	}
	var info taxRateInfo
	err = r.q.QueryRow(`
		SELECT rate, COALESCE(price_include, false)
		FROM tax_rates
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		tid, r.tenantID).Scan(&info.Rate, &info.PriceInclude)
	if err == sql.ErrNoRows {
		return taxRateInfo{}, fmt.Errorf("%w: %s", errTaxRateNotFound, taxID)
	}
	if err != nil {
		return taxRateInfo{}, err
	}
	r.cache[taxID] = info
	return info, nil
}

// lineTaxFor splits a line's net amount into (base, tax) under the rate.
//
//	exclusive:  base = net,               tax = net × r / 100
//	inclusive:  tax  = net × r / (100+r), base = net − tax
//
// `net` is the line amount after line discount, i.e. what the customer is
// charged for the line before/including tax depending on the flag. Callers
// store `base` as the line's taxable amount and add `tax` to the header.
func lineTaxFor(net float64, info taxRateInfo) (base, tax float64) {
	if info.Rate <= 0 || net == 0 {
		return net, 0
	}
	if info.PriceInclude {
		tax = net * info.Rate / (100.0 + info.Rate)
		return net - tax, tax
	}
	return net, net * info.Rate / 100.0
}

// salesOrderLineCalc is the resolved money picture of one order line.
type salesOrderLineCalc struct {
	Discount float64 // line-level discount amount
	Base     float64 // taxable/net line amount (after discount, ex-VAT)
	Tax      float64 // VAT on the line
}

// computeSalesOrderLines resolves discount and VAT for a set of order lines
// (create and update share this so they cannot drift). Line discount is
// applied first; then, when the line has a tax_id, the net is split by
// lineTaxFor. Returns errTaxRateNotFound (wrapped) for a bad tax_id.
func (h *Handler) computeSalesOrderLines(tenantID uuid.UUID, lines []entity.CreateSalesOrderLineInput) ([]salesOrderLineCalc, error) {
	out := make([]salesOrderLineCalc, len(lines))
	rates := newTaxRateResolver(h.db, tenantID)
	for i, line := range lines {
		gross := line.Quantity * line.UnitPrice
		var disc float64
		if line.DiscountType == "percentage" && line.DiscountValue > 0 {
			disc = gross * line.DiscountValue / 100
		} else if line.DiscountType == "fixed" && line.DiscountValue > 0 {
			disc = line.DiscountValue
		}
		net := gross - disc
		out[i] = salesOrderLineCalc{Discount: disc, Base: net}
		if line.TaxID == "" {
			continue
		}
		info, err := rates.resolve(line.TaxID)
		if err != nil {
			return nil, err
		}
		base, tax := lineTaxFor(net, info)
		out[i].Base, out[i].Tax = base, tax
	}
	return out, nil
}

// invoiceLineCalc is the resolved money picture of one invoice line
// (sales or purchase — both use qty × price − discount_amount as net).
type invoiceLineCalc struct {
	Base float64 // taxable line amount (net after discount, ex-VAT)
	Tax  float64
}

// computeInvoiceLines resolves VAT for invoice-style lines: net is
// qty × unit_price − discount_amount, then split by the line's tax_id
// honouring price_include. Shared by CreateSalesInvoice, UpdateSalesInvoice
// and CreatePurchaseInvoice.
func (h *Handler) computeInvoiceLines(tenantID uuid.UUID, lines []invoiceLineIn) ([]invoiceLineCalc, error) {
	out := make([]invoiceLineCalc, len(lines))
	rates := newTaxRateResolver(h.db, tenantID)
	for i, l := range lines {
		net := l.Quantity*l.UnitPrice - l.DiscountAmount
		out[i] = invoiceLineCalc{Base: net}
		if l.TaxID == "" {
			continue
		}
		info, err := rates.resolve(l.TaxID)
		if err != nil {
			return nil, err
		}
		out[i].Base, out[i].Tax = lineTaxFor(net, info)
	}
	return out, nil
}

// invoiceLineIn is the minimal shape computeInvoiceLines needs; both the sales
// and purchase invoice line inputs project onto it.
type invoiceLineIn struct {
	Quantity       float64
	UnitPrice      float64
	DiscountAmount float64
	TaxID          string
}

// warnUnreadTaxFields logs when a document-create payload carries a tax field
// the input struct does not read (P6). Gin's ShouldBindJSON drops unknown keys
// without a trace, and that is exactly how the mobile app shipped VAT-less
// orders for months: it sent tax_percent, the struct had no such field, the
// lines had no tax_id, tax_amount came out 0 and nobody could see why. This
// costs one JSON decode of the (already-read) body and only runs when the
// body is small enough to matter for a form post.
//
// Call it AFTER ShouldBindJSON, passing the field names the struct DOES read
// so they are not reported.
func (h *Handler) warnUnreadTaxFields(c *gin.Context, doc string, readFields ...string) {
	body, ok := c.Get(gin.BodyBytesKey)
	if !ok {
		return
	}
	raw, ok := body.([]byte)
	if !ok || len(raw) == 0 || len(raw) > 1<<20 {
		return
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return
	}
	read := map[string]bool{}
	for _, f := range readFields {
		read[f] = true
	}
	var dropped []string
	for _, f := range []string{"tax_percent", "tax_rate", "tax_rate_id", "tax_amount", "vat", "vat_percent", "vat_rate"} {
		if _, present := probe[f]; present && !read[f] {
			dropped = append(dropped, f)
		}
	}
	if len(dropped) > 0 {
		h.log.Warn("document create: tax field(s) sent but not read by the input struct — VAT must come from lines[].tax_id",
			"doc", doc, "dropped", strings.Join(dropped, ","), "path", c.FullPath())
	}
}

// taxRateNotFoundMessage is the user-facing 400 text for errTaxRateNotFound.
func taxRateNotFoundMessage(err error) string {
	return "Soliq stavkasi topilmadi: " + trimTaxErr(err)
}

func trimTaxErr(err error) string {
	s := err.Error()
	const p = "tax rate not found: "
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return s
}
