package response

import "strings"

// Uzbek at the API boundary.
//
// The web and mobile clients show error.Message to the user as-is (see
// genix-front src/utils/apiErrors.js — only error CODES are interpreted;
// the message is display text). Handlers emit ~2 500 distinct messages from
// ~6 800 call sites, almost all English — so users saw "Nothing delivered
// and uninvoiced on this order" in an interface that is otherwise Uzbek.
//
// Rewriting every call site would be a 6 800-line diff that regresses the
// next time anyone writes a handler. Instead every message passes through
// translateUserMessage here, in the one place all of them already flow
// through (Error / ErrorWithDetails / ConflictWithData):
//
//   1. exact — curated sentences (auth flow, business refusals, defaults).
//   2. "PREFIX: rest" — machine-readable prefixes (ALREADY_POSTED:,
//      OVER_PAYMENT:) keep the prefix, the rest is translated. Clients that
//      switch on the prefix keep working.
//   3. "Only <status> <things> can be <done>" — compositional: status,
//      noun and verb translate independently, covering the whole family.
//   4. "Failed to <do X>" — 500-class; known phrases translate, the rest
//      collapse to one honest generic. The English tail was for logs, and
//      logs still get it (handlers log before responding).
//   5. "Invalid <x>" / "<x> is required" / "<x> not found" /
//      "<x> already exists" — suffix/prefix rules with a shared noun map.
//   6. Anything unrecognized passes through UNCHANGED — messages already
//      written in Uzbek or Russian match no English pattern and survive
//      untouched, and a missed English string stays English rather than
//      becoming a wrong translation.
//
// Error CODES are never touched.

// nouns maps the resource/entity names used in messages to Uzbek. Keys are
// matched case-insensitively via nounUz.
var nouns = map[string]string{
	"account":                  "Schyot",
	"accounting period":        "Hisob davri",
	"act":                      "Akt",
	"act type":                 "Akt turi",
	"activity":                 "Faoliyat",
	"agent":                    "Agent",
	"amendment":                "O'zgartirish (qo'shimcha kelishuv)",
	"app":                      "Ilova",
	"approval workflow":        "Tasdiqlash jarayoni",
	"assignment":               "Biriktirish",
	"attachment":               "Ilova fayli",
	"attendance record":        "Davomat yozuvi",
	"bank account":             "Bank hisobi",
	"bank transaction":         "Bank amaliyoti",
	"bid":                      "Taklif",
	"blanket order":            "Ramka buyurtmasi",
	"board":                    "Doska",
	"bom":                      "Retseptura (BOM)",
	"bom line":                 "Retseptura qatori",
	"bom operation":            "Retseptura amali",
	"budget":                   "Byudjet",
	"budget line":              "Byudjet qatori",
	"building":                 "Bino",
	"calculation":              "Hisob-kitob",
	"call log":                 "Qo'ng'iroq yozuvi",
	"carrier":                  "Tashuvchi",
	"cash order":               "Kassa orderi",
	"cash register":            "Kassa",
	"cash transaction":         "Kassa amaliyoti",
	"category":                 "Kategoriya",
	"checklist item":           "Nazorat bandi",
	"closed period":            "Yopilgan davr",
	"closing":                  "Yopilish",
	"column":                   "Ustun",
	"comment":                  "Izoh",
	"company":                  "Kompaniya",
	"company profile":          "Kompaniya profili",
	"company tax rate":         "Kompaniya soliq stavkasi",
	"construction project":     "Qurilish loyihasi",
	"contact":                  "Kontragent",
	"contract":                 "Shartnoma",
	"conversation":             "Suhbat",
	"counterparty":             "Kontragent",
	"credit note":              "Kredit-nota",
	"currency":                 "Valyuta",
	"customer":                 "Mijoz",
	"customer followup status": "Mijoz kuzatuv holati",
	"daily log":                "Kunlik jurnal",
	"daily report":             "Kunlik hisobot",
	"debit note":               "Debet-nota",
	"deduction":                "Ushlab qolish",
	"delivery order":           "Yetkazib berish hujjati",
	"department":               "Bo'lim",
	"dependency":               "Bog'liqlik",
	"destination warehouse":    "Qabul qiluvchi ombor",
	"discount":                 "Chegirma",
	"dropship order":           "Dropship buyurtmasi",
	"e-invoice":                "Elektron hisob-faktura",
	"employee":                 "Xodim",
	"employee assignment":      "Xodim biriktiruvi",
	"employee contract":        "Mehnat shartnomasi",
	"employee profile":         "Xodim profili",
	"employee tax":             "Xodim solig'i",
	"equipment":                "Uskuna",
	"estimate":                 "Smeta",
	"estimate line":            "Smeta qatori",
	"expense":                  "Xarajat",
	"expense line":             "Xarajat qatori",
	"file":                     "Fayl",
	"fiscal period":            "Moliyaviy davr",
	"fiscal year":              "Moliyaviy yil",
	"follow-up level":          "Kuzatuv darajasi",
	"followup level":           "Kuzatuv darajasi",
	"forma 2 act":              "Forma-2 akti",
	"forma 19 act":             "Forma-19 akti",
	"goods receipt":            "Kirim hujjati",
	"import":                   "Import",
	"inventory lot":            "Partiya",
	"invoice":                  "Hisob-faktura",
	"item":                     "Element",
	"iteration":                "Iteratsiya",
	"job position":             "Lavozim",
	"journal":                  "Jurnal",
	"journal entry":            "Jurnal yozuvi",
	"journal payment method":   "Jurnal to'lov usuli",
	"lead":                     "Lid",
	"leave balance":            "Ta'til qoldig'i",
	"leave request":            "Ta'til so'rovi",
	"line":                     "Qator",
	"link":                     "Havola",
	"linked record":            "Bog'langan yozuv",
	"loan":                     "Qarz",
	"location":                 "Joylashuv",
	"lost reason":              "Yo'qotish sababi",
	"maintenance task":         "Texnik xizmat vazifasi",
	"manufacturing category":   "Ishlab chiqarish kategoriyasi",
	"material":                 "Material",
	"material request":         "Material so'rovi",
	"material usage":           "Material sarfi",
	"notification":             "Bildirishnoma",
	"operation type":           "Amaliyot turi",
	"opportunity":              "Imkoniyat",
	"organization":             "Tashkilot",
	"package":                  "Paket",
	"package content":          "Paket tarkibi",
	"package type":             "Paket turi",
	"packaging material":       "Qadoqlash materiali",
	"partner":                  "Hamkor",
	"payment":                  "To'lov",
	"payment term":             "To'lov sharti",
	"payroll entry":            "Ish haqi yozuvi",
	"payroll period":           "Ish haqi davri",
	"pipeline":                 "Voronka",
	"pipeline stage":           "Voronka bosqichi",
	"plan":                     "Tarif",
	"platform user":            "Platforma foydalanuvchisi",
	"price history":            "Narx tarixi",
	"pricelist":                "Narxlar ro'yxati",
	"procurement rule":         "Xarid qoidasi",
	"product":                  "Mahsulot",
	"product packaging":        "Mahsulot qadog'i",
	"product-vendor link":      "Mahsulot-ta'minotchi bog'lamasi",
	"production order":         "Ishlab chiqarish buyurtmasi",
	"project":                  "Loyiha",
	"prompt":                   "Prompt",
	"purchase invoice":         "Kirim hisob-fakturasi",
	"purchase order":           "Xarid buyurtmasi",
	"purchase requisition":     "Xarid talabnomasi",
	"purchase return":          "Xaridni qaytarish",
	"quotation":                "Narx taklifi",
	"recommendation":           "Tavsiya",
	"reconciliation":           "Akt sverka",
	"reconciliation act":       "Akt sverka",
	"release":                  "Chiqarish",
	"reorder rule":             "Qayta buyurtma qoidasi",
	"reservation":              "Rezerv",
	"resource":                 "Ma'lumot",
	"response":                 "Javob",
	"responsible employee":     "Mas'ul xodim",
	"rfq":                      "Narx so'rovi (RFQ)",
	"role":                     "Rol",
	"rule":                     "Qoida",
	"sales invoice":            "Hisob-faktura",
	"sales order":              "Savdo buyurtmasi",
	"sales return":             "Savdo qaytarishi",
	"scrap order":              "Chiqit buyurtmasi",
	"section":                  "Bo'lim",
	"shipment":                 "Jo'natma",
	"snapshot":                 "Saqlangan nusxa",
	"source building":          "Manba bino",
	"source estimate":          "Manba smeta",
	"source project":           "Manba loyiha",
	"source warehouse":         "Jo'natuvchi ombor",
	"stage":                    "Bosqich",
	"stock count":              "Inventarizatsiya",
	"stock count line":         "Inventarizatsiya qatori",
	"stock operation":          "Ombor amaliyoti",
	"stock operation line":     "Ombor amaliyoti qatori",
	"sub-stage":                "Quyi bosqich",
	"subcontract":              "Subpudrat shartnomasi",
	"subscription":             "Obuna",
	"supplier":                 "Ta'minotchi",
	"target project":           "Maqsad loyiha",
	"task":                     "Vazifa",
	"tax rate":                 "Soliq stavkasi",
	"tax report period":        "Soliq hisoboti davri",
	"team member":              "Jamoa a'zosi",
	"template":                 "Shablon",
	"tenant":                   "Kompaniya",
	"tender":                   "Tender",
	"top-up":                   "To'ldirish",
	"transaction":              "Amaliyot",
	"transfer":                 "Ko'chirish hujjati",
	"unit of measure":          "O'lchov birligi",
	"uploaded file":            "Yuklangan fayl",
	"user":                     "Foydalanuvchi",
	"variant":                  "Variant",
	"vendor":                   "Ta'minotchi",
	"vendor price":             "Ta'minotchi narxi",
	"vendor record":            "Ta'minotchi yozuvi",
	"vendor settings":          "Ta'minotchi sozlamalari",
	"warehouse":                "Ombor",
	"wbs item":                 "WBS elementi",
	"work":                     "Ish",
	"work center":              "Ish markazi",
	"work order":               "Ish buyurtmasi",
	"workflow":                 "Jarayon",
	"workflow log":             "Jarayon jurnali",
	"workflow rule":            "Jarayon qoidasi",
}

