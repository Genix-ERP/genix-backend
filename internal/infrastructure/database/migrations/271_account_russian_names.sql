-- Add Russian translations to default chart of accounts
-- name column stores Russian (default display), name_en stores English, name_uz stores Uzbek

-- Also fill in missing name_uz and name_en where needed

-- Assets
UPDATE accounts SET name = 'Касса', name_en = COALESCE(NULLIF(name_en,''), 'Cash'), name_uz = COALESCE(NULLIF(name_uz,''), 'Kassa') WHERE code = '1000' AND name_en = 'Cash';
UPDATE accounts SET name = 'Банковский счёт', name_en = COALESCE(NULLIF(name_en,''), 'Bank Account'), name_uz = COALESCE(NULLIF(name_uz,''), 'Bank hisobi') WHERE code = '1010' AND name_en = 'Bank Account';
UPDATE accounts SET name = 'Валютный счёт', name_en = COALESCE(NULLIF(name_en,''), 'Foreign Currency Account'), name_uz = COALESCE(NULLIF(name_uz,''), 'Valyuta hisobi') WHERE code = '1020' AND name_en = 'Foreign Currency Account';
UPDATE accounts SET name = 'Дебиторская задолженность', name_en = COALESCE(NULLIF(name_en,''), 'Accounts Receivable'), name_uz = COALESCE(NULLIF(name_uz,''), 'Debitorlar') WHERE code = '1100' AND name_en = 'Accounts Receivable';
UPDATE accounts SET name = 'Ожидаемые поступления', name_en = COALESCE(NULLIF(name_en,''), 'Outstanding Receipts'), name_uz = COALESCE(NULLIF(name_uz,''), 'Kutilayotgan tushumlar') WHERE code = '1150' AND name_en = 'Outstanding Receipts';
UPDATE accounts SET name = 'Ожидаемые платежи', name_en = COALESCE(NULLIF(name_en,''), 'Outstanding Payments'), name_uz = COALESCE(NULLIF(name_uz,''), 'Kutilayotgan to''lovlar') WHERE code = '1160' AND name_en = 'Outstanding Payments';
UPDATE accounts SET name = 'Резерв по сомнительным долгам', name_en = COALESCE(NULLIF(name_en,''), 'Allowance for Doubtful Accounts'), name_uz = COALESCE(NULLIF(name_uz,''), 'Shubhali qarzlar zaxirasi') WHERE code = '1210' AND name_en = 'Allowance for Doubtful Accounts';
UPDATE accounts SET name = 'Запасы', name_en = COALESCE(NULLIF(name_en,''), 'Inventory'), name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar-moddiy zaxiralar') WHERE code = '1300' AND name_en = 'Inventory';
UPDATE accounts SET name = 'Сырьё и материалы', name_en = COALESCE(NULLIF(name_en,''), 'Raw Materials'), name_uz = COALESCE(NULLIF(name_uz,''), 'Xom ashyo') WHERE code = '1310' AND name_en = 'Raw Materials';
UPDATE accounts SET name = 'Незавершённое производство', name_en = COALESCE(NULLIF(name_en,''), 'Work in Progress'), name_uz = COALESCE(NULLIF(name_uz,''), 'Tugallanmagan ishlab chiqarish') WHERE code = '1320' AND name_en = 'Work in Progress';
UPDATE accounts SET name = 'Готовая продукция', name_en = COALESCE(NULLIF(name_en,''), 'Finished Goods'), name_uz = COALESCE(NULLIF(name_uz,''), 'Tayyor mahsulot') WHERE code = '1330' AND name_en = 'Finished Goods';
UPDATE accounts SET name = 'Товары для перепродажи', name_en = COALESCE(NULLIF(name_en,''), 'Goods for Resale'), name_uz = COALESCE(NULLIF(name_uz,''), 'Tovarlar (sotish uchun)') WHERE code = '1340' AND name_en = 'Goods for Resale';
UPDATE accounts SET name = 'Расходы будущих периодов', name_en = COALESCE(NULLIF(name_en,''), 'Prepaid Expenses'), name_uz = COALESCE(NULLIF(name_uz,''), 'Oldindan to''langan xarajatlar') WHERE code = '1400' AND name_en = 'Prepaid Expenses';
UPDATE accounts SET name = 'НДС к вычету', name_en = COALESCE(NULLIF(name_en,''), 'Input VAT'), name_uz = COALESCE(NULLIF(name_uz,''), 'QQS kirim') WHERE code = '1410' AND name_en = 'Input VAT';
UPDATE accounts SET name = 'Основные средства', name_en = COALESCE(NULLIF(name_en,''), 'Fixed Assets'), name_uz = COALESCE(NULLIF(name_uz,''), 'Asosiy vositalar') WHERE code = '1500' AND name_en = 'Fixed Assets';
UPDATE accounts SET name = 'Накопленная амортизация', name_en = COALESCE(NULLIF(name_en,''), 'Accumulated Depreciation'), name_uz = COALESCE(NULLIF(name_uz,''), 'Eskirish (amortizatsiya)') WHERE code = '1510' AND name_en = 'Accumulated Depreciation';
UPDATE accounts SET name = 'Нематериальные активы', name_en = COALESCE(NULLIF(name_en,''), 'Intangible Assets'), name_uz = COALESCE(NULLIF(name_uz,''), 'Nomoddiy aktivlar') WHERE code = '1600' AND name_en = 'Intangible Assets';
UPDATE accounts SET name = 'Затраты на строительство', name_en = COALESCE(NULLIF(name_en,''), 'Construction Costs'), name_uz = COALESCE(NULLIF(name_uz,''), 'Qurilish xarajatlari') WHERE code = '1700' AND name_en = 'Construction Costs';
UPDATE accounts SET name = 'Авансы сотрудникам', name_en = COALESCE(NULLIF(name_en,''), 'Employee Advances'), name_uz = COALESCE(NULLIF(name_uz,''), 'Xodim avans') WHERE code = '1730' AND name_en = 'Employee Advances';

