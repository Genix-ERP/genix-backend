-- Fix account translations: match on English name in 'name' column (not name_en)
-- Sets: name=Russian, name_en=English, name_uz=Uzbek

-- Assets
UPDATE accounts SET name_en = 'Cash', name_uz = COALESCE(NULLIF(name_uz,''), 'Kassa'), name = 'Касса' WHERE code = '1000' AND name = 'Cash';
UPDATE accounts SET name_en = 'Bank Account', name_uz = COALESCE(NULLIF(name_uz,''), 'Bank hisobi'), name = 'Банковский счёт' WHERE code = '1010' AND name = 'Bank Account';
UPDATE accounts SET name_en = 'Foreign Currency Account', name_uz = COALESCE(NULLIF(name_uz,''), 'Valyuta hisobi'), name = 'Валютный счёт' WHERE code = '1020' AND name = 'Foreign Currency Account';
UPDATE accounts SET name_en = 'Accounts Receivable', name_uz = COALESCE(NULLIF(name_uz,''), 'Debitorlar'), name = 'Дебиторская задолженность' WHERE code = '1100' AND name = 'Accounts Receivable';
UPDATE accounts SET name_en = 'Outstanding Receipts', name_uz = COALESCE(NULLIF(name_uz,''), 'Kutilayotgan tushumlar'), name = 'Ожидаемые поступления' WHERE code = '1150' AND name = 'Outstanding Receipts';
UPDATE accounts SET name_en = 'Outstanding Payments', name_uz = COALESCE(NULLIF(name_uz,''), 'Kutilayotgan to''lovlar'), name = 'Ожидаемые платежи' WHERE code = '1160' AND name = 'Outstanding Payments';
UPDATE accounts SET name_en = 'Allowance for Doubtful Accounts', name_uz = COALESCE(NULLIF(name_uz,''), 'Shubhali qarzlar zaxirasi'), name = 'Резерв по сомнительным долгам' WHERE code = '1210' AND name = 'Allowance for Doubtful Accounts';
UPDATE accounts SET name_en = 'Inventory', name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar-moddiy zaxiralar'), name = 'Запасы' WHERE code = '1300' AND name = 'Inventory';
UPDATE accounts SET name_en = 'Raw Materials', name_uz = COALESCE(NULLIF(name_uz,''), 'Xom ashyo'), name = 'Сырьё и материалы' WHERE code = '1310' AND name = 'Raw Materials';
UPDATE accounts SET name_en = 'Work in Progress', name_uz = COALESCE(NULLIF(name_uz,''), 'Tugallanmagan ishlab chiqarish'), name = 'Незавершённое производство' WHERE code = '1320' AND name = 'Work in Progress';
UPDATE accounts SET name_en = 'Finished Goods', name_uz = COALESCE(NULLIF(name_uz,''), 'Tayyor mahsulot'), name = 'Готовая продукция' WHERE code = '1330' AND name = 'Finished Goods';
UPDATE accounts SET name_en = 'Goods for Resale', name_uz = COALESCE(NULLIF(name_uz,''), 'Tovarlar (sotish uchun)'), name = 'Товары для перепродажи' WHERE code = '1340' AND name = 'Goods for Resale';
UPDATE accounts SET name_en = 'Prepaid Expenses', name_uz = COALESCE(NULLIF(name_uz,''), 'Oldindan to''langan xarajatlar'), name = 'Расходы будущих периодов' WHERE code = '1400' AND name = 'Prepaid Expenses';
UPDATE accounts SET name_en = 'Input VAT', name_uz = COALESCE(NULLIF(name_uz,''), 'QQS kirim'), name = 'НДС к вычету' WHERE code = '1410' AND name = 'Input VAT';
UPDATE accounts SET name_en = 'Fixed Assets', name_uz = COALESCE(NULLIF(name_uz,''), 'Asosiy vositalar'), name = 'Основные средства' WHERE code = '1500' AND name = 'Fixed Assets';
UPDATE accounts SET name_en = 'Accumulated Depreciation', name_uz = COALESCE(NULLIF(name_uz,''), 'Eskirish (amortizatsiya)'), name = 'Накопленная амортизация' WHERE code = '1510' AND name = 'Accumulated Depreciation';
UPDATE accounts SET name_en = 'Intangible Assets', name_uz = COALESCE(NULLIF(name_uz,''), 'Nomoddiy aktivlar'), name = 'Нематериальные активы' WHERE code = '1600' AND name = 'Intangible Assets';
UPDATE accounts SET name_en = 'Construction Costs', name_uz = COALESCE(NULLIF(name_uz,''), 'Qurilish xarajatlari'), name = 'Затраты на строительство' WHERE code = '1700' AND name = 'Construction Costs';
UPDATE accounts SET name_en = 'Employee Advances', name_uz = COALESCE(NULLIF(name_uz,''), 'Xodim avans'), name = 'Авансы сотрудникам' WHERE code = '1730' AND name = 'Employee Advances';