// onlyStatuses / onlyNouns / onlyVerbs compose the "Only <status> <things>
// can be <done>" family: 142 distinct messages, three small vocabularies.
var onlyStatuses = map[string]string{
	"draft":               "qoralama",
	"posted":              "o'tkazilgan",
	"pending":             "kutilayotgan",
	"submitted":           "yuborilgan",
	"approved":            "tasdiqlangan",
	"confirmed":           "tasdiqlangan",
	"active":              "faol",
	"cancelled":           "bekor qilingan",
	"rejected":            "rad etilgan",
	"shipped":             "jo'natilgan",
	"sent":                "yuborilgan",
	"outgoing":            "chiquvchi",
	"paid":                "to'langan",
	"completed":           "yakunlangan",
	"open":                "ochiq",
	"closed":              "yopilgan",
	"draft or submitted":  "qoralama yoki yuborilgan",
	"draft or rejected":   "qoralama yoki rad etilgan",
	"draft or cancelled":  "qoralama yoki bekor qilingan",
	"draft or approved":   "qoralama yoki tasdiqlangan",
	"draft or active":     "qoralama yoki faol",
	"signed or confirmed": "imzolangan yoki tasdiqlangan",
	"ready":               "tayyor",
	"quotation":           "taklif",
	"in_transit":          "yo'lda",
	"pending_approval":    "tasdiqlash kutilayotgan",
	"processing":          "jarayondagi",
	"delivered":           "yetkazilgan",
	"ordered":             "buyurtma qilingan",
	"received":            "qabul qilingan",
	"partial":             "qisman to'langan",
}

var onlyNouns = map[string]string{
	"estimates":       "smetalarni",
	"payments":        "to'lovlarni",
	"entries":         "jurnal yozuvlarini",
	"acts":            "aktlarni",
	"invoices":        "hisob-fakturalarni",
	"returns":         "qaytarishlarni",
	"reservations":    "rezervlarni",
	"requisitions":    "talabnomalarni",
	"bids":            "takliflarni",
	"tenders":         "tenderlarni",
	"orders":          "buyurtmalarni",
	"contracts":       "shartnomalarni",
	"rfqs":            "narx so'rovlarini",
	"scrap orders":    "chiqit buyurtmalarini",
	"operations":      "amaliyotlarni",
	"goods receipts":  "kirim hujjatlarini",
	"expense lines":   "xarajat qatorlarini",
	"blanket orders":  "ramka buyurtmalarini",
	"releases":        "chiqarishlarni",
	"transfers":       "ko'chirishlarni",
	"expenses":        "xarajatlarni",
	"leads":           "lidlarni",
	"tasks":           "vazifalarni",
	"periods":         "davrlarni",
	"documents":       "hujjatlarni",
	"work orders":     "ish buyurtmalarini",
	"delivery orders": "yetkazib berish hujjatlarini",
}

var onlyVerbs = map[string]string{
	"modified":               "o'zgartirish",
	"edited":                 "tahrirlash",
	"updated":                "yangilash",
	"deleted":                "o'chirish",
	"confirmed":              "tasdiqlash",
	"approved":               "tasdiqlash",
	"rejected":               "rad etish",
	"posted":                 "o'tkazish",
	"reversed":               "storno qilish",
	"reset to draft":         "qoralamaga qaytarish",
	"submitted":              "yuborish",
	"submitted for approval": "tasdiqlashga yuborish",
	"cancelled":              "bekor qilish",
	"validated":              "tasdiqlash",
	"sent":                   "yuborish",
	"activated":              "faollashtirish",
	"received":               "qabul qilish",
	"marked as received":     "qabul qilingan deb belgilash",
	"closed":                 "yopish",
	"reopened":               "qayta ochish",
	"paid":                   "to'lash",
	"invoiced":               "fakturalash",
	"started":                "boshlash",
	"completed":              "yakunlash",
}

// failedPhrases: "Failed to <phrase>" for the operations users actually see
// fail. Anything not listed collapses to failedGeneric — the English tail
// existed for logs, and handlers still log it before responding.
const failedGeneric = "Nimadir xato ketdi. Birozdan so'ng qaytadan urinib ko'ring."

var failedPhrases = map[string]string{
	"start transaction":                  "Nimadir xato ketdi. Birozdan so'ng qaytadan urinib ko'ring.",
	"begin transaction":                  "Nimadir xato ketdi. Birozdan so'ng qaytadan urinib ko'ring.",
	"commit transaction":                 "O'zgarishlarni saqlab bo'lmadi. Qaytadan urinib ko'ring.",
	"commit":                             "O'zgarishlarni saqlab bo'lmadi. Qaytadan urinib ko'ring.",
	"post journal entry":                 "Jurnal yozuvini o'tkazib bo'lmadi",
	"create journal entry":               "Jurnal yozuvini yaratib bo'lmadi",
	"update journal entry":               "Jurnal yozuvini yangilab bo'lmadi",
	"reverse journal entry":              "Jurnal yozuvini storno qilib bo'lmadi",
	"reset journal entry":                "Jurnal yozuvini qoralamaga qaytarib bo'lmadi",
	"record payment":                     "To'lovni qayd etib bo'lmadi",
	"reset invoice":                      "Hisob-fakturani qoralamaga qaytarib bo'lmadi",
	"create invoice":                     "Hisob-faktura yaratib bo'lmadi",
	"update invoice":                     "Hisob-fakturani yangilab bo'lmadi",
	"post invoice":                       "Hisob-fakturani o'tkazib bo'lmadi",
	"confirm invoice":                    "Hisob-fakturani tasdiqlab bo'lmadi",
	"post vendor payment journal entry":  "Ta'minotchi to'lovini o'tkazib bo'lmadi",
	"post payroll accrual journal entry": "Ish haqi hisoblanmasini o'tkazib bo'lmadi",
	"confirm scrap order":                "Chiqit buyurtmasini tasdiqlab bo'lmadi",
	"transfer inventory":                 "Zaxirani ko'chirib bo'lmadi",
	"adjust inventory":                   "Zaxirani to'g'irlab bo'lmadi",
	"move task":                          "Vazifani ko'chirib bo'lmadi",
	"load material request":              "Material so'rovini yuklab bo'lmadi",
	"create purchase order":              "Xarid buyurtmasini yaratib bo'lmadi",
	"update purchase order":              "Xarid buyurtmasini yangilab bo'lmadi",
	"receive purchase order":             "Xarid buyurtmasini qabul qilib bo'lmadi",
	"create currency":                    "Valyutani yaratib bo'lmadi",
	"update currency":                    "Valyutani yangilab bo'lmadi",
	"complete production order":          "Ishlab chiqarish buyurtmasini yakunlab bo'lmadi",
	"start production order":             "Ishlab chiqarish buyurtmasini boshlab bo'lmadi",
	"update schedule":                    "Jadvalni yangilab bo'lmadi",
	"ship transfer":                      "Ko'chirishni jo'natib bo'lmadi",
	"receive transfer":                   "Ko'chirishni qabul qilib bo'lmadi",
	"post act costs":                     "Akt xarajatlarini o'tkazib bo'lmadi",
	"reverse act posting":                "Akt o'tkazmasini storno qilib bo'lmadi",
	"delete fiscal year":                 "Moliyaviy yilni o'chirib bo'lmadi",
	"verify fiscal period":               "Moliyaviy davrni tekshirib bo'lmadi",
	"update bank account":                "Bank hisobini yangilab bo'lmadi",
	"select winner":                      "G'olibni tanlab bo'lmadi",
	"save file":                          "Faylni saqlab bo'lmadi",
	"read file":                          "Faylni o'qib bo'lmadi",
	"upload file":                        "Faylni yuklab bo'lmadi",
	"process payroll":                    "Ish haqini hisoblab bo'lmadi",
	"create payment":                     "To'lovni yaratib bo'lmadi",
	"confirm payment":                    "To'lovni tasdiqlab bo'lmadi",
	"create expense":                     "Xarajatni yaratib bo'lmadi",
	"update expense":                     "Xarajatni yangilab bo'lmadi",
	"pay expense":                        "Xarajatni to'lab bo'lmadi",
	"create contact":                     "Kontragentni yaratib bo'lmadi",
	"update contact":                     "Kontragentni yangilab bo'lmadi",
	"create product":                     "Mahsulotni yaratib bo'lmadi",
	"update product":                     "Mahsulotni yangilab bo'lmadi",
	"create warehouse":                   "Omborni yaratib bo'lmadi",
	"update warehouse":                   "Omborni yangilab bo'lmadi",
	"create user":                        "Foydalanuvchini yaratib bo'lmadi",
	"update user":                        "Foydalanuvchini yangilab bo'lmadi",
	"send OTP":                           "SMS-kod yuborib bo'lmadi",
	"update email":                       "Emailni yangilab bo'lmadi",
	"update phone number":                "Telefon raqamini yangilab bo'lmadi",
	"update work order":                  "Ish buyurtmasini yangilab bo'lmadi",
	"update task":                        "Vazifani yangilab bo'lmadi",
	"create task":                        "Vazifani yaratib bo'lmadi",
	"delete iteration":                   "Iteratsiyani o'chirib bo'lmadi",
	"freeze iteration":                   "Iteratsiyani muzlatib bo'lmadi",
	"post dividend":                      "Dividendni o'tkazib bo'lmadi",
	"post":                               "Hujjatni o'tkazib bo'lmadi",
}