-- Liabilities
UPDATE accounts SET name = 'Кредиторская задолженность', name_en = COALESCE(NULLIF(name_en,''), 'Accounts Payable'), name_uz = COALESCE(NULLIF(name_uz,''), 'Kreditorlar') WHERE code = '2000' AND name_en = 'Accounts Payable';
UPDATE accounts SET name = 'Начисленные расходы', name_en = COALESCE(NULLIF(name_en,''), 'Accrued Expenses'), name_uz = COALESCE(NULLIF(name_uz,''), 'Hisoblangan xarajatlar') WHERE code = '2100' AND name_en = 'Accrued Expenses';
UPDATE accounts SET name = 'Задолженность по зарплате', name_en = COALESCE(NULLIF(name_en,''), 'Wages Payable'), name_uz = COALESCE(NULLIF(name_uz,''), 'Ish haqi bo''yicha qarz') WHERE code = '2110' AND name_en = 'Wages Payable';
UPDATE accounts SET name = 'Задолженность по процентам', name_en = COALESCE(NULLIF(name_en,''), 'Interest Payable'), name_uz = COALESCE(NULLIF(name_uz,''), 'Foiz bo''yicha qarz') WHERE code = '2120' AND name_en = 'Interest Payable';
UPDATE accounts SET name = 'Налоговая задолженность', name_en = COALESCE(NULLIF(name_en,''), 'Tax Payable'), name_uz = COALESCE(NULLIF(name_uz,''), 'Soliq bo''yicha qarz') WHERE code = '2200' AND name_en = 'Tax Payable';
UPDATE accounts SET name = 'НДС к уплате', name_en = COALESCE(NULLIF(name_en,''), 'VAT Payable'), name_uz = COALESCE(NULLIF(name_uz,''), 'QQS chiqim') WHERE code = '2210' AND name_en = 'VAT Payable';
UPDATE accounts SET name = 'Налог на прибыль', name_en = COALESCE(NULLIF(name_en,''), 'Income Tax Payable'), name_uz = COALESCE(NULLIF(name_uz,''), 'Daromad solig''i') WHERE code = '2220' AND name_en = 'Income Tax Payable';
UPDATE accounts SET name = 'Транзит товаров (приём)', name_en = COALESCE(NULLIF(name_en,''), 'Stock Interim Receipt'), name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar qabul (tranzit)') WHERE code = '2230' AND name_en = 'Stock Interim Receipt';
UPDATE accounts SET name = 'Транзит товаров (отправка)', name_en = COALESCE(NULLIF(name_en,''), 'Stock Interim Delivery'), name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar jo''natish (tranzit)') WHERE code = '2231' AND name_en = 'Stock Interim Delivery';
UPDATE accounts SET name = 'Транзит товаров (приём)', name_en = COALESCE(NULLIF(name_en,''), 'Stock Interim Receipt'), name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar qabul (tranzit)') WHERE code = '2250' AND name_en = 'Stock Interim Receipt';
UPDATE accounts SET name = 'Транзит товаров (отправка)', name_en = COALESCE(NULLIF(name_en,''), 'Stock Interim Delivery'), name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar jo''natish (tranzit)') WHERE code = '2260' AND name_en = 'Stock Interim Delivery';
UPDATE accounts SET name = 'Доходы будущих периодов', name_en = COALESCE(NULLIF(name_en,''), 'Unearned Revenue'), name_uz = COALESCE(NULLIF(name_uz,''), 'Oldindan olingan daromad') WHERE code = '2300' AND name_en = 'Unearned Revenue';
UPDATE accounts SET name = 'Краткосрочные кредиты', name_en = COALESCE(NULLIF(name_en,''), 'Short-term Loans'), name_uz = COALESCE(NULLIF(name_uz,''), 'Qisqa muddatli kreditlar') WHERE code = '2400' AND name_en = 'Short-term Loans';
UPDATE accounts SET name = 'Долгосрочные кредиты', name_en = COALESCE(NULLIF(name_en,''), 'Long-term Loans'), name_uz = COALESCE(NULLIF(name_uz,''), 'Uzoq muddatli kreditlar') WHERE code = '2500' AND name_en = 'Long-term Loans';
UPDATE accounts SET name = 'Начисленные затраты на оборудование', name_en = COALESCE(NULLIF(name_en,''), 'Accrued Machine Costs'), name_uz = COALESCE(NULLIF(name_uz,''), 'Hisoblangan stanok xarajatlari') WHERE code = '2590' AND name_en = 'Accrued Machine Costs';

