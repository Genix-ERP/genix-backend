package handler

import "testing"

// Daftar turi (447-migratsiya lug'ati) → reja operatsiyasi.
func TestValuationOpFor(t *testing.T) {
	cases := []struct {
		txType string
		qty    float64
		want   StockOperation
		ok     bool
		note   string
	}{
		{"receipt", 10, OpSupplierReceipt, true, "yetkazib beruvchidan kirim"},
		{"opening", 10, OpSupplierReceipt, true, "boshlang'ich qoldiq"},
		{"issue", -5, OpSaleIssue, true, "chiqim"},
		{"sale", -5, OpSaleIssue, true, "sotuv"},
		{"delivery", -5, OpSaleIssue, true, "yetkazib berish"},
		{"consume", -5, OpSaleIssue, true, "iste'mol"},
		{"production_out", -5, OpSaleIssue, true, "ishlab chiqarishga berish"},
		{"write_off", -2, OpScrap, true, "spisaniye"},
		{"scrap", -2, OpScrap, true, "brak"},
		{"production_scrap", -2, OpScrap, true, "ishlab chiqarish braki"},

		// Ishora yo'nalishni ajratadi.
		{"return", 3, OpCustomerReturn, true, "xaridordan qaytarish (kirim)"},
		{"return", -3, OpSupplierReturn, true, "yetkazib beruvchiga qaytarish (chiqim)"},
		{"adjustment", 4, OpCountSurplus, true, "ortiqcha"},
		{"adjustment", -4, OpCountShortage, true, "kamomad"},
		{"count", 1, OpCountSurplus, true, "inventarizatsiya: ortiqcha"},
		{"count", -1, OpCountShortage, true, "inventarizatsiya: kamomad"},

		// Registr va bo'shliqlar ahamiyatsiz.
		{"  RECEIPT  ", 1, OpSupplierReceipt, true, "normalizatsiya"},

		// Qatlam YARATILMAYDIGAN turlar.
		{"transfer", 5, "", false, "ichki ko'chirish — §4: provodka yo'q"},
		{"transfer_in", 5, "", false, "ichki ko'chirish"},
		{"transfer_out", -5, "", false, "ichki ko'chirish"},
		{"production_in", 5, "", false, "tayyor mahsulot tannarxi = BOM, v2"},
		{"production_complete", 5, "", false, "tayyor mahsulot, v2"},
		{"landed_cost", 0, "", false, "mavjud qatlam narxini o'zgartiradi, v2"},
		{"", 1, "", false, "bo'sh tur"},
		{"allaqachon_yoq", 1, "", false, "notanish tur"},
	}

	for _, c := range cases {
		got, ok := valuationOpFor(c.txType, c.qty)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: valuationOpFor(%q, %v) = (%q, %v), want (%q, %v)",
				c.note, c.txType, c.qty, got, ok, c.want, c.ok)
		}
	}
}

// Ichki ko'chirish hech qachon qatlam yaratmasligi kerak: tovar kompaniya
// ichida qoladi, tannarxi o'zgarmaydi. Qatlam yaratilsa zaxira qiymati ikki
// barobar bo'lib ketardi — shuning uchun buni alohida qayd etamiz.
func TestTransfersNeverCreateLayers(t *testing.T) {
	for _, tt := range []string{"transfer", "transfer_in", "transfer_out"} {
		for _, qty := range []float64{5, -5} {
			if _, ok := valuationOpFor(tt, qty); ok {
				t.Errorf("%q (qty %v) qatlam yaratmasligi kerak", tt, qty)
			}
		}
	}
}
