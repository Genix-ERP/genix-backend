"""
BOSQICH 2: KONTRAGENTLAR (Partners / Contacts)
"""
import uuid
import pytest


class TestCreateSupplier:
    """2.1 - Yetkazuvchi yaratish."""

    def test_create_supplier(self, api_client):
        code = f"SUP-{uuid.uuid4().hex[:6].upper()}"
        resp = api_client.post("/contacts", json={
            "type": "vendor",
            "code": code,
            "name": "Pytest Yetkazuvchi",
            "tax_id": "999888777",
            "email": f"{code}@test.uz",
            "phone": "+998901111111",
        })
        assert resp.status_code in (200, 201), f"Yetkazuvchi yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("name") == "Pytest Yetkazuvchi"
        assert data.get("type") in ("vendor", "supplier")
        # Cleanup
        if data.get("id"):
            api_client.delete(f"/contacts/{data['id']}")


class TestCreateCustomer:
    """2.2 - Xaridor yaratish."""

    def test_create_customer(self, api_client):
        code = f"CUS-{uuid.uuid4().hex[:6].upper()}"
        resp = api_client.post("/contacts", json={
            "type": "customer",
            "code": code,
            "name": "Pytest Xaridor",
            "tax_id": "111222333",
            "email": f"{code}@test.uz",
            "phone": "+998902222222",
        })
        assert resp.status_code in (200, 201), f"Xaridor yaratilmadi: {resp.text}"
        data = resp.json().get("data", resp.json())
        assert data.get("name") == "Pytest Xaridor"
        assert data.get("type") == "customer"
        if data.get("id"):
            api_client.delete(f"/contacts/{data['id']}")


class TestRequiredFields:
    """2.3 - Majburiy maydonlar."""

    def test_missing_name_rejected(self, api_client):
        resp = api_client.post("/contacts", json={
            "type": "customer",
            "code": f"BAD-{uuid.uuid4().hex[:6]}",
        })
        # Name is required
        assert resp.status_code in (400, 422), \
            f"Nomsiz kontragent qabul qilindi (kutilgan: 400): {resp.status_code}"

    def test_missing_type_handled(self, api_client):
        resp = api_client.post("/contacts", json={
            "code": f"BAD2-{uuid.uuid4().hex[:6]}",
            "name": "No type partner",
        })
        # Either rejected or defaults to customer
        if resp.status_code in (200, 201):
            data = resp.json().get("data", resp.json())
            assert data.get("type") is not None, "Type default bo'lishi kerak"
            if data.get("id"):
                api_client.delete(f"/contacts/{data['id']}")


class TestPartnerSearch:
    """2.5 - Nom/INN bo'yicha qidiruv."""

    def test_search_by_name(self, api_client, test_customer):
        name = test_customer.get("name", "")
        search_term = name[:5] if len(name) > 5 else name
        resp = api_client.get("/contacts", params={"search": search_term})
        assert resp.status_code == 200
        data = resp.json().get("data", resp.json())
        items = data if isinstance(data, list) else data.get("items", data.get("contacts", []))
        assert len(items) >= 1, f"Qidiruv natijasi bo'sh: '{search_term}'"


class TestPartnerBalance:
    """2.6 - Yangi kontragent qoldig'i 0."""

    def test_new_partner_zero_balance(self, api_client):
        code = f"BAL-{uuid.uuid4().hex[:6].upper()}"
        resp = api_client.post("/contacts", json={
            "type": "customer",
            "code": code,
            "name": "Zero Balance Test",
        })
        if resp.status_code in (200, 201):
            data = resp.json().get("data", resp.json())
            balance = data.get("current_balance", data.get("balance", 0))
            assert float(balance) == 0.0, f"Yangi kontragent qoldig'i 0 emas: {balance}"
            if data.get("id"):
                api_client.delete(f"/contacts/{data['id']}")


class TestListContacts:
    """Kontragentlar ro'yxati."""

    def test_list_contacts(self, api_client):
        resp = api_client.get("/contacts")
        assert resp.status_code == 200

    def test_filter_by_type(self, api_client):
        resp = api_client.get("/contacts", params={"type": "customer"})
        assert resp.status_code == 200
