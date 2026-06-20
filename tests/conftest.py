"""
Genix ERP - Moliya moduli test suite
Umumiy fixture va konfiguratsiya

O'zbekiston BHMS standartlari asosida
"""
import os
import uuid
import pytest
import requests
import psycopg2
from psycopg2.extras import RealDictCursor
from datetime import date, timedelta

# ============================================
# KONFIGURATSIYA
# ============================================

BASE_URL = os.getenv("GENIX_API_URL", "http://localhost:8080/api/v1")
DB_HOST = os.getenv("DB_HOST", "localhost")
DB_PORT = os.getenv("DB_PORT", "5432")
DB_USER = os.getenv("DB_USER", "genix")
DB_PASSWORD = os.getenv("DB_PASSWORD", "genix_secret")
DB_NAME = os.getenv("DB_NAME", "genixerp")

# BHMS schyot kodlari
BHMS = {
    "asosiy_vositalar_amort": "0200",
    "xom_ashyo": "1010",
    "ishlab_chiqarish": "2010",
    "tayyor_mahsulot": "2810",
    "debitorlik": "4010",
    "podotchyot": "4220",
    "qqs_hisoblangan": "4410",
    "qqs_oldindan": "4420",
    "kassa": "5010",
    "bank_uzs": "5110",
    "bank_valyuta": "5220",
    "kreditorlik": "6010",
    "ish_haqi": "6710",
    "boshqa_kreditorlik": "6990",
    "ishlab_chiq_xarajat": "7110",
    "sotish_daromad": "9010",
    "ijobiy_kurs_farqi": "9540",
    "salbiy_kurs_farqi": "9620",
}

QQS_RATE = 12.0  # O'zbekiston QQS stavkasi


# ============================================
# DATABASE FIXTURE
# ============================================

@pytest.fixture(scope="session")
def db_connection():
    """Session-scoped database connection."""
    conn = psycopg2.connect(
        host=DB_HOST,
        port=DB_PORT,
        user=DB_USER,
        password=DB_PASSWORD,
        dbname=DB_NAME,
    )
    conn.autocommit = False
    yield conn
    conn.close()


@pytest.fixture
def db_session(db_connection):
    """Per-test database session with rollback."""
    cur = db_connection.cursor(cursor_factory=RealDictCursor)
    # Savepoint for rollback per test
    cur.execute("SAVEPOINT test_savepoint")
    yield cur
    db_connection.rollback()


@pytest.fixture(scope="session")
def db_read():
    """Read-only DB cursor for verification queries."""
    conn = psycopg2.connect(
        host=DB_HOST,
        port=DB_PORT,
        user=DB_USER,
        password=DB_PASSWORD,
        dbname=DB_NAME,
    )
    conn.autocommit = True
    cur = conn.cursor(cursor_factory=RealDictCursor)
    yield cur
    cur.close()
    conn.close()


# ============================================
# AUTH & API CLIENT
# ============================================

class APIClient:
    """Test API client with authentication."""

    def __init__(self, base_url, token=None, tenant_id=None, org_id=None):
        self.base_url = base_url
        self.session = requests.Session()
        self.token = token
        self.tenant_id = tenant_id
        self.org_id = org_id
        self._update_headers()

    def _update_headers(self):
        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        if self.tenant_id:
            headers["X-Tenant-ID"] = self.tenant_id
        if self.org_id:
            headers["X-Organization-ID"] = self.org_id
        self.session.headers.update(headers)

    def get(self, path, params=None):
        return self.session.get(f"{self.base_url}{path}", params=params)

    def post(self, path, json=None):
        return self.session.post(f"{self.base_url}{path}", json=json)

    def put(self, path, json=None):
        return self.session.put(f"{self.base_url}{path}", json=json)

    def delete(self, path):
        return self.session.delete(f"{self.base_url}{path}")

    def patch(self, path, json=None):
        return self.session.patch(f"{self.base_url}{path}", json=json)


