#!/usr/bin/env python3
"""
Ishlab chiqarish demo seed — realistic Uzbek manufacturing data for the
demo tenant, API-only (no direct DB writes for business data).

Creates in the admin user's default organization:
  - 3 work centers (DEMO- codes, Uzbek names, sensible hourly costs)
  - 4 finished products + 12 components (Uzbek names), components stocked
    generously via POST /inventory/adjust (one deliberately short so the
    MRP-lite shortage list has something to show)
  - 1 BOM per product (2-4 component lines + 1-2 operations)
  - 10 production orders across the lifecycle: 2 draft, 2 confirmed (one
    overdue), 2 in_progress (one late), 3 completed (one clean, one with a
    shortfall, one with scrap for the Pareto), 1 cancelled after start

Idempotent: if any DEMO- work center already exists for the tenant, the
script prints a notice and exits without creating anything.

Run:  python3 seed_manufacturing_demo.py
(needs requests + psycopg2-binary; psycopg2 is used for READ-ONLY lookups
only — org id and the idempotency check.)
"""

import os
import sys
from datetime import date, timedelta

import psycopg2
import requests
from psycopg2.extras import RealDictCursor

BASE_URL = os.getenv("GENIX_API_URL", "http://localhost:8080/api/v1")
DB_HOST = os.getenv("DB_HOST", "localhost")
DB_PORT = os.getenv("DB_PORT", "5432")
DB_USER = os.getenv("DB_USER", "genix")
DB_PASSWORD = os.getenv("DB_PASSWORD", "genix_secret")
DB_NAME = os.getenv("DB_NAME", "genixerp")

ADMIN_EMAIL = os.getenv("GENIX_ADMIN_EMAIL", "admin@genixerp.com")
ADMIN_PASSWORD = os.getenv("GENIX_ADMIN_PASSWORD", "admin123")


# ============================================
# PLUMBING
# ============================================

