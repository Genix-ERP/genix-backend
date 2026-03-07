-- Add bilingual name support to accounts table (name_uz, name_en)
-- Per TZ: UI is in Uzbek, so account names should be stored in both languages

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS name_uz VARCHAR(255);
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS name_en VARCHAR(255);

-- Populate name_en from existing name column
UPDATE accounts SET name_en = name WHERE name_en IS NULL;

-- Populate name_uz for all known default accounts
UPDATE accounts SET name_uz = CASE code
    -- Assets
    WHEN '1000' THEN 'Kassa'
    WHEN '1010' THEN 'Mayda pul'
    WHEN '1020' THEN 'Valyuta hisobi'
    WHEN '1100' THEN 'Bank hisobi'
    WHEN '1150' THEN 'Kutilayotgan tushumlar'
    WHEN '1160' THEN 'Kutilayotgan to''lovlar'
    WHEN '1200' THEN 'Debitorlar'
    WHEN '1210' THEN 'Shubhali qarzlar zaxirasi'
    WHEN '1300' THEN 'Tovar-moddiy zaxiralar'
    WHEN '1310' THEN 'Xom ashyo'
    WHEN '1320' THEN 'Tugallanmagan ishlab chiqarish'
    WHEN '1330' THEN 'Tayyor mahsulot'
    WHEN '1340' THEN 'Tovarlar (sotish uchun)'
    WHEN '1400' THEN 'Oldindan to''langan xarajatlar'
    WHEN '1410' THEN 'QQS kirim'
    WHEN '1500' THEN 'Asosiy vositalar'
    WHEN '1510' THEN 'Eskirish (amortizatsiya)'
    WHEN '1600' THEN 'Nomoddiy aktivlar'
    WHEN '1700' THEN 'Qurilish xarajatlari'
    WHEN '1730' THEN 'Xodim avans'
    -- Liabilities
    WHEN '2000' THEN 'Kreditorlar'
    WHEN '2100' THEN 'Hisoblangan xarajatlar'
    WHEN '2110' THEN 'Ish haqi bo''yicha qarz'
    WHEN '2120' THEN 'Foiz bo''yicha qarz'
    WHEN '2200' THEN 'Soliq bo''yicha qarz'
    WHEN '2210' THEN 'QQS chiqim'
    WHEN '2220' THEN 'Daromad solig''i'
    WHEN '2230' THEN 'Tovar qabul (tranzit)'
    WHEN '2231' THEN 'Tovar jo''natish (tranzit)'
    WHEN '2300' THEN 'Oldindan olingan daromad'
    WHEN '2400' THEN 'Qisqa muddatli kreditlar'
    WHEN '2500' THEN 'Uzoq muddatli kreditlar'
    -- Equity
    WHEN '3000' THEN 'Eganing kapitali'
    WHEN '3100' THEN 'Ustav kapitali'
    WHEN '3200' THEN 'Taqsimlanmagan foyda'
    WHEN '3300' THEN 'Joriy yil foydasi'
    WHEN '3400' THEN 'Dividendlar'
    -- Revenue
    WHEN '4000' THEN 'Sotish daromadi'
    WHEN '4100' THEN 'Xizmat daromadi'
    WHEN '4300' THEN 'Qurilish daromadi'
    WHEN '4900' THEN 'Boshqa daromad'
    WHEN '4910' THEN 'Foiz daromadi'
    WHEN '4920' THEN 'Valyuta kursi foydasi'
    -- Expenses
    WHEN '5000' THEN 'Sotilgan tovarlar tannarxi'
    WHEN '5100' THEN 'Bevosita materiallar'
    WHEN '5200' THEN 'Bevosita ish haqi'
    WHEN '5300' THEN 'Ishlab chiqarish xarajatlari'
    WHEN '6000' THEN 'Ish haqi xarajatlari'
    WHEN '6100' THEN 'Ijara xarajati'
    WHEN '6200' THEN 'Kommunal xarajatlar'
    WHEN '6300' THEN 'Ofis xarajatlari'
    WHEN '6400' THEN 'Sug''urta xarajati'
    WHEN '6500' THEN 'Amortizatsiya xarajati'
    WHEN '6600' THEN 'Reklama va marketing'
    WHEN '6700' THEN 'Professional xizmatlar'
    WHEN '6800' THEN 'Xizmat safari xarajatlari'
    WHEN '6900' THEN 'Boshqa operatsion xarajatlar'
    WHEN '6910' THEN 'Zaxira tuzatish'
    WHEN '6950' THEN 'To''lov farqi hisobdan chiqarish'
    WHEN '7000' THEN 'Foiz xarajati'
    WHEN '7100' THEN 'Bank xarajatlari'
    WHEN '7200' THEN 'Valyuta kursi zarari'
    WHEN '7900' THEN 'Boshqa xarajatlar'
    ELSE name_uz
END
WHERE deleted_at IS NULL AND name_uz IS NULL;