// exact: curated whole-message translations — the sentences pattern rules
// cannot compose, plus every default this package emits on its own.
var exact = map[string]string{
	// Session / auth plumbing. "Tenant not found" is what the user sees when
	// their JWT no longer resolves — it is a session problem, not a lookup.
	"Tenant not found":                                     "Hisobingizga qaytadan kiring",
	"Invalid tenant":                                       "Hisobingizga qaytadan kiring",
	"Tenant not resolved":                                  "Hisobingizga qaytadan kiring",
	"User not authenticated":                               "Avval tizimga kiring",
	"Unauthorized":                                         "Bu amal uchun sizga ruxsat berilmagan",
	"Access denied":                                        "Bu amal uchun sizga ruxsat berilmagan",
	"Authentication required":                              "Avval tizimga kiring",
	"Authorization header required":                        "Avval tizimga kiring",
	"Invalid authorization header format":                  "Kirishda xatolik — qaytadan kiring",
	"Invalid authentication":                               "Kirish muddati tugagan — qaytadan kiring",
	"System administrator access required":                 "Bu amal uchun tizim administratori huquqi kerak",
	"Permission check failed":                              "Ruxsatni tekshirishda xatolik — qaytadan urinib ko'ring",
	"Read-only support session: mutations are not allowed": "Bu rejimda faqat ko'rish mumkin — o'zgartirib bo'lmaydi",
	"An unexpected error occurred":                         "Nimadir xato ketdi — qaytadan urinib ko'ring",
	"Rate limit exceeded":                                  "Juda ko'p urinish — birozdan so'ng qaytadan urinib ko'ring",
	"Rate limit exceeded. Please try again later.":         "Juda ko'p urinish — birozdan so'ng qaytadan urinib ko'ring",
	"Rate limit exceeded, please try again later":          "Juda ko'p urinish — birozdan so'ng qaytadan urinib ko'ring",

	// Login / registration / OTP — the highest-visibility screen there is.
	"Invalid credentials":           "Email yoki parol noto'g'ri",
	"Invalid email or password":     "Email yoki parol noto'g'ri",
	"Current password is incorrect": "Joriy parol noto'g'ri",
	"Email already registered":      "Bu email allaqachon ro'yxatdan o'tgan",
	"Email already registered. Please use a different email or login to your existing account.": "Bu email allaqachon ro'yxatdan o'tgan — boshqa email kiriting yoki mavjud hisobingizga kiring",
	"Phone number already registered. Please login to your existing account.":                   "Bu telefon raqami allaqachon ro'yxatdan o'tgan — mavjud hisobingizga kiring",
	"No account found with this phone number":                                                   "Bu telefon raqami bilan hisob topilmadi",
	"Phone number is required":                                                                  "Telefon raqami majburiy",
	"Phone and OTP code are required":                                                           "Telefon raqami va SMS-kod majburiy",
	"Email or phone is required":                                                                "Email yoki telefon raqami majburiy",
	"Email or phone number is required":                                                         "Email yoki telefon raqami majburiy",
	"Invalid OTP code":                                                                          "SMS-kod noto'g'ri",
	"OTP has expired. Please request a new one.":                                                "SMS-kod muddati tugagan — yangisini so'rang",
	"OTP already used. Please request a new one.":                                               "Bu SMS-kod allaqachon ishlatilgan — yangisini so'rang",
	"No OTP found. Please request a new one.":                                                   "SMS-kod topilmadi — yangisini so'rang",
	"No OTP found. Please request a new code.":                                                  "SMS-kod topilmadi — yangisini so'rang",
	"Too many failed attempts. Please request a new OTP.":                                       "Urinishlar soni tugadi — yangi SMS-kod so'rang",
	"Phone not verified. Please verify your phone first.":                                       "Telefon raqami tasdiqlanmagan — avval raqamingizni tasdiqlang",
	"Google account email is not verified":                                                      "Google hisobingizdagi email tasdiqlanmagan",
	"Invalid or expired invitation token":                                                       "Taklifnoma eskirgan — yangisini so'rang",
	"Invalid or expired reset token":                                                            "Parolni tiklash havolasi eskirgan — yangisini so'rang",
	"Invitation has expired. Please request a new invitation.":                                  "Taklifnoma muddati tugagan — yangisini so'rang",
	"Tenant code already exists":                                                                "Bu kompaniya kodi band",
	"Cannot invite users from other tenants":                                                    "Boshqa kompaniya foydalanuvchisini taklif qilib bo'lmaydi",

	// Generic validation.
	"Invalid input":               "Kiritilgan ma'lumotlarda xatolik bor — maydonlarni tekshirib qaytadan urinib ko'ring",
	"Invalid request body":        "Kiritilgan ma'lumotlarda xatolik bor — maydonlarni tekshirib qaytadan urinib ko'ring",
	"Invalid input: ":             "Kiritilgan ma'lumotlarda xatolik bor: ",
	"No fields to update":         "Hech narsa o'zgartirilmadi — avval maydonlarni to'ldiring",
	"No file provided":            "Faylni tanlang",
	"Unknown status":              "Noma'lum holat",
	"Resource not found":          "Ma'lumot topilmadi",
	"Tenant ID required":          "Kompaniyani tanlang",
	"tenant_id is required":       "Kompaniyani tanlang",
	"Bank account ID is required": "Bank hisobini tanlang",
	"name cannot be empty":        "Nomini kiriting",
	"code cannot be empty":        "Kodini kiriting",
	"Title cannot be empty":       "Sarlavhani kiriting",

	// Finance business rules.
	"A line cannot have both debit and credit amounts":       "Bitta qatorda ham debet, ham kredit bo'lishi mumkin emas",
	"Journal entry is not balanced":                          "Debet va kredit teng emas — yozuvni tekshiring",
	"Cannot record payment for cancelled invoice":            "Bekor qilingan hisob-fakturaga to'lov qayd etib bo'lmaydi",
	"Cannot delete base currency":                            "Asosiy valyutani o'chirib bo'lmaydi",
	"Cannot delete account with existing transactions":       "Bu schyotda amaliyotlar bor — o'chirib bo'lmaydi",
	"Can only delete invoices in draft status":               "Faqat qoralama holatidagi hisob-fakturalarni o'chirish mumkin",
	"Invoice is already fully paid":                          "Hisob-faktura allaqachon to'liq to'langan",
	"No journal is configured for this company.":             "Bu kompaniya uchun jurnal sozlanmagan",
	"No PAYROLL/MISC/GENERAL journal found for this tenant":  "Ish haqi jurnali topilmadi — jurnallarni sozlang",
	"No MISC/GENERAL journal found for this tenant":          "Umumiy jurnal topilmadi — jurnallarni sozlang",
	"Fiscal period does not belong to your tenant":           "Bu moliyaviy davr sizning kompaniyangizga tegishli emas",
	"Fiscal year does not belong to your tenant":             "Bu moliyaviy yil sizning kompaniyangizga tegishli emas",
	"Invoice is already in draft status":                     "Hisob-faktura allaqachon qoralama holatida",
	"A cancelled invoice cannot be reset to draft":           "Bekor qilingan hisob-fakturani qoralamaga qaytarib bo'lmaydi",
	"Invoice has recorded payments; remove them first":       "Bu hisob-fakturada to'lovlar bor — avval to'lovlarni olib tashlang",
	"Invoice status changed; reload and try again":           "Hisob-faktura holati o'zgargan — sahifani yangilab qaytadan urinib ko'ring",
	"This entry has been reversed; reset its reversal first": "Bu yozuv storno qilingan — avval stornosini bekor qiling",

	// Documents / workflow.
	"Nothing delivered and uninvoiced on this order":                                        "Bu buyurtmada hisob-faktura qilinadigan narsa qolmagan",
	"An invoice already exists for this order (use ?basis=delivered for partial invoicing)": "Bu buyurtma uchun hisob-faktura allaqachon mavjud",
	"Only outgoing invoices can be sent":                                                    "Faqat chiquvchi hisob-fakturalarni yuborish mumkin",
	"Can only confirm invoices in draft status":                                             "Faqat qoralama holatidagi hisob-fakturalarni tasdiqlash mumkin",
	"Can only send invoices in draft status":                                                "Faqat qoralama holatidagi hisob-fakturalarni yuborish mumkin",
	"Can only post invoices in draft or confirmed status":                                   "Faqat qoralama yoki tasdiqlangan hisob-fakturalarni o'tkazish mumkin",
	"You are not the approver for this step":                                                "Bu bosqichni tasdiqlash sizga biriktirilmagan",
	"Workflow is not pending":                                                               "Bu hujjat tasdiqlash kutmayapti",
	"No pending step found":                                                                 "Kutilayotgan bosqich topilmadi",
	"Operation is not awaiting approval":                                                    "Bu amal tasdiqlash kutmayapti",
	"Unknown or non-executable action":                                                      "Bu amalni bajarib bo'lmaydi",
	"Tender is not active":                                                                  "Tender faol emas",
	"Tender deadline has passed":                                                            "Tender muddati o'tib ketgan",
	"Only the tender owner can view bids":                                                   "Takliflarni faqat tender egasi ko'ra oladi",
	"Only the tender owner can update it":                                                   "Tenderni faqat egasi o'zgartira oladi",
	"Only the tender owner can delete it":                                                   "Tenderni faqat egasi o'chira oladi",
	"Only the tender owner can accept bids":                                                 "Takliflarni faqat tender egasi qabul qila oladi",
	"Only the requester can edit this request":                                              "So'rovni faqat uni yuborgan xodim tahrirlashi mumkin",
	"Only the requester can cancel this request":                                            "So'rovni faqat uni yuborgan xodim bekor qilishi mumkin",
	"Only the requester can accept this request":                                            "So'rovni faqat uni yuborgan xodim qabul qilishi mumkin",
	"Only the project foreman can update done quantity":                                     "Bajarilgan hajmni faqat loyiha prorabi o'zgartira oladi",
	"Only the product owner can update it":                                                  "Mahsulotni faqat egasi o'zgartira oladi",
	"Only the product owner can delete it":                                                  "Mahsulotni faqat egasi o'chira oladi",
	"Only the bid owner can update it":                                                      "Taklifni faqat egasi o'zgartira oladi",
	"Only the current (joriy) Forma 2 can be deleted":                                       "Faqat joriy Forma-2 aktini o'chirish mumkin",
	"No employee linked to this user":                                                       "Bu foydalanuvchiga xodim biriktirilmagan",
	"Action not allowed for your project role":                                              "Bu amal sizning loyihadagi rolingiz uchun ruxsat etilmagan",
	"Price data is hidden for your project role":                                            "Narx ma'lumotlari sizning rolingiz uchun yopiq",
	"Column does not belong to this board":                                                  "Ustun bu doskaga tegishli emas",
	"unknown tax_code":                                                                      "Noma'lum soliq kodi",

	// Production-order status refusals (NotFound with embedded reason).
	"Production order not found or cannot be cancelled": "Ishlab chiqarish buyurtmasi topilmadi yoki uni bekor qilib bo'lmaydi",
	"Production order not found or cannot be deleted":   "Ishlab chiqarish buyurtmasi topilmadi yoki uni o'chirib bo'lmaydi",
	"Production order not found or cannot be updated":   "Ishlab chiqarish buyurtmasi topilmadi yoki uni o'zgartirib bo'lmaydi",
	"Production order not found or not in draft status": "Ishlab chiqarish buyurtmasi topilmadi yoki qoralama holatida emas",
	"Production order not found or not in progress":     "Ishlab chiqarish buyurtmasi topilmadi yoki jarayonda emas",
	"Production order not found or not in valid status": "Ishlab chiqarish buyurtmasi topilmadi yoki holati mos emas",
	"Payment not found or already paid":                 "To'lov topilmadi yoki allaqachon to'langan",
	"User not found or already deleted":                 "Foydalanuvchi topilmadi yoki o'chirilgan",
	"Template not found or inactive":                    "Shablon topilmadi yoki faol emas",
	"No default pricelist found":                        "Standart narxlar ro'yxati topilmadi",
	"No matching works found":                           "Mos ishlar topilmadi",
	"No payment terms found":                            "To'lov shartlari topilmadi",
	"No price found for this vendor and product":        "Bu ta'minotchi va mahsulot uchun narx topilmadi",
	"No user to impersonate in this tenant":             "Bu kompaniyada kirish uchun foydalanuvchi topilmadi",

	// Validation stragglers the pattern rules can't compose.
	"period_from and period_to are required (YYYY-MM-DD)":                     "Boshlanish va tugash sanalari majburiy (YYYY-MM-DD)",
	"This operation is already posted":                                        "Bu amaliyot allaqachon o'tkazilgan",
	"Return not found or not in pending status":                               "Qaytarish topilmadi yoki kutish holatida emas",
	"End date must be after start date":                                       "Tugash sanasi boshlanish sanasidan keyin bo'lishi kerak",
	"Each line must have either a debit or credit amount":                     "Har bir qatorda debet yoki kredit summasi bo'lishi kerak",
	"Debits and credits must be equal":                                        "Debet va kredit teng bo'lishi kerak",
	"From and To organizations must be different":                             "Jo'natuvchi va qabul qiluvchi tashkilotlar har xil bo'lishi kerak",
	"WBS code already exists in this project":                                 "Bu WBS kodi loyihada allaqachon mavjud",
	"sched_start and sched_end must be set together":                          "Jadval boshlanishi va tugashi birga kiritilishi kerak",
	"rate must be between 0 and 100":                                          "Stavka 0 va 100 oralig'ida bo'lishi kerak",
	"priority must be 'normal' or 'urgent'":                                   "Muhimlik darajasi 'normal' yoki 'urgent' bo'lishi kerak",
	"platform must be 'android' or 'ios'":                                     "Platforma 'android' yoki 'ios' bo'lishi kerak",
	"material_type must be one of: standard, equipment, cable, metal, import": "Material turi noto'g'ri (standard, equipment, cable, metal yoki import)",
	"required_date must be YYYY-MM-DD":                                        "Sana YYYY-MM-DD ko'rinishida bo'lishi kerak",

	// The long tail: every remaining English refusal a user can hit.
	"Order not found or cannot be marked as shipped":                                            "Buyurtma topilmadi yoki uni jo'natilgan deb belgilab bo'lmaydi",
	"Order not found or cannot be marked as delivered":                                          "Buyurtma topilmadi yoki uni yetkazilgan deb belgilab bo'lmaydi",
	"Order not found or cannot be cancelled":                                                    "Buyurtma topilmadi yoki uni bekor qilib bo'lmaydi",
	"Order cannot be received in current status":                                                "Buyurtmani hozirgi holatida qabul qilib bo'lmaydi",
	"Order cannot be cancelled in current status":                                               "Buyurtmani hozirgi holatida bekor qilib bo'lmaydi",
	"Order cannot be approved in current status":                                                "Buyurtmani hozirgi holatida tasdiqlab bo'lmaydi",
	"Use the receive endpoint to receive a purchase order":                                      "Buyurtmani qabul qilish uchun «Qabul qilish» tugmasidan foydalaning",
	"Use the cancel endpoint to cancel a purchase order":                                        "Buyurtmani «Bekor qilish» tugmasi orqali bekor qiling",
	"Use the approve endpoint to approve a purchase order":                                      "Buyurtmani «Tasdiqlash» tugmasi orqali tasdiqlang",
	"Only draft RFQs can be opened":                                                             "Faqat qoralama narx so'rovlarini ochish mumkin",
	"Only closed RFQs with a selected winner can be converted to PO":                            "Buyurtmaga o'tkazish uchun avval narx so'rovini yopib, g'olibni tanlang",
	"Only cancelled entries can be deleted. Please cancel the entry first.":                     "O'chirish uchun avval yozuvni bekor qiling",
	"Only approved returns can be shipped":                                                      "Faqat tasdiqlangan qaytarishlarni jo'natish mumkin",
	"Only approved requisitions can be converted to PO":                                         "Faqat tasdiqlangan talabnomalarni buyurtmaga o'tkazish mumkin",
	"Only UZS cash orders can be confirmed":                                                     "Faqat so'mdagi kassa orderlarini tasdiqlash mumkin",
	"Releases can only be created for active blanket orders":                                    "Chiqarish faqat faol ramka buyurtmalarida mumkin",
	"Credit can only be applied to received returns":                                            "Kredit faqat qabul qilingan qaytarishlarga qo'llanadi",
	"Return must be approved before processing refund":                                          "Pulni qaytarishdan avval qaytarishni tasdiqlang",
	"Return not found or not in approved/completed status":                                      "Qaytarish topilmadi yoki holati mos emas",
	"Refund has already been processed for this return":                                         "Bu qaytarish uchun pul allaqachon qaytarilgan",
	"Quotation must have at least one item to convert to order":                                 "Buyurtmaga o'tkazish uchun taklifda kamida bitta mahsulot bo'lishi kerak",
	"Quotation must have a customer to convert to order":                                        "Buyurtmaga o'tkazish uchun taklifda mijoz ko'rsatilgan bo'lishi kerak",
	"Quotation already converted to order":                                                      "Bu taklif allaqachon buyurtmaga o'tkazilgan",
	"Quality inspection required before completion":                                             "Yakunlashdan avval sifat tekshiruvi o'tkazilishi kerak",
	"RFQ is not open for responses":                                                             "Bu narx so'rovi javoblar uchun yopiq",
	"RFQ has no selected winner":                                                                "Narx so'rovida g'olib tanlanmagan",
	"RFQ has no items":                                                                          "Narx so'rovida mahsulotlar yo'q",
	"A bill already exists for this purchase order":                                             "Bu buyurtma uchun hisob-faktura allaqachon mavjud",
	"A Purchase Order already exists for this RFQ":                                              "Bu narx so'rovi uchun buyurtma allaqachon mavjud",
	"Sales order must be confirmed or processing to create delivery order":                      "Yetkazib berish uchun avval buyurtmani tasdiqlang",
	"Sales journal not configured. Please create a journal with code SALES or SAL.":             "Savdo jurnali sozlanmagan — Moliya bo'limida SALES kodli jurnal yarating",
	"No cash journal (type 'cash' / code CASH) found for this tenant":                           "Kassa jurnali topilmadi — Moliya bo'limida jurnallarni sozlang",
	"No base currency (UZS) found. Please create UZS currency first.":                           "Asosiy valyuta (UZS) topilmadi — avval uni yarating",
	"Accounts Receivable account (4010) not found. Please configure chart of accounts.":         "Debitorlik schyoti (4010) topilmadi — hisoblar rejasini tekshiring",
	"Cannot resolve kassa GL account (5010)":                                                    "Kassa schyoti (5010) topilmadi — hisoblar rejasini tekshiring",
	"Cannot resolve kassa GL account (5010) for this register":                                  "Bu kassa uchun schyot (5010) topilmadi — hisoblar rejasini tekshiring",
	"Cannot resolve GL accounts for vendor payment (AP 6010 or kassa/bank 5010/5110)":           "To'lov uchun schyotlar topilmadi — hisoblar rejasini tekshiring",
	"Cannot resolve GL accounts for salary payment (kassa/bank 5010/5110 or payable 6710/6010)": "Ish haqi to'lovi uchun schyotlar topilmadi — hisoblar rejasini tekshiring",
	"Cannot resolve GL accounts for payroll accrual (salary 9420 or payable 6710/6010)":         "Ish haqi hisoblanmasi uchun schyotlar topilmadi — hisoblar rejasini tekshiring",
	"Cannot resolve GL accounts for payment (expense 9410 or kassa/bank 5010)":                  "To'lov uchun schyotlar topilmadi — hisoblar rejasini tekshiring",
	"Chart of accounts has no leaf account for stock input. Please add a leaf child under 1010 (Xom ashyo) or 6015 (Stock Interim Receipt) and try again.": "Kirim uchun schyot topilmadi — hisoblar rejasida 1010 yoki 6015 ostida schyot yarating",
	"Cannot remove the last super admin":                                                                        "Oxirgi bosh administratorni o'chirib bo'lmaydi",
	"Cannot modify system roles":                                                                                "Tizim rollarini o'zgartirib bo'lmaydi",
	"Cannot delete system roles":                                                                                "Tizim rollarini o'chirib bo'lmaydi",
	"Cannot delete system act types":                                                                            "Tizim akt turlarini o'chirib bo'lmaydi",
	"Cannot delete your own account":                                                                            "O'z hisobingizni o'chira olmaysiz",
	"Cannot delete the last organization":                                                                       "Oxirgi tashkilotni o'chirib bo'lmaydi",
	"Cannot delete the default pricelist":                                                                       "Standart narxlar ro'yxatini o'chirib bo'lmaydi",
	"Cannot delete the default pipeline":                                                                        "Standart voronkani o'chirib bo'lmaydi",
	"Cannot deactivate the base currency":                                                                       "Asosiy valyutani o'chirib qo'yib bo'lmaydi",
	"Set another currency as base instead of clearing this one":                                                 "Avval boshqa valyutani asosiy qilib belgilang",
	"Cannot delete journal with existing entries":                                                               "Bu jurnalda yozuvlar bor — o'chirib bo'lmaydi",
	"Cannot delete category with child categories":                                                              "Bu kategoriyada ichki kategoriyalar bor — o'chirib bo'lmaydi",
	"Cannot delete category with associated products":                                                           "Bu kategoriyada mahsulotlar bor — o'chirib bo'lmaydi",
	"Cannot delete subcontract with linked acts":                                                                "Bu shartnomaga aktlar bog'langan — o'chirib bo'lmaydi",
	"Cannot delete pipeline stage with existing opportunities":                                                  "Bu bosqichda bitimlar bor — o'chirib bo'lmaydi",
	"Cannot delete payment term that is in use. Deactivate it instead.":                                         "Bu to'lov sharti ishlatilmoqda — o'chirish o'rniga faolsizlantiring",
	"Cannot delete approved reservations":                                                                       "Tasdiqlangan rezervni o'chirib bo'lmaydi",
	"Cannot delete an active blanket order. Please cancel it first.":                                            "Faol ramka buyurtmasini o'chirishdan avval uni bekor qiling",
	"Cannot delete a filed report":                                                                              "Topshirilgan hisobotni o'chirib bo'lmaydi",
	"Cannot delete a completed stock count":                                                                     "Yakunlangan inventarizatsiyani o'chirib bo'lmaydi",
	"Cannot delete a completed reconciliation":                                                                  "Yakunlangan solishtirishni o'chirib bo'lmaydi",
	"Cannot delete lot that has been partially consumed":                                                        "Qisman ishlatilgan partiyani o'chirib bo'lmaydi",
	"Cannot delete default operation types. You can deactivate them instead.":                                   "Standart amaliyot turlarini o'chirish o'rniga faolsizlantiring",
	"Cannot delete location with child locations. Delete child locations first.":                                "Avval ichki joylashuvlarni o'chiring",
	"Cannot delete location with non-zero inventory (positive or negative). Move or reconcile inventory first.": "Bu joylashuvda qoldiq bor — avval zaxirani ko'chiring yoki nollang",
	"Cannot delete warehouse with non-zero inventory (positive or negative). Reconcile stock first, then set the warehouse to inactive.": "Bu omborda qoldiq bor — avval zaxirani nollang, keyin omborni faolsizlantiring",
	"Cannot delete the only warehouse. Create another warehouse first.":                                                                  "Yagona omborni o'chirib bo'lmaydi — avval boshqa ombor yarating",
	"Cannot delete product with non-zero inventory (positive or negative). Reconcile stock first, then set the product to inactive.":     "Bu mahsulotda qoldiq bor — avval zaxirani nollang, keyin mahsulotni faolsizlantiring",
	"Cannot delete: this unit is assigned to one or more products":                                                                       "Bu o'lchov birligi mahsulotlarda ishlatilmoqda — o'chirib bo'lmaydi",
	"Cannot cancel shipped or completed returns":                                                                                         "Jo'natilgan yoki yakunlangan qaytarishni bekor qilib bo'lmaydi",
	"Cannot cancel delivered orders":                                                                                                     "Yetkazilgan buyurtmani bekor qilib bo'lmaydi",
	"Cannot cancel completed goods receipt":                                                                                              "Yakunlangan kirimni bekor qilib bo'lmaydi",
	"Cannot cancel a received release":                                                                                                   "Qabul qilingan chiqarishni bekor qilib bo'lmaydi",
	"Cannot cancel a completed operation":                                                                                                "Yakunlangan amaliyotni bekor qilib bo'lmaydi",
	"Cannot add expenses to a completed project":                                                                                         "Yakunlangan loyihaga xarajat qo'shib bo'lmaydi",
	"Cannot update a completed reconciliation":                                                                                           "Yakunlangan solishtirishni o'zgartirib bo'lmaydi",
	"Cannot edit a payroll entry that has already been approved or paid":                                                                 "Tasdiqlangan yoki to'langan ish haqi yozuvini o'zgartirib bo'lmaydi",
	"Cannot inspect completed or cancelled goods receipt":                                                                                "Yakunlangan yoki bekor qilingan kirimni tekshirib bo'lmaydi",
	"Cannot recalculate a filed report":                                                                                                  "Topshirilgan hisobotni qayta hisoblab bo'lmaydi",
	"Scrap order cannot be cancelled":                                                                                                    "Bu chiqit buyurtmasini bekor qilib bo'lmaydi",
	"Invoice is posted to the ledger; create a credit note instead of cancelling":                                                        "Hisob-faktura daftarga o'tkazilgan — bekor qilish o'rniga kredit-nota yarating",
	"Insufficient inventory to scrap":                                                                                                    "Chiqitga chiqarish uchun zaxira yetarli emas",
	"Adjustment would result in negative inventory":                                                                                      "Bu o'zgartirish zaxirani manfiy qilib qo'yadi",
	"No inventory found at source location":                                                                                              "Bu joylashuvda zaxira yo'q",
	"No items to deliver":                                                                                                                "Yetkazib beriladigan mahsulot yo'q",
	"No remaining quantities for backorder":                                                                                              "Qoldiq buyurtma uchun miqdor qolmagan",
	"No lines provided":                                                                                                                  "Qatorlar kiritilmagan",
	"No rows provided":                                                                                                                   "Qatorlar kiritilmagan",
	"No transactions provided":                                                                                                           "Amaliyotlar kiritilmagan",
	"No organizations to import":                                                                                                         "Import qilinadigan tashkilotlar yo'q",
	"No available credit to reconcile":                                                                                                   "Solishtirish uchun kredit qoldig'i yo'q",
	"No attribute values configured for this product":                                                                                    "Bu mahsulot uchun xususiyatlar sozlanmagan",
	"No frozen Forma 2 to unfreeze":                                                                                                      "Muzlatilgan Forma-2 yo'q",
	"This product has variants. Please specify a variant to adjust stock.":                                                               "Bu mahsulotning variantlari bor — zaxirani variant bo'yicha o'zgartiring",
	"This material request has a linked delivery. Please confirm it through Stock Operations.":                                           "Bu so'rovga yetkazib berish bog'langan — uni Ombor amaliyotlari orqali tasdiqlang",
	"This production order does not use split output":                                                                                    "Bu buyurtmada bo'lib chiqarish yo'q",
	"This discount is only for new customers":                                                                                            "Bu chegirma faqat yangi mijozlar uchun",
	"Discount code has expired":                                                                                                          "Chegirma kodi muddati tugagan",
	"Discount code is not yet active":                                                                                                    "Chegirma kodi hali faollashmagan",
	"Discount code usage limit reached":                                                                                                  "Chegirma kodidan foydalanish soni tugagan",
	"You have already used this discount the maximum number of times":                                                                    "Bu chegirmadan foydalanish limitingiz tugagan",
	"You have already submitted a bid for this tender":                                                                                   "Bu tenderga taklifingizni allaqachon yuborgansiz",
	"You do not have impersonation capability":                                                                                           "Bu amal uchun sizga ruxsat berilmagan",
	"Dependency would create a cycle":                                                                                                    "Bunday bog'liqlik aylanma hosil qiladi — boshqa ishni tanlang",
	"A work cannot depend on itself":                                                                                                     "Ish o'z-o'ziga bog'lana olmaydi",
	"Both lines must be works of this project":                                                                                           "Ikkala qator ham shu loyihaning ishlari bo'lishi kerak",
	"Some lines are not works of this project":                                                                                           "Ba'zi qatorlar bu loyihaga tegishli emas",
	"Source act must be KS-2 type":                                                                                                       "Manba akt KS-2 turida bo'lishi kerak",
	"Act is not a Forma 19 (hidden_work) type":                                                                                           "Bu akt Forma-19 (yashirin ishlar) turida emas",
	"Forma 2 must be approved or signed before generating Forma 3":                                                                       "Forma-3 dan avval Forma-2 tasdiqlanishi yoki imzolanishi kerak",
	"Parent line belongs to a different estimate":                                                                                        "Yuqori qator boshqa smetaga tegishli",
	"Iteration does not belong to this project":                                                                                          "Iteratsiya bu loyihaga tegishli emas",
	"Warehouse does not belong to the selected organization":                                                                             "Ombor tanlangan tashkilotga tegishli emas",
	"Source and target organizations must be different":                                                                                  "Jo'natuvchi va qabul qiluvchi tashkilotlar har xil bo'lishi kerak",
	"Source and destination warehouses must be different":                                                                                "Jo'natuvchi va qabul qiluvchi omborlar har xil bo'lishi kerak",
	"Vendor not found or not a valid vendor contact":                                                                                     "Ta'minotchi topilmadi",
	"Lead not found or already converted":                                                                                                "Lid topilmadi yoki allaqachon mijozga aylantirilgan",
	"Lead partner reference is invalid":                                                                                                  "Lidning kontragent bog'lamasi noto'g'ri — sahifani yangilang",
	"Line not found or already posted":                                                                                                   "Qator topilmadi yoki allaqachon o'tkazilgan",
	"Pipeline has no won stage":                                                                                                          "Voronkada yutuq bosqichi yo'q — voronkani sozlang",
	"Pipeline has no lost stage":                                                                                                         "Voronkada yo'qotish bosqichi yo'q — voronkani sozlang",
	"Board has no columns":                                                                                                               "Doskada ustunlar yo'q",
	"Board name cannot be empty":                                                                                                         "Doska nomini kiriting",
	"Column name cannot be empty":                                                                                                        "Ustun nomini kiriting",
	"Column has tasks; pass move_to=<columnId> to relocate them":                                                                         "Bu ustunda vazifalar bor — avval ularni boshqa ustunga ko'chiring",
	"Comments are required for rejection":                                                                                                "Rad etish uchun izoh yozing",
	"Counter account must be a leaf account":                                                                                             "Qarshi schyot yakuniy (leaf) schyot bo'lishi kerak",
	"Counter account is required to confirm (set account_id on the order)":                                                               "Tasdiqlash uchun qarshi schyotni tanlang",
	"Counter account cannot be the register's own cash account":                                                                          "Qarshi schyot kassaning o'z schyoti bo'lishi mumkin emas",
	"Debit note is not in draft status":                                                                                                  "Debet-nota qoralama holatida emas",
	"Credit note is not in draft status":                                                                                                 "Kredit-nota qoralama holatida emas",
	"Deadline must be in the future":                                                                                                     "Muddat kelajakdagi sana bo'lishi kerak",
	"Delivery steps must be 1, 2, or 3":                                                                                                  "Yetkazib berish bosqichlari 1, 2 yoki 3 bo'lishi kerak",
	"Reception steps must be 1, 2, or 3":                                                                                                 "Qabul qilish bosqichlari 1, 2 yoki 3 bo'lishi kerak",
	"Expense is no longer in 'approved' status":                                                                                          "Xarajat endi tasdiqlangan holatda emas — sahifani yangilang",
	"Expense amount must be positive to pay":                                                                                             "To'lash uchun xarajat summasi 0 dan katta bo'lishi kerak",
	"Excel file has no sheets":                                                                                                           "Excel faylida varaqlar yo'q",
	"File too large (max 10 MB)":                                                                                                         "Fayl juda katta (eng ko'pi 10 MB)",
	"File required (multipart field: file)":                                                                                              "Faylni tanlang",
	"file is required (multipart)":                                                                                                       "Faylni tanlang",
	"Unsupported file type. Use JPEG, PNG or WebP":                                                                                       "Bu fayl turi qo'llab-quvvatlanmaydi — JPEG, PNG yoki WebP yuklang",
	"Unsupported file type. Supported: JPEG, PNG, WebP, PDF":                                                                             "Bu fayl turi qo'llab-quvvatlanmaydi — JPEG, PNG, WebP yoki PDF yuklang",
	"All lines must be counted before completing":                                                                                        "Yakunlashdan avval barcha qatorlarni sanab chiqing",
	"At least one line must have sarf > 0 to approve":                                                                                    "Tasdiqlash uchun kamida bitta qatorda sarf 0 dan katta bo'lishi kerak",
	"At least one of create_contact or create_opportunity must be true":                                                                  "Kontragent yoki bitim yaratishdan kamida bittasini tanlang",
	"Amount must be greater than zero":                                                                                                   "Summa 0 dan katta bo'lishi kerak",
	"Amount must be positive":                                                                                                            "Summa 0 dan katta bo'lishi kerak",
	"Amount must be greater than 0":                                                                                                      "Summa 0 dan katta bo'lishi kerak",
	"amount must be greater than 0":                                                                                                      "Summa 0 dan katta bo'lishi kerak",
	"Value cannot be negative":                                                                                                           "Qiymat manfiy bo'lishi mumkin emas",
	"Budget total amount must be greater than zero":                                                                                      "Byudjet summasi 0 dan katta bo'lishi kerak",
	"Budget line amount must be greater than zero":                                                                                       "Byudjet qatori summasi 0 dan katta bo'lishi kerak",
	"Too many items (max 500)":                                                                                                           "Juda ko'p qator (eng ko'pi 500)",
	"Label must be 100 characters or less":                                                                                               "Yorliq 100 belgidan oshmasligi kerak",
	"Dates must be YYYY-MM-DD":                                                                                                           "Sanalarni to'g'ri kiriting (masalan: 2026-01-31)",
	"invalid year":                                                                                                                       "Yilni to'g'ri kiriting",
	"no fields to update":                                                                                                                "Hech narsa o'zgartirilmadi — avval maydonlarni to'ldiring",
	"Email is required for email delivery":                                                                                               "Email orqali yuborish uchun email kiriting",
	"Phone number is required for SMS":                                                                                                   "SMS yuborish uchun telefon raqamini kiriting",
	"Phone number is required for SMS delivery":                                                                                          "SMS yuborish uchun telefon raqamini kiriting",
	"This email is already in use":                                                                                                       "Bu email allaqachon ishlatilmoqda",
	"INN already registered":                                                                                                             "Bu INN allaqachon ro'yxatdan o'tgan",
	"User already has a password set":                                                                                                    "Bu foydalanuvchida parol allaqachon bor",
	"Employee is already assigned to this organization":                                                                                  "Xodim bu tashkilotga allaqachon biriktirilgan",
	"Employee already has an active loan":                                                                                                "Bu xodimda faol qarz bor",
	"Payroll entry for this employee already exists in this period":                                                                      "Bu xodim uchun bu davrda ish haqi yozuvi allaqachon bor",
	"Entry already generated for this date":                                                                                              "Bu sana uchun yozuv allaqachon yaratilgan",
	"Goods receipt already completed":                                                                                                    "Kirim allaqachon yakunlangan",
	"Transfer already validated":                                                                                                         "Ko'chirish allaqachon tasdiqlangan",
	"Team member is already transferred":                                                                                                 "Bu xodim allaqachon o'tkazilgan",
	"This entry has already been reversed":                                                                                               "Bu yozuv allaqachon storno qilingan",
	"This record is already linked":                                                                                                      "Bu yozuv allaqachon bog'langan",
	"Order is already cancelled":                                                                                                         "Buyurtma allaqachon bekor qilingan",
	"Lead is already won":                                                                                                                "Bu lid allaqachon yutilgan",
	"Lead is already lost":                                                                                                               "Bu lid allaqachon yo'qotilgan deb belgilangan",
	"Material request is already approved":                                                                                               "Material so'rovi allaqachon tasdiqlangan",
	"Material request has no items":                                                                                                      "Material so'rovida qatorlar yo'q",
	"Project is already completed":                                                                                                       "Loyiha allaqachon yakunlangan",
	"Report is already filed":                                                                                                            "Hisobot allaqachon topshirilgan",
	"Report must be calculated before filing":                                                                                            "Topshirishdan avval hisobotni hisoblang",
	"Reconciliation is already completed":                                                                                                "Solishtirish allaqachon yakunlangan",
	"Expense line is already cancelled":                                                                                                  "Xarajat qatori allaqachon bekor qilingan",
	"Document is already fully paid":                                                                                                     "Hujjat allaqachon to'liq to'langan",
	"Closing is not in state 'closed'":                                                                                                   "Yopilish hali yakunlanmagan",
	"Period is already approved/paid; cannot recalculate":                                                                                "Davr tasdiqlangan yoki to'langan — qayta hisoblab bo'lmaydi",
	"Work is locked or already submitted; cannot edit done quantity":                                                                     "Ish qulflangan yoki topshirilgan — hajmni o'zgartirib bo'lmaydi",
	"Work order is not in progress":                                                                                                      "Ish buyurtmasi jarayonda emas",
	"A draft reconciliation already exists for this bank account. Please complete or delete it first.": "Bu bank hisobida tugallanmagan solishtirish bor — avval uni yakunlang yoki o'chiring",
	"Operation type with this code already exists in this warehouse":                                   "Bu kodli amaliyot turi bu omborda allaqachon mavjud",
	"Location with this code already exists in this warehouse":                                         "Bu kodli joylashuv bu omborda allaqachon mavjud",
	"Reorder rule already exists for this product/warehouse combination":                               "Bu mahsulot va ombor uchun qoida allaqachon mavjud",
	"Rule already exists for this source-target-type combination":                                      "Bunday qoida allaqachon mavjud",
	"Company profile could not be created. Please fill your Company Profile first.":                    "Avval Kompaniya profilini to'ldiring",
	"Could not allocate a contract number, please retry":                                               "Shartnoma raqami berilmadi — qaytadan urinib ko'ring",
	"Not an employee of this company: ":                                                                "Bu kompaniya xodimi emas: ",
	"Sales return created but failed to retrieve":                                                      "Qaytarish saqlandi, lekin qayta ochilmadi — sahifani yangilang",
	"Sales return updated but failed to retrieve":                                                      "Qaytarish saqlandi, lekin qayta ochilmadi — sahifani yangilang",
	"Quotation created but failed to retrieve":                                                         "Taklif saqlandi, lekin qayta ochilmadi — sahifani yangilang",
	"Quotation updated but failed to retrieve":                                                         "Taklif saqlandi, lekin qayta ochilmadi — sahifani yangilang",
	"Period closed but summary load failed":                                                            "Davr yopildi, lekin xulosani yuklab bo'lmadi — sahifani yangilang",
	"Transaction failed":                                                                               "Nimadir xato ketdi. Birozdan so'ng qaytadan urinib ko'ring.",
	"Could not start clone transaction":                                                                "Nimadir xato ketdi. Birozdan so'ng qaytadan urinib ko'ring.",
	"Lookup failed":                                                                                    "Qidiruvda xatolik — qaytadan urinib ko'ring",
	"Stored trigger data is not replayable":                                                            "Bu amalni qayta bajarib bo'lmaydi",
	"Push is not configured on the server (FCM credentials missing)":                                   "Push-bildirishnomalar sozlanmagan",
	"PBX is not configured":                                                                            "Telefoniya (PBX) sozlanmagan",
	"Account is disabled":                                                                              "Bu hisob o'chirib qo'yilgan — administratorga murojaat qiling",
	"Unknown tax code":                                                                                 "Noma'lum soliq kodi",
	"This currency is not in the catalogue. Adding one affects every tenant and requires a platform administrator.":           "Bu valyuta katalogda yo'q — qo'shish uchun platforma administratoriga murojaat qiling",
	"Currency name, symbol and decimal places are shared by every tenant and can only be changed by a platform administrator": "Valyuta sozlamalarini faqat platforma administratori o'zgartira oladi",
	"Template lines are not balanced (debits must equal credits)":                                                             "Shablon qatorlarida debet va kredit teng emas",
	"payment_status is derived from payments and cannot be set directly":                                                      "To'lov holati to'lovlardan hisoblanadi — uni qo'lda o'zgartirib bo'lmaydi",
	"Only cancellation is allowed here; use /send to post the invoice, payments set partial/paid":                             "Bu yerda faqat bekor qilish mumkin",
	"Provide item_type+item_id or sub_stage_id":                                                                               "Element yoki bosqichni tanlang",
	"Resolution must be 'employee', 'company', or 'cash'":                                                                     "Qaror turini tanlang: xodim, kompaniya yoki naqd",
	"Action must be 'confirm' or 'dispute'":                                                                                   "Amalni tanlang: tasdiqlash yoki e'tiroz",
	"Status must be 'draft' or 'submitted'":                                                                                   "Holat qoralama yoki yuborilgan bo'lishi kerak",
	"End date cannot be before start date":                                                                                    "Tugash sanasi boshlanish sanasidan oldin bo'lishi mumkin emas",
	"2FA yoqilmagan":                                                                                                          "Ikki bosqichli himoya (2FA) yoqilmagan",
	"NO_RECEIPT_WAREHOUSE":                                                                                                    "Qabul qiladigan ombor tanlanmagan",
	"min_version cannot be greater than latest_version":                                                                       "Minimal versiya oxirgi versiyadan katta bo'lishi mumkin emas",
	"move_to column does not belong to this board":                                                                            "Tanlangan ustun bu doskaga tegishli emas",
	"Can only ship approved transfers":                                                                                        "Faqat tasdiqlangan ko'chirishlarni jo'natish mumkin",
	"Can only receive in_transit transfers":                                                                                   "Faqat yo'ldagi ko'chirishlarni qabul qilish mumkin",
	"Can only edit lines in draft acts":                                                                                       "Qatorlarni faqat qoralama aktlarda o'zgartirish mumkin",

	// Machine-prefixed messages: prefix preserved by rule, tails here.
	"order has received stock and cannot be cancelled":   "buyurtma bo'yicha mahsulot qabul qilingan — bekor qilib bo'lmaydi",
	"received quantity exceeds ordered quantity":         "qabul qilingan miqdor buyurtmadagidan oshib ketdi",
	"shipped quantity would exceed the ordered quantity": "jo'natilayotgan miqdor buyurtmadagidan oshib ketadi",
	"delivery order was already validated":               "yetkazib berish allaqachon tasdiqlangan",
	"not enough on hand to ship this return":             "qaytarishni jo'natish uchun zaxira yetarli emas",
	"invoice already has a journal entry":                "bu hisob-faktura allaqachon o'tkazilgan",
	"amount exceeds the invoice's remaining balance":     "to'lov summasi hisob-faktura qoldig'idan oshib ketdi",
}