-- Equity
UPDATE accounts SET name = 'Собственный капитал', name_en = COALESCE(NULLIF(name_en,''), 'Owner''s Equity'), name_uz = COALESCE(NULLIF(name_uz,''), 'Eganing kapitali') WHERE code = '3000' AND (name_en = 'Owner''s Equity' OR name_en = 'Owners Equity');
UPDATE accounts SET name = 'Уставный капитал', name_en = COALESCE(NULLIF(name_en,''), 'Share Capital'), name_uz = COALESCE(NULLIF(name_uz,''), 'Ustav kapitali') WHERE code = '3100' AND name_en = 'Share Capital';
UPDATE accounts SET name = 'Нераспределённая прибыль', name_en = COALESCE(NULLIF(name_en,''), 'Retained Earnings'), name_uz = COALESCE(NULLIF(name_uz,''), 'Taqsimlanmagan foyda') WHERE code = '3200' AND name_en = 'Retained Earnings';
UPDATE accounts SET name = 'Прибыль текущего года', name_en = COALESCE(NULLIF(name_en,''), 'Current Year Earnings'), name_uz = COALESCE(NULLIF(name_uz,''), 'Joriy yil foydasi') WHERE code = '3300' AND name_en = 'Current Year Earnings';
UPDATE accounts SET name = 'Дивиденды', name_en = COALESCE(NULLIF(name_en,''), 'Dividends'), name_uz = COALESCE(NULLIF(name_uz,''), 'Dividendlar') WHERE code = '3400' AND name_en = 'Dividends';
UPDATE accounts SET name = 'Нераспределённая прибыль', name_en = COALESCE(NULLIF(name_en,''), 'Retained Earnings'), name_uz = COALESCE(NULLIF(name_uz,''), 'Taqsimlanmagan foyda') WHERE code = '3500' AND name_en = 'Retained Earnings';
UPDATE accounts SET name = 'Прибыль текущего года', name_en = COALESCE(NULLIF(name_en,''), 'Current Year Profit'), name_uz = COALESCE(NULLIF(name_uz,''), 'Joriy yil foydasi') WHERE code = '3600' AND name_en = 'Current Year Profit';