@pytest.fixture(scope="session")
def auth_token(db_read):
    """Get auth token by logging in."""
    credentials = [
        {"email": "admin@genixerp.com", "password": "admin123"},
        {"email": "user@genixerp.com", "password": "user123"},
    ]

    for cred in credentials:
        resp = requests.post(f"{BASE_URL}/auth/login", json=cred)
        if resp.status_code == 200:
            data = resp.json().get("data", resp.json())
            tenant = data.get("tenant", {})
            user = data.get("user", {})
            return {
                "token": data.get("access_token") or data.get("token"),
                "tenant_id": tenant.get("id") or data.get("tenant_id"),
                "user_id": user.get("id") or data.get("user_id"),
            }

    # Fallback: use existing user from DB (no token)
    db_read.execute("SELECT id FROM users LIMIT 1")
    user = db_read.fetchone()
    db_read.execute("SELECT id FROM tenants LIMIT 1")
    tenant = db_read.fetchone()

    if not user or not tenant:
        pytest.skip("No user/tenant found, cannot authenticate")

    return {
        "token": None,
        "tenant_id": str(tenant["id"]),
        "user_id": str(user["id"]),
    }


@pytest.fixture(scope="session")
def api_client(auth_token, db_read):
    """Authenticated API client."""
    # Find organization
    db_read.execute(
        "SELECT id FROM organizations WHERE tenant_id = %s LIMIT 1",
        (auth_token["tenant_id"],)
    )
    org = db_read.fetchone()
    org_id = str(org["id"]) if org else None

    client = APIClient(
        base_url=BASE_URL,
        token=auth_token["token"],
        tenant_id=auth_token["tenant_id"],
        org_id=org_id,
    )
    return client


# ============================================
# TEST COMPANY & ORG
# ============================================

@pytest.fixture(scope="session")
def test_company(db_read, auth_token):
    """Primary test company (organization)."""
    db_read.execute(
        "SELECT * FROM organizations WHERE tenant_id = %s LIMIT 1",
        (auth_token["tenant_id"],)
    )
    org = db_read.fetchone()
    if not org:
        pytest.skip("No organization found")
    return dict(org)


@pytest.fixture(scope="session")
def tenant_id(auth_token):
    """Current tenant ID."""
    return auth_token["tenant_id"]


# ============================================
# REFERENCE DATA LOOKUPS
# ============================================

@pytest.fixture(scope="session")
def account_types(db_read):
    """All account types keyed by code."""
    db_read.execute("SELECT * FROM account_types")
    return {row["code"]: dict(row) for row in db_read.fetchall()}


@pytest.fixture(scope="session")
def currencies(db_read):
    """All currencies keyed by code."""
    db_read.execute("SELECT * FROM currencies WHERE is_active = true")
    return {row["code"]: dict(row) for row in db_read.fetchall()}


@pytest.fixture(scope="session")
def accounts(db_read, tenant_id):
    """All accounts keyed by code."""
    db_read.execute(
        "SELECT * FROM accounts WHERE tenant_id = %s AND deleted_at IS NULL AND is_active = true ORDER BY code",
        (tenant_id,)
    )
    return {row["code"]: dict(row) for row in db_read.fetchall()}


@pytest.fixture(scope="session")
def journals(db_read, tenant_id):
    """All journals keyed by type."""
    db_read.execute(
        "SELECT * FROM journals WHERE tenant_id = %s AND deleted_at IS NULL ORDER BY type",
        (tenant_id,)
    )
    result = {}
    for row in db_read.fetchall():
        result[row["type"]] = dict(row)
        result[row["code"]] = dict(row)
    return result


# ============================================
# KONTRAGENTLAR
# ============================================

@pytest.fixture(scope="session")
def test_supplier(api_client, db_read, tenant_id):
    """Test supplier (yetkazuvchi)."""
    code = f"SUP-TEST-{uuid.uuid4().hex[:6].upper()}"
    resp = api_client.post("/contacts", json={
        "type": "vendor",
        "code": code,
        "name": "Test Yetkazuvchi MChJ",
        "tax_id": "111222333",
        "email": f"supplier-{code}@test.uz",
        "phone": "+998901234567",
    })
    if resp.status_code in (200, 201):
        data = resp.json().get("data", resp.json())
        return data
    # Fallback: find existing
    db_read.execute(
        "SELECT id, name, type FROM contacts WHERE tenant_id = %s AND type IN ('vendor','supplier') AND deleted_at IS NULL LIMIT 1",
        (tenant_id,)
    )
    row = db_read.fetchone()
    if row:
        return dict(row)
    pytest.skip("Cannot create or find test supplier")


