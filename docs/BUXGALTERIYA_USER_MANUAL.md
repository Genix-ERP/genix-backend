# Genix ERP — Buxgalteriya moduli foydalanuvchi qo'llanmasi

BHMS №21 talablariga muvofiq qo'sh yozuv (double-entry) asosidagi buxgalteriya moduli uchun operator qo'llanmasi.

Ushbu hujjat Genix ERP Buxgalteriya moduli TT (Texnik topshiriq) §10 band 8-sinov mezoniga javob beradi.

---

## 1. Kirish va rollar

Modul quyidagi foydalanuvchi rollari uchun mo'ljallangan (TT §1.2):

1. **Buxgalter-operator** — hujjatlarni kiritadi, provodkalarni shakllantiradi.
2. **Bosh buxgalter** — provodkalarni provodkalaydi (post), davrni yopadi, storno qiladi.
3. **Moliyaviy direktor / Iqtisodchi** — hisobotlarni ko'radi, tahlil qiladi.
4. **Rahbariyat** — yuqori darajadagi hisobotlarni ko'radi (read-only).
5. **Tashqi auditorlar** — audit izlarini va hisobotlarni ko'radi (read-only).

Rollar `rg.Group` permission middleware orqali boshqariladi. Har bir harakat `audit_logs` jadvalida qayd qilinadi.

## 2. Umumiy tamoyil: Dt = Kt

Har bir xo'jalik operatsiyasi ikki tomonlama yozuv qoidasi asosida ikkita hisobda aks ettiriladi:

- **Debet (Dt)** — hisobning chap tomoni.
- **Kredit (Kt)** — hisobning o'ng tomoni.

Har qanday provodkada va har qanday davr yakunida debet summasi kredit summasiga teng bo'lishi shart. Tizim bu tenglikni dasturiy darajada (backend + DB trigger) majburiy bajaradi (TT §4.1).

---

## 3. Hisoblar rejasi (Chart of Accounts)

### 3.1. Ko'rish va tahrirlash

Sahifa: **Moliya → Hisoblar rejasi** (`ChartOfAccounts.jsx`).

Hisoblar BHMS №21 standart bo'limlari bo'yicha ierarxik tuzilmada saqlanadi:

| Bo'lim | Nomi | Misol |
|--------|------|-------|
| 0xxx | Uzoq muddatli aktivlar | 0110 Asosiy vositalar |
| 1xxx | Tovar-moddiy zaxiralar | 1010 Xomashyo |
| 2xxx | Ishlab chiqarish xarajatlari | 2010 Asosiy ishlab chiqarish |
| 4xxx | Debitorlik qarzdorligi | 4010 Xaridorlar, 4210 Hisobdor shaxslar |
| 5xxx | Pul mablag'lari | 5010 Kassa, 5110 H/r |
| 6xxx | Qisqa muddatli majburiyatlar | 6010 Ta'minotchilar, 6410 QQS, 6710 Ish haqi |
| 8xxx | Kapital | 8330 Ustav kapitali |
| 9xxx | Daromadlar va xarajatlar | 9010 Sotishdan tushum, 9110 Tannarx |

### 3.2. Hisob turlari

Har bir hisobda quyidagi atributlar bo'ladi:

- **Code, Name** — 4 xonali kod va nomi (uz/en/ru).
- **Account nature**: `ACTIVE`, `PASSIVE`, yoki `ACTIVE_PASSIVE`.
- **is_leaf**: `true` bo'lsa, provodka qilish mumkin. `false` (guruh hisobi) bo'lsa, faqat tartibga solish uchun, provodka qilib bo'lmaydi (TT §4.2).
- **analytics_types** (`kontragent`, `shartnoma`, `ombor`, `xodim`) — subkonto turlari.
- **mandatory_analytics** — agar `true` bo'lsa, provodka bu analitikalarsiz qabul qilinmaydi (TT §4.5).

Mashhur majburiy subkontolar:

| Hisob | Majburiy analitika |
|-------|--------------------|
| 4010 Xaridorlar | kontragent + shartnoma |
| 6010 Ta'minotchilar | kontragent + shartnoma |
| 2910 Tovarlar | ombor |
| 4210 Hisobdor shaxs bo'naklari | xodim |
| 6710 Ish haqi | xodim |
| 1010-1090 Xomashyo | ombor |