-- Revenue
UPDATE accounts SET name = 'Выручка от продаж', name_en = COALESCE(NULLIF(name_en,''), 'Sales Revenue'), name_uz = COALESCE(NULLIF(name_uz,''), 'Sotish daromadi') WHERE code = '4000' AND name_en = 'Sales Revenue';
UPDATE accounts SET name = 'Доход от услуг', name_en = COALESCE(NULLIF(name_en,''), 'Service Revenue'), name_uz = COALESCE(NULLIF(name_uz,''), 'Xizmat daromadi') WHERE code = '4100' AND name_en = 'Service Revenue';
UPDATE accounts SET name = 'Продажа продукции', name_en = COALESCE(NULLIF(name_en,''), 'Product Sales'), name_uz = COALESCE(NULLIF(name_uz,''), 'Mahsulot sotish') WHERE code = '4200' AND name_en = 'Product Sales';
UPDATE accounts SET name = 'Доход от строительства', name_en = COALESCE(NULLIF(name_en,''), 'Construction Revenue'), name_uz = COALESCE(NULLIF(name_uz,''), 'Qurilish daromadi') WHERE code = '4300' AND name_en = 'Construction Revenue';
UPDATE accounts SET name = 'Прочие доходы', name_en = COALESCE(NULLIF(name_en,''), 'Other Income'), name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa daromad') WHERE code = '4900' AND name_en = 'Other Income';
UPDATE accounts SET name = 'Процентный доход', name_en = COALESCE(NULLIF(name_en,''), 'Interest Income'), name_uz = COALESCE(NULLIF(name_uz,''), 'Foiz daromadi') WHERE code = '4910' AND name_en = 'Interest Income';
UPDATE accounts SET name = 'Курсовая прибыль', name_en = COALESCE(NULLIF(name_en,''), 'Foreign Exchange Gain'), name_uz = COALESCE(NULLIF(name_uz,''), 'Valyuta kursi foydasi') WHERE code = '4920' AND name_en = 'Foreign Exchange Gain';

-- COGS
UPDATE accounts SET name = 'Себестоимость продаж', name_en = COALESCE(NULLIF(name_en,''), 'Cost of Goods Sold'), name_uz = COALESCE(NULLIF(name_uz,''), 'Sotilgan tovarlar tannarxi') WHERE code = '5000' AND name_en = 'Cost of Goods Sold';
UPDATE accounts SET name = 'Прямые материалы', name_en = COALESCE(NULLIF(name_en,''), 'Direct Materials'), name_uz = COALESCE(NULLIF(name_uz,''), 'Bevosita materiallar') WHERE code = '5100' AND name_en = 'Direct Materials';
UPDATE accounts SET name = 'Прямая зарплата', name_en = COALESCE(NULLIF(name_en,''), 'Direct Labor'), name_uz = COALESCE(NULLIF(name_uz,''), 'Bevosita ish haqi') WHERE code = '5200' AND name_en = 'Direct Labor';
UPDATE accounts SET name = 'Производственные накладные', name_en = COALESCE(NULLIF(name_en,''), 'Manufacturing Overhead'), name_uz = COALESCE(NULLIF(name_uz,''), 'Ishlab chiqarish xarajatlari') WHERE code = '5300' AND name_en = 'Manufacturing Overhead';

