"""
Modullararo GL integratsiya testlari.

Savdo fakturasi, xarid fakturasi va xarajatlar GL ga to'g'ri,
balanslangan provodkalar yozishini API + DB darajasida tekshiradi.
Yakunda butun ledger bo'yicha global invariantlar tekshiriladi.
"""
import uuid

import pytest

from conftest import today


# ============================================
# HELPERS
# ============================================

def get_je(db_read, je_id):
    db_read.execute(
        "SELECT * FROM journal_entries WHERE id = %s AND deleted_at IS NULL",
        (je_id,),
    )
    return db_read.fetchone()


def get_je_lines(db_read, je_id):
    db_read.execute(
        """SELECT jel.*, a.code AS account_code, a.is_leaf
           FROM journal_entry_lines jel
           JOIN accounts a ON a.id = jel.account_id
           WHERE jel.journal_entry_id = %s
           ORDER BY jel.line_number""",
        (je_id,),
    )
    return db_read.fetchall()


def assert_je_balanced_and_leaf(db_read, je_id, expect_total=None):
    """JE mavjud, Dt=Kt, kamida 2 qator, faqat leaf schyotlar."""
    je = get_je(db_read, je_id)
    assert je is not None, f"Journal entry {je_id} topilmadi"
    lines = get_je_lines(db_read, je_id)
    assert len(lines) >= 2, f"JE {je['entry_number']}: {len(lines)} ta qator (kamida 2 kutilgan)"
    total_debit = sum(float(l["debit_amount"] or 0) for l in lines)
    total_credit = sum(float(l["credit_amount"] or 0) for l in lines)
    assert abs(total_debit - total_credit) < 0.01, (
        f"JE {je['entry_number']} balanslanmagan: Dt={total_debit} Kt={total_credit}"
    )
    # Sarlavha (header) summalari qatorlar bilan mos bo'lishi kerak
    assert abs(float(je["total_debit"] or 0) - total_debit) < 0.01, (
        f"JE {je['entry_number']}: header total_debit={je['total_debit']} != qatorlar {total_debit}"
    )
    non_leaf = [l["account_code"] for l in lines if not l["is_leaf"]]
    assert not non_leaf, f"JE {je['entry_number']}: guruh schyotlariga provodka: {non_leaf}"
    if expect_total is not None:
        assert abs(total_debit - expect_total) < 0.01, (
            f"JE {je['entry_number']}: summa {total_debit}, kutilgan {expect_total}"
        )
    return je, lines


def api_data(resp):
    body = resp.json()
    return body.get("data", body)


# ============================================
# SAVDO FAKTURA -> GL
# ============================================