-- Liabilities
UPDATE accounts SET name_en = 'Accounts Payable', name_uz = COALESCE(NULLIF(name_uz,''), 'Kreditorlar'), name = 'Кредиторская задолженность' WHERE code = '2000' AND name = 'Accounts Payable';
UPDATE accounts SET name_en = 'Accrued Expenses', name_uz = COALESCE(NULLIF(name_uz,''), 'Hisoblangan xarajatlar'), name = 'Начисленные расходы' WHERE code = '2100' AND name = 'Accrued Expenses';
UPDATE accounts SET name_en = 'Wages Payable', name_uz = COALESCE(NULLIF(name_uz,''), 'Ish haqi bo''yicha qarz'), name = 'Задолженность по зарплате' WHERE code = '2110' AND name = 'Wages Payable';
UPDATE accounts SET name_en = 'Interest Payable', name_uz = COALESCE(NULLIF(name_uz,''), 'Foiz bo''yicha qarz'), name = 'Задолженность по процентам' WHERE code = '2120' AND name = 'Interest Payable';
UPDATE accounts SET name_en = 'Tax Payable', name_uz = COALESCE(NULLIF(name_uz,''), 'Soliq bo''yicha qarz'), name = 'Налоговая задолженность' WHERE code = '2200' AND name = 'Tax Payable';
UPDATE accounts SET name_en = 'VAT Payable', name_uz = COALESCE(NULLIF(name_uz,''), 'QQS chiqim'), name = 'НДС к уплате' WHERE code = '2210' AND name = 'VAT Payable';
UPDATE accounts SET name_en = 'Income Tax Payable', name_uz = COALESCE(NULLIF(name_uz,''), 'Daromad solig''i'), name = 'Налог на прибыль' WHERE code = '2220' AND name = 'Income Tax Payable';
UPDATE accounts SET name_en = 'Stock Interim Receipt', name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar qabul (tranzit)'), name = 'Транзит товаров (приём)' WHERE code = '2230' AND name = 'Stock Interim Receipt';
UPDATE accounts SET name_en = 'Stock Interim Delivery', name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar jo''natish (tranzit)'), name = 'Транзит товаров (отправка)' WHERE code = '2231' AND name = 'Stock Interim Delivery';
UPDATE accounts SET name_en = 'Stock Interim Receipt', name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar qabul (tranzit)'), name = 'Транзит товаров (приём)' WHERE code = '2250' AND name = 'Stock Interim Receipt';
UPDATE accounts SET name_en = 'Stock Interim Delivery', name_uz = COALESCE(NULLIF(name_uz,''), 'Tovar jo''natish (tranzit)'), name = 'Транзит товаров (отправка)' WHERE code = '2260' AND name = 'Stock Interim Delivery';
UPDATE accounts SET name_en = 'Unearned Revenue', name_uz = COALESCE(NULLIF(name_uz,''), 'Oldindan olingan daromad'), name = 'Доходы будущих периодов' WHERE code = '2300' AND name = 'Unearned Revenue';
UPDATE accounts SET name_en = 'Short-term Loans', name_uz = COALESCE(NULLIF(name_uz,''), 'Qisqa muddatli kreditlar'), name = 'Краткосрочные кредиты' WHERE code = '2400' AND name = 'Short-term Loans';
UPDATE accounts SET name_en = 'Long-term Loans', name_uz = COALESCE(NULLIF(name_uz,''), 'Uzoq muddatli kreditlar'), name = 'Долгосрочные кредиты' WHERE code = '2500' AND name = 'Long-term Loans';
UPDATE accounts SET name_en = 'Accrued Machine Costs', name_uz = COALESCE(NULLIF(name_uz,''), 'Hisoblangan stanok xarajatlari'), name = 'Начисленные затраты на оборудование' WHERE code = '2590' AND name = 'Accrued Machine Costs';

