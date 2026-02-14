-- Remove all currencies except UZS, USD, RUB
DELETE FROM currencies WHERE code NOT IN ('UZS', 'USD', 'RUB');

-- Ensure UZS exists
INSERT INTO currencies (code, name, symbol, decimal_places)
VALUES ('UZS', 'Uzbek Som', 'so''m', 0)
ON CONFLICT (code) DO NOTHING;

-- Ensure RUB exists
INSERT INTO currencies (code, name, symbol, decimal_places)
VALUES ('RUB', 'Russian Ruble', '₽', 2)
ON CONFLICT (code) DO NOTHING;