class TestSalesInvoiceGL:
    AMOUNT = 1_000_000.0

    @pytest.fixture(scope="class")
    def invoice(self, api_client, test_customer):
        resp = api_client.post("/sales-invoices", json={
            "customer_id": str(test_customer["id"]),
            "invoice_date": today(),
            "due_date": today(),
            "lines": [
                {"description": "Test mahsulot X-MOD", "quantity": 2, "unit_price": 500_000},
            ],
        })
        if resp.status_code == 403:
            pytest.skip("sales invoice create: permission yo'q")
        assert resp.status_code in (200, 201), f"Create failed: {resp.status_code} {resp.text[:300]}"
        return api_data(resp)

    def test_draft_has_no_journal_entry(self, db_read, invoice):
        db_read.execute(
            "SELECT journal_entry_id, status FROM sales_invoices WHERE id = %s",
            (invoice["id"],),
        )
        row = db_read.fetchone()
        assert row is not None
        assert row["journal_entry_id"] is None, "Draft fakturada JE bo'lmasligi kerak"

    def test_send_creates_balanced_revenue_je(self, api_client, db_read, invoice):
        resp = api_client.post(f"/sales-invoices/{invoice['id']}/send")
        assert resp.status_code in (200, 201), f"Send failed: {resp.status_code} {resp.text[:300]}"

        db_read.execute(
            "SELECT journal_entry_id, status, total_amount FROM sales_invoices WHERE id = %s",
            (invoice["id"],),
        )
        row = db_read.fetchone()
        assert row["journal_entry_id"] is not None, (
            "Faktura yuborildi, lekin GL provodka (journal_entry_id) yaratilmadi"
        )
        je, lines = assert_je_balanced_and_leaf(db_read, row["journal_entry_id"])
        assert je["status"] == "posted", f"Revenue JE statusi: {je['status']}"
        # Dt qatorlar orasida debitorlik (4xxx), Kt orasida daromad (9xxx) bo'lishi kerak
        debit_codes = {l["account_code"] for l in lines if float(l["debit_amount"] or 0) > 0}
        credit_codes = {l["account_code"] for l in lines if float(l["credit_amount"] or 0) > 0}
        assert any(c.startswith("4") for c in debit_codes), (
            f"AR (4xxx) debit topilmadi, debit: {debit_codes}"
        )
        assert any(c.startswith("9") for c in credit_codes), (
            f"Daromad (9xxx) kredit topilmadi, kredit: {credit_codes}"
        )

    def test_record_payment_creates_je_and_updates_paid(self, api_client, db_read, invoice):
        resp = api_client.post(
            f"/sales-invoices/{invoice['id']}/record-payment",
            json={"amount": self.AMOUNT, "payment_date": today(), "payment_method": "bank"},
        )
        assert resp.status_code in (200, 201), f"record-payment failed: {resp.status_code} {resp.text[:300]}"
        db_read.execute(
            "SELECT amount_paid, status FROM sales_invoices WHERE id = %s",
            (invoice["id"],),
        )
        row = db_read.fetchone()
        assert abs(float(row["amount_paid"]) - self.AMOUNT) < 0.01, (
            f"amount_paid={row['amount_paid']}, kutilgan {self.AMOUNT}"
        )
        # To'lov JE si: source bo'yicha qidiramiz
        db_read.execute(
            """SELECT id FROM journal_entries
               WHERE source_id = %s AND deleted_at IS NULL AND source_type ILIKE '%%payment%%'
               ORDER BY created_at DESC LIMIT 1""",
            (invoice["id"],),
        )
        je_row = db_read.fetchone()
        if je_row is None:
            # ba'zi implementatsiyalar payments jadvali orqali bog'laydi
            db_read.execute(
                """SELECT je.id FROM journal_entries je
                   JOIN payments p ON p.journal_entry_id = je.id
                   WHERE p.invoice_id = %s AND je.deleted_at IS NULL
                   ORDER BY je.created_at DESC LIMIT 1""",
                (invoice["id"],),
            )
            je_row = db_read.fetchone()
        assert je_row is not None, "To'lov uchun GL provodka topilmadi"
        assert_je_balanced_and_leaf(db_read, je_row["id"])


# ============================================
# XARID FAKTURA -> GL
# ============================================