-- Equity
UPDATE accounts SET name_en = name, name_uz = COALESCE(NULLIF(name_uz,''), 'Eganing kapitali'), name = 'Собственный капитал' WHERE code = '3000' AND (name = 'Owner''s Equity' OR name = 'Owners Equity');
UPDATE accounts SET name_en = 'Share Capital', name_uz = COALESCE(NULLIF(name_uz,''), 'Ustav kapitali'), name = 'Уставный капитал' WHERE code = '3100' AND name = 'Share Capital';
UPDATE accounts SET name_en = 'Retained Earnings', name_uz = COALESCE(NULLIF(name_uz,''), 'Taqsimlanmagan foyda'), name = 'Нераспределённая прибыль' WHERE code = '3200' AND name = 'Retained Earnings';
UPDATE accounts SET name_en = 'Current Year Earnings', name_uz = COALESCE(NULLIF(name_uz,''), 'Joriy yil foydasi'), name = 'Прибыль текущего года' WHERE code = '3300' AND name = 'Current Year Earnings';
UPDATE accounts SET name_en = 'Dividends', name_uz = COALESCE(NULLIF(name_uz,''), 'Dividendlar'), name = 'Дивиденды' WHERE code = '3400' AND name = 'Dividends';
UPDATE accounts SET name_en = 'Retained Earnings', name_uz = COALESCE(NULLIF(name_uz,''), 'Taqsimlanmagan foyda'), name = 'Нераспределённая прибыль' WHERE code = '3500' AND name = 'Retained Earnings';
UPDATE accounts SET name_en = 'Current Year Profit', name_uz = COALESCE(NULLIF(name_uz,''), 'Joriy yil foydasi'), name = 'Прибыль текущего года' WHERE code = '3600' AND name = 'Current Year Profit';

-- Revenue
UPDATE accounts SET name_en = 'Sales Revenue', name_uz = COALESCE(NULLIF(name_uz,''), 'Sotish daromadi'), name = 'Выручка от продаж' WHERE code = '4000' AND name = 'Sales Revenue';
UPDATE accounts SET name_en = 'Service Revenue', name_uz = COALESCE(NULLIF(name_uz,''), 'Xizmat daromadi'), name = 'Доход от услуг' WHERE code = '4100' AND name = 'Service Revenue';
UPDATE accounts SET name_en = 'Product Sales', name_uz = COALESCE(NULLIF(name_uz,''), 'Mahsulot sotish'), name = 'Продажа продукции' WHERE code = '4200' AND name = 'Product Sales';
UPDATE accounts SET name_en = 'Construction Revenue', name_uz = COALESCE(NULLIF(name_uz,''), 'Qurilish daromadi'), name = 'Доход от строительства' WHERE code = '4300' AND name = 'Construction Revenue';
UPDATE accounts SET name_en = 'Other Income', name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa daromad'), name = 'Прочие доходы' WHERE code = '4900' AND name = 'Other Income';
UPDATE accounts SET name_en = 'Interest Income', name_uz = COALESCE(NULLIF(name_uz,''), 'Foiz daromadi'), name = 'Процентный доход' WHERE code = '4910' AND name = 'Interest Income';
UPDATE accounts SET name_en = 'Foreign Exchange Gain', name_uz = COALESCE(NULLIF(name_uz,''), 'Valyuta kursi foydasi'), name = 'Курсовая прибыль' WHERE code = '4920' AND name = 'Foreign Exchange Gain';

-- COGS
UPDATE accounts SET name_en = 'Cost of Goods Sold', name_uz = COALESCE(NULLIF(name_uz,''), 'Sotilgan tovarlar tannarxi'), name = 'Себестоимость продаж' WHERE code = '5000' AND name = 'Cost of Goods Sold';
UPDATE accounts SET name_en = 'Direct Materials', name_uz = COALESCE(NULLIF(name_uz,''), 'Bevosita materiallar'), name = 'Прямые материалы' WHERE code = '5100' AND name = 'Direct Materials';
UPDATE accounts SET name_en = 'Direct Labor', name_uz = COALESCE(NULLIF(name_uz,''), 'Bevosita ish haqi'), name = 'Прямая зарплата' WHERE code = '5200' AND name = 'Direct Labor';
UPDATE accounts SET name_en = 'Manufacturing Overhead', name_uz = COALESCE(NULLIF(name_uz,''), 'Ishlab chiqarish xarajatlari'), name = 'Производственные накладные' WHERE code = '5300' AND name = 'Manufacturing Overhead';