// nounUz translates a resource/entity name, falling back to the original.
func nounUz(s string) string {
	if uz, ok := nouns[strings.ToLower(strings.TrimSpace(s))]; ok {
		return uz
	}
	return strings.TrimSpace(s)
}

// looksLikeDateFormat catches the many "Invalid <field> format ... YYYY-MM-DD"
// spellings in one place.
func looksLikeDateFormat(tail string) bool {
	low := strings.ToLower(tail)
	return strings.Contains(low, "yyyy-mm-dd") ||
		strings.HasSuffix(low, "date format") ||
		strings.Contains(low, "date format")
}

// translateUserMessage maps an outgoing message to Uzbek. Unrecognized
// messages come back unchanged — never a guessed translation.
func translateUserMessage(msg string) string {
	if msg == "" {
		return msg
	}
	if uz, ok := exact[msg]; ok {
		return uz
	}

	// "PREFIX: rest" — keep the machine-readable prefix, translate the rest.
	if i := strings.Index(msg, ": "); i > 0 {
		prefix := msg[:i]
		if prefix == strings.ToUpper(prefix) && !strings.Contains(prefix, " ") {
			if uz, ok := exact[msg[i+2:]]; ok {
				return prefix + ": " + uz
			}
		}
	}

	// "Only <status> <things> can be <done>" — compositional.
	if rest, ok := strings.CutPrefix(msg, "Only "); ok {
		if head, verb, found := cutOnce(rest, " can be "); found {
			if uzVerb, ok := onlyVerbs[strings.TrimSpace(verb)]; ok {
				// head = "<status> <plural noun>"; try longest status first.
				for status, uzStatus := range onlyStatuses {
					if nounPart, ok := strings.CutPrefix(head, status+" "); ok {
						if uzNoun, ok := onlyNouns[strings.ToLower(strings.TrimSpace(nounPart))]; ok {
							return "Faqat " + uzStatus + " holatidagi " + uzNoun + " " + uzVerb + " mumkin"
						}
					}
				}
			}
		}
	}

	// "Failed to <phrase>" — known phrase or the honest generic.
	if phrase, ok := strings.CutPrefix(msg, "Failed to "); ok {
		if uz, ok := failedPhrases[phrase]; ok {
			return uz
		}
		return failedGeneric
	}

	// "Invalid <x>".
	if tail, ok := strings.CutPrefix(msg, "Invalid "); ok {
		low := strings.ToLower(tail)
		switch {
		case looksLikeDateFormat(tail):
			return "Sanani to'g'ri kiriting (masalan: 2026-01-31)"
		case low == "id" || strings.HasSuffix(low, " id") || strings.HasSuffix(low, "_id"):
			// A malformed ID is never something the user typed — it is a
			// stale page or a broken link. Tell them the fix, not the cause.
			noun := strings.TrimSuffix(strings.TrimSuffix(tail, " ID"), " id")
			noun = strings.TrimSuffix(noun, "_id")
			if noun == "" || strings.EqualFold(noun, "id") {
				return "Ma'lumot ochilmadi — sahifani yangilab, qaytadan urinib ko'ring"
			}
			return nounUz(noun) + " ochilmadi — sahifani yangilab, qaytadan urinib ko'ring"
		case low == "email":
			return "Emailni to'g'ri kiriting"
		case low == "date":
			return "Sanani to'g'ri kiriting"
		case low == "amount":
			return "Summani to'g'ri kiriting"
		case low == "status":
			return "Bu holatda bu amalni bajarib bo'lmaydi"
		default:
			return "Kiritilgan ma'lumot noto'g'ri: " + tail
		}
	}

	// "<x> is required" / "<x> are required" / "<x> required" — with or
	// without a trailing hint like "(YYYY-MM-DD)"; the hint survives.
	for _, marker := range []string{" is required", " are required", " required"} {
		if i := strings.Index(msg, marker); i > 0 {
			rest := strings.TrimSpace(msg[i+len(marker):])
			out := nounUz(msg[:i]) + " ko'rsatilmagan — to'ldirib qaytadan urinib ko'ring"
			if rest != "" {
				out += " " + rest
			}
			return out
		}
	}

	// "You do not have permission ..." (Sprintf variants append the action).
	if strings.HasPrefix(msg, "You do not have permission") {
		return "Bu amal uchun sizda ruxsat yo'q"
	}

	// "<x> must be positive" / "<x> must be greater than 0".
	if head, ok := strings.CutSuffix(msg, " must be positive"); ok {
		return nounUz(head) + " musbat bo'lishi kerak"
	}
	if head, ok := strings.CutSuffix(msg, " must be greater than 0"); ok {
		return nounUz(head) + " 0 dan katta bo'lishi kerak"
	}
	if head, ok := strings.CutSuffix(msg, " must be >= 0"); ok {
		return nounUz(head) + " manfiy bo'lmasligi kerak"
	}

	// "<x> not found in this tenant" / "<x> not found".
	if head, ok := strings.CutSuffix(msg, " not found in this tenant"); ok {
		return nounUz(head) + " topilmadi"
	}
	if head, ok := strings.CutSuffix(msg, " not found"); ok {
		return nounUz(head) + " topilmadi"
	}

	// "Can only <do something> in <statuses> status" and its "from
	// <statuses> orders" cousin — the statuses are the useful part; the rest
	// of the sentence is always "you cannot do this right now".
	if strings.HasPrefix(msg, "Can only ") {
		if list, ok := statusList(msg); ok {
			return "Bu amal faqat " + list + " holatida mumkin"
		}
	}

	// "Missing required permission: x" / capability variants.
	if strings.HasPrefix(msg, "Missing required permission") ||
		strings.HasPrefix(msg, "Missing required platform capability") {
		return "Bu amal uchun sizga ruxsat berilmagan"
	}

	// Sprintf prefixes with a dynamic tail.
	for eng, uz := range map[string]string{
		"Fetch failed: ":                                  "Yuklab bo'lmadi: ",
		"Send failed: ":                                   "Yuborib bo'lmadi: ",
		"AI request failed: ":                             "AI so'rovi bajarilmadi: ",
		"Unknown provider: ":                              "Noma'lum provayder: ",
		"Unknown section: ":                               "Noma'lum bo'lim: ",
		"Could not read the Excel file: ":                 "Excel faylini o'qib bo'lmadi: ",
		"Could not read sheet rows: ":                     "Excel qatorlarini o'qib bo'lmadi: ",
		"Assignee is not an employee of this company: ":   "Bu kompaniya xodimi emas: ",
		"Work order is not in progress. Current status: ": "Ish buyurtmasi jarayonda emas. Hozirgi holati: ",
		"Work order cannot be started. Current status: ":  "Ish buyurtmasini boshlab bo'lmaydi. Hozirgi holati: ",
		"Stock count already ":                            "Inventarizatsiya allaqachon ",
		"Operation is already ":                           "Amaliyot allaqachon ",
		"Quotation already converted to order: ":          "Bu taklif allaqachon buyurtmaga o'tkazilgan: ",
		"Provider adapter not registered: ":               "Provayder sozlanmagan: ",
		"Cannot update lines of a ":                       "Hujjatni hozirgi holatida o'zgartirib bo'lmaydi — ",
		"Cannot delete lines from a ":                     "Hujjatni hozirgi holatida o'zgartirib bo'lmaydi — ",
		"Cannot add lines to a ":                          "Hujjatni hozirgi holatida o'zgartirib bo'lmaydi — ",
		"Cannot update a ":                                "Hujjatni hozirgi holatida o'zgartirib bo'lmaydi — ",
		"Cannot cancel a ":                                "Hujjatni hozirgi holatida bekor qilib bo'lmaydi — ",
		"Cannot delete lot with status '":                 "Bu holatdagi partiyani o'chirib bo'lmaydi: '",
		"Work is in '":                                    "Bu ishning hozirgi holatida bu amal mumkin emas: '",
		"No credentials for provider ":                    "Provayder sozlanmagan: ",
		"No active credentials configured for provider ":  "Provayder sozlanmagan: ",
		"Column does not belong to this board: ":          "Ustun bu doskaga tegishli emas: ",
	} {
		if rest, ok := strings.CutPrefix(msg, eng); ok {
			return uz + rest
		}
	}

	// "<x> must be between A and B".
	if i := strings.Index(msg, " must be between "); i > 0 {
		return nounUz(msg[:i]) + " " + strings.Replace(msg[i+len(" must be between "):], " and ", " va ", 1) + " oralig'ida bo'lishi kerak"
	}

	// Date-order family: whatever the two field names are, the meaning is
	// always "the end comes before the start".
	if strings.Contains(msg, " must be after ") || strings.Contains(msg, " must be on or after ") ||
		strings.Contains(msg, " must not be earlier than ") || strings.Contains(msg, " is before ") ||
		strings.Contains(msg, " cannot be before ") {
		return "Sanalar tartibi noto'g'ri — tugash sanasi boshlanishidan keyin bo'lishi kerak"
	}

	if head, ok := strings.CutSuffix(msg, " must be > 0"); ok {
		return nounUz(head) + " 0 dan katta bo'lishi kerak"
	}
	if head, ok := strings.CutSuffix(msg, " must be YYYY-MM-DD"); ok {
		return nounUz(head) + " sanasini to'g'ri kiriting (masalan: 2026-01-31)"
	}

	// "<x> must be a UUID" — a client bug or stale page, never user input.
	if strings.HasSuffix(msg, " must be a UUID") || strings.HasSuffix(msg, " must contain 1..100 UUIDs") {
		return "Ma'lumot ochilmadi — sahifani yangilab, qaytadan urinib ko'ring"
	}

	// "<x> must be 'a' or 'b'" / "must be one of: ..." — the allowed values
	// are literal codes, so they stay; the frame becomes Uzbek.
	if i := strings.Index(msg, " must be "); i > 0 {
		tail := msg[i+len(" must be "):]
		if strings.Contains(tail, "'") || strings.HasPrefix(tail, "one of") {
			tail = strings.TrimPrefix(tail, "one of: ")
			tail = strings.ReplaceAll(tail, " or ", " yoki ")
			return nounUz(msg[:i]) + " qiymati noto'g'ri. Ruxsat etilgani: " + tail
		}
	}

	// "<x> already exists".
	if head, ok := strings.CutSuffix(msg, " already exists"); ok {
		return nounUz(head) + " allaqachon mavjud"
	}

	return msg
}

func cutOnce(s, sep string) (before, after string, found bool) {
	i := strings.Index(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// statusList pulls the "<statuses>" out of a "Can only ... in <statuses>
// status" or "... from <statuses> orders" sentence and translates each
// token, keeping unknown ones as-is.
func statusList(msg string) (string, bool) {
	var raw string
	if i := strings.Index(msg, " in "); i > 0 && strings.Contains(msg[i:], " status") {
		raw = msg[i+4 : strings.Index(msg, " status")]
	} else if i := strings.Index(msg, " for "); i > 0 {
		rest := msg[i+5:]
		if j := strings.LastIndex(rest, " "); j > 0 {
			raw = rest[:j]
		}
	} else if i := strings.Index(msg, " from "); i > 0 {
		rest := msg[i+6:]
		if j := strings.LastIndex(rest, " "); j > 0 {
			raw = rest[:j] // drop the trailing noun ("orders", "bills", ...)
		}
	}
	if raw == "" {
		return "", false
	}
	raw = strings.ReplaceAll(raw, ", or ", ", ")
	raw = strings.ReplaceAll(raw, " or ", ", ")
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if uz, ok := onlyStatuses[p]; ok {
			out = append(out, uz)
		} else {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, " yoki "), true
}
