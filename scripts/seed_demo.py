#!/usr/bin/env python3
"""Genix ERP demo seeder — fills one organization with realistic data across
every module, THROUGH THE API (real business flows), so stock moves, GL
postings, dashboards and charts all light up the way they would in real use.

Usage:
  GENIX_BASE=https://app.genixerp.com/api/v1 \
  GENIX_EMAIL=... GENIX_PASSWORD=... GENIX_TENANT=tenant_code \
  GENIX_ORG="EVROPLIT" python3 seed_demo.py

Every created code is prefixed DEMO- so the data is recognizable and the
script can be re-run: duplicates are skipped, not doubled.
"""
import json, os, random, sys, urllib.request, urllib.error
from datetime import date, timedelta

BASE = os.environ.get("GENIX_BASE", "http://localhost:8099/api/v1")
EMAIL = os.environ.get("GENIX_EMAIL", "demo@test.uz")
PASSWORD = os.environ.get("GENIX_PASSWORD", "Passw0rd!234")
TENANT = os.environ.get("GENIX_TENANT", "demoseed")
ORG_NAME = os.environ.get("GENIX_ORG", "")  # substring; empty = first org
REGISTER = os.environ.get("GENIX_REGISTER", "") == "1"  # local dev only

random.seed(42)
TODAY = date.today()
TOKEN = None
ORG = None
oks, fails = [], []

def api(method, path, body=None, quiet=False):
    req = urllib.request.Request(BASE + path, method=method)
    req.add_header("Content-Type", "application/json")
    if TOKEN: req.add_header("Authorization", "Bearer " + TOKEN)
    if ORG: req.add_header("X-Organization-ID", ORG)
    data = json.dumps(body).encode() if body is not None else None
    try:
        with urllib.request.urlopen(req, data) as r:
            out = json.load(r)
            return out.get("data", out), out.get("meta"), None
    except urllib.error.HTTPError as e:
        try: msg = json.loads(e.read().decode()).get("error", {}).get("message", "")
        except Exception: msg = ""
        if not quiet:
            print(f"    !! {e.code} {method} {path}: {msg[:110]}")
        return None, None, f"{e.code} {msg[:110]}"

def step(name, fn):
    print(f"== {name}")
    try:
        fn()
        oks.append(name)
    except Exception as e:
        print(f"    STEP FAILED: {e}")
        fails.append(f"{name}: {e}")

def d(days_ago):
    return (TODAY - timedelta(days=days_ago)).isoformat()

# ── auth ────────────────────────────────────────────────────────────────
def login():
    global TOKEN, ORG
    if REGISTER:
        api("POST", "/auth/register", {
            "tenant_code": TENANT, "tenant_name": "Demo Seed",
            "email": EMAIL, "password": PASSWORD,
            "first_name": "Demo", "last_name": "Admin"}, quiet=True)
    data, _, err = api("POST", "/auth/login",
                       {"email": EMAIL, "password": PASSWORD, "tenant_code": TENANT})
    if err: raise RuntimeError("login failed: " + err)
    TOKEN = data["access_token"]
    orgs, _, _ = api("GET", "/organizations")
    orgs = orgs or []
    if not orgs and REGISTER:
        api("POST", "/organizations", {"name": "Demo Org", "code": "DEMO"})
        orgs, _, _ = api("GET", "/organizations")
    pick = None
    for o in orgs or []:
        if not ORG_NAME or ORG_NAME.lower() in (o.get("company_name") or o.get("name") or "").lower():
            pick = o; break
    if not pick: raise RuntimeError(f"organization matching {ORG_NAME!r} not found")
    ORG = pick["id"]
    print(f"    org: {pick.get('company_name') or pick.get('name')} ({ORG})")

# ── reference data ──────────────────────────────────────────────────────
UNIT_ID = None
def units():
    global UNIT_ID
    data, _, _ = api("GET", "/units-of-measure")
    lst = data or []
    for u in lst:
        if (u.get("code") or "").lower() in ("dona", "pcs", "sht", "ta"):
            UNIT_ID = u["id"]; break
    if not UNIT_ID and lst: UNIT_ID = lst[0]["id"]
    if not UNIT_ID:
        u, _, _ = api("POST", "/units-of-measure", {"code": "dona", "name": "Dona"})
        UNIT_ID = (u or {}).get("id")