@pytest.fixture(scope="session")
def test_customer(api_client, db_read, tenant_id):
    """Test customer (xaridor)."""
    code = f"CUS-TEST-{uuid.uuid4().hex[:6].upper()}"
    resp = api_client.post("/contacts", json={
        "type": "customer",
        "code": code,
        "name": "Test Xaridor MChJ",
        "tax_id": "444555666",
        "email": f"customer-{code}@test.uz",
        "phone": "+998907654321",
    })
    if resp.status_code in (200, 201):
        data = resp.json().get("data", resp.json())
        return data
    db_read.execute(
        "SELECT id, name, type FROM contacts WHERE tenant_id = %s AND type = 'customer' AND deleted_at IS NULL LIMIT 1",
        (tenant_id,)
    )
    row = db_read.fetchone()
    if row:
        return dict(row)
    pytest.skip("Cannot create or find test customer")


# ============================================
# BANK & KASSA
# ============================================

@pytest.fixture(scope="session")
def test_bank_account(api_client, db_read, tenant_id, accounts):
    """Test bank account (UZS)."""
    # Find bank GL account (1100 or similar)
    bank_account = accounts.get("1100") or accounts.get("5110")
    account_id = str(bank_account["id"]) if bank_account else None

    resp = api_client.post("/bank-accounts", json={
        "name": "Test Bank UZS",
        "bank_name": "NBU Test",
        "account_number": f"20208000{uuid.uuid4().hex[:10]}",
        "currency": "UZS",
        "account_type": "checking",
        "balance": 0,
    })
    if resp.status_code in (200, 201):
        data = resp.json().get("data", resp.json())
        return data
    db_read.execute(
        "SELECT ba.* FROM bank_accounts ba WHERE ba.tenant_id = %s AND ba.is_active = true AND ba.deleted_at IS NULL LIMIT 1",
        (tenant_id,)
    )
    row = db_read.fetchone()
    if row:
        return dict(row)
    pytest.skip("Cannot create or find test bank account")


@pytest.fixture(scope="session")
def test_cash_register(api_client, db_read, tenant_id):
    """Test cash register (Asosiy kassa, limit 10M UZS)."""
    code = f"KASSA-TEST-{uuid.uuid4().hex[:4].upper()}"
    resp = api_client.post("/cash/registers", json={
        "name": "Test Asosiy Kassa",
        "code": code,
        "currency": "UZS",
        "limit_amount": 10000000,
    })
    if resp.status_code in (200, 201):
        data = resp.json().get("data", resp.json())
        return data
    db_read.execute(
        "SELECT * FROM cash_registers WHERE tenant_id = %s AND deleted_at IS NULL AND is_active = true LIMIT 1",
        (tenant_id,)
    )
    row = db_read.fetchone()
    if row:
        return dict(row)
    pytest.skip("Cannot create or find test cash register")


# ============================================
# HELPER FUNCTIONS
# ============================================

def assert_balanced(entry_data):
    """Assert that journal entry is balanced (Dt = Kt)."""
    lines = entry_data.get("lines", [])
    total_debit = sum(float(l.get("debit_amount", 0)) for l in lines)
    total_credit = sum(float(l.get("credit_amount", 0)) for l in lines)
    assert abs(total_debit - total_credit) < 0.01, \
        f"Entry not balanced: Debit={total_debit}, Credit={total_credit}"


def assert_balance_equation(assets, liabilities, equity):
    """Aktiv = Passiv + Kapital."""
    assert abs(assets - (liabilities + equity)) < 0.01, \
        f"Balance equation violated: Assets={assets} != Liab({liabilities}) + Equity({equity})"


def find_account_id(accounts_dict, code):
    """Find account ID by code."""
    acc = accounts_dict.get(code)
    if acc:
        return str(acc["id"])
    # Partial match
    for k, v in accounts_dict.items():
        if code in k:
            return str(v["id"])
    return None


def find_leaf_account(accounts_dict, *codes):
    """Birinchi mavjud LEAF schyotni topish (TT §4.2).

    Guruh (sarlavha) schyotlariga provodka taqiqlangan, shuning uchun
    kod mavjud bo'lsa ham guruh bo'lsa keyingi kodga o'tiladi.
    """
    for code in codes:
        acc = accounts_dict.get(code)
        if acc and acc.get("is_leaf", True):
            return acc
    return None


def today():
    """Bugungi sana."""
    return date.today().isoformat()


def yesterday():
    """Kechagi sana."""
    return (date.today() - timedelta(days=1)).isoformat()


def first_of_month():
    """Oy boshi."""
    d = date.today()
    return d.replace(day=1).isoformat()


def end_of_month():
    """Oy oxiri."""
    d = date.today()
    if d.month == 12:
        return d.replace(year=d.year + 1, month=1, day=1).isoformat()
    next_month = d.replace(month=d.month + 1, day=1)
    return (next_month - timedelta(days=1)).isoformat()