-- Operating Expenses
UPDATE accounts SET name = 'Зарплата и оклады', name_en = COALESCE(NULLIF(name_en,''), 'Salaries and Wages'), name_uz = COALESCE(NULLIF(name_uz,''), 'Ish haqi xarajatlari') WHERE code = '6000' AND (name_en = 'Salaries and Wages' OR name_en = 'Salaries & Wages');
UPDATE accounts SET name = 'Аренда', name_en = COALESCE(NULLIF(name_en,''), 'Rent Expense'), name_uz = COALESCE(NULLIF(name_uz,''), 'Ijara xarajati') WHERE code = '6100' AND name_en = 'Rent Expense';
UPDATE accounts SET name = 'Коммунальные услуги', name_en = COALESCE(NULLIF(name_en,''), 'Utilities'), name_uz = COALESCE(NULLIF(name_uz,''), 'Kommunal xarajatlar') WHERE code = '6200' AND name_en = 'Utilities';
UPDATE accounts SET name = 'Канцелярские расходы', name_en = COALESCE(NULLIF(name_en,''), 'Office Supplies'), name_uz = COALESCE(NULLIF(name_uz,''), 'Ofis xarajatlari') WHERE code = '6300' AND name_en = 'Office Supplies';
UPDATE accounts SET name = 'Страхование', name_en = COALESCE(NULLIF(name_en,''), 'Insurance Expense'), name_uz = COALESCE(NULLIF(name_uz,''), 'Sug''urta xarajati') WHERE code = '6400' AND name_en = 'Insurance Expense';
UPDATE accounts SET name = 'Амортизация', name_en = COALESCE(NULLIF(name_en,''), 'Depreciation Expense'), name_uz = COALESCE(NULLIF(name_uz,''), 'Amortizatsiya xarajati') WHERE code = '6500' AND name_en = 'Depreciation Expense';
UPDATE accounts SET name = 'Реклама и маркетинг', name_en = COALESCE(NULLIF(name_en,''), 'Advertising & Marketing'), name_uz = COALESCE(NULLIF(name_uz,''), 'Reklama va marketing') WHERE code = '6600' AND (name_en = 'Advertising & Marketing' OR name_en = 'Advertising and Marketing');
UPDATE accounts SET name = 'Профессиональные услуги', name_en = COALESCE(NULLIF(name_en,''), 'Professional Fees'), name_uz = COALESCE(NULLIF(name_uz,''), 'Professional xizmatlar') WHERE code = '6700' AND name_en = 'Professional Fees';
UPDATE accounts SET name = 'Расходы на зарплату', name_en = COALESCE(NULLIF(name_en,''), 'Salary Expense'), name_uz = COALESCE(NULLIF(name_uz,''), 'Ish haqi xarajati') WHERE code = '6710' AND name_en = 'Salary Expense';
UPDATE accounts SET name = 'Начисленная зарплата', name_en = COALESCE(NULLIF(name_en,''), 'Accrued Salaries'), name_uz = COALESCE(NULLIF(name_uz,''), 'To''lanmagan ish haqi') WHERE code = '6720' AND name_en = 'Accrued Salaries';
UPDATE accounts SET name = 'Командировочные расходы', name_en = COALESCE(NULLIF(name_en,''), 'Travel & Entertainment'), name_uz = COALESCE(NULLIF(name_uz,''), 'Xizmat safari xarajatlari') WHERE code = '6800' AND (name_en = 'Travel & Entertainment' OR name_en = 'Travel and Entertainment');
UPDATE accounts SET name = 'Прочие операционные расходы', name_en = COALESCE(NULLIF(name_en,''), 'Miscellaneous Expense'), name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa operatsion xarajatlar') WHERE code = '6900' AND name_en = 'Miscellaneous Expense';
UPDATE accounts SET name = 'Корректировка запасов', name_en = COALESCE(NULLIF(name_en,''), 'Stock Adjustment'), name_uz = COALESCE(NULLIF(name_uz,''), 'Zaxira tuzatish') WHERE code = '6910' AND name_en = 'Stock Adjustment';
UPDATE accounts SET name = 'Списание разницы по оплате', name_en = COALESCE(NULLIF(name_en,''), 'Payment Difference Write-off'), name_uz = COALESCE(NULLIF(name_uz,''), 'To''lov farqi hisobdan chiqarish') WHERE code = '6950' AND name_en = 'Payment Difference Write-off';

-- Financial
UPDATE accounts SET name = 'Процентные расходы', name_en = COALESCE(NULLIF(name_en,''), 'Interest Expense'), name_uz = COALESCE(NULLIF(name_uz,''), 'Foiz xarajati') WHERE code = '7000' AND name_en = 'Interest Expense';
UPDATE accounts SET name = 'Банковские расходы', name_en = COALESCE(NULLIF(name_en,''), 'Bank Charges'), name_uz = COALESCE(NULLIF(name_uz,''), 'Bank xarajatlari') WHERE code = '7100' AND name_en = 'Bank Charges';
UPDATE accounts SET name = 'Курсовой убыток', name_en = COALESCE(NULLIF(name_en,''), 'Foreign Exchange Loss'), name_uz = COALESCE(NULLIF(name_uz,''), 'Valyuta kursi zarari') WHERE code = '7200' AND name_en = 'Foreign Exchange Loss';
UPDATE accounts SET name = 'Прочие расходы', name_en = COALESCE(NULLIF(name_en,''), 'Other Expenses'), name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa xarajatlar') WHERE code = '7900' AND name_en = 'Other Expenses';

-- Other
UPDATE accounts SET name = 'Прочие доходы', name_en = COALESCE(NULLIF(name_en,''), 'Other Income'), name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa daromadlar') WHERE code = '9110' AND name_en = 'Other Income';
UPDATE accounts SET name = 'Прочие убытки', name_en = COALESCE(NULLIF(name_en,''), 'Other Losses'), name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa zararlar') WHERE code = '9430' AND name_en = 'Other Losses';

-- Special accounts
UPDATE accounts SET name = 'Основные средства', name_uz = 'Asosiy vositalar', name_en = 'Fixed Assets' WHERE code = '0100' AND name = 'Asosiy vositalar';
UPDATE accounts SET name = 'Капитальные вложения', name_uz = 'Kapital qo''yilmalar', name_en = 'Capital Investments' WHERE code = '0810' AND name = 'Kapital qo''yilmalar';