class Api:
    def __init__(self):
        resp = requests.post(f"{BASE_URL}/auth/login",
                             json={"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
        if resp.status_code != 200:
            sys.exit(f"login failed: {resp.status_code} {resp.text[:300]}")
        body = resp.json().get("data", resp.json())
        self.token = body.get("access_token") or body.get("token")
        self.tenant_id = (body.get("tenant") or {}).get("id") or body.get("tenant_id")

        # READ-ONLY DB lookup: default organization for the tenant
        conn = psycopg2.connect(host=DB_HOST, port=DB_PORT, user=DB_USER,
                                password=DB_PASSWORD, dbname=DB_NAME)
        conn.autocommit = True
        self.db = conn.cursor(cursor_factory=RealDictCursor)
        self.db.execute(
            "SELECT id FROM organizations WHERE tenant_id = %s ORDER BY created_at LIMIT 1",
            (self.tenant_id,))
        row = self.db.fetchone()
        if not row:
            sys.exit("no organization found for tenant")
        self.org_id = str(row["id"])

        self.s = requests.Session()
        self.s.headers.update({
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self.token}",
            "X-Tenant-ID": str(self.tenant_id),
            "X-Organization-ID": self.org_id,
        })

    def post(self, path, payload, desc):
        resp = self.s.post(f"{BASE_URL}{path}", json=payload)
        if resp.status_code not in (200, 201):
            sys.exit(f"FAILED {desc}: POST {path} -> {resp.status_code} {resp.text[:400]}")
        body = resp.json()
        return body.get("data", body)

    def get(self, path, params=None):
        return self.s.get(f"{BASE_URL}{path}", params=params)


def d(offset_days):
    return (date.today() + timedelta(days=offset_days)).isoformat()


# ============================================
# SEED DATA DEFINITIONS
# ============================================

WORK_CENTERS = [
    {"code": "DEMO-YIG1", "name": "Yig'ish liniyasi 1",
     "description": "Mebel va eshik yig'ish liniyasi",
     "capacity_per_hour": 5, "hourly_cost": 80_000, "working_hours_per_day": 8,
     "currency": "UZS", "status": "active"},
    {"code": "DEMO-CNC1", "name": "CNC stanok markazi",
     "description": "Yog'och va plitalarni aniq kesish",
     "capacity_per_hour": 8, "hourly_cost": 150_000, "working_hours_per_day": 8,
     "currency": "UZS", "status": "active"},
    {"code": "DEMO-QAD1", "name": "Qadoqlash uchastkasi",
     "description": "Tayyor mahsulotni qadoqlash",
     "capacity_per_hour": 12, "hourly_cost": 40_000, "working_hours_per_day": 8,
     "currency": "UZS", "status": "active"},
]

# component code -> (name, unit cost so'm, stock qty)
COMPONENTS = {
    "DEMO-TAXTA-40":   ("Yog'och taxta 40mm quritilgan", 85_000, 500),
    "DEMO-OYNA-ESH":   ("Eshik oynasi matlangan", 120_000, 300),
    "DEMO-OSHIQ":      ("Metall oshiq-moshiq to'plami", 25_000, 800),
    "DEMO-DSP-XOM":    ("DSP plita xom 2750x1830", 95_000, 600),
    "DEMO-LAMINAT":    ("Laminat plyonka dekorativ", 18_000, 1_000),
    "DEMO-OYOQ-MET":   ("Stol oyoqlari metall xrom", 45_000, 400),
    "DEMO-STOLUSTKI":  ("Stolustki plitasi 1400x700", 160_000, 200),
    "DEMO-MURVAT-M8":  ("Murvat to'plami M8", 800, 2_000),
    "DEMO-YONPANEL":   ("Yon panel 2000mm oq", 110_000, 300),
    "DEMO-ESHPANEL":   ("Eshik paneli shkaf uchun", 95_000, 300),
    "DEMO-RELS":       ("Ilgich relsi alyumin", 35_000, 250),
    "DEMO-FURNITURA":  ("Furnitura to'plami shkaf", 60_000, 25),   # ATAYLAB KAM — shortage demo
}

# fg code -> (name, list price, bom lines [(component, qty)], operations)
FINISHED = {
    "DEMO-ESHIK-P": ("Yog'och eshik Premium", 1_400_000,
                     [("DEMO-TAXTA-40", 2), ("DEMO-OYNA-ESH", 1), ("DEMO-OSHIQ", 3)],
                     [("Kesish va profillash", "DEMO-CNC1", 20, 10),
                      ("Yig'ish va o'rnatish", "DEMO-YIG1", 15, 12)]),
    "DEMO-PANEL-18": ("Mebel paneli 18mm", 210_000,
                      [("DEMO-DSP-XOM", 1.2), ("DEMO-LAMINAT", 2)],
                      [("Laminatlash", "DEMO-CNC1", 15, 4)]),
    "DEMO-STOL-OF": ("Ofis stoli Standart", 950_000,
                     [("DEMO-OYOQ-MET", 4), ("DEMO-STOLUSTKI", 1), ("DEMO-MURVAT-M8", 12)],
                     [("Yig'ish", "DEMO-YIG1", 20, 8),
                      ("Qadoqlash", "DEMO-QAD1", 5, 3)]),
    "DEMO-SHKAF-K": ("Kiyim shkafi Klassik", 2_600_000,
                     [("DEMO-YONPANEL", 2), ("DEMO-ESHPANEL", 2),
                      ("DEMO-RELS", 1), ("DEMO-FURNITURA", 1)],
                     [("Panel tayyorlash", "DEMO-CNC1", 25, 9),
                      ("Yig'ish va sozlash", "DEMO-YIG1", 20, 15)]),
}

# (fg code, qty, sched_start offset, sched_end offset, target status, complete kwargs)
ORDERS = [
    ("DEMO-ESHIK-P", 25, +2, +12, "draft", None),
    ("DEMO-PANEL-18", 120, +7, +17, "draft", None),
    ("DEMO-STOL-OF", 40, +1, +9, "confirmed", None),
    ("DEMO-SHKAF-K", 40, -2, -1, "confirmed", None),          # overdue + shortage demo
    ("DEMO-ESHIK-P", 30, -2, -1, "in_progress", None),        # late
    ("DEMO-PANEL-18", 150, -1, +7, "in_progress", None),
    ("DEMO-STOL-OF", 40, -2, 0, "completed",
     {"quantity_produced": 40}),                              # clean
    ("DEMO-ESHIK-P", 50, -2, 0, "completed",
     {"good_quantity": 46.0, "shortfall_reason": "Xom ashyo nuqsoni tufayli 4 dona kam"}),
    ("DEMO-PANEL-18", 60, -2, 0, "completed",
     {"quantity_produced": 55, "quantity_scrapped": 5}),      # brak / Pareto
    ("DEMO-SHKAF-K", 10, -1, +5, "cancelled", None),
]


# ============================================
# SEED
# ============================================

def main():
    api = Api()

    # Idempotency: DEMO- work centers already there -> nothing to do
    api.db.execute(
        """SELECT COUNT(*) AS n FROM work_centers
           WHERE tenant_id = %s AND code LIKE 'DEMO-%%' AND deleted_at IS NULL""",
        (api.tenant_id,))
    if int(api.db.fetchone()["n"]) > 0:
        print("DEMO- work centers already exist — seed skipped (idempotent).")
        return

    # Warehouse: default active warehouse of the organization
    api.db.execute(
        """SELECT id, name FROM warehouses
           WHERE tenant_id = %s AND organization_id = %s
             AND deleted_at IS NULL AND is_active = true
           ORDER BY created_at LIMIT 1""",
        (api.tenant_id, api.org_id))
    wh = api.db.fetchone()
    if not wh:
        sys.exit("no active warehouse in the default organization")
    wh_id, wh_name = str(wh["id"]), wh["name"]

    # 1. Work centers
    wc_ids = {}
    for wc in WORK_CENTERS:
        created = api.post("/work-centers", wc, f"work center {wc['code']}")
        wc_ids[wc["code"]] = created["id"]

    # 2. Components (+ generous stock, except the deliberate shortage)
    comp_ids = {}
    for code, (name, cost, qty) in COMPONENTS.items():
        pid = api.post("/products", {
            "name": name, "code": code, "sku": code, "type": "product",
            "is_stockable": True, "track_inventory": True,
            "cost_price": cost, "list_price": round(cost * 1.3), "is_active": True,
        }, f"component {code}")["id"]
        comp_ids[code] = pid
        api.post("/inventory/adjust", {
            "product_id": pid, "warehouse_id": wh_id,
            "quantity": qty, "unit_cost": cost, "reason": "Demo boshlang'ich zaxira",
        }, f"stock {code}")

    # 3. Finished products + BOMs (+ operations)
    fg_ids, bom_ids = {}, {}
    for code, (name, list_price, lines, ops) in FINISHED.items():
        pid = api.post("/products", {
            "name": name, "code": code, "sku": code, "type": "product",
            "is_stockable": True, "track_inventory": True,
            "cost_price": 0, "list_price": list_price, "is_active": True,
        }, f"product {code}")["id"]
        fg_ids[code] = pid
        bom_id = api.post("/boms", {
            "code": f"DEMO-BOM-{code[5:]}", "name": f"BOM — {name}",
            "product_id": pid, "quantity": 1, "warehouse_id": wh_id,
            "lines": [
                {"component_id": comp_ids[c], "quantity": q, "unit_of_measure": "pcs"}
                for c, q in lines
            ],
        }, f"BOM for {code}")["id"]
        bom_ids[code] = bom_id
        for seq, (op_name, wc_code, setup_min, run_min) in enumerate(ops, start=1):
            api.post(f"/boms/{bom_id}/operations", {
                "sequence": seq * 10, "operation_name": op_name,
                "work_center_id": wc_ids[wc_code],
                "setup_time_minutes": setup_min, "run_time_minutes": run_min,
            }, f"operation {op_name}")

    # 4. Production orders across the lifecycle
    summary = []
    for fg_code, qty, s_off, e_off, target, complete_kwargs in ORDERS:
        fg_name = FINISHED[fg_code][0]
        mo = api.post("/production-orders", {
            "name": f"{fg_name} — partiya {qty} dona",
            "product_id": fg_ids[fg_code],
            "bom_id": bom_ids[fg_code],
            "quantity_planned": qty, "uom": "pcs",
            "warehouse_id": wh_id,
            "scheduled_start": d(s_off), "scheduled_end": d(e_off),
            "notes": "Demo buyurtma (seed_manufacturing_demo.py)",
        }, f"MO {fg_code} x{qty}")
        mo_id = mo["id"]

        if target in ("confirmed", "in_progress", "completed", "cancelled"):
            api.post(f"/production-orders/{mo_id}/confirm", None, f"confirm {mo['code']}")
        if target in ("in_progress", "completed", "cancelled"):
            api.post(f"/production-orders/{mo_id}/start", None, f"start {mo['code']}")
        if target == "completed":
            api.post(f"/production-orders/{mo_id}/complete", complete_kwargs or {},
                     f"complete {mo['code']}")
        if target == "cancelled":
            api.post(f"/production-orders/{mo_id}/cancel",
                     {"reason": "Buyurtmachi rad etdi (demo)"}, f"cancel {mo['code']}")

        summary.append((mo["code"], fg_name, qty, target, d(s_off), d(e_off)))

    # 5. Summary
    print(f"\nSeed muvaffaqiyatli — ombor: {wh_name}\n")
    print("Ish markazlari:")
    for wc in WORK_CENTERS:
        print(f"  {wc['code']:<12} {wc['name']:<24} {wc['hourly_cost']:>9,} so'm/soat")
    print("\nKomponentlar (zaxira):")
    for code, (name, cost, qty) in COMPONENTS.items():
        flag = "  <-- ATAYLAB KAM (shortage demo)" if code == "DEMO-FURNITURA" else ""
        print(f"  {code:<16} {name:<36} {qty:>6} dona @ {cost:>8,}{flag}")
    print("\nTayyor mahsulotlar / BOM:")
    for code, (name, _, lines, ops) in FINISHED.items():
        print(f"  {code:<14} {name:<26} {len(lines)} komponent, {len(ops)} operatsiya")
    print("\nIshlab chiqarish buyurtmalari:")
    print(f"  {'Kod':<9} {'Mahsulot':<26} {'Reja':>5} {'Holat':<12} {'Boshlanish':<12} {'Tugash':<12}")
    for code, name, qty, status, s, e in summary:
        print(f"  {code:<9} {name:<26} {qty:>5} {status:<12} {s:<12} {e:<12}")


if __name__ == "__main__":
    main()