WH = None
def warehouse():
    global WH
    whs, _, _ = api("GET", "/warehouses")
    for w in whs or []:
        if (w.get("code") or "") == "DEMO-WH":
            WH = w["id"]; return
    w, _, _ = api("POST", "/warehouses", {
        "code": "DEMO-WH", "name": "DEMO Asosiy ombor",
        "reception_steps": 1, "delivery_steps": 1})
    if not w: raise RuntimeError("warehouse create failed")
    WH = w["id"]

CATS = {}
def categories():
    existing, _, _ = api("GET", "/product-categories")
    by_name = {(c.get("name") or ""): c["id"] for c in (existing or [])}
    for code, name in [("DEMO-KAF", "Kafel va plitka"), ("DEMO-LAM", "Laminat"),
                       ("DEMO-SAN", "Santexnika"), ("DEMO-KLY", "Qurilish kimyosi"),
                       ("DEMO-PROF", "Profil va plintus"), ("DEMO-XIZ", "Xizmatlar")]:
        if name in by_name:
            CATS[code] = by_name[name]; continue
        c, _, _ = api("POST", "/product-categories", {"code": code, "name": name})
        if c: CATS[code] = c["id"]

PRODUCTS = {}
PRODUCT_DEFS = [
    # code, name, cat, cost, price, reorder
    ("DEMO-P01", "Kafel Atlas 60x60 oq",        "DEMO-KAF", 78000, 115000, 40),
    ("DEMO-P02", "Kafel Atlas 60x60 kulrang",   "DEMO-KAF", 78000, 115000, 40),
    ("DEMO-P03", "Plitka Marazzi 30x60 bej",    "DEMO-KAF", 92000, 138000, 30),
    ("DEMO-P04", "Plitka mozaika 30x30",        "DEMO-KAF", 65000, 98000, 25),
    ("DEMO-P05", "Keramogranit 80x80 marmar",   "DEMO-KAF", 145000, 210000, 20),
    ("DEMO-P06", "Laminat Kronospan 8mm dub",   "DEMO-LAM", 88000, 129000, 35),
    ("DEMO-P07", "Laminat Egger 10mm yong'oq",  "DEMO-LAM", 112000, 165000, 25),
    ("DEMO-P08", "Vinil pol SPC 4mm",           "DEMO-LAM", 95000, 142000, 30),
    ("DEMO-P09", "Unitaz Roca monoblok",        "DEMO-SAN", 850000, 1250000, 6),
    ("DEMO-P10", "Rakovina Vitra 60sm",         "DEMO-SAN", 420000, 640000, 8),
    ("DEMO-P11", "Smesitel Grohe oshxona",      "DEMO-SAN", 380000, 570000, 10),
    ("DEMO-P12", "Dush kabina 90x90",           "DEMO-SAN", 2100000, 3100000, 3),
    ("DEMO-P13", "Plitka kleyi Knauf 25kg",     "DEMO-KLY", 32000, 46000, 80),
    ("DEMO-P14", "Fuga Mapei 2kg oq",           "DEMO-KLY", 28000, 42000, 60),
    ("DEMO-P15", "Gidroizolyatsiya 10L",        "DEMO-KLY", 145000, 205000, 15),
    ("DEMO-P16", "Gruntovka 10L",               "DEMO-KLY", 55000, 82000, 30),
    ("DEMO-P17", "Plintus MDF 2.4m oq",         "DEMO-PROF", 24000, 38000, 100),
    ("DEMO-P18", "Alyumin profil 2.7m",         "DEMO-PROF", 31000, 47000, 60),
    ("DEMO-P19", "Poroq (podlojka) 3mm rulon",  "DEMO-PROF", 8500, 14000, 120),
    ("DEMO-P20", "Burchak profili PVX",         "DEMO-PROF", 6000, 9500, 150),
]
SERVICE_DEFS = [
    ("DEMO-S01", "Plitka yotqizish xizmati m2", "DEMO-XIZ", 0, 65000),
    ("DEMO-S02", "Laminat yotqizish xizmati m2", "DEMO-XIZ", 0, 35000),
    ("DEMO-S03", "Yetkazib berish (shahar ichi)", "DEMO-XIZ", 0, 80000),
]

