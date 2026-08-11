package response

import "testing"

// One case per rule family, plus the pass-through guarantees. The point is
// not to pin every dictionary entry but to prove each RULE fires — and,
// just as important, that non-English messages survive untouched.
func TestTranslateUserMessage(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// 1. exact
		{"exact sentence", "Invalid input", "Kiritilgan ma'lumotlarda xatolik bor — maydonlarni tekshirib qaytadan urinib ko'ring"},
		{"session tenant", "Tenant not found", "Hisobingizga qaytadan kiring"},

		// 2. machine prefix preserved, tail translated
		{"already-posted prefix", "ALREADY_POSTED: invoice already has a journal entry",
			"ALREADY_POSTED: bu hisob-faktura allaqachon o'tkazilgan"},
		{"over-payment prefix", "OVER_PAYMENT: amount exceeds the invoice's remaining balance",
			"OVER_PAYMENT: to'lov summasi hisob-faktura qoldig'idan oshib ketdi"},

		// 3. compositional Only-rule
		{"only draft estimates", "Only draft estimates can be modified",
			"Faqat qoralama holatidagi smetalarni o'zgartirish mumkin"},
		{"only posted entries", "Only posted entries can be reversed",
			"Faqat o'tkazilgan holatidagi jurnal yozuvlarini storno qilish mumkin"},
		{"only pending requisitions", "Only pending requisitions can be approved",
			"Faqat kutilayotgan holatidagi talabnomalarni tasdiqlash mumkin"},

		// 4. Failed-to: known phrase and generic fallback
		{"failed known", "Failed to record payment", "To'lovni qayd etib bo'lmadi"},
		{"failed generic", "Failed to recompute the flux capacitor", failedGeneric},

		// 5. Invalid-rules
		{"invalid entity id", "Invalid project ID", "Loyiha ochilmadi — sahifani yangilab, qaytadan urinib ko'ring"},
		{"invalid bare id", "Invalid ID", "Ma'lumot ochilmadi — sahifani yangilab, qaytadan urinib ko'ring"},
		{"invalid date format", "Invalid start_date format. Use YYYY-MM-DD",
			"Sanani to'g'ri kiriting (masalan: 2026-01-31)"},
		{"invalid unknown", "Invalid webhook signature", "Kiritilgan ma'lumot noto'g'ri: webhook signature"},

		// required / not found / already exists
		{"required known noun", "Warehouse is required", "Ombor ko'rsatilmagan — to'ldirib qaytadan urinib ko'ring"},
		{"required raw field", "assignee_id is required", "assignee_id ko'rsatilmagan — to'ldirib qaytadan urinib ko'ring"},
		{"not found known", "Sales order not found", "Savdo buyurtmasi topilmadi"},
		{"not found unknown noun", "Webhook not found", "Webhook topilmadi"},
		{"already exists", "Warehouse already exists", "Ombor allaqachon mavjud"},

		// 6. pass-through: already-localized and unmatched English
		{"uzbek untouched", "To'lov qayd etilmadi — sozlanmagan: debitorlik schyoti (4010)",
			"To'lov qayd etilmadi — sozlanmagan: debitorlik schyoti (4010)"},
		{"russian untouched", "Журнал продаж не настроен", "Журнал продаж не настроен"},
		{"unmatched english untouched", "The moon is in the wrong phase",
			"The moon is in the wrong phase"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := translateUserMessage(tc.in); got != tc.want {
				t.Errorf("translateUserMessage(%q)\n  got  %q\n  want %q", tc.in, got, tc.want)
			}
		})
	}
}

// NotFound used to blindly append " not found", so callers that passed a
// full sentence produced "Project not found not found". The append is now
// conditional; both caller styles must come out identical.
func TestNotFoundNoDoubleSuffix(t *testing.T) {
	if got := translateUserMessage("Project not found"); got != "Loyiha topilmadi" {
		t.Errorf("full-sentence caller: got %q", got)
	}
	if got := translateUserMessage("Project" + " not found"); got != "Loyiha topilmadi" {
		t.Errorf("bare-resource caller: got %q", got)
	}
}