-- Operating Expenses
UPDATE accounts SET name_en = name, name_uz = COALESCE(NULLIF(name_uz,''), 'Ish haqi xarajatlari'), name = 'Зарплата и оклады' WHERE code = '6000' AND (name = 'Salaries and Wages' OR name = 'Salaries & Wages');
UPDATE accounts SET name_en = 'Rent Expense', name_uz = COALESCE(NULLIF(name_uz,''), 'Ijara xarajati'), name = 'Аренда' WHERE code = '6100' AND name = 'Rent Expense';
UPDATE accounts SET name_en = 'Utilities', name_uz = COALESCE(NULLIF(name_uz,''), 'Kommunal xarajatlar'), name = 'Коммунальные услуги' WHERE code = '6200' AND name = 'Utilities';
UPDATE accounts SET name_en = 'Office Supplies', name_uz = COALESCE(NULLIF(name_uz,''), 'Ofis xarajatlari'), name = 'Канцелярские расходы' WHERE code = '6300' AND name = 'Office Supplies';
UPDATE accounts SET name_en = 'Insurance Expense', name_uz = COALESCE(NULLIF(name_uz,''), 'Sug''urta xarajati'), name = 'Страхование' WHERE code = '6400' AND name = 'Insurance Expense';
UPDATE accounts SET name_en = 'Depreciation Expense', name_uz = COALESCE(NULLIF(name_uz,''), 'Amortizatsiya xarajati'), name = 'Амортизация' WHERE code = '6500' AND name = 'Depreciation Expense';
UPDATE accounts SET name_en = name, name_uz = COALESCE(NULLIF(name_uz,''), 'Reklama va marketing'), name = 'Реклама и маркетинг' WHERE code = '6600' AND (name = 'Advertising & Marketing' OR name = 'Advertising and Marketing');
UPDATE accounts SET name_en = 'Professional Fees', name_uz = COALESCE(NULLIF(name_uz,''), 'Professional xizmatlar'), name = 'Профессиональные услуги' WHERE code = '6700' AND name = 'Professional Fees';
UPDATE accounts SET name_en = 'Salary Expense', name_uz = COALESCE(NULLIF(name_uz,''), 'Ish haqi xarajati'), name = 'Расходы на зарплату' WHERE code = '6710' AND name = 'Salary Expense';
UPDATE accounts SET name_en = 'Accrued Salaries', name_uz = COALESCE(NULLIF(name_uz,''), 'To''lanmagan ish haqi'), name = 'Начисленная зарплата' WHERE code = '6720' AND name = 'Accrued Salaries';
UPDATE accounts SET name_en = name, name_uz = COALESCE(NULLIF(name_uz,''), 'Xizmat safari xarajatlari'), name = 'Командировочные расходы' WHERE code = '6800' AND (name = 'Travel & Entertainment' OR name = 'Travel and Entertainment');
UPDATE accounts SET name_en = 'Miscellaneous Expense', name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa operatsion xarajatlar'), name = 'Прочие операционные расходы' WHERE code = '6900' AND name = 'Miscellaneous Expense';
UPDATE accounts SET name_en = 'Stock Adjustment', name_uz = COALESCE(NULLIF(name_uz,''), 'Zaxira tuzatish'), name = 'Корректировка запасов' WHERE code = '6910' AND name = 'Stock Adjustment';
UPDATE accounts SET name_en = 'Payment Difference Write-off', name_uz = COALESCE(NULLIF(name_uz,''), 'To''lov farqi hisobdan chiqarish'), name = 'Списание разницы по оплате' WHERE code = '6950' AND name = 'Payment Difference Write-off';

-- Financial
UPDATE accounts SET name_en = 'Interest Expense', name_uz = COALESCE(NULLIF(name_uz,''), 'Foiz xarajati'), name = 'Процентные расходы' WHERE code = '7000' AND name = 'Interest Expense';
UPDATE accounts SET name_en = 'Bank Charges', name_uz = COALESCE(NULLIF(name_uz,''), 'Bank xarajatlari'), name = 'Банковские расходы' WHERE code = '7100' AND name = 'Bank Charges';
UPDATE accounts SET name_en = 'Foreign Exchange Loss', name_uz = COALESCE(NULLIF(name_uz,''), 'Valyuta kursi zarari'), name = 'Курсовой убыток' WHERE code = '7200' AND name = 'Foreign Exchange Loss';
UPDATE accounts SET name_en = 'Other Expenses', name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa xarajatlar'), name = 'Прочие расходы' WHERE code = '7900' AND name = 'Other Expenses';

-- Other
UPDATE accounts SET name_en = 'Other Income', name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa daromadlar'), name = 'Прочие доходы' WHERE code = '9110' AND name = 'Other Income';
UPDATE accounts SET name_en = 'Other Losses', name_uz = COALESCE(NULLIF(name_uz,''), 'Boshqa zararlar'), name = 'Прочие убытки' WHERE code = '9430' AND name = 'Other Losses';