class TestPurchaseInvoiceGL:
    SUBTOTAL = 2_000_000.0
    TAX = 240_000.0  # 12% QQS
    TOTAL = 2_240_000.0

    @pytest.fixture(scope="class")
    def invoice(self, api_client, test_supplier):
        resp = api_client.post("/purchase-invoices", json={
            "vendor_id": str(test_supplier["id"]),
            "vendor_invoice_number": f"VI-XMOD-{uuid.uuid4().hex[:6].upper()}",
            "invoice_date": today(),
            "due_date": today(),
            "subtotal": self.SUBTOTAL,
            "tax_amount": self.TAX,
            "total_amount": self.TOTAL,
        })
        if resp.status_code == 403:
            pytest.skip("purchase invoice create: permission yo'q")
        assert resp.status_code in (200, 201), f"Create failed: {resp.status_code} {resp.text[:300]}"
        return api_data(resp)

    def test_post_creates_balanced_ap_je(self, api_client, db_read, invoice):
        # ba'zi oqimlarda avval confirm kerak
        api_client.post(f"/purchase-invoices/{invoice['id']}/confirm")
        resp = api_client.post(f"/purchase-invoices/{invoice['id']}/post")
        assert resp.status_code in (200, 201), f"Post failed: {resp.status_code} {resp.text[:300]}"

        db_read.execute(
            "SELECT journal_entry_id, status FROM purchase_invoices WHERE id = %s",
            (invoice["id"],),
        )
        row = db_read.fetchone()
        assert row["journal_entry_id"] is not None, (
            "Xarid fakturasi post qilindi, lekin GL provodka yaratilmadi"
        )
        je, lines = assert_je_balanced_and_leaf(
            db_read, row["journal_entry_id"], expect_total=self.TOTAL
        )
        credit_codes = {l["account_code"] for l in lines if float(l["credit_amount"] or 0) > 0}
        assert any(c.startswith("6") for c in credit_codes), (
            f"Kreditorlik (6xxx) kredit topilmadi, kredit: {credit_codes}"
        )

    def test_pay_updates_amount_paid_with_je(self, api_client, db_read, invoice):
        resp = api_client.post(
            f"/purchase-invoices/{invoice['id']}/pay",
            json={"amount": self.TOTAL, "payment_method": "bank"},
        )
        assert resp.status_code in (200, 201), f"Pay failed: {resp.status_code} {resp.text[:300]}"
        db_read.execute(
            "SELECT amount_paid, status FROM purchase_invoices WHERE id = %s",
            (invoice["id"],),
        )
        row = db_read.fetchone()
        assert abs(float(row["amount_paid"]) - self.TOTAL) < 0.01
        db_read.execute(
            """SELECT id FROM journal_entries
               WHERE source_id = %s AND deleted_at IS NULL
                 AND source_type ILIKE '%%payment%%'
               ORDER BY created_at DESC LIMIT 1""",
            (invoice["id"],),
        )
        je_row = db_read.fetchone()
        if je_row is not None:
            assert_je_balanced_and_leaf(db_read, je_row["id"])


# ============================================
# XARAJAT -> GL (raqamlash regressiyasi bilan)
# ============================================

class TestExpenseGL:
    def _create_and_approve(self, api_client, amount, tag):
        resp = api_client.post("/expenses", json={
            "date": today(),
            "description": f"Test xarajat {tag}",
            "amount": amount,
            "reimbursable": False,
        })
        if resp.status_code == 403:
            pytest.skip("expense create: permission yo'q")
        assert resp.status_code in (200, 201), f"Expense create failed: {resp.status_code} {resp.text[:300]}"
        exp = api_data(resp)
        resp = api_client.post(f"/expenses/{exp['id']}/approve")
        assert resp.status_code in (200, 201), f"Approve failed: {resp.status_code} {resp.text[:300]}"
        return exp

    def test_two_sequential_approvals_both_get_je(self, api_client, db_read):
        """Ketma-ket 2 ta xarajat tasdiqlansa, ikkalasi ham JE olishi shart.

        Regressiya: ApproveExpense JE raqamini journals.next_number dan
        oladi va insert xatosini yutib yuboradi — dublikat raqam bo'lsa
        ikkinchi xarajat JE siz qoladi.
        """
        exp_a = self._create_and_approve(api_client, 150_000, f"A-{uuid.uuid4().hex[:4]}")
        exp_b = self._create_and_approve(api_client, 250_000, f"B-{uuid.uuid4().hex[:4]}")

        missing = []
        for exp, amount in ((exp_a, 150_000.0), (exp_b, 250_000.0)):
            db_read.execute(
                """SELECT id FROM journal_entries
                   WHERE source_type = 'expense' AND source_id = %s AND deleted_at IS NULL""",
                (exp["id"],),
            )
            row = db_read.fetchone()
            if row is None:
                missing.append(exp["id"])
            else:
                assert_je_balanced_and_leaf(db_read, row["id"], expect_total=amount)
        assert not missing, (
            f"Tasdiqlangan xarajat(lar) GL provodkasiz qoldi: {missing} — "
            "JE raqami to'qnashuvi xatosi yutilgan bo'lishi ehtimol"
        )


# ============================================
# GLOBAL LEDGER INVARIANTLARI
# ============================================

