-- 472_subcontract_contract_number.sql — qurilish-v2 audit bug-fix.
--
-- SubcontractorsTab formasi «Shartnoma raqami» (contract_number) yuboradi,
-- lekin backend struct'da ham, jadvalda ham maydon yo'q edi — qiymat
-- ShouldBindJSON tomonidan jimgina tashlab yuborilardi (foydalanuvchi
-- kiritadi, saqlanadi deb o'ylaydi, aslida yo'qoladi). Ustun qo'shiladi;
-- handler structlariga maydon shu turkumda qo'shildi.
ALTER TABLE construction_subcontract ADD COLUMN IF NOT EXISTS contract_number VARCHAR(100);