def products():
    existing, _, _ = api("GET", "/products?search=DEMO-P&limit=100")
    have = {(p.get("code") or ""): p["id"] for p in (existing or [])}
    existing2, _, _ = api("GET", "/products?search=DEMO-S&limit=100")
    have.update({(p.get("code") or ""): p["id"] for p in (existing2 or [])})
    for code, name, cat, cost, price, reorder in PRODUCT_DEFS:
        if code in have: PRODUCTS[code] = have[code]; continue
        p, _, _ = api("POST", "/products", {
            "code": code, "name": name, "type": "product",
            "category_id": CATS.get(cat), "unit_id": UNIT_ID,
            "cost_price": cost, "list_price": price,
            "min_stock_level": max(5, reorder // 2), "reorder_point": reorder,
            "inventory_type": "trade", "is_stockable": True,
            "track_inventory": True, "is_sellable": True, "is_purchasable": True})
        if p: PRODUCTS[code] = p["id"]
    for code, name, cat, cost, price in SERVICE_DEFS:
        if code in have: PRODUCTS[code] = have[code]; continue
        p, _, _ = api("POST", "/products", {
            "code": code, "name": name, "type": "service",
            "category_id": CATS.get(cat), "unit_id": UNIT_ID,
            "cost_price": cost, "list_price": price,
            "inventory_type": "trade", "is_stockable": False,
            "track_inventory": False, "is_sellable": True, "is_purchasable": False})
        if p: PRODUCTS[code] = p["id"]
    print(f"    products ready: {len(PRODUCTS)}")

VENDORS, CUSTOMERS = {}, {}
VENDOR_DEFS = [
    ("DEMO-V01", "Keramika Trade MChJ"), ("DEMO-V02", "EuroCeramic Import"),
    ("DEMO-V03", "Knauf Distribyutor"), ("DEMO-V04", "Santehnika Optom"),
    ("DEMO-V05", "Laminat Markazi"), ("DEMO-V06", "Profil Servis"),
]
CUSTOMER_DEFS = [
    ("DEMO-C01", "Oqtepa Qurilish MChJ"), ("DEMO-C02", "Bunyodkor Invest"),
    ("DEMO-C03", "Malika Remont Servis"), ("DEMO-C04", "Grand Building Group"),
    ("DEMO-C05", "Do'stlik Savdo Markazi"), ("DEMO-C06", "Istiqbol Injiniring"),
    ("DEMO-C07", "Navro'z Mehmonxonasi"), ("DEMO-C08", "Kamolot Biznes Markaz"),
    ("DEMO-C09", "Turon Development"), ("DEMO-C10", "Chilonzor Uy-Joy"),
    ("DEMO-C11", "Sardor aka (chakana)"), ("DEMO-C12", "Feruza opa (chakana)"),
]
def contacts():
    existing, _, _ = api("GET", "/contacts?search=DEMO-&limit=200")
    have = {(c.get("code") or ""): c["id"] for c in (existing or [])}
    for code, name in VENDOR_DEFS:
        if code in have: VENDORS[code] = have[code]; continue
        c, _, _ = api("POST", "/contacts", {"code": code, "name": name, "type": "vendor",
                                            "phone": f"+9989{random.randint(10000000, 99999999)}"})
        if c: VENDORS[code] = c["id"]
    for code, name in CUSTOMER_DEFS:
        if code in have: CUSTOMERS[code] = have[code]; continue
        c, _, _ = api("POST", "/contacts", {"code": code, "name": name, "type": "customer",
                                            "phone": f"+9989{random.randint(10000000, 99999999)}",
                                            "payment_terms": 30})
        if c: CUSTOMERS[code] = c["id"]
    print(f"    vendors {len(VENDORS)}, customers {len(CUSTOMERS)}")

# ── purchases: PO → receive → bill → pay ────────────────────────────────
def purchases():
    wh = WH
    pcodes = list(PRODUCTS.keys())
    vlist = list(VENDORS.values())
    for i in range(6):
        vendor = vlist[i % len(vlist)]
        picks = random.sample([c for c in pcodes if c.startswith("DEMO-P")], 4)
        lines = [{"product_id": PRODUCTS[c], "quantity": random.choice([50, 80, 120, 200]),
                  "unit_price": dict((x[0], x[3]) for x in PRODUCT_DEFS)[c]} for c in picks]
        po, _, err = api("POST", "/purchase-orders", {
            "vendor_id": vendor, "warehouse_id": wh, "order_date": d(75 - i * 12),
            "expected_date": d(60 - i * 12), "lines": lines})
        if not po: continue
        poid = po["id"]
        api("POST", f"/purchase-orders/{poid}/submit", {})
        # submit auto-approves when no approval workflow is configured
        api("POST", f"/purchase-orders/{poid}/approve", {}, quiet=True)
        if i < 5:  # receive 5 of 6 (one stays ordered)
            full, _, _ = api("GET", f"/purchase-orders/{poid}")
            rl = [{"line_id": l["id"], "quantity_received": l["quantity"]}
                  for l in (full or {}).get("lines", [])]
            api("POST", f"/purchase-orders/{poid}/receive", {"lines": rl})
            bill, _, _ = api("POST", f"/purchase-orders/{poid}/bill", {})
            if bill and i < 3:  # pay 3 bills (retry once: payment numbers
                # are second-granular and collide in tight loops)
                body = {"amount": bill.get("total_amount", 0), "payment_date": d(50 - i * 12)}
                _, _, perr = api("POST", f"/purchase-invoices/{bill['id']}/pay", body, quiet=True)
                if perr:
                    import time; time.sleep(1.2)
                    api("POST", f"/purchase-invoices/{bill['id']}/pay", body)
    print("    purchase flow done")

# ── sales: past-dated invoices for the revenue chart ────────────────────
def sales_invoices():
    price = dict((x[0], x[4]) for x in PRODUCT_DEFS + [(s[0], "", "", 0, s[4]) for s in SERVICE_DEFS])
    clist = list(CUSTOMERS.values())
    made = 0
    for i in range(14):
        days_ago = random.randint(2, 9) if i < 4 else random.randint(15, 170)
        cust = clist[i % len(clist)]
        picks = random.sample(list(PRODUCTS.keys()), random.choice([2, 3]))
        lines = [{"product_id": PRODUCTS[c],
                  "description": c,
                  "quantity": random.choice([5, 10, 20, 40]),
                  "unit_price": price.get(c, 50000)} for c in picks]
        inv, _, _ = api("POST", "/sales-invoices", {
            "customer_id": cust, "invoice_date": d(days_ago),
            "due_date": d(days_ago - 20), "lines": lines})
        if not inv: continue
        api("POST", f"/sales-invoices/{inv['id']}/send", {})
        made += 1
        if i % 3 != 2:  # pay two thirds; the rest stay open (some overdue)
            full, _, _ = api("GET", f"/sales-invoices/{inv['id']}")
            total = (full or {}).get("total_amount", 0)
            if total:
                pbody = {"amount": total, "payment_date": d(max(1, days_ago - 15)),
                         "payment_method": "bank_transfer"}
                _, _, perr = api("POST", f"/sales-invoices/{inv['id']}/record-payment", pbody, quiet=True)
                if perr:
                    import time; time.sleep(1.2)
                    api("POST", f"/sales-invoices/{inv['id']}/record-payment", pbody)
    print(f"    invoices sent: {made}")

# ── sales orders in mixed statuses + ship a few ─────────────────────────
def sales_orders():
    price = dict((x[0], x[4]) for x in PRODUCT_DEFS)
    clist = list(CUSTOMERS.values())
    inv, _, _ = api("GET", "/inventory?limit=500")
    stocked_ids = {r.get("product_id") for r in (inv or [])
                   if (r.get("quantity_on_hand") or 0) > 25}
    stocked = [c for c in PRODUCTS if c.startswith("DEMO-P") and PRODUCTS[c] in stocked_ids]
    for i in range(8):
        cust = clist[(i * 3) % len(clist)]
        pool = stocked if (i < 3 and len(stocked) >= 2) else [c for c in PRODUCTS if c.startswith("DEMO-P")]
        picks = random.sample(pool, 2)
        lines = [{"product_id": PRODUCTS[c], "quantity": random.choice([5, 10, 15]),
                  "unit_price": price[c]} for c in picks]
        so, _, _ = api("POST", "/sales-orders", {"customer_id": cust, "lines": lines})
        if not so: continue
        soid = so["id"]
        if i < 6:
            api("POST", f"/sales-orders/{soid}/confirm", {})
        if i < 3:  # ship via delivery order (stock came from POs)
            do, _, _ = api("POST", "/sales/delivery-orders",
                           {"sales_order_id": soid, "warehouse_id": WH})
            if do:
                api("POST", f"/sales/delivery-orders/{do['id']}/validate", {})
    print("    sales orders done")

# ── expenses over past months ───────────────────────────────────────────
EXP_DEFS = [
    ("Ofis ijarasi", 12000000), ("Kommunal to'lovlar", 1800000),
    ("Yuk tashish xizmati", 3500000), ("Reklama (Instagram)", 2500000),
    ("Internet va aloqa", 450000), ("Kanstovarlar", 380000),
    ("Transport yoqilg'isi", 1400000), ("Do'kon dekoratsiyasi", 5200000),
    ("Hodimlar tushligi", 950000), ("Bank xizmatlari", 220000),
]
def expenses():
    cats, _, _ = api("GET", "/expense-categories")
    clist = cats or []
    if not clist:
        c, _, _ = api("POST", "/expense-categories", {"name": "Umumiy xarajatlar"})
        clist = [c] if c else []
    if not clist: raise RuntimeError("no expense categories")
    emps, _, _ = api("GET", "/employees?limit=5")
    emp = ((emps or [{}])[0] or {}).get("id")
    for i, (desc, amount) in enumerate(EXP_DEFS):
        cat = clist[i % len(clist)]
        e, _, _ = api("POST", "/expenses", {
            "description": desc, "amount": amount, "date": d(random.randint(3, 150)),
            "category_id": cat["id"], "employee_id": emp, "payment_method": "cash"})
        if not e: continue
        eid = e["id"]
        # created as 'submitted' by default; approve directly
        api("POST", f"/expenses/{eid}/approve", {})
        if i < 7:
            api("POST", f"/expenses/{eid}/pay", {})
    print("    expenses done")

# ── HR ──────────────────────────────────────────────────────────────────
EMP_DEFS = [
    ("Aziz", "Karimov", "Direktor", 15000000), ("Malika", "Yusupova", "Bosh buxgalter", 9000000),
    ("Jasur", "Toshpulatov", "Sotuv menejeri", 6500000), ("Dilnoza", "Rahimova", "Sotuv menejeri", 6500000),
    ("Bobur", "Ergashev", "Omborchi", 4500000), ("Sherzod", "Nazarov", "Haydovchi", 4000000),
    ("Nilufar", "Abdullayeva", "Kassir", 4200000), ("Rustam", "Qodirov", "Usta-yotqizuvchi", 5500000),
]
def employees():
    existing, _, _ = api("GET", "/employees?limit=200")
    have = {f'{e.get("first_name","")} {e.get("last_name","")}' for e in (existing or [])}
    for fn, ln, pos, salary in EMP_DEFS:
        if f"{fn} {ln}" in have: continue
        api("POST", "/employees", {
            "employee_number": f"DEMO-{fn[:2].upper()}{ln[:2].upper()}",
            "first_name": fn, "last_name": ln, "position": pos,
            "salary": salary, "hire_date": d(random.randint(100, 700)),
            "phone": f"+9989{random.randint(10000000, 99999999)}",
            "employment_type": "full_time"})
    print("    employees done")

# ── CRM: leads + opportunities ──────────────────────────────────────────
LEAD_DEFS = [
    "Yangi TRTS uchun kafel kerak", "Hovli uyga santexnika to'plami",
    "Ofis remontiga laminat 300m2", "Mehmonxona loyihasi — keramogranit",
    "Do'kon uchun plitka va kley", "Kvartira remonti to'liq paket",
    "Restoran oshxonasiga kafel", "Fitnes zal pol qoplamasi",
    "Klinika uchun santexnika", "Maktab remonti tenderi",
]
def crm():
    for i, title in enumerate(LEAD_DEFS):
        api("POST", "/leads", {
            "name": title, "contact_name": CUSTOMER_DEFS[i % len(CUSTOMER_DEFS)][1].split()[0] + " aka",
            "company_name": CUSTOMER_DEFS[i % len(CUSTOMER_DEFS)][1],
            "phone": f"+9989{random.randint(10000000, 99999999)}",
            "source": random.choice(["instagram", "telegram", "referral", "website"]),
            "expected_value": random.choice([15, 25, 40, 80, 120]) * 1000000}, quiet=True)
    ops, _, _ = api("GET", "/opportunities?limit=5")
    for i in range(5):
        api("POST", "/opportunities", {
            "name": f"DEMO bitim: {LEAD_DEFS[i]}",
            "contact_id": list(CUSTOMERS.values())[i],
            "expected_value": random.choice([20, 45, 90]) * 1000000,
            "probability": random.choice([30, 50, 70])}, quiet=True)
    print("    crm done")

# ── contracts ───────────────────────────────────────────────────────────
def contracts():
    defs = [("DEMO-CNT-01", "Grand Building — yillik ta'minot", "DEMO-C04", 480000000, "active"),
            ("DEMO-CNT-02", "Turon Development — obyekt ta'minoti", "DEMO-C09", 260000000, "active"),
            ("DEMO-CNT-03", "Navro'z Mehmonxonasi — remont", "DEMO-C07", 150000000, "completed"),
            ("DEMO-CNT-04", "Istiqbol Injiniring — dilerlik", "DEMO-C06", 90000000, "active"),
            ("DEMO-CNT-05", "Kamolot BM — loyiha kelishuvi", "DEMO-C08", 60000000, "draft")]
    existing, _, _ = api("GET", "/contracts?limit=100")
    have = {(c.get("contract_number") or "") for c in ((existing or {}).get("data") if isinstance(existing, dict) else (existing or []))}
    for num, title, cust, value, status in defs:
        if num in have: continue
        c, _, _ = api("POST", "/contracts", {
            "contract_number": num, "title": title,
            "vendor_id": CUSTOMERS.get(cust), "direction": "income",
            "contract_type": "annual", "value": value, "currency": "UZS",
            "start_date": d(120), "end_date": (TODAY + timedelta(days=245)).isoformat()})
        if c and status != "draft":
            api("POST", f"/contracts/{c['id']}/status", {"status": "active"}, quiet=True) or \
            api("PUT", f"/contracts/{c['id']}/status", {"status": "active"}, quiet=True)
            if status == "completed":
                api("POST", f"/contracts/{c['id']}/status", {"status": "completed"}, quiet=True)
    print("    contracts done")

# ── tasks board ─────────────────────────────────────────────────────────
def tasks():
    boards, _, _ = api("GET", "/task-boards")
    blist = (boards or {}).get("data") if isinstance(boards, dict) else (boards or [])
    board = next((b for b in (blist or []) if (b.get("name") or "").startswith("DEMO")), None)
    if not board:
        board, _, _ = api("POST", "/task-boards", {"name": "DEMO Savdo jamoasi"})
    if isinstance(board, dict) and "board" in board: board = board["board"]
    bid = (board or {}).get("id")
    if not bid:
        print("    board create returned:", json.dumps(board)[:200]); return
    full, _, _ = api("GET", f"/task-boards/{bid}")
    cols = (full or {}).get("columns") or []
    if not cols:
        for cn in ["Yangi", "Jarayonda", "Bajarildi"]:
            api("POST", f"/task-boards/{bid}/columns", {"name": cn}, quiet=True)
        full, _, _ = api("GET", f"/task-boards/{bid}")
        cols = (full or {}).get("columns") or []
    if not cols: return
    titles = ["Grand Building KP tayyorlash", "Yangi kafel kolleksiyasini saytga qo'shish",
              "Omborda inventarizatsiya", "Turon uchun namunalar yetkazish",
              "Instagram aksiya posti", "Ta'minotchi bilan narx kelishuvi",
              "Navro'z obyekti o'lchov ishlari", "Haftalik sotuv hisoboti"]
    for i, tt in enumerate(titles):
        api("POST", f"/task-boards/{bid}/tasks", {
            "title": tt, "column_id": cols[i % len(cols)]["id"],
            "due_date": (TODAY + timedelta(days=random.randint(1, 14))).isoformat()}, quiet=True)
    print("    tasks done")

# ── run ─────────────────────────────────────────────────────────────────
step("login", login)
step("units", units)
step("warehouse", warehouse)
step("categories", categories)
step("products", products)
step("contacts", contacts)
step("purchases", purchases)
step("sales invoices", sales_invoices)
step("sales orders", sales_orders)
step("employees", employees)
step("expenses", expenses)
step("crm", crm)
step("contracts", contracts)
step("tasks", tasks)

print("\n──────── summary ────────")
print("OK:", ", ".join(oks))
if fails: print("FAILED:", "; ".join(fails))
d1, _, _ = api("GET", f"/reports/finance-dashboard?period_from={d(180)}&period_to={TODAY.isoformat()}")
d2, _, _ = api("GET", "/inventory/stats")
d3, _, _ = api("GET", "/products/stats")
print("finance: income", (d1 or {}).get("total_income"), "| expense", (d1 or {}).get("total_expense"),
      "| AR", ((d1 or {}).get("receivables") or {}).get("total"))
print("inventory totals:", json.dumps(((d2 or {}).get("totals") or {}))[:200])
print("product stats:", json.dumps(d3 or {}))