class TestLedgerInvariants:
    """Yuqoridagi oqimlardan keyin butun ledger sog'lomligini tekshirish."""

    def test_all_posted_entries_balanced(self, db_read, tenant_id):
        db_read.execute(
            """SELECT je.id, je.entry_number,
                      SUM(jel.debit_amount) AS dt, SUM(jel.credit_amount) AS kt
               FROM journal_entries je
               JOIN journal_entry_lines jel ON jel.journal_entry_id = je.id
               WHERE je.tenant_id = %s AND je.status = 'posted' AND je.deleted_at IS NULL
               GROUP BY je.id, je.entry_number
               HAVING ABS(SUM(jel.debit_amount) - SUM(jel.credit_amount)) > 0.01
               LIMIT 20""",
            (tenant_id,),
        )
        bad = db_read.fetchall()
        assert not bad, f"Balanslanmagan posted JE lar: {[(b['entry_number'], float(b['dt']), float(b['kt'])) for b in bad]}"

    def test_no_duplicate_entry_numbers_per_org(self, db_read, tenant_id):
        db_read.execute(
            """SELECT organization_id, entry_number, COUNT(*) AS n
               FROM journal_entries
               WHERE tenant_id = %s AND deleted_at IS NULL
               GROUP BY organization_id, entry_number
               HAVING COUNT(*) > 1
               LIMIT 20""",
            (tenant_id,),
        )
        dups = db_read.fetchall()
        assert not dups, f"Dublikat entry_number lar: {[(d['entry_number'], d['n']) for d in dups]}"

    def test_no_postings_to_group_accounts(self, db_read, tenant_id):
        db_read.execute(
            """SELECT DISTINCT a.code, a.name
               FROM journal_entry_lines jel
               JOIN accounts a ON a.id = jel.account_id
               JOIN journal_entries je ON je.id = jel.journal_entry_id
               WHERE je.tenant_id = %s AND je.deleted_at IS NULL AND a.is_leaf = false
               LIMIT 20""",
            (tenant_id,),
        )
        bad = db_read.fetchall()
        assert not bad, f"Guruh (sarlavha) schyotlariga provodkalar: {[b['code'] for b in bad]}"

    def test_no_posted_entries_without_lines(self, db_read, tenant_id):
        db_read.execute(
            """SELECT je.id, je.entry_number
               FROM journal_entries je
               LEFT JOIN journal_entry_lines jel ON jel.journal_entry_id = je.id
               WHERE je.tenant_id = %s AND je.status = 'posted' AND je.deleted_at IS NULL
               GROUP BY je.id, je.entry_number
               HAVING COUNT(jel.id) < 2
               LIMIT 20""",
            (tenant_id,),
        )
        bad = db_read.fetchall()
        assert not bad, f"Qatorlari 2 tadan kam posted JE lar: {[b['entry_number'] for b in bad]}"

    def test_no_orphan_sent_invoices(self, db_read, tenant_id):
        """Yuborilgan/qisman to'langan savdo fakturalari JE siz qolmasligi kerak."""
        db_read.execute(
            """SELECT invoice_number, status FROM sales_invoices
               WHERE tenant_id = %s AND deleted_at IS NULL
                 AND status IN ('sent', 'partially_paid', 'paid')
                 AND journal_entry_id IS NULL
               LIMIT 20""",
            (tenant_id,),
        )
        orphans = db_read.fetchall()
        assert not orphans, (
            f"GL provodkasiz {len(orphans)} ta yuborilgan/to'langan faktura: "
            f"{[o['invoice_number'] for o in orphans]}"
        )

    def test_header_totals_match_lines(self, db_read, tenant_id):
        db_read.execute(
            """SELECT je.entry_number, je.total_debit, SUM(jel.debit_amount) AS line_dt
               FROM journal_entries je
               JOIN journal_entry_lines jel ON jel.journal_entry_id = je.id
               WHERE je.tenant_id = %s AND je.deleted_at IS NULL
               GROUP BY je.id, je.entry_number, je.total_debit
               HAVING ABS(COALESCE(je.total_debit, 0) - SUM(jel.debit_amount)) > 0.01
               LIMIT 20""",
            (tenant_id,),
        )
        bad = db_read.fetchall()
        assert not bad, (
            f"Header/qator summalari mos emas: "
            f"{[(b['entry_number'], float(b['total_debit']), float(b['line_dt'])) for b in bad]}"
        )