---

## 4. Provodka kiritish (Journal Entry)

### 4.1. Manual (buxgalterlik ma'lumotnomasi)

Sahifa: **Moliya → Jurnallar → Yangi provodka** (`JournalManagement.jsx`).

Qadamlar:

1. **Jurnalni tanlang** (MISC / GENERAL / SALES / PURCHASE / BANK / CASH).
2. **Sana** va **izoh** kiriting (izoh majburiy).
3. Kamida 2 ta **qator** qo'shing:
   - Hisobni tanlang (faqat `is_leaf=true` hisoblari tanlash mumkin).
   - Dt yoki Kt summasini kiriting (ikkalasi bir vaqtda bo'la olmaydi).
   - Zarur analitikalarni kiriting: kontragent, shartnoma, ombor, xodim.
4. **Saqlash** tugmasi — provodka `draft` holatida saqlanadi.
5. **Provodkalash (Post)** tugmasi — qator summalarini hisoblar balansiga qo'llaydi.

### 4.2. Tizim majburiy tekshiruvlari

Tizim quyidagi qoidalarni buzgan provodkani rad etadi:

1. **Balans** (TT §4.1) — ∑Dt ≠ ∑Kt bo'lsa, xato: `Journal entry is not balanced...`
2. **Bargli hisob** (TT §4.2) — guruh hisobi tanlansa, xato: `TT §4.2: account ... is a group account — postings are only allowed on leaf accounts`
3. **Majburiy analitika** (TT §4.5) — masalan, 4010 hisobiga kontragentsiz yozish: `TT §4.5: account 4010 ... requires analytics dimension 'kontragent'`
4. **Yopiq davr** (TT §4.3) — yopilgan davrga yozish: `TT §4.3: fiscal period ... is closed; use storno in the current open period`
5. **Provodkalangan hujjat** (TT §4.4) — `posted` holatdagi provodkani tahrirlash taqiqlanadi, faqat storno orqali.

### 4.3. Valyuta operatsiyalari (TT §4.6)

Har bir provodka qatori `amount_base` (so'mdagi ekvivalent) ni saqlaydi. Bu avtomatik hisoblanadi:

```
amount_base = (debit_amount + credit_amount) * exchange_rate
```

Agar qatorlar bir xil valyutada bo'lmasa, kurs farqi avtomatik ravishda `9540` (kurs farqidan daromad) yoki `9630` (kurs farqidan zarar) hisoblariga yoziladi.

### 4.4. Storno (rad etish)

Provodkalangan hujjatni tahrirlash taqiqlanadi (TT §4.4). Xatoni to'g'rilash uchun:

1. Asl provodkani oching.
2. **Storno** tugmasini bosing.
3. Sababni kiriting.
4. Tizim tegal qiymatlari bilan yangi `is_reversal=true` provodkani yaratadi va `reversal_of_id` orqali bog'laydi.

---

## 5. Hujjat shablonlari (avtomatik provodkalash)

Operator odatda hujjat kiritadi, tizim provodkalarni avtomatik shakllantiradi:

### 5.1. "Tovar keldi" (Goods Receipt)

- **Dt 2910** (Tovarlar / ombor) — QQS siz summa
- **Kt 6010** (Ta'minotchilar / kontragent) — QQS siz summa

Qo'shimcha qator QQS uchun:

- **Dt 4410** (QQS kirim)
- **Kt 6010**

### 5.2. "Chiquvchi to'lov topshirig'i"

- **Dt 6010** (shartnoma bo'yicha hisob / kontragent)
- **Kt 5110** (H/r)

### 5.3. "Xaridorga sotish"

- **Dt 4010** (Xaridorlar / kontragent) — jami summa
- **Kt 9010** (Sotishdan tushum) — QQS siz
- **Kt 6410** (QQS payable) — QQS
- **Dt 9110** (Tannarx) / **Kt 2910** (Tovarlar / ombor) — avtomatik COGS

### 5.4. "Ish haqi"

- **Dt 2010/9420** / **Kt 6710** — hisoblash
- **Dt 6710** / **Kt 6410** — daromad solig'i
- **Dt 6710** / **Kt 6520** — pensiya
- **Dt 6710** / **Kt 5010** — kassadan berish

---

## 6. Davrni yopish (Period Close)

Sahifa: **Moliya → Davrni yopish** (`PeriodClose.jsx`).

Qadamlar:

1. **Davr chegaralarini** tanlang (YYYY-MM-DD dan YYYY-MM-DD gacha).
2. **Davrni yopish** tugmasini bosing.
3. Tizim 3 ta invariant tekshiruvini bajaradi (`period_closing_checks`):
   - `balance_invariant`: ∑Dt = ∑Kt
   - `no_drafts`: davrda `draft` holatdagi provodkalar yo'q
   - `final_result_account`: 9900 hisobi mavjud
4. Agar hammasi o'tsa:
   - 9xxx P&L hisoblari 9900 ga o'tkaziladi.
   - `fiscal_periods.status` → `closed`.
   - `accounting_periods.is_locked` → `true`.
   - Natija `period_closings` jadvalida saqlanadi.

### 6.1. Qayta ochish (Reopen)

Admin ruxsati bilan:

1. Davr yozuvini toping (yopilgan bo'lishi kerak).
2. **Qayta ochish** tugmasini bosing, sababni yozing.
3. Yopilish storno qilinadi (`is_reversal=true` yangi provodka), davr qayta ochiladi.

---

## 7. Hisobotlar

### 7.1. Aylanma-saldo qaydnomasi (ASQ, TT §6.1)

Sahifa: **Moliya → Hisobotlar → ASQ** (`TrialBalanceASQ.jsx`).

Har bir hisob bo'yicha 6 ta ustun: boshi saldosi (Dt/Kt), davr aylanmasi (Dt/Kt), oxiri saldosi (Dt/Kt). **Excel'ga yuklash** tugmasi bor.

### 7.2. Bosh kitob (TT §6.2)

Endpoint: `GET /finance/reports/general-ledger/monthly?year=2026`

Har bir hisob bo'yicha 12 oylik kesim + yillik yig'indi.

### 7.3. Hisob kartochkasi (TT §6.3)

Sahifa: **Moliya → Hisob kartochkasi** (`AccountCard.jsx`).

Bitta hisob bo'yicha barcha provodkalar xronologik tartibda. Filtrlari:

- Davr (period_from / period_to)
- Kontragent (contact_name, counterpart_code)
- Summa chegarasi (amount_min, amount_max)
- Hujjat turi (doc_type)

### 7.4. BHMS №21 Forma 1/2/3 (TT §6.4)

Sahifa: **Moliya → BHMS hisobotlari** (`BhmsForms.jsx`).

- **Forma 1** — Buxgalteriya balansi (aktivlar = majburiyatlar + kapital).
- **Forma 2** — Moliyaviy natijalar (sof foyda hisob-kitobi).
- **Forma 3** — Pul mablag'lari harakati (to'g'ri usul).

Uchta formani bir Excel faylga yuklab olish mumkin.

### 7.5. Soliq hisobotlari (TT §6.4)

Sahifa: **Moliya → Soliqlar → Hisobotlar** (`TaxReports.jsx`).

- QQS deklaratsiyasi
- Foyda solig'i hisob-kitobi
- Chorak va yillik davrlar

### 7.6. Operativ hisobotlar (TT §6.5)

- **Akt-sverka** — kontragent bilan hisob-kitob kelishuvi (`ActSverka.jsx`). Tashqi foydalanuvchilar uchun imzosiz havola yaratish mumkin.
- **Aging** — debitorlik/kreditorlik qarzdorligi yoshi bo'yicha tahlil.
- **Pul qoldig'i** — `CashRegister.jsx` va `/finance/cash-book`.
- **Ombor qoldig'i** — `/finance/reports/inventory-summary`.

---

## 8. Integratsiyalar

### 8.1. Bank-mijoz (TT §8.1)

Sahifa: **Moliya → Bank ko'chirmasi** (import).

- 1C Bank-client .txt formatini qabul qiladi.
- Har bir tranzaksiya INN bo'yicha mavjud kontragentga mos kelishga harakat qilinadi.
- Tasdiqlangandan keyin provodka yaratiladi.

### 8.2. E-invoice (TT §8.2)

- **Kiruvchi**: didox.uz / faktura / soliq.uz dan keluvchi hisob-fakturalarni qabul qilish (`POST /einvoices/ingest`).
- **Chiquvchi**: provayderga yuborish (provayder adapter alohida modulda).
- Hujjat tasdiqlanmaguncha purchase invoice yaratilmaydi.

### 8.3. Webhooks (TT §7.4)

Tashqi tizimlarga voqea jo'natish uchun:

1. Sahifa: **Admin sozlamalari → Webhook obunalari**.
2. URL, event ro'yxati, secret kiriting. Voqealar:
   - `journal_entry.posted`, `journal_entry.reversed`
   - `period.closed`, `period.reopened`
   - `einvoice.approved`
   - `payment.confirmed`, `bank_statement.imported`
3. Har bir yuborish `webhook_deliveries` jadvalida qayd qilinadi; muvaffaqiyatsiz urinishlar eksponensial backoff bilan takrorlanadi.

---

## 9. Audit izi

Har bir moliyaviy harakat avtomatik qayd etiladi:

| Jadval | Nima qayd etiladi |
|--------|-------------------|
| `activity_logs` | Umumiy foydalanuvchi harakatlari |
| `audit_logs` | Entity-level o'zgarishlar (journal_entries, accounts, ...) |
| `period_closings` | Davr yopish urinishlari + invariant tekshiruvlari |
| `period_closing_checks` | Yopish paytida o'tkazilgan har bir tekshiruv |
| `webhook_deliveries` | Tashqi tizimlarga yuborilgan voqealar |

Audit izini ko'rish: `GET /finance/journal-entries/{id}/audit-logs`.

---

## 10. Xato xabarlari (troubleshooting)

| Xato | Sabab | Yechim |
|------|-------|--------|
| `Journal entry is not balanced` | Dt ≠ Kt | Qatorlarni qayta tekshiring |
| `TT §4.2: account ... is a group account` | Guruh hisobiga yozmoqda | Tegishli bargli (is_leaf) hisobni tanlang |
| `TT §4.3: fiscal period ... is closed` | Yopiq davrga yozmoqda | Davrni qayta oching yoki storno yarating |
| `TT §4.4: posted entry is immutable` | Provodkalangan hujjatni tahrirlamoqda | Storno qiling, keyin yangi provodka kiriting |
| `TT §4.5: requires analytics dimension 'kontragent'` | Majburiy subkonto yo'q | Kontragent/shartnoma/ombor/xodim qo'shing |
| `Cash account ... balance insufficient` | Kassada/h/r da mablag' yetmaydi | Avval kassa/h/r ni to'ldiring |

---

## 11. Maxsus holatlar

### 11.1. Valyuta kursi farqi

Kunning oxirida valyutadagi aktivlar/majburiyatlar qayta baholashi uchun:

1. Yangi manual provodka yarating.
2. Valyuta hisobidan UZS ekvivalenti farqini hisoblang.
3. Farq miqdorida 9540 (daromad) yoki 9630 (zarar) ga yozing.

Aslida tizim bir provodka ichida avtomatik qiladi agar qatorlar har xil kurs bilan yozilgan bo'lsa.

### 11.2. Storno qilgandan keyin xato aniqlanishi

Agar storno o'zi ham noto'g'ri bo'lsa, stornoni storno qilish mumkin emas (reversal of reversal). O'rniga qo'lda to'g'rilovchi provodka yarating.

### 11.3. Davr yopishi muvaffaqiyatsiz bo'lsa

`period_closings.status = 'failed'` bo'lsa:

1. `period_closing_checks` tekshiring.
2. Aniqlangan muammoni hal qiling (draft provodkalarni post qiling yoki balansni to'g'rilang).
3. Davrni yopishni qayta ishga tushiring.

---

## Murojaat

Texnik savollar uchun: support@genixerp.uz

Oxirgi yangilanish: 2026-04-20. Versiya: TT v1.0 asosida.
